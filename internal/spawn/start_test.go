package spawn_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
)

func TestStartMateWritesManualAndMeta(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if res.Agent != "mate-shop" {
		t.Fatalf("agent = %q, want mate-shop", res.Agent)
	}
	if res.Harness != harness.KindClaude {
		t.Fatalf("harness = %q, want claude (the workspace default)", res.Harness)
	}
	if res.StaleMeta {
		t.Fatal("a first start must not report a stale meta")
	}

	mateDir := w.MateDir("shop")
	manual, err := os.ReadFile(filepath.Join(mateDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md: %v", err)
	}
	if !strings.Contains(string(manual), w.Root()) {
		t.Fatal("the rendered manual does not carry the absolute workspace root")
	}
	if _, err := os.Stat(filepath.Join(mateDir, "CLAUDE.md")); err != nil {
		t.Fatalf("CLAUDE.md: %v", err)
	}
	settings, err := os.ReadFile(filepath.Join(mateDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	wantSettings, err := spawn.ClaudeSettings(deps.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(settings) != string(wantSettings) {
		t.Fatalf("settings.json = %s, want %s", settings, wantSettings)
	}
	if !strings.Contains(string(settings), "hook mate-prompt") || !strings.Contains(string(settings), "hook mate-stop") {
		t.Fatalf("settings.json %s does not wire both hooks", settings)
	}

	meta := readMeta(t, w, "shop")
	want := map[string]string{
		spawn.MetaHarness:   "claude",
		spawn.MetaAgent:     "mate-shop",
		spawn.MetaPane:      res.Pane,
		spawn.MetaTab:       res.Tab,
		spawn.MetaWorkspace: res.Workspace,
		spawn.MetaSession:   w.Session(),
		spawn.MetaSessionID: "11111111-2222-3333-4444-555555555555",
		spawn.MetaStartedAt: "2026-09-17T10:00:00Z",
	}
	for k, v := range want {
		if meta[k] != v {
			t.Errorf("meta[%s] = %q, want %q", k, meta[k], v)
		}
		if v == "" {
			t.Errorf("meta[%s] is empty", k)
		}
	}
	if len(meta) != len(want) {
		t.Errorf("meta has %d keys (%v), want exactly %d", len(meta), meta, len(want))
	}

	// The launch carries the session id task 10 resumes from, and the pane
	// the agent started in is the Mate's own cwd.
	if len(rt.StartArgv) != 1 {
		t.Fatalf("StartArgv = %v, want one start", rt.StartArgv)
	}
	argv := rt.StartArgv[0]
	if !slices.Contains(argv, "--session-id") || !slices.Contains(argv, "11111111-2222-3333-4444-555555555555") {
		t.Fatalf("argv %v carries no --session-id", argv)
	}
	// The manual reaches the Mate exactly once, through the CLAUDE.md its
	// cwd already loads, so no context flag is passed (docs/mvp.md task 17).
	if slices.Contains(argv, "--append-system-prompt-file") || slices.Contains(argv, "--append-system-prompt") {
		t.Fatalf("argv %v carries a context flag; the cwd's CLAUDE.md already loads the manual", argv)
	}
	tab, ok := rt.Tabs[res.Pane]
	if !ok {
		t.Fatalf("pane %s is not a live tab", res.Pane)
	}
	if tab.Label != "mate" {
		t.Fatalf("tab label = %q, want mate", tab.Label)
	}
	if tab.Cwd != mateDir {
		t.Fatalf("tab cwd = %q, want %q", tab.Cwd, mateDir)
	}
}

func TestStartMateRefusesWhenHerdrStillHasTheAgent(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatalf("first StartMate: %v", err)
	}

	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err == nil {
		t.Fatal("a second start must be refused while Herdr still has the agent")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("error = %v, want it to say the Mate is already running", err)
	}
	if got := len(rt.StartArgv); got != 1 {
		t.Fatalf("%d starts, want the refusal to have launched nothing", got)
	}
}

func TestStartMateProceedsOverStaleMeta(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	first, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("first StartMate: %v", err)
	}

	// The pane died behind matev2's back: the meta still names the agent,
	// Herdr no longer does.
	rt.ClosePane(first.Pane)

	second, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate over a stale meta: %v", err)
	}
	if !second.StaleMeta {
		t.Fatal("the start must report that the meta it found was stale")
	}
	meta := readMeta(t, w, "shop")
	if meta[spawn.MetaPane] != second.Pane || meta[spawn.MetaPane] == first.Pane {
		t.Fatalf("meta pane = %q, want the new pane %q", meta[spawn.MetaPane], second.Pane)
	}
}

func TestStartMateCompensatesAfterTabCreation(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	// Readiness fails after the agent is live: everything created by this
	// start must be undone.
	rt.WaitErr = errors.New("herdr wait exploded")

	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err == nil {
		t.Fatal("StartMate must fail when readiness cannot be observed")
	}
	if _, statErr := os.Stat(w.MateMeta("shop")); !os.IsNotExist(statErr) {
		t.Fatalf("a failed start left a mate.meta: %v", statErr)
	}
	if len(rt.Agents) != 0 {
		t.Fatalf("agents left behind: %v", rt.Agents)
	}
	if len(rt.Tabs) != 0 {
		t.Fatalf("tabs left behind: %v", rt.Tabs)
	}
	if !slices.Contains(rt.Calls, "StopAgent:force") {
		t.Fatalf("calls %v: compensation did not force-stop the agent", rt.Calls)
	}
}

func TestStartMateCompensationClearsAStaleMeta(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	first, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("first StartMate: %v", err)
	}
	rt.ClosePane(first.Pane)
	rt.WaitErr = errors.New("herdr wait exploded")

	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"}); err == nil {
		t.Fatal("StartMate must fail when readiness cannot be observed")
	}
	if _, statErr := os.Stat(w.MateMeta("shop")); !os.IsNotExist(statErr) {
		t.Fatal("a failed start must not leave the previous mate.meta behind: it names a pane nobody owns")
	}
}

func TestStartMateCodexWritesTheDiscoveryFile(t *testing.T) {
	w := newWorkspace(t, "blog")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "blog", Harness: harness.KindCodex})
	if err != nil {
		t.Fatalf("StartMate codex: %v", err)
	}
	if res.SessionID != "" {
		t.Fatalf("session id = %q, want empty for Codex", res.SessionID)
	}
	override := harness.CodexInstructionPath(w.MateDir("blog"))
	manual, err := os.ReadFile(override)
	if err != nil {
		t.Fatalf("%s: %v", override, err)
	}
	same, err := os.ReadFile(filepath.Join(w.MateDir("blog"), "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(manual) != string(same) {
		t.Fatal("the Codex discovery file must carry the same manual as AGENTS.md")
	}
	meta := readMeta(t, w, "blog")
	if meta[spawn.MetaHarness] != "codex" {
		t.Fatalf("meta harness = %q, want codex", meta[spawn.MetaHarness])
	}
	// session_id is written for every harness and is empty for Codex, which
	// has no launch-time session identity: the key's absence would be a
	// different statement from "there is none".
	if v, ok := meta[spawn.MetaSessionID]; !ok || v != "" {
		t.Fatalf("meta session_id = %q (present %v), want present and empty for Codex", v, ok)
	}
}

// TestStartMateResumesRecordedSessionID covers task 10: a start after a
// stop must resume the harness session mate.meta kept, passing --resume
// (never --session-id) and keeping session_id= unchanged.
func TestStartMateResumesRecordedSessionID(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)

	first, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Resume: true})
	if err != nil {
		t.Fatalf("first StartMate: %v", err)
	}
	if first.Resumed {
		t.Fatal("a first start has nothing to resume")
	}
	if _, err := spawn.StopMate(context.Background(), w, deps, "shop"); err != nil {
		t.Fatalf("StopMate: %v", err)
	}

	second, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Resume: true})
	if err != nil {
		t.Fatalf("second StartMate: %v", err)
	}
	if !second.Resumed {
		t.Fatal("a start after stop must resume the recorded session")
	}
	if second.ResumedFrom != first.SessionID {
		t.Fatalf("resumed from %q, want %q", second.ResumedFrom, first.SessionID)
	}
	if second.SessionID != first.SessionID {
		t.Fatalf("session_id changed on resume: %q -> %q", first.SessionID, second.SessionID)
	}

	argv := rt.StartArgv[len(rt.StartArgv)-1]
	if !slices.Contains(argv, "--resume") || !slices.Contains(argv, first.SessionID) {
		t.Fatalf("resumed argv %v carries no --resume %s", argv, first.SessionID)
	}
	if slices.Contains(argv, "--session-id") {
		t.Fatalf("resumed argv %v must never carry --session-id", argv)
	}

	meta := readMeta(t, w, "shop")
	if meta[spawn.MetaSessionID] != first.SessionID {
		t.Fatalf("meta session_id = %q, want %q", meta[spawn.MetaSessionID], first.SessionID)
	}
	if meta[spawn.MetaResumed] != "true" || meta[spawn.MetaResumedFrom] != first.SessionID {
		t.Fatalf("meta resumed/resumed_from = %q/%q", meta[spawn.MetaResumed], meta[spawn.MetaResumedFrom])
	}
}

// TestStartMateFreshMintsANewSessionID covers `matev2 mate start --fresh`:
// it must overwrite the recorded session_id with a brand new one rather
// than resuming, even though mate.meta still carries one to resume.
func TestStartMateFreshMintsANewSessionID(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	deps.NewSessionID = idSequence(t, "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222")

	first, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Resume: true})
	if err != nil {
		t.Fatalf("first StartMate: %v", err)
	}
	if _, err := spawn.StopMate(context.Background(), w, deps, "shop"); err != nil {
		t.Fatalf("StopMate: %v", err)
	}

	second, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Fresh: true})
	if err != nil {
		t.Fatalf("fresh StartMate: %v", err)
	}
	if second.Resumed {
		t.Fatal("--fresh must never resume")
	}
	if second.SessionID == first.SessionID {
		t.Fatal("--fresh must mint a session id different from the recorded one")
	}

	argv := rt.StartArgv[len(rt.StartArgv)-1]
	if !slices.Contains(argv, "--session-id") || !slices.Contains(argv, second.SessionID) {
		t.Fatalf("fresh argv %v carries no --session-id %s", argv, second.SessionID)
	}
	if slices.Contains(argv, "--resume") {
		t.Fatalf("fresh argv %v must never carry --resume", argv)
	}

	meta := readMeta(t, w, "shop")
	if meta[spawn.MetaSessionID] != second.SessionID {
		t.Fatalf("meta session_id = %q, want the fresh id %q", meta[spawn.MetaSessionID], second.SessionID)
	}
	if meta[spawn.MetaResumed] != "" || meta[spawn.MetaResumedFrom] != "" {
		t.Fatalf("a fresh start must not leave resumed=/resumed_from=, got %q/%q", meta[spawn.MetaResumed], meta[spawn.MetaResumedFrom])
	}
}

// TestStartMateHarnessMismatchFallsBackToFresh covers task 10's rule that a
// recorded session_id from one harness is never handed to a different one:
// mate.meta's harness disagreeing with the requested start must start fresh
// and say so, not silently resume (or fail).
func TestStartMateHarnessMismatchFallsBackToFresh(t *testing.T) {
	w := newWorkspace(t, "blog")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)

	first, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "blog", Harness: harness.KindClaude, Resume: true})
	if err != nil {
		t.Fatalf("first StartMate: %v", err)
	}
	if _, err := spawn.StopMate(context.Background(), w, deps, "blog"); err != nil {
		t.Fatalf("StopMate: %v", err)
	}

	second, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "blog", Harness: harness.KindCodex, Resume: true})
	if err != nil {
		t.Fatalf("StartMate with a different harness: %v", err)
	}
	if second.Resumed {
		t.Fatal("a harness mismatch must never resume")
	}
	if second.ResumeNote == "" {
		t.Fatal("a harness mismatch must say why this start went fresh")
	}
	if second.SessionID != "" {
		t.Fatalf("session id = %q, want empty for Codex", second.SessionID)
	}
	_ = first
}

func idSequence(t *testing.T, ids ...string) func() string {
	t.Helper()
	i := 0
	return func() string {
		if i >= len(ids) {
			t.Fatalf("idSequence exhausted after %d ids", len(ids))
		}
		v := ids[i]
		i++
		return v
	}
}

func TestStartMateRefusesAnUnregisteredProject(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	if _, err := spawn.StartMate(context.Background(), w, fakeDeps(t, rt), spawn.StartRequest{Project: "ghost"}); err == nil {
		t.Fatal("an unregistered project must be refused")
	}
	if len(rt.Calls) != 0 {
		t.Fatalf("calls %v, want nothing to have reached Herdr", rt.Calls)
	}
}
