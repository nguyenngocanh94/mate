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
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// TestLiveAssignQueuesWhileTheMateIsBusy is the docs/mvp.md task 30 proof:
// `[assign]` on a Mate that is mid-turn is queued instead of refused, and the
// `resolve:` line reaches the Mate once its turn ends - exactly once, and
// without anybody pressing the button again.
//
// Task 24 measured the state this is about: a supervising Mate sits inside a
// tool call for most of every cycle, and `[assign]` met `target_blocked:
// agent is mid-turn` four or five times in a row. Here the Mate is put into
// one long tool call on purpose (a 45-second wait), and the test confirms
// send.ClassifyComposer reads Busy before it presses anything, so the queue
// is exercised for certain rather than by luck.
//
//  1. a real Claude Mate, streamed at the geometry the console gives it, and
//     the console's delivery loop running (consolePilot, as cmdConsole
//     starts it)
//  2. a real Codex crew appends `needs-decision: pick A or B`
//  3. the captain tells the Mate to wait 45s in a foreground shell command;
//     its composer reads Busy
//  4. `[assign]` through consoleAction returns "queued", and the inbox row
//     says "assigned, queued"
//  5. within 60s of the Mate's turn ending, `sent.log` holds the line twice:
//     the outbox's record (written once the composer cleared) and the Mate's
//     own UserPromptSubmit hook's (written when the model read it). Two, not
//     more: the line was typed once
//  6. the row now says "assigned HH:MM" and is still in the inbox, because
//     handing the question over is not answering the crew (unless the Mate
//     has already answered it, which is the rule that closes it)
func TestLiveAssignQueuesWhileTheMateIsBusy(t *testing.T) {
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
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	// 1. The Mate, and the delivery loop exactly as cmdConsole starts it.
	startOut, err := action(ctx, console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate", Harness: query.HarnessClaude})
	if err != nil {
		t.Fatalf("console start action: %v", err)
	}
	t.Logf("start action: %s", startOut)
	delivery, err := consolePilot(root, deps)
	if err != nil {
		t.Fatalf("consolePilot: %v", err)
	}
	delivery.Start(ctx)
	defer delivery.Stop()

	size := console.StreamSize(console.SessionTargetMate, 120, 36)
	snap, err := query.Load(ctx, w)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	channel, err := consoleSessionStream(w, rt)(ctx, console.SessionTarget{
		Kind:        console.SessionTargetMate,
		ID:          snap.Projects[0].Mate.Designated.Value.MateID,
		ProjectID:   "shop",
		HarnessKind: query.HarnessClaude,
		AgentName:   snap.Projects[0].Mate.AgentName.Value,
		Mode:        snap.Projects[0].Mode,
	}, size)
	if err != nil {
		t.Fatalf("open the Mate's session stream: %v", err)
	}
	defer func() { _ = channel.Close(context.Background()) }()
	waitForStreamComposer(t, ctx, channel, 90*time.Second)
	t.Logf("the Mate is streamed at %dx%d", size.Cols, size.Rows)

	mateHandle, mateKind, err := spawn.MateHandle(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("MateHandle: %v", err)
	}
	mateTail := func() string {
		s, readErr := rt.ReadAgent(ctx, mateHandle, send.DefaultLines)
		if readErr != nil {
			return "(mate pane not readable: " + readErr.Error() + ")"
		}
		return harness.StartupScreenTail(s, 20)
	}

	// 2. A crew that asks and stops.
	crewRes, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop",
		Crew:    "k3",
		Harness: harness.KindCodex,
		BriefText: brieftest.Ship(`Append needs-decision: pick A or B to the status file and stop; ` +
			`when answered, append wait-mate: chose <answer>`),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Logf("spawned crew %s in pane %s", crewRes.Agent, crewRes.Pane)
	crewHandle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    crewRes.Agent, RawID: "k3", Kind: harness.KindCodex,
	}
	crewTail := func() string {
		screen, readErr := rt.ReadAgent(ctx, crewHandle, send.DefaultLines)
		if readErr != nil {
			return "(pane not readable: " + readErr.Error() + ")"
		}
		return harness.StartupScreenTail(screen, 20)
	}
	ask := waitForInboxItem(t, ctx, w, "shop", 180*time.Second, crewTail)
	t.Logf("inbox item: %s %s %s (key %s)", ask.Crew, ask.Verb, ask.Text, ask.AssignKey)
	if ask.Verb != "needs-decision" || ask.AssignKey == "" {
		t.Fatalf("inbox item = %+v, want the needs-decision question with an assign key", ask)
	}

	// 3. The captain puts the Mate into one long tool call, and the test
	// waits until the composer really reads Busy.
	//
	// Not `sleep 45`: measured 2026-09-24 (first run of this test), Claude
	// Code's Bash tool refuses a foreground `sleep N` ("Blocked: sleep 45
	// ...; use run_in_background"), so the Mate backgrounded it and ended
	// its turn after eight seconds. The same wait spelled as a program the
	// block does not recognise keeps the tool call - and the Mate - busy for
	// the whole 45 seconds.
	busyPrompt := "run `python3 -c 'import time; time.sleep(45)'` in the foreground (not in the background) and then reply done"
	sendUntilDelivered(t, ctx, deps, mateHandle, mateKind, busyPrompt, 2*time.Minute)
	promptAt := time.Now()
	t.Logf("captain → Mate: %s", busyPrompt)
	busy := waitForComposerState(t, ctx, rt, mateHandle, mateKind, send.StateBusy, 60*time.Second, mateTail)
	t.Logf("the Mate reads Busy %s after the prompt: %q", time.Since(promptAt).Round(time.Second), busy.Evidence)
	// The first Busy can be the turn's own start (its hooks running); ten
	// seconds on, a Mate still Busy is inside the tool call.
	time.Sleep(10 * time.Second)
	busy = waitForComposerState(t, ctx, rt, mateHandle, mateKind, send.StateBusy, 5*time.Second, mateTail)
	t.Logf("%s after the prompt the Mate is still Busy: %q", time.Since(promptAt).Round(time.Second), busy.Evidence)

	// 4. `[assign]`: queued, not refused.
	assignAt := time.Now()
	out, err := action(ctx, console.ActionRequest{
		Action: console.ActionResolve, Target: "shop", TargetKind: "project",
		Crew: ask.Crew, Input: ask.Resolve, Key: ask.AssignKey})
	if err != nil {
		t.Fatalf("[assign] on a busy Mate: %v\nmate pane:\n%s", err, mateTail())
	}
	t.Logf("[assign] outcome: %s", out)
	if !strings.HasPrefix(out, "queued for the Mate") {
		t.Fatalf("[assign] outcome = %q, want it queued behind the Mate's turn\nmate pane:\n%s", out, mateTail())
	}
	row := assignedRow(t, w)
	rail := strings.Join(console.RenderInboxRail(query.LoadBox(w, "shop"), -1, 54, 8), "\n")
	t.Logf("rail while queued:\n%s", rail)
	if row.Assigned.State != query.BoxAssignQueued || !strings.Contains(rail, "needs an answer · assigned, queued") {
		t.Fatalf("inbox row = %+v, want it assigned, queued", row)
	}

	// 5. Once the turn ends the outbox delivers it - watched closely, because
	// step 6 has to read the row before the Mate answers the crew and the
	// item leaves the inbox (measured on the first run: under ten seconds).
	item := waitForOutboxSent(t, ctx, w, 4*time.Minute, mateTail)

	// 6. Still in the inbox, now saying when it was handed over.
	box := query.LoadBox(w, "shop")
	rail = strings.Join(console.RenderInboxRail(box, -1, 54, 8), "\n")
	t.Logf("rail right after the send:\n%s", rail)
	switch {
	case box.IsKnown() && len(box.Value.Inbox) == 1:
		row = box.Value.Inbox[0]
		if row.Assigned.State != query.BoxAssignSent ||
			!strings.Contains(rail, "needs an answer · assigned "+item.SentAt.UTC().Format("15:04")) {
			t.Fatalf("inbox row = %+v, want it assigned %s", row, item.SentAt.UTC().Format("15:04"))
		}
	case mateAnswered(t, w, "k3"):
		// The Mate read the file and answered between the send and this
		// read. The item left the inbox by rule 1 of section 4, which is
		// the other half of what this row promises; the suffix itself is
		// pinned by TestBoxInboxRowSaysWhetherItWasAssigned.
		t.Logf("the Mate had already answered k3 when the row was read; the item left the inbox as it should")
	default:
		t.Fatalf("inbox = %+v (%s) right after the send, and the Mate has not answered: the assign must not close the item",
			box.Value.Inbox, box.Reason)
	}

	// The Mate's own hook read it: two copies in sent.log.
	waitForSentCount(t, ctx, w, "shop", 3*time.Minute, ask.Resolve, 2, mateTail)
	var turnEnd time.Time
	for _, e := range sentEntries(t, w, "shop") {
		t.Logf("sent.log %s %s → %s: %s", e.Time.Format("15:04:05"), e.Source, e.Target, e.Text)
		// The Stop hook's line for the busy turn: the first Mate answer
		// written after the captain's prompt and before the outbox's send.
		if turnEnd.IsZero() && e.Source == store.SourceMate && e.Target == store.SourceUser &&
			!e.Time.Before(promptAt.Truncate(time.Second)) && !e.Time.After(item.SentAt) {
			turnEnd = e.Time
		}
	}
	t.Logf("assign queued %s, sent %s after %d attempt(s): waited %s",
		item.At.Format("15:04:05"), item.SentAt.Format("15:04:05"), item.Attempts,
		item.SentAt.Sub(assignAt).Round(time.Second))
	if item.Attempts < 2 {
		t.Fatalf("outbox item = %+v, want at least one refused attempt before the send: the Mate was busy", item)
	}
	if turnEnd.IsZero() {
		t.Logf("the Mate's Stop hook left no line for the busy turn (section 7: it does not fire every turn)")
		if wait := item.SentAt.Sub(assignAt); wait > 45*time.Second+60*time.Second+30*time.Second {
			t.Fatalf("the assign waited %s; want it within 60s of a 45s turn ending", wait)
		}
	} else {
		lag := item.SentAt.Sub(turnEnd)
		t.Logf("the Mate's turn ended %s; the line went in %s later", turnEnd.Format("15:04:05"), lag.Round(time.Second))
		if lag > 60*time.Second {
			t.Fatalf("the line reached the Mate %s after its turn ended, want within 60s", lag)
		}
	}

	// Exactly once: the loop keeps running, and nothing types it again.
	time.Sleep(10 * time.Second)
	copies := 0
	for _, e := range sentEntries(t, w, "shop") {
		if e.Text == ask.Resolve {
			copies++
			if e.Source != store.SourceApp || e.Target != store.TargetMate {
				t.Fatalf("the resolve line was recorded as %s → %s, want app → mate", e.Source, e.Target)
			}
		}
	}
	if copies != 2 {
		t.Fatalf("sent.log holds %d copies of the resolve line, want exactly 2 (the outbox's and the hook's)", copies)
	}
	if items, err := w.ReadOutbox("shop"); err != nil || len(items) != 1 || items[0].State != store.OutboxSent {
		t.Fatalf("outbox = %+v (%v), want the one assign, sent", items, err)
	}
}

// waitForOutboxSent polls the Mate's outbox until its one item is sent.
func waitForOutboxSent(t *testing.T, ctx context.Context, w *store.Workspace, within time.Duration,
	tail func() string) store.OutboxItem {
	t.Helper()
	deadline := time.Now().Add(within)
	var last []store.OutboxItem
	for time.Now().Before(deadline) {
		items, err := w.ReadOutbox("shop")
		if err != nil {
			t.Fatalf("ReadOutbox: %v", err)
		}
		last = items
		if len(items) == 1 && items[0].State == store.OutboxSent {
			return items[0]
		}
		if len(items) != 1 || !items[0].Queued() {
			t.Fatalf("outbox = %+v, want the one assign, queued until sent", items)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for the outbox to send; outbox %+v", last)
		case <-time.After(250 * time.Millisecond):
		}
	}
	t.Fatalf("the outbox did not send within %s; outbox %+v\nmate pane:\n%s", within, last, tail())
	return store.OutboxItem{}
}

// mateAnswered reports whether the Mate has answered the crew itself.
func mateAnswered(t *testing.T, w *store.Workspace, crew string) bool {
	t.Helper()
	for _, e := range sentEntries(t, w, "shop") {
		if e.Source == store.SourceMate && e.Target == store.CrewTarget(crew) {
			return true
		}
	}
	return false
}

// assignedRow is the one inbox item, read the way the Console reads it.
func assignedRow(t *testing.T, w *store.Workspace) query.BoxEntry {
	t.Helper()
	box := query.LoadBox(w, "shop")
	if !box.IsKnown() || len(box.Value.Inbox) != 1 {
		t.Fatalf("inbox = %+v (%s), want the one question still waiting", box.Value.Inbox, box.Reason)
	}
	return box.Value.Inbox[0]
}

// waitForComposerState polls the styled read internal/send classifies until
// the composer is in the wanted state.
func waitForComposerState(t *testing.T, ctx context.Context, rt runtime.Adapter, handle runtime.AgentHandle,
	kind harness.Kind, want send.ComposerState, within time.Duration, tail func() string) send.Classification {
	t.Helper()
	deadline := time.Now().Add(within)
	var last send.Classification
	for time.Now().Before(deadline) {
		screen, err := rt.ReadAgentStyled(ctx, handle, send.DefaultLines)
		if err == nil {
			if c, cerr := send.ClassifyComposer(kind, screen); cerr == nil {
				last = c
				if c.State == want {
					return c
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for the composer to read %s", want)
		case <-time.After(time.Second):
		}
	}
	t.Fatalf("the composer never read %s within %s (last %s %q)\npane:\n%s", want, within, last.State, last.Evidence, tail())
	return send.Classification{}
}
