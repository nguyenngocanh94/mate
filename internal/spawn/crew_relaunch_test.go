package spawn_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestRelaunchCrewStartsAFreshAgentInTheSameWorktree(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	// The crew had reported progress before the pane died; that line is the
	// one record the replacement reads, so it must survive untouched.
	if err := os.WriteFile(w.CrewStatus("shop", "k3"), []byte("working: reading the repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The pane is gone and Herdr no longer knows the agent.
	rt.ClosePane(res.Pane)

	rec := &promptRecorder{Fake: rt}
	relaunchDeps := deps
	relaunchDeps.Runtime = rec
	again, err := spawn.RelaunchCrew(context.Background(), w, relaunchDeps, "shop", "k3", "")
	if err != nil {
		t.Fatalf("RelaunchCrew: %v", err)
	}
	if again.Agent != "crew-k3" {
		t.Fatalf("agent = %q, want crew-k3", again.Agent)
	}
	if again.Stopped {
		t.Fatal("no live agent was recorded; the relaunch must not claim it stopped one")
	}
	if !again.AlreadyGone {
		t.Fatal("the recorded agent was gone; AlreadyGone must say so")
	}
	if again.Worktree != res.Worktree || again.BriefPath != res.BriefPath {
		t.Fatalf("relaunch moved the crew: worktree %q brief %q, want %q and %q",
			again.Worktree, again.BriefPath, res.Worktree, res.BriefPath)
	}
	if again.Pane == res.Pane {
		t.Fatal("the relaunch kept the dead pane instead of opening a new one")
	}
	if again.Pane == "" || again.Tab == "" || again.Agent == "" {
		t.Fatalf("relaunch result is missing a live handle: %+v", again)
	}
	if !again.BriefDelivered {
		t.Fatalf("the brief was not confirmed delivered: %s", again.DeliveryWarning)
	}
	// The worktree is still the crew's own branch, untouched by the restart.
	if got := strings.TrimSpace(git(t, again.Worktree, "rev-parse", "--abbrev-ref", "HEAD")); got != "mate/k3" {
		t.Fatalf("worktree branch = %q, want mate/k3", got)
	}
	if status, err := os.ReadFile(w.CrewStatus("shop", "k3")); err != nil || string(status) != "working: reading the repo\n" {
		t.Fatalf("status file = %q, %v; relaunch must not rewrite it", status, err)
	}
	// One prompt reached the new pane, and it is the brief pointer.
	if len(rec.prompts) != 1 {
		t.Fatalf("prompts = %q, want exactly one", rec.prompts)
	}
	if !strings.Contains(rec.prompts[0], res.BriefPath) {
		t.Fatalf("prompt %q does not name the brief at %s", rec.prompts[0], res.BriefPath)
	}
	// The meta now names the new pane and no longer the dead one.
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaAgent] != again.Agent || meta[spawn.MetaPane] != again.Pane {
		t.Fatalf("meta names agent %q pane %q, want %q and %q",
			meta[spawn.MetaAgent], meta[spawn.MetaPane], again.Agent, again.Pane)
	}
	if meta[spawn.MetaState] != spawn.CrewStateSpawned {
		t.Fatalf("meta state = %q, want spawned (the crew has no terminal state)", meta[spawn.MetaState])
	}
	if meta[spawn.MetaBranch] != "mate/k3" || meta[spawn.MetaTask] == "" {
		t.Fatalf("relaunch dropped the crew's own record: %+v", meta)
	}
}

func TestRelaunchCrewCarriesTheNoteIntoThePrompt(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	rt.ClosePane(res.Pane)
	rec := &promptRecorder{Fake: rt}
	deps.Runtime = rec
	if _, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", "  pushed  two\ncommits  "); err != nil {
		t.Fatalf("RelaunchCrew: %v", err)
	}
	if len(rec.prompts) != 1 {
		t.Fatalf("prompts = %q, want exactly one", rec.prompts)
	}
	got := rec.prompts[0]
	if !strings.Contains(got, "pushed two commits") {
		t.Fatalf("prompt %q does not carry the flattened note", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("prompt %q is not one line", got)
	}
}

func TestRelaunchCrewStopsALiveAgentFirst(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	again, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", "")
	if err != nil {
		t.Fatalf("RelaunchCrew: %v", err)
	}
	if !again.Stopped {
		t.Fatal("a live agent was running; the relaunch must stop it before starting a new one")
	}
	if again.AlreadyGone {
		t.Fatal("the agent was live; AlreadyGone must be false")
	}
	if rt.LiveAgentCount() != 1 {
		t.Fatalf("live agents = %d, want exactly the new one", rt.LiveAgentCount())
	}
	if again.Pane == res.Pane {
		t.Fatal("the relaunch reused the stopped agent's pane")
	}
}

func TestRelaunchCrewSurvivesAHerdrThatIsNotRunning(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	// A machine restart: the Herdr server is gone, and its panes with it.
	rt.ClosePane(res.Pane)
	rt.SessionNotRunning = true

	again, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", "")
	if err != nil {
		t.Fatalf("RelaunchCrew with Herdr down: %v", err)
	}
	if again.Agent == "" || again.Pane == "" {
		t.Fatalf("relaunch result is missing a live handle: %+v", again)
	}
	if !again.AlreadyGone {
		t.Fatal("with no server, no agent was live; AlreadyGone must be true")
	}
}

func TestRelaunchCrewRefusesAClosedCrew(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if res.Agent == "" {
		t.Fatal("the spawn recorded no agent")
	}
	if _, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	before := rt.LiveAgentCount()
	_, err = spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", "")
	if err == nil {
		t.Fatal("relaunching a finished crew must be refused")
	}
	if !strings.Contains(err.Error(), "finished") {
		t.Fatalf("refusal = %v, want it to name the closed state", err)
	}
	if rt.LiveAgentCount() != before {
		t.Fatal("a refused relaunch must not start an agent")
	}
}

func TestRelaunchCrewRefusesWhenTheWorktreeIsGone(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if err := os.RemoveAll(res.Worktree); err != nil {
		t.Fatal(err)
	}
	if _, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", ""); err == nil {
		t.Fatal("relaunching a crew whose worktree is gone must be refused")
	}
}

func TestRelaunchCrewStartsAFreshClaudeSession(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	ids := []string{
		"11111111-2222-3333-4444-555555555555",
		"99999999-8888-7777-6666-555555555555",
	}
	i := 0
	deps.NewSessionID = func() string {
		id := ids[i%len(ids)]
		i++
		return id
	}
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: claude.KindClaude, BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	rt.ClosePane(res.Pane)
	again, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", "")
	if err != nil {
		t.Fatalf("RelaunchCrew: %v", err)
	}
	if again.SessionID == res.SessionID {
		t.Fatalf("session id %q was reused; a relaunch is a fresh session", again.SessionID)
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaSessionID] != again.SessionID {
		t.Fatalf("meta session_id = %q, want %q", meta[spawn.MetaSessionID], again.SessionID)
	}
}

func TestRelaunchCrewReportsTheRecordedModelAndEffort(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: claude.KindClaude, Model: "haiku", Effort: harness.EffortLow,
		BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	rt.ClosePane(res.Pane)
	again, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", "")
	if err != nil {
		t.Fatalf("RelaunchCrew: %v", err)
	}
	if again.Harness != claude.KindClaude || again.Model != "haiku" || again.Effort != harness.EffortLow {
		t.Fatalf("relaunch reports harness %q model %q effort %q, want claude, haiku, low",
			again.Harness, again.Model, again.Effort)
	}
}

// A relaunch that cannot prepare its launch must leave a live crew running:
// the old agent is stopped only once nothing but Herdr can still refuse.
func TestRelaunchCrewThatCannotPrepareLeavesALiveAgentRunning(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: codex.KindCodex, BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	// Codex's discovery file can no longer be written.
	override := codex.CodexInstructionPath(res.Worktree)
	if err := os.Remove(override); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(override, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", ""); err == nil {
		t.Fatal("RelaunchCrew succeeded with an unwritable discovery file")
	}
	if rt.LiveAgentCount() != 1 {
		t.Fatalf("live agents = %d, want the original one still running", rt.LiveAgentCount())
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaPane] != res.Pane {
		t.Fatalf("meta pane = %q, want the untouched %q", meta[spawn.MetaPane], res.Pane)
	}
}

func spawnClaudeCrew(t *testing.T, w *store.Workspace, deps spawn.Deps, rt *runtime.Fake) spawn.CrewResult {
	t.Helper()
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: claude.KindClaude, BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	rt.ClosePane(res.Pane)
	return res
}

func TestRelaunchCrewResumesTheClaudeSession(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res := spawnClaudeCrew(t, w, deps, rt)

	rec := &promptRecorder{Fake: rt}
	deps.Runtime = rec
	again, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", "", spawn.RelaunchOptions{Resume: true})
	if err != nil {
		t.Fatalf("RelaunchCrew: %v", err)
	}
	argv := lastArgv(t, rt)
	if !slices.Contains(argv, "--resume") || !slices.Contains(argv, res.SessionID) {
		t.Fatalf("argv %v does not resume session %s", argv, res.SessionID)
	}
	if slices.Contains(argv, "--session-id") {
		t.Fatalf("argv %v carries --session-id beside --resume", argv)
	}
	if !again.Resumed || again.ResumedFrom != res.SessionID || again.SessionID != res.SessionID {
		t.Fatalf("result = resumed %v from %q id %q, want the spawn's session %s", again.Resumed, again.ResumedFrom, again.SessionID, res.SessionID)
	}
	if len(rec.prompts) != 1 || strings.Contains(rec.prompts[0], "not available") {
		t.Fatalf("prompts = %q, want the plain brief pointer", rec.prompts)
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaSessionID] != res.SessionID || meta[spawn.MetaResumed] != "true" {
		t.Fatalf("meta = %v, want the session kept and resumed=true", meta)
	}
}

func TestRelaunchCrewByHandStaysFresh(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	spawnClaudeCrew(t, w, deps, rt)
	again, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", "")
	if err != nil {
		t.Fatalf("RelaunchCrew: %v", err)
	}
	if slices.Contains(lastArgv(t, rt), "--resume") || again.Resumed {
		t.Fatalf("a relaunch without Resume must start fresh: argv %v resumed %v", lastArgv(t, rt), again.Resumed)
	}
}

func TestRelaunchCrewResumesTheCodexRollout(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps, sessions := codexDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: codex.KindCodex, BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	cwd, err := filepath.EvalSymlinks(res.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	writeCodexRollout(t, sessions, codexSessionA, cwd, deps.Now().Add(2*time.Second))
	rt.ClosePane(res.Pane)

	again, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", "", spawn.RelaunchOptions{Resume: true})
	if err != nil {
		t.Fatalf("RelaunchCrew: %v", err)
	}
	argv := lastArgv(t, rt)
	if !slices.Contains(argv, "resume") || argv[len(argv)-1] != codexSessionA {
		t.Fatalf("argv %v is not `codex resume ... %s`", argv, codexSessionA)
	}
	if !again.Resumed || again.ResumedFrom != codexSessionA {
		t.Fatalf("result = %+v, want resumed from %s", again, codexSessionA)
	}
}

func TestRelaunchCrewGoesFreshWithANoteWhenTheConversationIsGone(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps, _ := codexDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: codex.KindCodex, BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	rt.ClosePane(res.Pane)
	rec := &promptRecorder{Fake: rt}
	deps.Runtime = rec
	// No rollout was written: the conversation lived on another machine.
	again, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", "", spawn.RelaunchOptions{Resume: true})
	if err != nil {
		t.Fatalf("RelaunchCrew: %v", err)
	}
	if again.Resumed || again.ResumeNote == "" {
		t.Fatalf("result = resumed %v note %q, want fresh with a note", again.Resumed, again.ResumeNote)
	}
	if slices.Contains(lastArgv(t, rt), "resume") {
		t.Fatalf("argv %v must not resume", lastArgv(t, rt))
	}
	if len(rec.prompts) != 1 || !strings.Contains(rec.prompts[0], res.BriefPath) || !strings.Contains(rec.prompts[0], "earlier conversation is not available") {
		t.Fatalf("prompts = %q, want the brief pointer with the note", rec.prompts)
	}
}

func TestRelaunchCrewFallsBackToFreshWhenTheResumedLaunchFails(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res := spawnClaudeCrew(t, w, deps, rt)
	rec := &promptRecorder{Fake: rt}
	deps.Runtime = rec
	// The resumed pane shows a screen the settle cannot name; the fake shows
	// it once, then the composer again.
	rt.NextStartupScreen = "ERROR: something claude has never printed before\n"
	again, err := spawn.RelaunchCrew(context.Background(), w, deps, "shop", "k3", "", spawn.RelaunchOptions{Resume: true})
	if err != nil {
		t.Fatalf("RelaunchCrew: %v", err)
	}
	if n := len(rt.StartArgv); n != 3 {
		t.Fatalf("StartAgent calls = %d, want 3 (spawn, failed resume, fresh)", n)
	}
	if !slices.Contains(rt.StartArgv[1], "--resume") || slices.Contains(rt.StartArgv[2], "--resume") {
		t.Fatalf("argv: resume attempt %v, fallback %v", rt.StartArgv[1], rt.StartArgv[2])
	}
	if again.Resumed || !strings.Contains(again.ResumeNote, "resuming the claude session "+res.SessionID+" failed") {
		t.Fatalf("result = resumed %v note %q", again.Resumed, again.ResumeNote)
	}
	if len(rec.prompts) != 1 || !strings.Contains(rec.prompts[0], "earlier conversation is not available") {
		t.Fatalf("prompts = %q, want one prompt carrying the note", rec.prompts)
	}
	meta, _ := w.ReadCrewMeta("shop", "k3")
	if meta[spawn.MetaAgent] != again.Agent || meta[spawn.MetaResumed] != "" {
		t.Fatalf("meta after fallback = %v", meta)
	}
}
