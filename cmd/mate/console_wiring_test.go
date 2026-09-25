package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/host"
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
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(w.Root(), project)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject(project, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	rt := runtime.NewFake()
	deps := spawn.Deps{
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
	snap, err := query.Load(ctx, w)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if snap.Projects[0].Mode != query.ModeManual {
		t.Fatalf("snapshot mode = %q, want manual", snap.Projects[0].Mode)
	}
	if err := w.SetAuto("shop", true); err != nil {
		t.Fatal(err)
	}
	if snap, err = query.Load(ctx, w); err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if snap.Projects[0].Mode != query.ModeAuto {
		t.Fatalf("snapshot mode = %q, want auto", snap.Projects[0].Mode)
	}
}

type recordingHost struct {
	targets []host.StageTarget
}

func (r *recordingHost) EnsureSplit(context.Context) (host.StageHandle, error) {
	return host.StageHandle{PaneID: "pane-1"}, nil
}

func (r *recordingHost) Stage(_ context.Context, t host.StageTarget) (host.StageHandle, error) {
	r.targets = append(r.targets, t)
	return host.StageHandle{PaneID: "pane-1"}, nil
}

func TestConsoleStageResolvesMateMeta(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	action := consoleAction(w, deps)
	if _, err := action(context.Background(), console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	rec := &recordingHost{}
	fn := consoleStage(w, rec)
	if fn == nil {
		t.Fatal("consoleStage on a Host is nil")
	}
	err := fn(context.Background(), console.StageTarget{Kind: console.StageMate, ProjectID: "shop"})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if len(rec.targets) != 1 {
		t.Fatalf("stage calls = %d, want 1", len(rec.targets))
	}
	got := rec.targets[0]
	if got.Session != w.Session() {
		t.Fatalf("session = %q, want workspace session %q", got.Session, w.Session())
	}
	if got.AgentName != "mate-shop" {
		t.Fatalf("agent = %q, want mate-shop", got.AgentName)
	}
}

func TestConsoleStageNilHostIsNil(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	if consoleStage(w, nil) != nil {
		t.Fatal("consoleStage(nil) must be nil")
	}
}

// TestConsoleStageRefusesAStoppedMateWithTheStoppedState: a project whose
// `mate.meta` names no agent has nothing to show. The refusal names the
// stopped state and the key that starts it, and the host is never asked.
func TestConsoleStageRefusesAStoppedMateWithTheStoppedState(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	rec := &recordingHost{}
	fn := consoleStage(w, rec)
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
	if len(rec.targets) != 0 {
		t.Fatalf("a stopped Mate reached the host: %+v", rec.targets)
	}
}

// TestConsoleStageShowsACrewFromItsMeta: a crew resolves exactly like a
// Mate, out of `crews/<id>.meta`, into the Herdr agent it names; once torn
// down it is refused as stopped without reaching the host.
func TestConsoleStageShowsACrewFromItsMeta(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	rec := &recordingHost{}
	fn := consoleStage(w, rec)
	target := console.StageTarget{Kind: console.StageCrew, ID: "k3", ProjectID: "shop"}

	if err := fn(context.Background(), target); err != nil {
		t.Fatalf("show a running crew: %v", err)
	}
	if len(rec.targets) != 1 || rec.targets[0].AgentName != res.Agent || rec.targets[0].Session != w.Session() {
		t.Fatalf("staged %+v, want exactly the crew's own agent %s", rec.targets, res.Agent)
	}

	if _, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", true); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	err := fn(context.Background(), target)
	if err == nil || !strings.Contains(err.Error(), "stopped") || !strings.Contains(err.Error(), "k3") {
		t.Fatalf("error = %v, want the stopped state naming the crew", err)
	}
	if len(rec.targets) != 1 {
		t.Fatalf("the stopped crew still reached the host: %+v", rec.targets)
	}
}
