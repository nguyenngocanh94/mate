package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLiveAutoPolicyMateAnswersADigest is the mvp.md task 20 acceptance
// scenario: not that a digest reaches the Mate's pane (task 19's
// TestLiveAutoDigestReachesTheMate already proves that), but that a real
// Mate, taught the policy in AGENTS.md section 10, acts on a digest the way
// the manual says to.
//
//  1. a real Claude Mate, `.auto` on, and a real Codex crew
//  2. the crew appends `needs-decision: choose colour red or blue for the
//     button` and stops its turn
//  3. one tick of the daemon delivers a digest carrying that question
//  4. the Mate reads the status file itself (the manual forbids deciding
//     from the digest's own truncated excerpt) and answers the crew with
//     `mate send`, recorded in sent.log as `Source: mate` to `crew:k3`
//  5. the crew takes that as a new prompt and appends
//     `wait-mate: chose <red|blue>`
//  6. the Mate does not run `crew stop`: closing a crew on a digest's
//     wait-mate is the captain's word, not the Mate's, per section 4b and
//     section 9's "Closing a Crew" - so the crew's own meta state is still
//     `spawned`, never `finished`
//
// Everything runs through the seams cmdConsole wires (consolePilot's daemon
// over the real Herdr adapter), the same way task 19's live test does.
func TestLiveAutoPolicyMateAnswersADigest(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	root := t.TempDir()
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = names
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("the lab session is provisioned by the runner; this test must not start a Herdr server")
		return nil
	}
	deps := spawn.Deps{
		Harnesses:            harnesses,
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               consoleBinaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	// 1. The Mate, and auto mode on it.
	mateRes, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: claude.KindClaude})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	t.Logf("Mate %s is running on %s in pane %s", mateRes.Agent, mateRes.Harness, mateRes.Pane)
	if err := w.SetAuto("shop", true); err != nil {
		t.Fatalf("SetAuto: %v", err)
	}

	// 2. The crew: ask a decision the Mate must answer, then hand back.
	const brief = `Append needs-decision: choose colour red or blue for the button to the status file and stop; ` +
		`when the Mate answers, append wait-mate: chose <answer> and stop`
	crewRes, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project:   "shop",
		Crew:      "k3",
		Harness:   codex.KindCodex,
		BriefText: brieftest.Ship(brief),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Logf("spawned crew %s in pane %s (branch %s)", crewRes.Agent, crewRes.Pane, crewRes.Branch)
	if crewRes.DeliveryWarning != "" {
		t.Logf("brief delivery warning: %s\n%s", crewRes.DeliveryWarning, crewRes.PaneTail)
	}

	crewHandle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    crewRes.Agent, RawID: "k3", Kind: codex.KindCodex,
	}
	paneTail := func() string {
		screen, readErr := rt.ReadAgent(ctx, crewHandle, harness.ReadRecentUnwrapped, 40)
		if readErr != nil {
			return "(pane not readable: " + readErr.Error() + ")"
		}
		return harness.StartupScreenTail(screen, 20)
	}
	ask := waitForInboxItem(t, ctx, w, "shop", 120*time.Second, paneTail)
	t.Logf("inbox item: %s %s %s", ask.Crew, ask.Verb, ask.Text)
	if ask.Verb != "needs-decision" {
		t.Fatalf("inbox item = %+v, want the needs-decision question", ask)
	}

	// 3. One tick of the daemon, wired exactly as cmdConsole wires it
	// (consolePilot / consoleMateHandle in console_auto.go).
	pilot := consoleAutoPilot(w, deps)
	digest := tickUntilDigest(t, ctx, pilot, w, "shop", 2*time.Minute)
	t.Logf("digest: %s", digest)
	if !strings.Contains(digest, "k3 needs-decision") {
		t.Fatalf("digest = %q, want the crew's question", digest)
	}
	if !strings.Contains(digest, w.CrewsDir("shop")) {
		t.Fatalf("digest = %q, want the absolute crews directory (section 10 requires an absolute path)", digest)
	}

	// 4. The Mate reads the status file itself and answers the crew - not
	// the test, not the console. Source: mate is written because the
	// Mate's own pane carries MATE_AGENT_ROLE=mate (internal/spawn), so
	// only `mate send` run from inside that pane produces it.
	mate := waitForSent(t, ctx, w, "shop", 180*time.Second, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate && e.Target == store.CrewTarget("k3")
	})
	t.Logf("mate -> crew:k3 %q", mate.Text)

	// 5. The crew takes it as a new prompt and hands back.
	done := waitForBoxEntry(t, ctx, w, "shop", 180*time.Second, paneTail, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Verb == "wait-mate" && strings.Contains(strings.ToLower(e.Text), "chose")
	})
	t.Logf("crew finished: %s: %s", done.Verb, done.Text)
	lower := strings.ToLower(done.Text)
	if !strings.Contains(lower, "red") && !strings.Contains(lower, "blue") {
		t.Fatalf("done line %q names neither colour", done.Text)
	}

	// 6. The Mate did not close the crew on its own: `crew stop` was never
	// run, so the crew's own meta still reads `spawned`, not `finished`.
	// Closing on a wait-mate is the captain's word (section 4b, section 9's
	// "Closing a Crew"), not something a digest's policy hands the Mate.
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	if state := meta[spawn.MetaState]; state != spawn.CrewStateSpawned {
		t.Fatalf("crew k3 meta state = %q, want %q: the Mate ran `crew stop` on a wait-mate from a digest, which section 4b reserves for the captain",
			state, spawn.CrewStateSpawned)
	}

	// 7. Stop both, through the seams that own them - this is the test's
	// own teardown, not the Mate's, and it is expected to succeed exactly
	// because the Mate left the crew's branch alone.
	if _, err := spawn.StopCrew(ctx, w, deps, "shop", "k3", true); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if _, err := spawn.StopMate(ctx, w, deps, "shop"); err != nil {
		t.Fatalf("StopMate: %v", err)
	}
}
