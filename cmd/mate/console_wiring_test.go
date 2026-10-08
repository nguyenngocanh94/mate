package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/host"
	"github.com/nguyenngocanh94/mate/internal/panerun"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// consoleFixture is a workspace with one registered git project and a spawn
// Deps over the fake Herdr adapter: the same code path cmdConsole builds,
// with the runtime replaced. Nothing here stubs internal/spawn itself - the
// point of these tests is that the Console's ActionFunc really does drive
// StartMate/StopMate and that `mate.meta` moves as a result.
func consoleFixture(t *testing.T, project string) (*store.Workspace, spawn.Deps) {
	t.Helper()
	root := t.TempDir()
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(w.ProjectHome(project), project)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject(project, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	rt := runtime.NewFake()
	deps := spawn.Deps{
		Harnesses:            harnesses,
		Runtime:              rt,
		Names:                rt.Names,
		ConfigHome:           t.TempDir(),
		Binary:               filepath.Join(t.TempDir(), "mate"),
		ReadinessTimeout:     time.Second,
		StartupPromptTimeout: 50 * time.Millisecond,
		StartTimeout:         10 * time.Second,
		Sleep:                func(context.Context, time.Duration) error { return nil },
		Now:                  func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) },
		NewSessionID:         func() string { return "11111111-2222-3333-4444-555555555555" },
	}
	return w, deps
}

// TestConsoleActionStartAndStopMoveTheMateMeta is the seam mvp.md task 09
// replaced: before it, every Console action refused by naming a task. A
// start through ActionFunc must write `mate.meta`'s agent and pane, and a
// stop through the same seam must drop them again while keeping the
// session id a later resume needs.
func TestConsoleActionStartAndStopMoveTheMateMeta(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	action := consoleAction(w, deps)
	ctx := context.Background()

	if meta, err := w.ReadMateMeta("shop"); err != nil || meta[spawn.MetaAgent] != "" {
		t.Fatalf("before the start, mate.meta = %v (err %v), want no agent", meta, err)
	}

	out, err := action(ctx, console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(out, "mate-shop") {
		t.Fatalf("start outcome = %q, want it to name the agent it started", out)
	}
	started, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatalf("ReadMateMeta after start: %v", err)
	}
	if started[spawn.MetaAgent] != "mate-shop" {
		t.Fatalf("meta agent after start = %q, want mate-shop", started[spawn.MetaAgent])
	}
	if started[spawn.MetaPane] == "" {
		t.Fatal("meta records no pane after a start")
	}

	stopOut, err := action(ctx, console.ActionRequest{Action: console.ActionStop, Target: "shop", TargetKind: "mate"})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(stopOut, "mate-shop") {
		t.Fatalf("stop outcome = %q, want it to name the agent it stopped", stopOut)
	}
	stopped, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatalf("ReadMateMeta after stop: %v", err)
	}
	if stopped[spawn.MetaAgent] != "" || stopped[spawn.MetaPane] != "" {
		t.Fatalf("meta after stop still names a live agent/pane: %v", stopped)
	}
	if stopped[spawn.MetaSessionID] != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("stop dropped the harness session id: %v", stopped)
	}
}

// TestConsoleActionStartPropagatesTheSpawnErrorVerbatim: a second start is
// refused by StartMate because the first one is still live, and the
// Console must show that refusal, not a reworded or swallowed one.
func TestConsoleActionStartPropagatesTheSpawnErrorVerbatim(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	action := consoleAction(w, deps)
	ctx := context.Background()

	if _, err := action(ctx, console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("first start: %v", err)
	}
	out, err := action(ctx, console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"})
	if err == nil {
		t.Fatalf("a second start returned %q and no error; want the running Mate refusal", out)
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("error = %v, want spawn's own already-running refusal", err)
	}
}

// TestConsoleActionRepairReportsWhatHerdrSays: 'repair' is `mate status` -
// it re-asks Herdr about the agent mate.meta names and reports the answer.
func TestConsoleActionRepairReportsWhatHerdrSays(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	action := consoleAction(w, deps)
	ctx := context.Background()

	out, err := action(ctx, console.ActionRequest{Action: console.ActionRepair, Target: "shop", TargetKind: "mate"})
	if err != nil {
		t.Fatalf("repair on a project with no Mate: %v", err)
	}
	if !strings.Contains(out, string(spawn.StateStopped)) {
		t.Fatalf("repair outcome = %q, want the stopped state", out)
	}

	if _, err := action(ctx, console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	out, err = action(ctx, console.ActionRequest{Action: console.ActionRepair, Target: "shop", TargetKind: "mate"})
	if err != nil {
		t.Fatalf("repair on a running Mate: %v", err)
	}
	if !strings.Contains(out, string(spawn.StateRunning)) || !strings.Contains(out, "mate-shop") {
		t.Fatalf("repair outcome = %q, want the running state and the agent name", out)
	}
}

// TestConsoleActionModeTogglesTheAutoFlagAndTheLabel: 'm' only moves the
// flag. The daemon (mvp.md task 19) re-reads that file every tick and again
// at the moment it types, so the keystroke needs no channel to it, and the
// outcome line names the window rather than only the word - "auto" alone
// does not tell a reader when the first line might land in the Mate's pane.
func TestConsoleActionModeTogglesTheAutoFlagAndTheLabel(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	action := consoleAction(w, deps)
	ctx := context.Background()

	if w.Auto("shop") {
		t.Fatal("a fresh project is not manual")
	}
	out, err := action(ctx, console.ActionRequest{Action: console.ActionMode, Target: "shop", TargetKind: "project"})
	if err != nil {
		t.Fatalf("mode: %v", err)
	}
	if !w.Auto("shop") {
		t.Fatal("the .auto flag was not created")
	}
	if !strings.Contains(out, string(query.ModeAuto)) || !strings.Contains(out, autopilot.DefaultInterval.String()) {
		t.Fatalf("mode outcome = %q, want the new label and the digest window", out)
	}

	out, err = action(ctx, console.ActionRequest{Action: console.ActionMode, Target: "shop", TargetKind: "project"})
	if err != nil {
		t.Fatalf("mode back: %v", err)
	}
	if w.Auto("shop") {
		t.Fatal("the .auto flag was not removed")
	}
	if !strings.Contains(out, string(query.ModeManual)) {
		t.Fatalf("mode outcome = %q, want the manual label", out)
	}

	// And the snapshot the Console renders follows the flag.
	snap, err := query.Load(ctx, w, consoleHarnesses(), consoleTools())
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if snap.Projects[0].Mode != query.ModeManual {
		t.Fatalf("snapshot mode = %q, want manual", snap.Projects[0].Mode)
	}
	if err := w.SetAuto("shop", true); err != nil {
		t.Fatal(err)
	}
	if snap, err = query.Load(ctx, w, consoleHarnesses(), consoleTools()); err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if snap.Projects[0].Mode != query.ModeAuto {
		t.Fatalf("snapshot mode = %q, want auto", snap.Projects[0].Mode)
	}
}

// roleReview is the tab the report viewer's key binding names; rolePlan
// is a project-scoped fake tool's (planTool).
const (
	roleReview = "review"
	rolePlan   = "plan"
)

// recordingColumns is a Console's stage column, review tab and tasks tab
// with a recorder listening on each socket in place of `mate pane serve`:
// what each surface was told to show, in order. Its findTool finds every
// binary under /opt.
type recordingColumns struct {
	*consoleColumns
	mu          sync.Mutex
	shown       map[string][]panerun.Command
	layouts     int
	fronts      int
	closedRoles []string
}

type layoutCounter struct {
	n      *int
	closed *[]string
	fronts *int
}

func (l layoutCounter) Layout(context.Context, []host.Column) error { *l.n++; return nil }
func (layoutCounter) Tab(context.Context, host.Column) error        { return nil }
func (l layoutCounter) Front(context.Context, string) error {
	if l.fronts != nil {
		*l.fronts++
	}
	return nil
}
func (l layoutCounter) Close(_ context.Context, roles ...string) error {
	*l.closed = append(*l.closed, roles...)
	return nil
}

func newRecordingColumns(t *testing.T) *recordingColumns {
	t.Helper()
	dir, err := panerun.SocketDir("mc", "stage.sock", "review.sock", "plan.sock", "tasks.sock", "absent.sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	r := &recordingColumns{shown: map[string][]panerun.Command{}}
	r.consoleColumns = &consoleColumns{
		h: layoutCounter{n: &r.layouts, closed: &r.closedRoles, fronts: &r.fronts}, dir: dir,
		stage: filepath.Join(dir, "stage.sock"), tasks: filepath.Join(dir, "tasks.sock"),
		tasksCol: host.Column{Role: roleTasks},
		tabs: map[string]toolTab{
			roleReview: {col: host.Column{Role: roleReview}, socket: filepath.Join(dir, "review.sock")},
			rolePlan:   {col: host.Column{Role: rolePlan}, socket: filepath.Join(dir, "plan.sock")},
		},
		herdr:    "/opt/herdr",
		findTool: func(name string) string { return "/opt/" + name },
	}
	for role, socket := range map[string]string{roleStage: r.stage, roleReview: r.tabs[roleReview].socket, rolePlan: r.tabs[rolePlan].socket, roleTasks: r.tasks} {
		ln, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				line, _ := bufio.NewReader(conn).ReadBytes('\n')
				var cmd panerun.Command
				_ = json.Unmarshal(line, &cmd)
				r.mu.Lock()
				r.shown[role] = append(r.shown[role], cmd)
				r.mu.Unlock()
				_, _ = conn.Write([]byte(`{"ok":true}` + "\n"))
				_ = conn.Close()
			}
		}()
	}
	return r
}

func (r *recordingColumns) of(role string) []panerun.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]panerun.Command(nil), r.shown[role]...)
}

// Enter on a Mate row attaches the Mate in the stage. The report tab is
// `e`, so Enter neither opens nor closes it.
func TestConsoleStageResolvesMateMeta(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	action := consoleAction(w, deps)
	if _, err := action(context.Background(), console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	rec := newRecordingColumns(t)
	fn := consoleStage(w, deps, rec.consoleColumns)
	if fn == nil {
		t.Fatal("consoleStage on columns is nil")
	}
	if err := fn(context.Background(), console.StageTarget{Kind: console.StageMate, ProjectID: "shop"}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	stage := rec.of(roleStage)
	want := []string{"/opt/herdr", "--session", w.Session(), "agent", "attach", "mate-shop", "--takeover"}
	if len(stage) != 1 || !slices.Equal(stage[0].Argv, want) {
		t.Fatalf("stage shown %+v, want %q", stage, want)
	}
	if len(rec.of(roleReview)) != 0 || len(rec.closedRoles) != 0 || rec.fronts != 0 {
		t.Fatalf("review told %+v, host closed %q, fronts %d; Enter leaves the report tab alone", rec.of(roleReview), rec.closedRoles, rec.fronts)
	}
}

// TestConsoleStageRefusesAnAgentWhoseHerdrIsGone: a Mate recorded in
// `mate.meta` whose Herdr session is no longer running has nothing to
// attach to. `herdr agent attach` against a dead server prints its own
// error and exits, which the stage column would leave on screen while the
// Console believed the pane was fine; the refusal is the status line's
// instead, and no column is touched (measured 2026-09-30, after a Herdr
// server was lost to a machine restart).
func TestConsoleStageRefusesAnAgentWhoseHerdrIsGone(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	if _, err := consoleAction(w, deps)(context.Background(), console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatal(err)
	}
	deps.Runtime.(*runtime.Fake).SessionNotRunning = true
	rec := newRecordingColumns(t)
	err := consoleStage(w, deps, rec.consoleColumns)(context.Background(), console.StageTarget{Kind: console.StageMate, ProjectID: "shop"})
	if err == nil || !strings.Contains(err.Error(), "herdr is not running") {
		t.Fatalf("error = %v, want the herdr-not-running refusal", err)
	}
	if len(rec.of(roleStage))+len(rec.of(roleReview)) != 0 {
		t.Fatal("a dead Herdr still reached a column")
	}
}

func TestConsoleStageNilHostIsNil(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	if consoleStage(w, deps, nil) != nil {
		t.Fatal("consoleStage(nil) must be nil")
	}
	if consoleToolView(w, nil, tools) != nil {
		t.Fatal("consoleToolView(nil) must be nil")
	}
}

// TestConsoleStageRefusesAStoppedMateWithTheStoppedState: a project whose
// `mate.meta` names no agent has nothing to show. The refusal names the
// stopped state and the key that starts it, and the host is never asked.
func TestConsoleStageRefusesAStoppedMateWithTheStoppedState(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	rec := newRecordingColumns(t)
	fn := consoleStage(w, deps, rec.consoleColumns)
	target := console.StageTarget{Kind: console.StageMate, ID: "mate:shop", ProjectID: "shop"}

	// No meta at all.
	err := fn(context.Background(), target)
	if err == nil || !strings.Contains(err.Error(), "stopped") || !strings.Contains(err.Error(), "press s") {
		t.Fatalf("error = %v, want the stopped state and the key that starts it", err)
	}

	// A meta left behind by a stop: harness and session id survive, agent
	// and pane do not. That is still the stopped state.
	if err := w.WriteMateMeta("shop", map[string]string{
		spawn.MetaHarness:   "claude",
		spawn.MetaSession:   w.Session(),
		spawn.MetaSessionID: "11111111-2222-3333-4444-555555555555",
		spawn.MetaStoppedAt: "2026-09-17T10:00:00Z",
	}); err != nil {
		t.Fatalf("WriteMateMeta: %v", err)
	}
	if err := fn(context.Background(), target); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("error = %v, want the stopped state", err)
	}
	if len(rec.of(roleStage))+len(rec.of(roleReview)) != 0 {
		t.Fatal("a stopped Mate reached a column")
	}
}

// TestConsoleStageShowsACrewFromItsMeta: a crew resolves exactly like a
// Mate, out of `crews/<id>.meta`, into the Herdr agent it names; once torn
// down it is refused as stopped without reaching the host.
func TestConsoleStageShowsACrewFromItsMeta(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	rec := newRecordingColumns(t)
	fn := consoleStage(w, deps, rec.consoleColumns)
	target := console.StageTarget{Kind: console.StageCrew, ID: "k3", ProjectID: "shop"}

	if err := fn(context.Background(), target); err != nil {
		t.Fatalf("show a running crew: %v", err)
	}
	stage := rec.of(roleStage)
	if len(stage) != 1 || stage[0].Argv[5] != res.Agent || stage[0].Argv[2] != w.Session() {
		t.Fatalf("staged %+v, want exactly the crew's own agent %s", stage, res.Agent)
	}
	if len(rec.of(roleReview)) != 0 || rec.fronts != 0 {
		t.Fatalf("review shown %d, fronts %d; Enter does not open the report", len(rec.of(roleReview)), rec.fronts)
	}
	if rec.layouts != 0 {
		t.Fatalf("laid out %d times; the recorder's columns were all there", rec.layouts)
	}

	if _, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", true); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	err := fn(context.Background(), target)
	if err == nil || !strings.Contains(err.Error(), "stopped") || !strings.Contains(err.Error(), "k3") {
		t.Fatalf("error = %v, want the stopped state naming the crew", err)
	}
	if len(rec.of(roleStage)) != 1 {
		t.Fatal("the stopped crew still reached the stage")
	}
}

// A column the captain closed is laid out again, once, and asked again.
func TestConsoleStageRemakesAClosedColumn(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	if _, err := consoleAction(w, deps)(context.Background(), console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatal(err)
	}
	rec := newRecordingColumns(t)
	gone := filepath.Join(rec.dir, "gone.sock")
	live := rec.stage
	rec.stage = gone
	// The layout "makes" the column again: its socket is the live one.
	rec.h = layoutFunc(func() { rec.layouts++; _ = os.Symlink(live, gone) })
	fn := consoleStage(w, deps, rec.consoleColumns)
	if err := fn(context.Background(), console.StageTarget{Kind: console.StageMate, ProjectID: "shop"}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if rec.layouts != 1 || len(rec.of(roleStage)) != 1 {
		t.Fatalf("layouts %d, stage shown %d; want one relayout and one show", rec.layouts, len(rec.of(roleStage)))
	}
}

type layoutFunc func()

func (f layoutFunc) Layout(context.Context, []host.Column) error { f(); return nil }
func (layoutFunc) Tab(context.Context, host.Column) error        { return nil }
func (layoutFunc) Front(context.Context, string) error           { return nil }
func (layoutFunc) Close(context.Context, ...string) error        { return nil }

// A column whose runner died under a pane the host keeps is made afresh:
// the layout alone sees the pane and makes nothing.
func TestConsoleStageRemakesAColumnWhoseRunnerDied(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	if _, err := consoleAction(w, deps)(context.Background(), console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatal(err)
	}
	rec := newRecordingColumns(t)
	dead := filepath.Join(rec.dir, "dead.sock")
	live := rec.stage
	rec.stage = dead
	closed := 0
	prev := columnStartWait
	columnStartWait = 100 * time.Millisecond
	t.Cleanup(func() { columnStartWait = prev })
	rec.h = closeAware{layout: func() { rec.layouts++ }, close: func() { closed++; _ = os.Symlink(live, dead) }}
	fn := consoleStage(w, deps, rec.consoleColumns)
	if err := fn(context.Background(), console.StageTarget{Kind: console.StageMate, ProjectID: "shop"}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if closed != 1 || rec.layouts != 2 || len(rec.of(roleStage)) != 1 {
		t.Fatalf("closed %d, layouts %d, shown %d; want the columns closed and made again once", closed, rec.layouts, len(rec.of(roleStage)))
	}
}

type closeAware struct{ layout, close func() }

func (c closeAware) Layout(context.Context, []host.Column) error { c.layout(); return nil }
func (closeAware) Tab(context.Context, host.Column) error        { return nil }
func (closeAware) Front(context.Context, string) error           { return nil }
func (c closeAware) Close(_ context.Context, roles ...string) error {
	if len(roles) == 0 { // the review closing is not the columns made afresh
		c.close()
	}
	return nil
}

// `e` opens the report tab the first time. That is a tab, not a column, so
// the stage layout is never asked.
func TestConsoleReviewMakesTheTabForACrew(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	spawnFakeCrew(t, w, deps, "shop", "k3")
	rec := newRecordingColumns(t)
	absent := filepath.Join(rec.dir, "absent.sock")
	live := rec.tabs[roleReview].socket
	rec.tabs[roleReview] = toolTab{col: host.Column{Role: roleReview, Argv: []string{"/bin/mate", "pane", "serve", "--role", roleReview}}, socket: absent}
	var opened []host.Column
	var laid [][]host.Column
	rec.h = reviewHost{
		layout: func(cols []host.Column) { laid = append(laid, cols) },
		open:   func(col host.Column) { opened = append(opened, col); _ = os.Symlink(live, absent) },
	}
	if err := consoleToolView(w, rec.consoleColumns, tools)(context.Background(), "e", console.StageTarget{Kind: console.StageCrew, ID: "k3", ProjectID: "shop"}); err != nil {
		t.Fatal(err)
	}
	if len(laid) != 0 {
		t.Fatalf("laid out %v; the report is a tab, not a column", laid)
	}
	if len(opened) != 1 || opened[0].Role != roleReview {
		t.Fatalf("opened %v, want the report tab once", opened)
	}
	if len(rec.of(roleReview)) != 1 || len(rec.of(roleStage)) != 0 {
		t.Fatal("the report tab was not shown once, or the stage was touched")
	}
}

// A report tab whose runner died under a pane the host keeps is closed
// and opened again. The stage column is left where it is.
func TestConsoleReviewRemakesATabWhoseRunnerDied(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	spawnFakeCrew(t, w, deps, "shop", "k3")
	rec := newRecordingColumns(t)
	dead := filepath.Join(rec.dir, "dead-review.sock")
	live := rec.tabs[roleReview].socket
	rec.tabs[roleReview] = toolTab{col: host.Column{Role: roleReview, Argv: []string{"/bin/mate", "pane", "serve", "--role", roleReview}}, socket: dead}
	prev := columnStartWait
	columnStartWait = 100 * time.Millisecond
	t.Cleanup(func() { columnStartWait = prev })
	opened := 0
	var closed []string
	rec.h = reviewHost{
		open: func(host.Column) { opened++ },
		close: func(roles []string) {
			closed = append(closed, roles...)
			if slices.Contains(roles, roleReview) {
				_ = os.Symlink(live, dead)
			}
		},
	}
	if err := consoleToolView(w, rec.consoleColumns, tools)(context.Background(), "e", console.StageTarget{Kind: console.StageCrew, ID: "k3", ProjectID: "shop"}); err != nil {
		t.Fatal(err)
	}
	if opened != 2 || !slices.Equal(closed, []string{roleReview}) || len(rec.of(roleReview)) != 1 || len(rec.of(roleStage)) != 0 {
		t.Fatalf("opened %d, closed %v, shown %d; want the report tab closed and opened again, and the stage left up", opened, closed, len(rec.of(roleReview)))
	}
}

type reviewHost struct {
	layout func([]host.Column)
	open   func(host.Column)
	close  func([]string)
}

func (h reviewHost) Layout(_ context.Context, cols []host.Column) error {
	if h.layout != nil {
		h.layout(cols)
	}
	return nil
}

func (h reviewHost) Tab(_ context.Context, col host.Column) error {
	if h.open != nil {
		h.open(col)
	}
	return nil
}

func (reviewHost) Front(context.Context, string) error { return nil }

func (h reviewHost) Close(_ context.Context, roles ...string) error {
	if h.close != nil {
		h.close(roles)
	}
	return nil
}

// `e` opens the crew's own folder, and report.md inside it once that file
// exists. The worktree and the workspace `.mate/` are not the report. The
// folder stays after the crew stops, so `e` still opens it.
func TestConsoleReviewOpensTheCrewReport(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	rec := newRecordingColumns(t)
	view := consoleToolView(w, rec.consoleColumns, tools)
	fn := func(ctx context.Context, target console.StageTarget) error { return view(ctx, "e", target) }
	target := console.StageTarget{Kind: console.StageCrew, ID: "k3", ProjectID: "shop"}
	ctx := context.Background()

	if err := fn(ctx, target); err != nil {
		t.Fatalf("open the folder: %v", err)
	}
	dir := w.CrewDir("shop", "k3")
	review := rec.of(roleReview)
	if len(review) != 1 || !slices.Equal(review[0].Argv, []string{"/opt/fresh", dir}) || review[0].Dir != dir {
		t.Fatalf("review shown %+v, want Fresh on the crew folder %s", review, dir)
	}
	wt := res.Worktree
	if !filepath.IsAbs(wt) {
		wt = filepath.Join(w.Root(), wt)
	}
	if review[0].Dir == wt || review[0].Dir == w.StateDir() {
		t.Fatalf("opened %s, which is the worktree or the workspace .mate", review[0].Dir)
	}
	if rec.fronts != 1 {
		t.Fatalf("fronts = %d, want the existing tab selected", rec.fronts)
	}

	report := w.CrewReport("shop", "k3")
	if err := os.WriteFile(report, []byte("# report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fn(ctx, target); err != nil {
		t.Fatalf("open the report: %v", err)
	}
	review = rec.of(roleReview)
	if len(review) != 2 || !slices.Equal(review[1].Argv, []string{"/opt/fresh", report}) || review[1].Dir != dir {
		t.Fatalf("review shown %+v, want Fresh on %s with dir %s", review, report, dir)
	}
	if rec.fronts != 2 {
		t.Fatalf("fronts = %d, want the tab selected again for the new file", rec.fronts)
	}

	if _, err := spawn.StopCrew(ctx, w, deps, "shop", "k3", true); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if err := fn(ctx, target); err != nil {
		t.Fatalf("a stopped crew's report: %v", err)
	}
	if got := rec.of(roleReview); len(got) != 3 || !slices.Equal(got[2].Argv, []string{"/opt/fresh", report}) {
		t.Fatalf("after stop, review shown %+v", got)
	}

	err := fn(ctx, console.StageTarget{Kind: console.StageMate, ProjectID: "shop"})
	if err == nil || !strings.Contains(err.Error(), "e opens a crew's report") {
		t.Fatalf("mate: %v, want a refusal that e opens a crew's report", err)
	}
	err = fn(ctx, console.StageTarget{Kind: console.StageCrew, ID: "nope", ProjectID: "shop"})
	if err == nil || !strings.Contains(err.Error(), "no folder") {
		t.Fatalf("missing crew: %v, want no folder", err)
	}
	rec.findTool = func(string) string { return "" }
	err = fn(ctx, target)
	if err == nil || !strings.Contains(err.Error(), "brew install fresh-editor") {
		t.Fatalf("no fresh: %v", err)
	}
	if len(rec.of(roleReview)) != 3 {
		t.Fatal("the tab was told to run a tool that is not installed")
	}
}
