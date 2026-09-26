package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nguyenngocanh94/mate/internal/host"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/panerun"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// consoleColumns are the Console's two sibling columns (docs/mvp.md M13):
// the stage, which shows the agent of the row Enter was pressed on, and the
// review, which shows that row's repo in terminal-code. Each column runs
// `mate pane serve` for its whole life; the Console tells it what to show
// over its socket, and the host never re-splits a column once made.
type consoleColumns struct {
	h      host.Host
	cols   []host.Column
	dir    string
	stage  string
	review string // "" when terminal-code is not installed
	tode   string
	herdr  string
	// env is what every column's program gets over the pane's own: the
	// Console's PATH.
	env []string
}

// newConsoleColumns plans the columns for this Console: the stage always,
// the review when terminal-code (`tode`) is installed.
func newConsoleColumns(h host.Host, getenv func(string) string) (*consoleColumns, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find the mate binary: %w", err)
	}
	// A short directory: a unix socket path is limited to about 100 bytes.
	dir, err := os.MkdirTemp("", "mate-cols-")
	if err != nil {
		return nil, err
	}
	c := &consoleColumns{h: h, dir: dir, stage: filepath.Join(dir, roleStage+".sock"), herdr: findTool(getenv, "herdr")}
	if path := getenv("PATH"); path != "" {
		c.env = []string{"PATH=" + path}
	}
	owner := strconv.Itoa(os.Getpid())
	column := func(role, socket string) host.Column {
		return host.Column{Role: role, Argv: []string{exe, "pane", "serve", "--role", role, "--socket", socket, "--owner", owner}}
	}
	c.cols = []host.Column{column(roleStage, c.stage)}
	if tode := findTool(getenv, "tode"); tode != "" && filepath.IsAbs(tode) {
		c.tode, c.review = tode, filepath.Join(dir, roleReview+".sock")
		c.cols = append(c.cols, column(roleReview, c.review))
	}
	return c, nil
}

// toolDirs are where installers put a CLI when a pane's PATH, which
// starts from login's, may not reach it: terminal-code's and herdr's
// installers use ~/.local/bin (or $XDG_BIN_HOME), Homebrew the other two.
func toolDirs(getenv func(string) string) []string {
	dirs := []string{getenv("XDG_BIN_HOME")}
	if home := getenv("HOME"); home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	return append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
}

// findTool is name as an absolute path: on PATH, else in toolDirs. Not
// found, it is the bare name, so a failure still says what was looked for.
func findTool(getenv func(string) string, name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return host.ResolveExec(p)
	}
	for _, d := range toolDirs(getenv) {
		if d == "" {
			continue
		}
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return name
}

// layout makes the columns, or makes again the ones the captain closed.
func (c *consoleColumns) layout(ctx context.Context) error { return c.h.Layout(ctx, c.cols) }

// show tells a column what to run. A column that is gone is made again,
// once, and asked again.
func (c *consoleColumns) show(ctx context.Context, socket string, cmd panerun.Command) error {
	err := panerun.Send(ctx, socket, cmd)
	if !errors.Is(err, panerun.ErrGone) {
		return err
	}
	if err := c.layout(ctx); err != nil {
		return err
	}
	err = c.waitSend(ctx, socket, cmd)
	if !errors.Is(err, panerun.ErrGone) {
		return err
	}
	// The pane is there but its runner is not: a column that died under
	// a pane the host keeps. Nothing else can revive it, so the columns
	// are made afresh.
	if err := c.h.Close(ctx); err != nil {
		return err
	}
	if err := c.layout(ctx); err != nil {
		return err
	}
	return c.waitSend(ctx, socket, cmd)
}

// waitSend retries a send while a column just made is still starting its
// runner.
func (c *consoleColumns) waitSend(ctx context.Context, socket string, cmd panerun.Command) error {
	deadline := time.Now().Add(columnStartWait)
	for {
		err := panerun.Send(ctx, socket, cmd)
		if !errors.Is(err, panerun.ErrGone) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// columnStartWait is how long a new column's runner has to open its socket;
// a variable so a test need not wait it out.
var columnStartWait = 5 * time.Second

// close ends both columns, and with them their panes: the next Console
// lays out its own.
func (c *consoleColumns) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, s := range []string{c.stage, c.review} {
		if s != "" {
			_ = panerun.Send(ctx, s, panerun.Command{Exit: true})
		}
	}
	_ = c.h.Close(ctx)
	_ = os.RemoveAll(c.dir)
}

// consoleStage is the Console's Enter seam: the stage attaches the row's
// agent, and the review opens the row's repo. A nil columns yields a nil
// StageFunc, which the Console reads as "no next pane".
func consoleStage(ws *store.Workspace, c *consoleColumns) console.StageFunc {
	if c == nil {
		return nil
	}
	return func(ctx context.Context, target console.StageTarget) error {
		ref, meta, stageErr := stageRef(ws, target)
		// The review goes first and does not wait on the agent: a stopped
		// Mate's repo still has changes worth reading.
		if err := c.showReview(ctx, ws, target, meta); err != nil {
			if stageErr != nil {
				return stageErr
			}
			return err
		}
		if stageErr != nil {
			return stageErr
		}
		return c.show(ctx, c.stage, panerun.Command{Argv: host.AttachArgv(c.herdr, ref.HerdrSession, ref.AgentName), Env: c.env})
	}
}

// showReview opens the row's repo in the review column; nothing without
// terminal-code.
func (c *consoleColumns) showReview(ctx context.Context, ws *store.Workspace, target console.StageTarget, meta map[string]string) error {
	if c.review == "" {
		return nil
	}
	folder, err := reviewFolder(ws, target, meta)
	if err != nil {
		return fmt.Errorf("file changes: %w", err)
	}
	if err := c.show(ctx, c.review, panerun.Command{Argv: []string{c.tode, "--review", folder}, Dir: folder, Env: c.env}); err != nil {
		return fmt.Errorf("file changes: %w", err)
	}
	return nil
}

// reviewFolder is what the review column opens for a row: a crew's
// worktree, where its changes are; for a Mate, which has none, the
// project's first repo.
func reviewFolder(ws *store.Workspace, target console.StageTarget, meta map[string]string) (string, error) {
	if target.Kind == console.StageCrew {
		if wt := meta[spawn.MetaWorktree]; wt != "" {
			// The meta records it relative to the workspace root.
			if !filepath.IsAbs(wt) {
				wt = filepath.Join(ws.Root(), wt)
			}
			if _, err := os.Stat(wt); err != nil {
				return "", fmt.Errorf("the worktree of crew %s is gone", target.ID)
			}
			return wt, nil
		}
		return "", fmt.Errorf("crew %s records no worktree", target.ID)
	}
	cfg, err := ws.LoadProject(target.ProjectID)
	if err != nil {
		return "", err
	}
	if len(cfg.Repos) == 0 {
		return "", fmt.Errorf("project %s has no repo yet", target.ProjectID)
	}
	return ws.RepoDir(cfg.Repos[0].Path), nil
}

// stageRef resolves the Herdr identity of the agent a target names out
// of its `.meta` - `mate.meta` for a Mate, `crews/<id>.meta` for a crew -
// which is the only record there is (internal/spawn/doc.go). The meta is
// re-read on every open rather than captured from the Console's snapshot:
// the snapshot can be a refresh old, and attaching a pane to an agent
// nobody owns is exactly the failure that record is a hint about, not
// proof of.
func stageRef(ws *store.Workspace, target console.StageTarget) (runtime.AgentSessionRef, map[string]string, error) {
	if target.ProjectID == "" {
		return runtime.AgentSessionRef{}, nil, observability.NewError(observability.CodeUsage,
			"the stage target names no Project")
	}
	var (
		meta    map[string]string
		err     error
		stopped error
	)
	switch target.Kind {
	case console.StageMate:
		meta, err = ws.ReadMateMeta(target.ProjectID)
		stopped = errMateStopped(target.ProjectID)
	case console.StageCrew:
		if target.ID == "" {
			return runtime.AgentSessionRef{}, nil, observability.NewError(observability.CodeUsage,
				"the stage target names no crew")
		}
		meta, err = ws.ReadCrewMeta(target.ProjectID, target.ID)
		stopped = errCrewStopped(target.ProjectID, target.ID)
	default:
		return runtime.AgentSessionRef{}, nil, observability.NewError(observability.CodeUsage,
			fmt.Sprintf("there is no stage target of kind %q", target.Kind))
	}
	if err != nil {
		return runtime.AgentSessionRef{}, nil, err
	}
	if meta[spawn.MetaAgent] == "" || meta[spawn.MetaPane] == "" {
		// An agent with no pane is the same stopped record seen from the
		// other side: StopMate and StopCrew drop both keys together, so one
		// without the other is a half-written meta, and neither is
		// something to attach a pane to.
		return runtime.AgentSessionRef{}, meta, stopped
	}
	session := meta[spawn.MetaSession]
	if session == "" {
		session = ws.Session()
	}
	return runtime.AgentSessionRef{HerdrSession: session, AgentName: meta[spawn.MetaAgent]}, meta, nil
}

// errMateStopped is the stopped state, not a failure: a Project whose Mate
// has never been started (or has been stopped) has nothing to show, and
// the reader's next move is the 's' key. It is coded CodeStateConflict
// rather than CodeUnknown so nothing downstream treats it as a transport
// fault.
func errMateStopped(project string) error {
	return observability.NewError(observability.CodeStateConflict,
		fmt.Sprintf("the Mate of %s is stopped; press s to start it", project))
}

// errCrewStopped is the crew counterpart. A stopped crew is not restarted
// from the Console - the Mate spawns a new one - so there is no key to
// offer, only the record that remains.
func errCrewStopped(project, crew string) error {
	return observability.NewError(observability.CodeStateConflict,
		fmt.Sprintf("crew %s of %s is stopped; its record stays in crews/%s", crew, project, crew))
}
