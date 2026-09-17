package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/ui/console"
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
	if err := w.AddProject(project, store.ProjectConfig{Repo: repo, DefaultBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	rt := runtime.NewFake()
	deps := spawn.Deps{
		Runtime:              rt,
		Names:                rt.Names,
		ConfigHome:           t.TempDir(),
		Binary:               filepath.Join(t.TempDir(), "matev2"),
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
// flag and what the header says - the daemon that acts on auto mode is
// mvp.md task 19, and the outcome line must say so rather than implying
// the Console has started answering the Mate.
func TestConsoleActionModeTogglesTheAutoFlagAndTheLabel(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	action := consoleAction(w, deps)
	ctx := context.Background()

	if w.Auto("shop") {
		t.Fatal("a fresh project is not supervised")
	}
	out, err := action(ctx, console.ActionRequest{Action: console.ActionMode, Target: "shop", TargetKind: "project"})
	if err != nil {
		t.Fatalf("mode: %v", err)
	}
	if !w.Auto("shop") {
		t.Fatal("the .auto flag was not created")
	}
	if !strings.Contains(out, string(query.ModeAuto)) || !strings.Contains(out, "task 19") {
		t.Fatalf("mode outcome = %q, want the new label and the task that makes it act", out)
	}

	out, err = action(ctx, console.ActionRequest{Action: console.ActionMode, Target: "shop", TargetKind: "project"})
	if err != nil {
		t.Fatalf("mode back: %v", err)
	}
	if w.Auto("shop") {
		t.Fatal("the .auto flag was not removed")
	}
	if !strings.Contains(out, string(query.ModeSupervised)) {
		t.Fatalf("mode outcome = %q, want the supervised label", out)
	}

	// And the snapshot the Console renders follows the flag.
	snap, err := query.Load(ctx, w)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if snap.Projects[0].Mode != query.ModeSupervised {
		t.Fatalf("snapshot mode = %q, want supervised", snap.Projects[0].Mode)
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

// TestConsoleSessionStreamRefusesAStoppedMateWithTheStoppedState: a project
// whose `mate.meta` names no agent has nothing to stream. The factory must
// say so in the words the reader can act on - the stopped state and the key
// that starts it - rather than letting a PTY be opened against a pane
// nobody owns and reporting whatever Herdr says about it.
func TestConsoleSessionStreamRefusesAStoppedMateWithTheStoppedState(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	factory := consoleSessionStream(w, runtime.NewFakeSessionStream())
	if factory == nil {
		t.Fatal("a non-nil transport produced no factory")
	}
	target := console.SessionTarget{Kind: console.SessionTargetMate, ID: "mate:shop", ProjectID: "shop"}

	// No meta at all.
	channel, err := factory(context.Background(), target, console.TerminalSize{Cols: 120, Rows: 36})
	if channel != nil {
		t.Fatal("a stopped Mate handed back a channel")
	}
	if err == nil || !strings.Contains(err.Error(), "stopped") || !strings.Contains(err.Error(), "press s") {
		t.Fatalf("error = %v, want the stopped state and the key that starts it", err)
	}

	// A meta left behind by a stop: harness and session id survive, agent
	// and pane do not. That is still the stopped state, not a transport
	// failure.
	if err := w.WriteMateMeta("shop", map[string]string{
		spawn.MetaHarness:   "claude",
		spawn.MetaSession:   w.Session(),
		spawn.MetaSessionID: "11111111-2222-3333-4444-555555555555",
		spawn.MetaStoppedAt: "2026-09-17T10:00:00Z",
	}); err != nil {
		t.Fatalf("WriteMateMeta: %v", err)
	}
	channel, err = factory(context.Background(), target, console.TerminalSize{Cols: 120, Rows: 36})
	if channel != nil {
		t.Fatal("a stopped Mate handed back a channel")
	}
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("error = %v, want the stopped state", err)
	}
}

// TestConsoleSessionMetadataReportsRecordedAndObservedSeparately: the
// metadata reader never reads the pane and never rewrites the meta. It
// reports the lifecycle state `mate status` established and, separately,
// whether Herdr still has the agent.
func TestConsoleSessionMetadataReportsRecordedAndObservedSeparately(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	ctx := context.Background()
	if _, err := consoleAction(w, deps)(ctx, console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	target := console.SessionTarget{Kind: console.SessionTargetMate, ID: "mate:shop", ProjectID: "shop"}

	snap, err := consoleSessionMetadata(w, deps)(ctx, target)
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if snap.RecordedStatus.State != query.Known || snap.RecordedStatus.Value != string(spawn.StateRunning) {
		t.Fatalf("recorded status = %+v, want a known running", snap.RecordedStatus)
	}
	if snap.Runtime.Status != query.Known {
		t.Fatalf("runtime = %+v, want the live agent observed", snap.Runtime)
	}
	if snap.Transcript.Raw != "" || len(snap.Transcript.Entries) != 0 {
		t.Fatal("the metadata reader produced transcript content; the PTY is the only source of the live frame")
	}

	if _, err := consoleAction(w, deps)(ctx, console.ActionRequest{
		Action: console.ActionStop, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	snap, err = consoleSessionMetadata(w, deps)(ctx, target)
	if err != nil {
		t.Fatalf("metadata after stop: %v", err)
	}
	if snap.Runtime.Status != query.Absent {
		t.Fatalf("runtime after stop = %+v, want absent", snap.Runtime)
	}
}
