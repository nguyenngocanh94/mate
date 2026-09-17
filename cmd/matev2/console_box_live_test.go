package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/process"
	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/ui/console"
)

// TestLiveConsoleBoxRoundTrip is the mvp.md task 15 proof, and it is the
// loop the task row asks for: a crew asks, the reader answers with `r`, the
// crew continues, and the reader hands the result to the Mate with Enter.
//
//  1. a real Claude Mate and a real Codex crew
//  2. the crew writes `needs-decision:` and stops its turn
//  3. the box - the same query.BoxView the rail draws - shows that entry
//  4. the reply action types "A" into the crew's own pane
//  5. the crew continues and writes `done: chose A`
//  6. the forward action hands `signal: crews/k3.status` to the Mate
//  7. sent.log carries app -> mate, and the Mate's own Stop hook then
//     records a mate line - which it only can after reading the file the
//     signal pointed it at
//
// Everything runs through the seams cmdConsole wires (consoleAction over the
// real Herdr adapter). The Bubble Tea Program is not run - it needs a
// terminal - but no part of the path being proved lives inside it.
func TestLiveConsoleBoxRoundTrip(t *testing.T) {
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
	if err := w.AddProject("shop", store.ProjectConfig{Repo: repo, DefaultBranch: "main"}); err != nil {
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

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	action := consoleAction(w, deps)

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3")
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	// 1. The Mate, through the Console's own action seam.
	startOut, err := action(ctx, console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate", Harness: query.HarnessClaude})
	if err != nil {
		t.Fatalf("console start action: %v", err)
	}
	t.Logf("start action: %s", startOut)

	// 2. The crew, with the brief the task row pins: ask, stop, and report
	// what it was told once somebody answers.
	crewRes, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop",
		Crew:    "k3",
		Harness: harness.KindCodex,
		BriefText: `Append needs-decision: pick A or B to the status file and stop; ` +
			`when answered, append done: chose <answer>`,
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

	// 3. The needs-decision entry, read the way the rail reads it. Waiting
	// on the box rather than on the raw file is the point: what the reader
	// sees is query.BoxView, and an entry that does not reach it is not in
	// the rail whatever the file says.
	ask := waitForBoxEntry(t, ctx, w, 180*time.Second, paneTail, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Verb == "needs-decision"
	})
	t.Logf("box shows: %s %s %s (attention=%v, signal=%q)", e2s(ask), ask.Verb, ask.Text, ask.Attention, ask.Signal)
	if !ask.Attention {
		t.Fatalf("a needs-decision entry is not marked attention: %+v", ask)
	}
	if ask.Signal != query.BoxStatusSignal("k3") {
		t.Fatalf("signal = %q, want %q", ask.Signal, query.BoxStatusSignal("k3"))
	}

	// 4. The `r` key's action: one line into the crew's own composer.
	replyOut, err := action(ctx, console.ActionRequest{
		Action: console.ActionReply, Target: "shop", TargetKind: "project", Crew: "k3", Input: "A"})
	if err != nil {
		t.Fatalf("reply action: %v\npane:\n%s", err, paneTail())
	}
	t.Logf("reply action: %s", replyOut)
	assertSentLine(t, w, store.SourceUser, store.CrewTarget("k3"), "A")

	// 5. The crew continues. This is the whole of mvp.md section 4's answer
	// protocol: no interaction row, no correlation id - to the crew the
	// reply was simply a new prompt.
	done := waitForBoxEntry(t, ctx, w, 240*time.Second, paneTail, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Verb == "done" && strings.Contains(strings.ToLower(e.Text), "chose a")
	})
	t.Logf("crew continued: %s %s", done.Verb, done.Text)

	// 6. Enter on that entry: the signal line into the Mate's composer.
	forwardOut, err := action(ctx, console.ActionRequest{
		Action: console.ActionForward, Target: "shop", TargetKind: "project",
		Crew: done.Crew, Input: done.Signal})
	if err != nil {
		t.Fatalf("forward action: %v", err)
	}
	t.Logf("forward action: %s", forwardOut)
	assertSentLine(t, w, store.SourceApp, store.TargetMate, done.Signal)

	// 7. The Mate answers. Its Stop hook (mvp.md task 08) appends a mate
	// line to sent.log at the end of the turn, and the turn only started
	// because the signal reached the composer - so this is the end-to-end
	// proof that the app's line became a Mate turn.
	mate := waitForSent(t, ctx, w, 240*time.Second, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate
	})
	t.Logf("the Mate replied after the signal: %s", mate.Text)

	// And the whole exchange is in the box, in order.
	box := query.LoadBox(w, "shop")
	if !box.IsKnown() {
		t.Fatalf("LoadBox: %s", box.Reason)
	}
	for _, e := range box.Value.Entries {
		t.Logf("box | %-8s %-14s %-8s %s", e.Kind, e.Verb, e.Source, e.Text)
	}

	// 8. Stop both, through the seams that own them.
	if _, err := spawn.StopCrew(ctx, w, deps, "shop", "k3"); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	stopOut, err := action(ctx, console.ActionRequest{Action: console.ActionStop, Target: "shop", TargetKind: "mate"})
	if err != nil {
		t.Fatalf("console stop action: %v", err)
	}
	t.Logf("stop action: %s", stopOut)
}

func e2s(e query.BoxEntry) string { return e.At.UTC().Format("15:04:05") + " " + e.Crew }

// waitForBoxEntry polls query.LoadBox - the Console's own read - until an
// entry matches, and fails with the crew's screen so a timeout says what the
// agent was actually doing rather than only that nothing arrived.
func waitForBoxEntry(t *testing.T, ctx context.Context, w *store.Workspace, within time.Duration,
	tail func() string, match func(query.BoxEntry) bool) query.BoxEntry {
	t.Helper()
	deadline := time.Now().Add(within)
	var last string
	for time.Now().Before(deadline) {
		box := query.LoadBox(w, "shop")
		if box.IsKnown() {
			var lines []string
			for _, e := range box.Value.Entries {
				lines = append(lines, fmt.Sprintf("%s %s %s", e.Kind, e.Verb, e.Text))
				if match(e) {
					return e
				}
			}
			last = strings.Join(lines, "\n")
		} else {
			last = "box unreadable: " + box.Reason
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for a box entry; box:\n%s\npane:\n%s", last, tail())
		case <-time.After(3 * time.Second):
		}
	}
	t.Fatalf("no matching box entry within %s; box:\n%s\npane:\n%s", within, last, tail())
	return query.BoxEntry{}
}

// waitForSent polls sent.log for a matching line.
func waitForSent(t *testing.T, ctx context.Context, w *store.Workspace, within time.Duration,
	match func(store.SentEntry) bool) store.SentEntry {
	t.Helper()
	deadline := time.Now().Add(within)
	var last []store.SentEntry
	for time.Now().Before(deadline) {
		entries, _, err := w.ReadSent("shop", 0)
		if err != nil {
			t.Fatalf("ReadSent: %v", err)
		}
		last = entries
		for _, e := range entries {
			if match(e) {
				return e
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for a sent.log line; sent.log:\n%+v", last)
		case <-time.After(3 * time.Second):
		}
	}
	t.Fatalf("no matching sent.log line within %s; sent.log:\n%+v", within, last)
	return store.SentEntry{}
}

func assertSentLine(t *testing.T, w *store.Workspace, source, target, text string) {
	t.Helper()
	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatalf("ReadSent: %v", err)
	}
	for _, e := range entries {
		if e.Source == source && e.Target == target && e.Text == text {
			return
		}
	}
	t.Fatalf("sent.log has no %s -> %s line %q; it holds:\n%+v", source, target, text, entries)
}
