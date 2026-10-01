package spawn_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
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
	wantSettings, err := harness.ClaudeSettings(deps.Binary)
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
		spawn.MetaHarness:    "claude",
		spawn.MetaAgent:      "mate-shop",
		spawn.MetaPane:       res.Pane,
		spawn.MetaTab:        res.Tab,
		spawn.MetaWorkspace:  res.Workspace,
		spawn.MetaSession:    w.Session(),
		spawn.MetaSessionID:  "11111111-2222-3333-4444-555555555555",
		spawn.MetaStartedAt:  "2026-09-17T10:00:00Z",
		spawn.MetaLaunchedAt: "2026-09-17T10:00:00Z",
		spawn.MetaModel:      "opus",
		spawn.MetaEffort:     "medium",
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

	// The pane died behind mate's back: the meta still names the agent,
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

// TestStartMateFreshMintsANewSessionID covers `mate mate start --fresh`:
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

// startedManual starts a Mate for shop and returns the manual it was given.
func startedManual(t *testing.T, w *store.Workspace) string {
	t.Helper()
	if _, err := spawn.StartMate(context.Background(), w, fakeDeps(t, runtime.NewFake()), spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	manual, err := os.ReadFile(filepath.Join(w.MateDir("shop"), "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(manual)
}

// TestStartMateForAProjectWithNoRepo: a project with no repo still gets a
// Mate (docs/mvp.md M9); its manual says no Crew can be spawned yet.
func TestStartMateForAProjectWithNoRepo(t *testing.T) {
	w, err := store.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	manual := startedManual(t, w)
	if !strings.Contains(manual, "no repo yet") || !strings.Contains(manual, "mate project repo add shop <git-url|repo-path>") {
		t.Fatal("the manual of a project with no repo does not say so, or how one is added")
	}
}

// TestStartMateForAProjectWithTwoRepos: the manual lists every repo with
// its absolute path and its own default branch.
func TestStartMateForAProjectWithTwoRepos(t *testing.T) {
	w := newWorkspace(t, "shop")
	api := filepath.Join(w.Root(), "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, api)
	if _, err := w.AddRepo("shop", store.RepoConfig{Name: "api", Path: api, DefaultBranch: "develop"}); err != nil {
		t.Fatal(err)
	}
	manual := startedManual(t, w)
	for _, row := range []string{
		"| `shop` | `" + w.RepoDir("shop") + "` | `main` |",
		"| `api` | `" + api + "` | `develop` |",
	} {
		if !strings.Contains(manual, row) {
			t.Errorf("the manual does not list the repo row %s", row)
		}
	}
}

// TestStartMateAdoptsAnInterruptedStart: a start interrupted after Herdr
// launched the agent (the Console quit mid-start) leaves the Mate running
// with no mate.meta. The next start adopts it - no second launch, which
// Herdr would refuse by name, and no stop, which would lose its
// conversation.
func TestStartMateAdoptsAnInterruptedStart(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	first, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("first StartMate: %v", err)
	}
	if err := os.Remove(w.MateMeta("shop")); err != nil {
		t.Fatal(err)
	}

	second, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate over an interrupted start: %v", err)
	}
	if got := len(rt.StartArgv); got != 1 {
		t.Fatalf("%d launches, want the running agent adopted, not launched again", got)
	}
	if slices.Contains(rt.Calls, "StopAgent:force") {
		t.Fatalf("calls %v: the adopted Mate was stopped", rt.Calls)
	}
	meta := readMeta(t, w, "shop")
	if meta[spawn.MetaPane] != first.Pane || meta[spawn.MetaAgent] != first.Agent || second.Pane != first.Pane {
		t.Fatalf("meta %v / result pane %q, want the running agent %s in pane %s recorded", meta, second.Pane, first.Agent, first.Pane)
	}
	if !second.Adopted || !strings.Contains(second.ResumeNote, "adopted agent "+first.Agent) {
		t.Fatalf("result note = %q, want it to say the agent was adopted", second.ResumeNote)
	}
}

// TestStartMateLeavesASameNamedAgentElsewhereAlone: an agent holding the
// Mate's name but started outside the Mate directory is not this project's
// Mate. The start refuses, and neither adopts nor stops it.
func TestStartMateLeavesASameNamedAgentElsewhereAlone(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	first, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("first StartMate: %v", err)
	}
	if err := os.Remove(w.MateMeta("shop")); err != nil {
		t.Fatal(err)
	}
	tab := rt.Tabs[first.Pane]
	tab.Cwd = t.TempDir()
	rt.Tabs[first.Pane] = tab

	_, err = spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err == nil || observability.ExitCode(err) != observability.ExitStateConflict || !strings.Contains(err.Error(), "neither adopts nor stops") {
		t.Fatalf("err = %v, want an already-exists refusal saying the agent is left alone", err)
	}
	if slices.Contains(rt.Calls, "StopAgent:force") || len(rt.Agents) != 1 {
		t.Fatalf("calls %v, agents %v: the unrelated agent was touched", rt.Calls, rt.Agents)
	}
	if _, statErr := os.Stat(w.MateMeta("shop")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused start wrote mate.meta: %v", statErr)
	}
}

// TestStartMateAdoptionKeepsTheRunningHarness: adopting never swaps the
// agent's harness; asking for another one is refused with the fix.
func TestStartMateAdoptionKeepsTheRunningHarness(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude}); err != nil {
		t.Fatalf("first StartMate: %v", err)
	}
	if err := os.Remove(w.MateMeta("shop")); err != nil {
		t.Fatal(err)
	}
	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex})
	if err == nil || !strings.Contains(err.Error(), "left a claude Mate running") {
		t.Fatalf("err = %v, want a refusal naming the running claude Mate", err)
	}
	if len(rt.Agents) != 1 || slices.Contains(rt.Calls, "StopAgent:force") {
		t.Fatalf("agents %v calls %v: the running Mate was touched", rt.Agents, rt.Calls)
	}
}

// cancelOnWait cancels the start's context the moment readiness is awaited,
// the way quitting the Console mid-start does, and fails the wait.
type cancelOnWait struct {
	*runtime.Fake
	cancel context.CancelFunc
}

func (c cancelOnWait) WaitAgent(ctx context.Context, h runtime.AgentHandle, until runtime.WaitCondition) (runtime.ObservedAgent, error) {
	c.cancel()
	return runtime.ObservedAgent{}, context.Canceled
}

// TestStartMateCleansUpAfterItsContextIsCancelled: quitting the Console
// mid-start cancels the start's context. The compensation must still stop
// the agent and close the tab it created - with the cancelled context every
// Herdr call would fail at once and leave the Mate running unrecorded.
func TestStartMateCleansUpAfterItsContextIsCancelled(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deps := fakeDeps(t, rt)
	deps.Runtime = cancelOnWait{Fake: rt, cancel: cancel}

	if _, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop"}); err == nil {
		t.Fatal("StartMate succeeded although its context was cancelled mid-start")
	}
	if len(rt.Agents) != 0 || len(rt.Tabs) != 0 {
		t.Fatalf("agents %v tabs %v left behind by a cancelled start", rt.Agents, rt.Tabs)
	}
	if _, statErr := os.Stat(w.MateMeta("shop")); !os.IsNotExist(statErr) {
		t.Fatalf("a cancelled start left a mate.meta: %v", statErr)
	}
}

func TestMateProfileComesFromProjectConfig(t *testing.T) {
	w := newWorkspace(t, "shop")
	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Mate = store.MateConfig{Model: "sonnet", Effort: "high", RefreshContext: 170000}
	if err := w.SaveProject("shop", cfg); err != nil {
		t.Fatal(err)
	}
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	meta := readMeta(t, w, "shop")
	if meta[spawn.MetaModel] != "sonnet" || meta[spawn.MetaEffort] != "high" {
		t.Fatal(meta)
	}
	argv := rt.StartArgv[0]
	for flag, want := range map[string]string{"--model": "sonnet", "--effort": "high"} {
		i := slices.Index(argv, flag)
		if i < 0 || i+1 >= len(argv) || argv[i+1] != want {
			t.Fatal(argv)
		}
	}
}
