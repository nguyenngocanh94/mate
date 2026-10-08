package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/host"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/panerun"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/tool"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// consoleColumns are the Console's surfaces (docs/mvp.md M13): the stage
// column, which shows the agent of the row Enter was pressed on, and a tab
// per tool role the tool registry binds a key to - the review tab, which
// `e` opens on a crew's report, and the tasks tab, which `t` opens on a
// project's tracker. A tool opens in a tab in the same window: a column
// left the editor about half the window, and a separate window covered the
// console. Each surface runs `mate pane serve` for its whole life; the
// Console tells it what to show over its socket, and the host never
// re-splits a column or opens a second tab of a role.
type consoleColumns struct {
	h host.Host
	// cols is the stage column.
	cols []host.Column
	// tabs are the tool tabs by role, one per Role a tool binding names,
	// planned whether or not the tool is installed: a missing binary is
	// said when its key is pressed (tool.Viewer.Argv) and on the status
	// line when the Console opens (missingTools).
	tabs  map[string]toolTab
	dir   string
	stage string
	herdr string
	// findTool is a tool binary's absolute path, "" when it is not
	// installed: the seam tool.Viewer.Argv resolves binaries through.
	findTool func(string) string
	// env is what every column's program gets over the pane's own: the
	// Console's PATH.
	env []string
}

// toolTab is one tool tab: the program the host runs in it, and the
// socket that program listens on.
type toolTab struct {
	col    host.Column
	socket string
}

// newConsoleColumns plans the surfaces for this Console: the stage column,
// and a tab for each role tools binds a key to.
func newConsoleColumns(h host.Host, getenv func(string) string, tools tool.Registry) (*consoleColumns, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find the mate binary: %w", err)
	}
	roles := toolRoles(tools)
	sockets := []string{roleStage + ".sock"}
	for _, role := range roles {
		sockets = append(sockets, role+".sock")
	}
	// A short directory: a unix socket path is limited to about 100 bytes.
	dir, err := panerun.SocketDir("mate-cols-", sockets...)
	if err != nil {
		return nil, err
	}
	c := &consoleColumns{
		h: h, dir: dir, stage: filepath.Join(dir, roleStage+".sock"), herdr: findTool(getenv, "herdr"),
		tabs: map[string]toolTab{},
		findTool: func(name string) string {
			if p := findTool(getenv, name); filepath.IsAbs(p) {
				return p
			}
			return ""
		},
	}
	if path := getenv("PATH"); path != "" {
		c.env = []string{"PATH=" + path}
	}
	owner := strconv.Itoa(os.Getpid())
	column := func(role, socket string) host.Column {
		return host.Column{Role: role, Argv: []string{exe, "pane", "serve", "--role", role, "--socket", socket, "--owner", owner}}
	}
	c.cols = []host.Column{column(roleStage, c.stage)}
	for _, role := range roles {
		socket := filepath.Join(dir, role+".sock")
		c.tabs[role] = toolTab{col: column(role, socket), socket: socket}
	}
	return c, nil
}

// toolRoles are the roles tools binds keys to, each once, in binding
// order.
func toolRoles(tools tool.Registry) []string {
	var roles []string
	for _, b := range tools.Bindings() {
		if !slices.Contains(roles, b.Role) {
			roles = append(roles, b.Role)
		}
	}
	return roles
}

// missingTools is a status-line notice for each tool with a console key
// whose binaries findTool does not find: the key, the tool and how to
// install it.
func (c *consoleColumns) missingTools(tools tool.Registry) []string {
	var notices []string
	seen := map[tool.Name]bool{}
	for _, b := range tools.Bindings() {
		if seen[b.Tool] {
			continue
		}
		seen[b.Tool] = true
		p, err := tools.Lookup(b.Tool)
		if err != nil {
			continue
		}
		info := p.Info()
		for _, bin := range info.Binaries {
			if c.findTool(bin) == "" {
				notices = append(notices, fmt.Sprintf("%s %s needs %s: %s", b.Key, b.Label, info.Title, info.Install))
				break
			}
		}
	}
	return notices
}

// toolDirs are where installers put a CLI when a pane's PATH, which
// starts from login's, may not reach it: herdr's installer uses
// ~/.local/bin (or $XDG_BIN_HOME), Homebrew the other two.
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

// close ends every column and tab, and with them their panes: the next
// Console lays out its own.
func (c *consoleColumns) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sockets := []string{c.stage}
	for _, t := range c.tabs {
		sockets = append(sockets, t.socket)
	}
	for _, s := range sockets {
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

// consoleToolView is the Console's seam for the tool keys of the snapshot
// (`e`: the crew's report; `t`: the project's tasks): the tool bound to
// key on the target's row builds its process (tool.Viewer.Argv), and that
// tool's tab beside the Console runs it, in the directory and with the
// environment the tool asked for. A tool whose data the project does not
// have yet gets it made first (consoleToolData). A nil columns yields a
// nil ToolViewFunc.
func consoleToolView(ws *store.Workspace, c *consoleColumns, tools tool.Registry) console.ToolViewFunc {
	if c == nil {
		return nil
	}
	return func(ctx context.Context, key string, target console.StageTarget) error {
		b, err := toolBinding(tools, key, target)
		if err != nil {
			return err
		}
		p, err := tools.Lookup(b.Tool)
		if err != nil {
			return err
		}
		tab, ok := c.tabs[b.Role]
		if !ok {
			return fmt.Errorf("%s: the console has no %s tab", b.Label, b.Role)
		}
		vctx, err := viewerContext(ws, target)
		if err != nil {
			return fmt.Errorf("%s: %w", b.Label, err)
		}
		if err := consoleToolData(ctx, ws, target.ProjectID, p); err != nil {
			return fmt.Errorf("%s: %w", b.Label, err)
		}
		// Argv's error is the tool's own sentence: how to install it.
		inv, err := p.Capabilities().Viewer.Impl.Argv(vctx, c.findTool)
		if err != nil {
			return err
		}
		dir := inv.Dir
		if dir == "" {
			dir = vctx.CrewDir
		}
		if dir == "" {
			dir = vctx.ProjectDir
		}
		cmd := panerun.Command{
			Argv: append([]string{inv.Name}, inv.Args...),
			Dir:  dir,
			Env:  append(slices.Clone(c.env), inv.Env...),
		}
		if err := c.showRoleTab(ctx, tab.col, tab.socket, cmd); err != nil {
			return fmt.Errorf("%s: %w", b.Label, err)
		}
		return nil
	}
}

// consoleToolData makes a tool's data for project when the tool keeps data
// and the project has none yet, the way `mate tool beads` makes the tracker
// before opening the viewer: Beads Viewer on a project with no tracker
// shows nothing. The viewer's export is not refreshed here when the data
// exists: bv reads the tracker itself (--db). Making it is refused while
// older data sits where toolDataRefusal says. The tool's own
// diagnostic, if it failed, is the last line of what it wrote.
func consoleToolData(ctx context.Context, ws *store.Workspace, project string, p tool.Profile) error {
	data := p.Capabilities().Data
	if !data.Verified() {
		return nil
	}
	exists, err := data.Impl.Exists(ws.ProjectHome(project))
	if err != nil || exists {
		return err
	}
	if err := toolDataRefusal(ws, project, p, false); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, toolCommandTimeout)
	defer cancel()
	var diagnostic bytes.Buffer
	if err := data.Impl.Init(ctx, toolEnv(ws, project, p), &diagnostic); err != nil {
		lines := strings.Split(strings.TrimSpace(diagnostic.String()), "\n")
		if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
			return fmt.Errorf("%w: %s", err, last)
		}
		return err
	}
	return nil
}

// toolBinding is the binding of key on the target's row: a crew target is
// a crew row, a target with only a project a project row. A key bound on
// another row only says where it acts.
func toolBinding(tools tool.Registry, key string, target console.StageTarget) (tool.Binding, error) {
	var scope tool.BindScope
	switch {
	case target.Kind == console.StageCrew && target.ID != "" && target.ProjectID != "":
		scope = tool.ScopeCrew
	case target.Kind == "" && target.ID == "" && target.ProjectID != "":
		scope = tool.ScopeProject
	}
	var other *tool.Binding
	for _, b := range tools.Bindings() {
		if b.Key != key {
			continue
		}
		if b.Scope == scope {
			return b, nil
		}
		if other == nil {
			other = &b
		}
	}
	if other != nil {
		return tool.Binding{}, fmt.Errorf("%s opens a %s's %s", key, other.Scope, other.Label)
	}
	return tool.Binding{}, fmt.Errorf("no tool bound to %s", key)
}

// viewerContext is what a tool opens on for target: the project, and for a
// crew its folder and report.md (reviewReport).
func viewerContext(ws *store.Workspace, target console.StageTarget) (tool.ViewerContext, error) {
	vctx := tool.ViewerContext{ProjectDir: ws.ProjectHome(target.ProjectID)}
	if target.Kind != console.StageCrew {
		return vctx, nil
	}
	dir, file, err := reviewReport(ws, target)
	if err != nil {
		return tool.ViewerContext{}, err
	}
	vctx.CrewDir, vctx.ReportPath = dir, file
	return vctx, nil
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

// showRoleTab tells the tab of col's role what to run, leaving other tabs
// and the agent stage intact. A tab that is gone - never opened, or closed
// - is opened, and asked again. A tab whose runner died under a pane the
// host keeps is closed and opened again; that does not touch the stage
// column. A tab already showing is brought to the front after what it
// shows changes.
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

// reviewReport is what a crew key opens its tool on: the crew's own folder
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
