package spawn_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/mateassets"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

func TestStopMateConfirmsTheAgentIsGone(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	started, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}

	res, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if res.Agent != started.Agent || res.AlreadyGone {
		t.Fatalf("stop result = %+v, want it to have stopped a live agent", res)
	}
	if len(rt.Agents) != 0 {
		t.Fatalf("agents left behind: %v", rt.Agents)
	}
	if _, ok := rt.Tabs[started.Pane]; ok {
		t.Fatal("the Mate tab was not closed")
	}
	if !slices.Contains(rt.Calls, "StopAgent:graceful") {
		t.Fatalf("calls %v: Claude has a graceful stop and it must be tried first", rt.Calls)
	}
	if !slices.Equal(rt.ExitPrompts, []string{"/exit"}) || res.Forced != "" {
		t.Fatalf("exit prompts %q, forced %q: the stop types the exit Claude declares, and is not forced", rt.ExitPrompts, res.Forced)
	}

	meta := readMeta(t, w, "shop")
	for _, gone := range []string{spawn.MetaAgent, spawn.MetaPane, spawn.MetaTab, spawn.MetaWorkspace} {
		if _, ok := meta[gone]; ok {
			t.Errorf("meta still carries %s=%s after a stop", gone, meta[gone])
		}
	}
	if meta[spawn.MetaSessionID] != started.SessionID {
		t.Errorf("meta session_id = %q, want %q kept for resume", meta[spawn.MetaSessionID], started.SessionID)
	}
	if meta[spawn.MetaHarness] != "claude" || meta[spawn.MetaSession] != w.Session() {
		t.Errorf("meta = %v, want the harness and Herdr session kept", meta)
	}
	if meta[spawn.MetaStoppedAt] != "2026-09-17T10:00:00Z" {
		t.Errorf("meta stopped_at = %q", meta[spawn.MetaStoppedAt])
	}
}

// claudeWithout is Claude's profile with one capability declared unknown,
// so a test can show the core asks the capability, not the kind.
type claudeWithout struct {
	harness.Claude
	capability string
}

// claudeUnmeasured is the reason claudeWithout declares.
const claudeUnmeasured = "a test harness that never measured it"

func (c claudeWithout) Capabilities() harness.Capabilities {
	caps := c.Claude.Capabilities()
	switch c.capability {
	case "GracefulStop":
		caps.GracefulStop = harness.Cap[harness.GracefulStopper]{Status: harness.CapUnknown, Reason: claudeUnmeasured}
	case "Session":
		caps.Session = harness.Cap[harness.SessionIdentity]{Status: harness.CapUnknown, Reason: claudeUnmeasured}
	case "Hooks":
		caps.Hooks = harness.Cap[harness.HookInstaller]{Status: harness.CapUnsupported, Reason: claudeUnmeasured}
	case "TurnEnd":
		caps.TurnEnd = harness.Cap[harness.TurnEndEvidence]{Status: harness.CapUnknown, Reason: claudeUnmeasured}
	default:
		panic("claudeWithout: no capability " + c.capability)
	}
	return caps
}

// onlyClaudeWithout is a registry whose one harness is Claude lacking
// capability, default for both roles.
func onlyClaudeWithout(t *testing.T, capability string) harness.Registry {
	t.Helper()
	reg, err := harness.NewRegistry(map[harness.AgentRole]harness.Kind{
		harness.RoleMate: harness.KindClaude, harness.RoleCrew: harness.KindClaude,
	}, claudeWithout{capability: capability})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// The stop asks the profile, not the kind: the same Claude Mate is forced
// when the registered profile declares no verified graceful stop, and the
// result says why.
func TestStopMateForcesWhenGracefulStopIsNotVerified(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	deps.Harnesses = onlyClaudeWithout(t, "GracefulStop")
	res, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if slices.Contains(rt.Calls, "StopAgent:graceful") || !slices.Contains(rt.Calls, "StopAgent:force") {
		t.Fatalf("calls %v: a harness without a verified graceful stop is forced, and only forced", rt.Calls)
	}
	if len(rt.Agents) != 0 {
		t.Fatalf("agents left behind: %v", rt.Agents)
	}
	if !strings.Contains(res.Forced, "no verified graceful stop") || !strings.Contains(res.Forced, claudeUnmeasured) {
		t.Fatalf("Forced = %q, want the missing capability and its reason", res.Forced)
	}
}

// A Codex Mate has no verified graceful stop either: it is forced, and the
// result says so rather than leaving the force to be guessed.
func TestStopMateRecordsWhyACodexMateWasForced(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps, _ := codexDeps(t, rt)
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex}); err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	res, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if slices.Contains(rt.Calls, "StopAgent:graceful") || !strings.Contains(res.Forced, "codex harness declares no verified graceful stop") {
		t.Fatalf("calls %v, Forced %q: want a force with its reason", rt.Calls, res.Forced)
	}
}

// A harness without verified hooks cannot run a Mate: the start is refused
// before anything is written or launched, naming the capability.
func TestStartMateRefusesAHarnessWithoutHooks(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	deps.Harnesses = onlyClaudeWithout(t, "Hooks")
	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err == nil || !strings.Contains(err.Error(), "Hooks") || !strings.Contains(err.Error(), claudeUnmeasured) {
		t.Fatalf("StartMate = %v, want a refusal naming Hooks and its reason", err)
	}
	if len(rt.StartArgv) != 0 || len(rt.Tabs) != 0 {
		t.Fatalf("a refused Mate start launched %v and opened %d tab(s)", rt.StartArgv, len(rt.Tabs))
	}
	if _, err := os.Stat(filepath.Join(w.MateDir("shop"), mateassets.ManualName)); !os.IsNotExist(err) {
		t.Fatalf("a refused Mate start rendered its manual (%v)", err)
	}
}

// The same harness still runs a Crew: hooks are a Mate's need only.
func TestSpawnCrewNeedsNoHooks(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	deps.Harnesses = onlyClaudeWithout(t, "Hooks")
	if _, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: harness.KindClaude,
		BriefFile: briefFile(t, w, brieftest.Ship("Add a healthcheck endpoint.\n")),
	}); err != nil {
		t.Fatalf("SpawnCrew on a harness without hooks: %v", err)
	}
	if len(rt.StartArgv) != 1 {
		t.Fatalf("started %d agents, want 1", len(rt.StartArgv))
	}
}

// A harness without a verified session identity never resumes: the start
// goes fresh and says why, instead of launching a resume nobody measured.
func TestStartMateGoesFreshWithoutASessionIdentity(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	ctx := context.Background()
	first, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Resume: true})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if _, err := spawn.StopMate(ctx, w, deps, "shop"); err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	deps.Harnesses = onlyClaudeWithout(t, "Session")
	second, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Resume: true})
	if err != nil {
		t.Fatalf("second StartMate: %v", err)
	}
	if second.Resumed || slices.Contains(lastArgv(t, rt), "--resume") {
		t.Fatalf("resumed %v, argv %v: a harness without a session identity must not resume", second.Resumed, lastArgv(t, rt))
	}
	if !strings.Contains(second.ResumeNote, "no verified session identity") || !strings.Contains(second.ResumeNote, first.SessionID) {
		t.Fatalf("ResumeNote = %q, want the missing capability and the session it could not resume", second.ResumeNote)
	}
}

func TestStopMateFailsWhenTheAgentSurvives(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	// Both stops report success and the agent is still in the inventory.
	rt.StopLeavesAgent = true

	_, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err == nil {
		t.Fatal("a stop that Herdr reported fine but did not perform must fail")
	}
	if !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("error = %v, want it to say the stop is not confirmed", err)
	}
	meta := readMeta(t, w, "shop")
	if meta[spawn.MetaAgent] == "" {
		t.Fatal("an unconfirmed stop must not clear the agent from the meta")
	}
}

func TestStopMateClearsAStaleRecord(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	started, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	rt.ClosePane(started.Pane)

	res, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate over a stale record: %v", err)
	}
	if !res.AlreadyGone {
		t.Fatal("the stop must report that the agent was already gone")
	}
	if meta := readMeta(t, w, "shop"); meta[spawn.MetaAgent] != "" || meta[spawn.MetaSessionID] == "" {
		t.Fatalf("meta = %v, want the agent cleared and the session id kept", meta)
	}
}

func TestStopMateWithoutARecordedAgent(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	if _, err := spawn.StopMate(context.Background(), w, fakeDeps(t, rt), "shop"); err == nil {
		t.Fatal("stopping a project with no recorded Mate must be an error")
	}
}

func TestMateStatusReportsWhatHerdrSees(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)

	stopped, err := spawn.MateStatus(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus: %v", err)
	}
	if stopped.State != spawn.StateStopped {
		t.Fatalf("state = %q, want stopped", stopped.State)
	}

	started, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	running, err := spawn.MateStatus(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus: %v", err)
	}
	if running.State != spawn.StateRunning {
		t.Fatalf("state = %q, want running", running.State)
	}
	if running.Observed != runtime.AgentIdle {
		t.Fatalf("observed = %q, want the Herdr status", running.Observed)
	}
	if !strings.Contains(running.Line(), "mate-shop") || !strings.Contains(running.Line(), "running") {
		t.Fatalf("line = %q", running.Line())
	}

	rt.ClosePane(started.Pane)
	stale, err := spawn.MateStatus(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus: %v", err)
	}
	if stale.State != spawn.StateStale {
		t.Fatalf("state = %q, want stale", stale.State)
	}
	if !strings.Contains(stale.Detail, "mate-shop") {
		t.Fatalf("detail = %q, want the recorded agent named", stale.Detail)
	}
}
