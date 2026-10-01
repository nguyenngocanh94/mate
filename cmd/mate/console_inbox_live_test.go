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
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// TestLiveConsoleInboxResolve is the proof for the inbox and the `resolve:`
// line: not that a line reached the Mate's composer, but that the Mate did
// something with it - read the status file, decided, and answered the crew
// itself. That is the whole of what the reader asked for after using the old
// box ("clicking an entry is meaningless"), and it is the one claim no unit
// test can make.
//
//  1. a real Claude Mate and a real Codex crew
//  2. the crew writes `needs-decision: pick A or B` and stops its turn
//  3. the box's Inbox - the rows the rail actually draws - holds exactly that
//     one item, and the rendered rail says so: the crew, what it needs, and
//     the header's own count
//  4. the resolve action hands the Mate the `resolve:` line through the same
//     ActionFunc the `[assign]` button uses
//  5. the Mate answers the crew with `mate send`, which records
//     `Source: mate` because spawn puts MATE_AGENT_ROLE=mate in its pane
//  6. the crew takes that as a new prompt and reaches `wait-mate: chose <A|B>`
//  7. the inbox is empty again - by the rules in internal/box/inbox.go, from
//     the Mate's own reply and from the crew moving on
//
// Everything runs through the seams cmdConsole wires (consoleAction over the
// real Herdr adapter). The Bubble Tea Program is not run - it needs a
// terminal - but no part of the path being proved lives inside it.
func TestLiveConsoleInboxResolve(t *testing.T) {
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
	action := consoleAction(w, deps)

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	// 1. The Mate, through the Console's own action seam.
	startOut, err := action(ctx, console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate", Harness: query.HarnessKind("claude")})
	if err != nil {
		t.Fatalf("console start action: %v", err)
	}
	t.Logf("start action: %s", startOut)

	// 2. The crew: ask, stop, and act on whatever answer arrives.
	crewRes, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop",
		Crew:    "k3",
		Harness: codex.KindCodex,
		BriefText: brieftest.Ship(`Append needs-decision: pick A or B to the status file and stop; ` +
			`when answered, append wait-mate: chose <answer>`),
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

	// 3. The inbox, not the log: this is what the rail draws, and an item
	// that does not reach it is one the reader can never act on.
	ask := waitForInboxItem(t, ctx, w, "shop", 180*time.Second, paneTail)
	t.Logf("inbox item: %s %s %s", ask.Crew, ask.Verb, ask.Text)
	if ask.Verb != "needs-decision" {
		t.Fatalf("inbox item = %+v, want the needs-decision question", ask)
	}

	// And the row is legible in the rail a reader would be looking at: the
	// crew and what it needs, in the words the inbox uses (2026-09-19 - the
	// crew's own text is not on the row any more; the row opens its pane).
	// Asserting on the DTO instead would pass while the rail showed a row
	// cut at "need…".
	rail := console.RenderBox("shop", query.LoadBox(w, "shop"), 40, 12)
	t.Logf("rail:\n%s", strings.Join(rail, "\n"))
	joined := strings.Join(strings.Fields(strings.Join(rail, " ")), " ")
	if !strings.Contains(joined, "1 waiting") {
		t.Fatalf("the rail header does not say \"1 waiting\":\n%s", strings.Join(rail, "\n"))
	}
	for _, want := range []string{ask.Crew, "decide", "[assign]"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the rail row does not say %q:\n%s", want, strings.Join(rail, "\n"))
		}
	}

	// 4. `[assign]`: the line into the Mate's composer, through ActionFunc.
	resolveOut, err := action(ctx, console.ActionRequest{
		Action: console.ActionResolve, Target: "shop", TargetKind: "project",
		Crew: ask.Crew, Input: ask.Resolve})
	if err != nil {
		t.Fatalf("resolve action: %v\ncrew pane:\n%s", err, paneTail())
	}
	t.Logf("resolve action: %s", resolveOut)
	t.Logf("resolve line: %s", ask.Resolve)
	assertSentLine(t, w, "shop", store.SourceApp, store.TargetMate, ask.Resolve)

	// 5. The Mate answers the crew itself. `mate send` records Source:
	// mate because the Mate's pane carries MATE_AGENT_ROLE=mate
	// (internal/spawn/start.go), so this line is proof the Mate - not the
	// test, not the console - decided and replied.
	mate := waitForSent(t, ctx, w, "shop", 300*time.Second, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate && e.Target == store.CrewTarget("k3")
	})
	t.Logf("mate -> crew:k3 %q", mate.Text)

	// 6. The crew takes it as a new prompt and finishes.
	done := waitForBoxEntry(t, ctx, w, "shop", 300*time.Second, paneTail, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Verb == "wait-mate" && strings.Contains(strings.ToLower(e.Text), "chose")
	})
	t.Logf("crew finished: %s: %s", done.Verb, done.Text)
	if lower := strings.ToLower(done.Text); !strings.Contains(lower, "a") && !strings.Contains(lower, "b") {
		t.Fatalf("done line %q names neither option", done.Text)
	}

	// 7. The inbox is empty, and the log still holds everything.
	box := query.LoadBox(w, "shop")
	if !box.IsKnown() {
		t.Fatalf("LoadBox: %s", box.Reason)
	}
	for _, e := range box.Value.Entries {
		t.Logf("log | %-8s %-14s %-8s %s", e.Kind, e.Verb, e.Source, e.Text)
	}
	if len(box.Value.Inbox) != 0 {
		t.Fatalf("inbox after the answer = %+v, want empty", box.Value.Inbox)
	}
	if len(box.Value.Entries) < 4 {
		t.Fatalf("the merged log holds %d entries; the filter must not shrink the record", len(box.Value.Entries))
	}
	empty := console.RenderBox("shop", box, 40, 6)
	t.Logf("rail after the answer:\n%s", strings.Join(empty, "\n"))
	if !strings.Contains(strings.Join(empty, " "), "nothing waiting") {
		t.Fatalf("the emptied rail does not say so:\n%s", strings.Join(empty, "\n"))
	}

	// 8. Stop both, through the seams that own them.
	if _, err := spawn.StopCrew(ctx, w, deps, "shop", "k3", true); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	stopOut, err := action(ctx, console.ActionRequest{Action: console.ActionStop, Target: "shop", TargetKind: "mate"})
	if err != nil {
		t.Fatalf("console stop action: %v", err)
	}
	t.Logf("stop action: %s", stopOut)
}

// waitForInboxItem polls query.LoadBox until the inbox holds exactly one
// item, and fails with the crew's screen so a timeout says what the agent was
// actually doing rather than only that nothing arrived.
func waitForInboxItem(t *testing.T, ctx context.Context, w *store.Workspace, project string, within time.Duration,
	tail func() string) query.BoxEntry {
	t.Helper()
	deadline := time.Now().Add(within)
	var last string
	for time.Now().Before(deadline) {
		box := query.LoadBox(w, project)
		switch {
		case !box.IsKnown():
			last = "box unreadable: " + box.Reason
		case len(box.Value.Inbox) == 1:
			return box.Value.Inbox[0]
		default:
			var lines []string
			for _, e := range box.Value.Inbox {
				lines = append(lines, e.Crew+" "+e.Verb+" "+e.Text)
			}
			last = "inbox: " + strings.Join(lines, " | ")
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for an inbox item; %s\npane:\n%s", last, tail())
		case <-time.After(3 * time.Second):
		}
	}
	t.Fatalf("no single inbox item within %s; %s\npane:\n%s", within, last, tail())
	return query.BoxEntry{}
}
