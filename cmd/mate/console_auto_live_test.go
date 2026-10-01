package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLiveAutoDigestReachesTheMate is the mvp.md task 19 proof, and the only
// one that can be made: that a digest the daemon typed was received by the
// Mate *as an app line*, and that the captain typing one unmarked line ends
// auto mode without anybody telling the daemon.
//
// The proof is Claude's own UserPromptSubmit hook. `mate mate-prompt`
// (internal/hook) records every prompt the model receives into `sent.log`,
// with `Source: app` when and only when the prompt carried the sentinel
// marker. So a digest that really landed in the model's turn appears there
// twice - once written by the daemon after it verified the composer cleared,
// once written by the hook when Claude read it - and a digest that only
// reached the pane appears once. Nothing about a screen can establish that.
//
//  1. a real Claude Mate and a real Codex crew, `.auto` on
//  2. the crew appends `needs-decision: pick A or B` and stops its turn
//  3. one tick of the daemon delivers one digest carrying that question
//  4. `sent.log` holds the hook's copy of it: the Mate read it as an app line
//  5. the captain types an unmarked line into the same composer
//  6. the Mate's hook deletes `.auto`, and the next tick sends nothing -
//     even with a fresh question waiting, which is the case that would catch
//     a daemon that had cached the flag
//
// Everything runs through the seams cmdConsole wires (consoleMateHandle over
// the real Herdr adapter). The Bubble Tea Program is not run - it needs a
// terminal - but no part of the path being proved lives inside it.
func TestLiveAutoDigestReachesTheMate(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	repo := filepath.Join(w.Root(), "shop")
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

	ctx, cancel := context.WithTimeout(context.Background(), 13*time.Minute)
	defer cancel()

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	// 1. The Mate, and auto mode on it.
	mateRes, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	t.Logf("Mate %s is running on %s in pane %s", mateRes.Agent, mateRes.Harness, mateRes.Pane)
	if err := w.SetAuto("shop", true); err != nil {
		t.Fatalf("SetAuto: %v", err)
	}

	// 2. The crew: ask, then stop the turn.
	crewRes, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project:   "shop",
		Crew:      "k3",
		Harness:   harness.KindCodex,
		BriefText: brieftest.Ship(`Append needs-decision: pick A or B to the status file and then stop; do nothing else`),
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
		Name:    crewRes.Agent, RawID: "k3", Kind: harness.KindCodex,
	}
	paneTail := func() string {
		screen, readErr := rt.ReadAgent(ctx, crewHandle, 40)
		if readErr != nil {
			return "(pane not readable: " + readErr.Error() + ")"
		}
		return harness.StartupScreenTail(screen, 20)
	}
	ask := waitForInboxItem(t, ctx, w, "shop", 180*time.Second, paneTail)
	t.Logf("inbox item: %s %s %s", ask.Crew, ask.Verb, ask.Text)

	// 3. One tick of the daemon, wired exactly as cmdConsole wires it.
	pilot := consoleAutoPilot(w, deps)
	digest := tickUntilDigest(t, ctx, pilot, w, "shop", 3*time.Minute)
	t.Logf("digest: %s", digest)
	if !strings.Contains(digest, "k3 needs-decision") {
		t.Fatalf("digest = %q, want the crew's question", digest)
	}
	if !strings.Contains(digest, w.CrewsDir("shop")) {
		t.Fatalf("digest = %q, want the absolute crews directory", digest)
	}
	if status := pilot.Snapshot()["shop"]; status.Sends != 1 || status.Notice != "" {
		t.Fatalf("daemon status = %+v, want one delivered digest", status)
	}
	// Since task 30 the daemon types nothing itself: the digest went through
	// the Mate's outbox, which is what marked it sent and moved the cursor.
	outboxItems, err := w.ReadOutbox("shop")
	if err != nil {
		t.Fatalf("ReadOutbox: %v", err)
	}
	if len(outboxItems) != 1 || outboxItems[0].Source != store.OutboxSourceDigest ||
		outboxItems[0].State != store.OutboxSent || outboxItems[0].Text != digest {
		t.Fatalf("outbox = %+v, want the one digest, sent", outboxItems)
	}
	t.Logf("outbox: digest sent after %d attempt(s), queued %s, sent %s",
		outboxItems[0].Attempts, outboxItems[0].At.Format(time.RFC3339), outboxItems[0].SentAt.Format(time.RFC3339))

	// 4. The Mate's own hook recorded it as an app line. Two copies of the
	//    same text: the daemon's, written when the composer cleared, and the
	//    hook's, written when Claude read the prompt.
	waitForSentCount(t, ctx, w, "shop", 180*time.Second, digest, 2, paneTail)
	for _, e := range sentEntries(t, w, "shop") {
		if e.Text == digest && e.Source != store.SourceApp {
			t.Fatalf("the digest was recorded as %q, want %q: the marker did not survive to the hook",
				e.Source, store.SourceApp)
		}
	}
	t.Log("the digest reached the Mate's UserPromptSubmit payload with its marker intact")

	// 5. The captain types one unmarked line into the same composer. This is
	//    what the `r` key and `mate send <project>` both do: no marker,
	//    because the line is the human's.
	mateHandle, mateKind, err := spawn.MateHandle(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("MateHandle: %v", err)
	}
	captain := "thanks, I will take it from here"
	sendUntilDelivered(t, ctx, deps, mateHandle, mateKind, captain, 4*time.Minute)
	t.Logf("captain typed: %s", captain)

	// 6. The Mate's hook deleted `.auto`, and the daemon stops within one
	//    tick - with a brand new question waiting, so a cached flag would be
	//    caught here.
	waitForAutoOff(t, ctx, w, "shop", 180*time.Second)
	if err := w.AppendStatus("shop", "k3", "needs-decision: and what about C"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	before := len(sentEntries(t, w, "shop"))
	if err := pilot.Tick(ctx); err != nil {
		t.Fatalf("Tick after auto off: %v", err)
	}
	after := sentEntries(t, w, "shop")
	for _, e := range after[before:] {
		if e.Source == store.SourceApp && e.Target == store.TargetMate {
			t.Fatalf("the daemon sent %q after .auto was deleted", e.Text)
		}
	}
	if status := pilot.Snapshot()["shop"]; status.Sends != 1 {
		t.Fatalf("daemon status = %+v, want no second send", status)
	}

	// 7. Stop both, through the seams that own them.
	if _, err := spawn.StopCrew(ctx, w, deps, "shop", "k3", true); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if _, err := spawn.StopMate(ctx, w, deps, "shop"); err != nil {
		t.Fatalf("StopMate: %v", err)
	}
}

// tickUntilDigest ticks the daemon until one digest is delivered, and returns
// the line as `sent.log` recorded it. The retry is not a weaker claim than
// "within one tick": a tick that finds the Mate mid-turn refuses by design
// (mvp.md section 5), and this loop is how a real console's ninety-second
// window behaves compressed.
func tickUntilDigest(t *testing.T, ctx context.Context, pilot *autopilot.Pilot,
	w *store.Workspace, project string, within time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if err := pilot.Tick(ctx); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		for _, e := range sentEntries(t, w, project) {
			if e.Source == store.SourceApp && e.Target == store.TargetMate && strings.HasPrefix(e.Text, "digest: ") {
				return e.Text
			}
		}
		if notice := pilot.Snapshot()[project].Notice; notice != "" {
			t.Logf("tick refused: %s", notice)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for a digest")
		case <-time.After(5 * time.Second):
		}
	}
	t.Fatalf("no digest was delivered within %s", within)
	return ""
}

// waitForSentCount waits for `sent.log` to hold n copies of one line.
func waitForSentCount(t *testing.T, ctx context.Context, w *store.Workspace, project string, within time.Duration,
	text string, n int, tail func() string) {
	t.Helper()
	deadline := time.Now().Add(within)
	got := 0
	for time.Now().Before(deadline) {
		got = 0
		for _, e := range sentEntries(t, w, project) {
			if e.Text == text {
				got++
			}
		}
		if got >= n {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for %d copies of %q; have %d\npane:\n%s", n, text, got, tail())
		case <-time.After(3 * time.Second):
		}
	}
	t.Fatalf("sent.log holds %d copies of %q within %s, want %d: the Mate never read it as an app line",
		got, text, within, n)
}

// waitForAutoOff waits for the Mate's own hook to delete `mate/.auto`.
func waitForAutoOff(t *testing.T, ctx context.Context, w *store.Workspace, project string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !w.Auto(project) {
			t.Log(".auto is gone: the Mate's UserPromptSubmit hook saw an unmarked prompt")
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("context ended waiting for .auto to be deleted")
		case <-time.After(2 * time.Second):
		}
	}
	t.Fatalf(".auto still exists %s after the captain typed; the hook did not fire", within)
}

// sendUntilDelivered types one line into a pane, retrying the refusals that
// are not failures: a Mate mid-turn on the digest it was just handed is
// exactly the state a captain typing over it meets.
func sendUntilDelivered(t *testing.T, ctx context.Context, deps spawn.Deps,
	handle runtime.AgentHandle, kind harness.Kind, text string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	var last error
	for time.Now().Before(deadline) {
		_, err := send.Send(ctx, send.Deps{Harnesses: harnesses, Runtime: deps.Runtime}, handle, kind, text, send.Options{})
		if err == nil {
			return
		}
		last = err
		t.Logf("captain's line refused (%v); retrying", err)
		select {
		case <-ctx.Done():
			t.Fatalf("context ended typing the captain's line: %v", last)
		case <-time.After(10 * time.Second):
		}
	}
	t.Fatalf("could not type the captain's line within %s: %v", within, last)
}
