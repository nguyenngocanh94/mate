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

// consoleColumns are the Console's surfaces (docs/mvp.md M13): the stage
// column, which shows the agent of the row Enter was pressed on, and the
// review tab, which `e` opens on a crew's report. The review is a tab in
// the same window: a column left Fresh about half the window, and a
// separate window covered the console. Each surface runs `mate pane serve`
// for its whole life; the Console tells it what to show over its socket,
// and the host never re-splits a column or opens a second review tab.
type consoleColumns struct {
	h host.Host
	// cols is the stage column. reviewCol is the program of the review
	// tab; its Role is empty when Fresh is not installed.
	cols      []host.Column
	reviewCol host.Column
	tasksCol  host.Column
	dir       string
	stage     string
	review    string // "" when Fresh is not installed
	tasks     string
	editor    string
	herdr     string
	// env is what every column's program gets over the pane's own: the
	// Console's PATH.
	env []string
}

// newConsoleColumns plans the surfaces for this Console: the stage column
// always, the review tab when the Fresh editor (`fresh`) is installed.
func newConsoleColumns(h host.Host, getenv func(string) string) (*consoleColumns, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find the mate binary: %w", err)
	}
	// A short directory: a unix socket path is limited to about 100 bytes.
	dir, err := panerun.SocketDir("mate-cols-", roleStage+".sock", roleReview+".sock", roleTasks+".sock")
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
	c.tasks = filepath.Join(dir, roleTasks+".sock")
	c.tasksCol = column(roleTasks, c.tasks)
	if editor := findTool(getenv, "fresh"); filepath.IsAbs(editor) {
		c.editor, c.review = editor, filepath.Join(dir, roleReview+".sock")
		c.reviewCol = column(roleReview, c.review)
	}
	return c, nil
}

// toolDirs are where installers put a CLI when a pane's PATH, which
// starts from login's, may not reach it: herdr's installer uses
// ~/.local/bin (or $XDG_BIN_HOME), Homebrew (Fresh) the other two.
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

// layout makes the stage, or makes it again if the captain closed it.
func (c *consoleColumns) layout(ctx context.Context) error { return c.h.Layout(ctx, c.cols) }

// show tells a column what to run. A column that is gone - never made, or
// closed - is made, and asked again. cols is the layout the column belongs
// to.
func (c *consoleColumns) show(ctx context.Context, cols []host.Column, socket string, cmd panerun.Command) error {
	err := panerun.Send(ctx, socket, cmd)
	if !errors.Is(err, panerun.ErrGone) {
		return err
	}
	if err := c.h.Layout(ctx, cols); err != nil {
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
	if err := c.h.Layout(ctx, cols); err != nil {
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
	for _, s := range []string{c.stage, c.review, c.tasks} {
		if s != "" {
			_ = panerun.Send(ctx, s, panerun.Command{Exit: true})
		}
	}
	_ = c.h.Close(ctx)
	_ = os.RemoveAll(c.dir)
}

// consoleStage is the Console's Enter seam: the stage attaches the row's
// agent and stays there. The report tab is `e`, not Enter. A nil columns
// yields a nil StageFunc, which the Console reads as "no next pane".
func consoleStage(ws *store.Workspace, deps spawn.Deps, c *consoleColumns) console.StageFunc {
	if c == nil {
		return nil
	}
	return func(ctx context.Context, target console.StageTarget) error {
		ref, _, err := stageRef(ws, target)
		if err != nil {
			return err
		}
		if err := herdrSession(ctx, ws, deps); err != nil {
			return err
		}
		return c.show(ctx, c.cols, c.stage, panerun.Command{Argv: host.AttachArgv(c.herdr, ref.HerdrSession, ref.AgentName), Env: c.env})
	}
}

// consoleReview is the Console's `e` seam: a tab beside the Console runs
// Fresh on that crew's report. A nil columns yields a nil ReviewFunc.
func consoleReview(ws *store.Workspace, c *consoleColumns) console.ReviewFunc {
	if c == nil {
		return nil
	}
	return func(ctx context.Context, target console.StageTarget) error {
		if c.review == "" {
			return fmt.Errorf("a crew's report needs the Fresh editor: brew install fresh-editor")
		}
		if target.Kind != console.StageCrew || target.ID == "" || target.ProjectID == "" {
			return fmt.Errorf("e opens a crew's report")
		}
		dir, file, err := reviewReport(ws, target)
		if err != nil {
			return fmt.Errorf("report: %w", err)
		}
		argv := []string{c.editor, dir}
		if file != "" {
			argv = []string{c.editor, file}
		}
		if err := c.showTab(ctx, panerun.Command{Argv: argv, Dir: dir, Env: c.env}); err != nil {
			return fmt.Errorf("report: %w", err)
		}
		return nil
	}
}

// herdrSession refuses to attach to an agent whose Herdr session is not up.
// `herdr agent attach` against a dead server prints its own error and exits
// nonzero: the stage column would keep that text on screen as a stray
// terminal, and nothing on the Console would explain it (measured
// 2026-09-30, after a Herdr server was lost to a machine restart). The
// session is asked first - LookupSession never starts a server - so the
// refusal lands on the Console's status line and no column is touched.
//
// It asks through the runtime adapter the Console already pointed at the
// `herdr` binary the stage column runs (findTool's, which can reach
// ~/.local/bin when the Console's PATH cannot). Asking by the bare name
// instead made the check and the attach disagree about whether Herdr exists
// at all, and refused an attach that would have worked.
//
// Only a session that is positively down is reworded to a stopped session.
// A different runtime failure - the executable would not run, a transport
// fault - is its own sentence: the attach is a separate subprocess and may
// still be the path that works.
func herdrSession(ctx context.Context, ws *store.Workspace, deps spawn.Deps) error {
	spec, err := spawn.SessionSpec(deps, ws)
	if err != nil {
		return err
	}
	_, running, err := deps.Runtime.LookupSession(ctx, spec)
	if err != nil {
		if runtime.IsServerNotRunning(err) {
			return herdrDown(spec.Name)
		}
		return err
	}
	if !running {
		return herdrDown(spec.Name)
	}
	return nil
}

// herdrDown is the one sentence both the stage refusal and the observer's
// runtime notice use for a session that is positively not up.
func herdrDown(session string) error {
	return observability.NewError(observability.CodeRuntimeUnavailable,
		fmt.Sprintf("herdr is not running; start or resume the Mate with s to bring it back (session %s)", session))
}

// showTab tells the review tab what to run. A tab that is gone - never
// opened, or closed - is opened, and asked again. A tab whose runner died
// under a pane the host keeps is closed and opened again; that does not
// touch the stage column. A tab already showing is brought to the front
// after the file changes.
func (c *consoleColumns) showTab(ctx context.Context, cmd panerun.Command) error {
	return c.showRoleTab(ctx, c.reviewCol, c.review, cmd)
}

// showRoleTab opens/reuses only the requested surface, leaving other tabs
// and the agent stage intact.
func (c *consoleColumns) showRoleTab(ctx context.Context, col host.Column, socket string, cmd panerun.Command) error {
	err := panerun.Send(ctx, socket, cmd)
	if !errors.Is(err, panerun.ErrGone) {
		if err != nil {
			return err
		}
		return c.h.Front(ctx, col.Role)
	}
	if err := c.h.Tab(ctx, col); err != nil {
		return err
	}
	err = c.waitSend(ctx, socket, cmd)
	if !errors.Is(err, panerun.ErrGone) {
		return err
	}
	if err := c.h.Close(ctx, col.Role); err != nil {
		return err
	}
	if err := c.h.Tab(ctx, col); err != nil {
		return err
	}
	return c.waitSend(ctx, socket, cmd)
}

func consoleTasks(ws *store.Workspace, c *consoleColumns) console.TasksFunc {
	if c == nil {
		return nil
	}
	return func(ctx context.Context, project string) error {
		if _, err := ws.BeadsDir(project); err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return c.showRoleTab(ctx, c.tasksCol, c.tasks, panerun.Command{
			Argv: []string{exe, "tasks", project, "--workspace", ws.Root()}, Dir: ws.Root(), Env: c.env,
		})
	}
}

// reviewReport is where `e` opens Fresh: the crew's own folder
// (`crews/<id>/`, the one that crew worked in), and report.md inside it
// when the crew has written one. The folder stays after the crew stops.
func reviewReport(ws *store.Workspace, target console.StageTarget) (dir, file string, err error) {
	dir = ws.CrewDir(target.ProjectID, target.ID)
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return "", "", fmt.Errorf("crew %s has no folder", target.ID)
	}
	report := ws.CrewReport(target.ProjectID, target.ID)
	if st, err := os.Stat(report); err == nil && !st.IsDir() {
		return dir, report, nil
	}
	return dir, "", nil
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
