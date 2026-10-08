package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// The message box's three actions through the Console's own ActionFunc
// (mvp.md task 15), over the fake Herdr adapter and a scripted pane. What is
// under test is the whole bridge - resolve the handle, verify the composer,
// type, record - not internal/send, which has its own captured-screen tests.

// claudeEmptyScreen and claudePendingScreen are the two composer states a
// box action turns on, in the shape internal/send's classifier measured
// against Claude Code 2.1.274 (its own testdata/screens): a composer inside
// a ruled box, empty or holding somebody else's half-typed line.
const (
	claudeRule            = "─────────────────────────────────────────────"
	claudeEmptyScreen     = claudeRule + "\n" + claude.ClaudeComposerMarker + "\n" + claudeRule + "\n"
	claudePendingScreen   = claudeRule + "\n" + claude.ClaudeComposerMarker + " half typed\n" + claudeRule + "\n"
	codexBoxPendingScreen = "› rebase onto main\n\n  gpt-5.6-terra high · /tmp/x\n"
)

// boxFixture is a workspace with a started Mate and one recorded crew whose
// pane the fake knows about - the state a reader is in when they press a key
// on a box entry. The crew is recorded directly rather than spawned:
// SpawnCrew's worktree saga is task 11's own test surface, and what this
// file needs is only the `.meta` the bridge resolves a handle from.
type boxFixture struct {
	ws     *store.Workspace
	rt     *runtime.Fake
	deps   spawn.Deps
	action console.ActionFunc
	mate   runtime.AgentHandle
	crew   runtime.AgentHandle
}

func newBoxFixture(t *testing.T) boxFixture {
	t.Helper()
	ws, deps := consoleFixture(t, "shop")
	rt, ok := deps.Runtime.(*runtime.Fake)
	if !ok {
		t.Fatalf("consoleFixture runtime is %T, want *runtime.Fake", deps.Runtime)
	}
	action := consoleAction(ws, deps)

	if _, err := action(context.Background(), console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate",
	}); err != nil {
		t.Fatalf("start the Mate: %v", err)
	}
	meta, err := ws.ReadMateMeta("shop")
	if err != nil {
		t.Fatalf("ReadMateMeta: %v", err)
	}
	session := runtime.SessionHandle{Name: meta[spawn.MetaSession]}
	mate := runtime.AgentHandle{Session: session, Name: meta[spawn.MetaAgent]}

	crewMeta := map[string]string{
		spawn.MetaHarness:   string(codex.KindCodex),
		spawn.MetaAgent:     "crew-shop-k3",
		spawn.MetaPane:      "pane-k3",
		spawn.MetaTab:       "tab-k3",
		spawn.MetaWorkspace: meta[spawn.MetaWorkspace],
		spawn.MetaSession:   session.Name,
		spawn.MetaTask:      "pick a database",
	}
	if err := ws.WriteCrewMeta("shop", "k3", crewMeta); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	crew := runtime.AgentHandle{
		Session: session, Name: "crew-shop-k3", RawID: "k3", Kind: codex.KindCodex,
		Tab: runtime.TabHandle{Session: session, TabID: "tab-k3", PaneID: "pane-k3", Label: "crew-k3"},
	}
	rt.PutAgent(crew, runtime.AgentIdle)

	if err := ws.AppendStatus("shop", "k3", "needs-decision: pick A or B"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	rt.SetReadOutput(mate, claudeEmptyScreen)
	rt.SetReadOutput(crew, codexEmptyScreen)
	return boxFixture{ws: ws, rt: rt, deps: deps, action: action, mate: mate, crew: crew}
}

// resolveRequest is the request the rail builds for the one inbox item
// (internal/ui/console/box_keys.go's boxResolveChoice), read off the same
// query.BoxView the rail draws so the test cannot invent a line the console
// would never send. It reads Inbox, not Entries: that is what the rail
// draws, and an item that is not in the inbox is not one a reader can act
// on.
func (f boxFixture) resolveRequest(t *testing.T) console.ActionRequest {
	t.Helper()
	box := query.LoadBox(f.ws, "shop")
	if !box.IsKnown() {
		t.Fatalf("LoadBox: %s", box.Reason)
	}
	if len(box.Value.Inbox) != 1 {
		t.Fatalf("inbox = %+v, want exactly the one needs-decision item", box.Value.Inbox)
	}
	e := box.Value.Inbox[0]
	if e.Verb != "needs-decision" || !e.Resolvable() {
		t.Fatalf("inbox item = %+v, want a resolvable needs-decision", e)
	}
	return console.ActionRequest{
		Action: console.ActionResolve, Target: "shop", TargetKind: "project",
		Crew: e.Crew, Input: e.Resolve, Key: e.AssignKey,
	}
}

func sentLines(t *testing.T, ws *store.Workspace) []store.SentEntry {
	t.Helper()
	entries, _, err := ws.ReadSent("shop", 0)
	if err != nil {
		t.Fatalf("ReadSent: %v", err)
	}
	return entries
}

// TestConsoleBoxResolveTypesTheResolveLineAndRecordsIt is Enter on an inbox
// item: the marked `resolve:` line reaches the Mate's composer carrying the
// crew's own question, the file to read, and the command that answers the
// crew. The status text is quoted rather than copied as the record: the file
// the line points at is still the record, which is why the path is there.
func TestConsoleBoxResolveTypesTheResolveLineAndRecordsIt(t *testing.T) {
	f := newBoxFixture(t)
	wantResolve := query.BoxResolveLine("shop", "k3", "pick A or B", f.ws.CrewStatus("shop", "k3"))
	req := f.resolveRequest(t)
	if req.Input != wantResolve {
		t.Fatalf("resolve line = %q, want %q", req.Input, wantResolve)
	}
	for _, part := range []string{
		`resolve: k3 asked: "pick A or B"`,
		"read " + f.ws.CrewStatus("shop", "k3"),
		`answer with mate send shop k3 "<one line>"`,
	} {
		if !strings.Contains(wantResolve, part) {
			t.Errorf("resolve line %q does not carry %q", wantResolve, part)
		}
	}

	out, err := f.action(context.Background(), req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.HasPrefix(out, "sent to "+f.mate.Name) {
		t.Errorf("outcome = %q, want it to say the line was sent to the idle Mate at once", out)
	}

	var typed []string
	for _, s := range f.rt.SentText {
		if s.Handle.Name == f.mate.Name {
			typed = append(typed, s.Text)
		}
	}
	if len(typed) != 1 {
		t.Fatalf("text typed into the Mate = %q, want exactly one line", typed)
	}
	if typed[0] != send.Marker+wantResolve {
		t.Errorf("typed %q, want the marker then the resolve line", typed[0])
	}

	sent := sentLines(t, f.ws)
	if len(sent) != 1 {
		t.Fatalf("sent.log = %+v, want exactly one line", sent)
	}
	if sent[0].Source != store.SourceApp || sent[0].Target != store.TargetMate {
		t.Errorf("sent.log line = %+v, want app -> mate", sent[0])
	}
	if sent[0].Text != wantResolve {
		t.Errorf("sent.log text = %q, want the resolve line without the marker", sent[0].Text)
	}
	// The path in the line must be absolute: the Mate's cwd is its own
	// workspace directory, not the project's, so `crews/k3.status` (relative
	// to the project) resolves to nothing there.
	if !strings.Contains(wantResolve, "read "+f.ws.Root()) {
		t.Fatalf("resolve line %q does not name a path rooted at the workspace (%s)", wantResolve, f.ws.Root())
	}
}

// TestConsoleBoxResolveEmptiesTheInbox is rule 1 of the inbox, end to end
// through the action seam: the crew's own next status line is what closes
// the item, and `resolve` on its own does not - handing the question to the
// Mate is not an answer to the crew.
func TestConsoleBoxResolveEmptiesTheInbox(t *testing.T) {
	f := newBoxFixture(t)
	if _, err := f.action(context.Background(), f.resolveRequest(t)); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	box := query.LoadBox(f.ws, "shop")
	if len(box.Value.Inbox) != 1 {
		t.Fatalf("inbox after a resolve = %+v, want the item still open until the crew is answered", box.Value.Inbox)
	}
	if err := f.ws.AppendSent("shop", store.SentEntry{
		Source: store.SourceMate, Target: store.CrewTarget("k3"), Text: "go with A",
	}); err != nil {
		t.Fatalf("AppendSent: %v", err)
	}
	box = query.LoadBox(f.ws, "shop")
	if len(box.Value.Inbox) != 0 {
		t.Fatalf("inbox after the Mate answered = %+v, want empty", box.Value.Inbox)
	}
	if len(box.Value.Entries) == 0 {
		t.Fatal("the merged log is empty; the inbox filter must not shrink the record")
	}
}

// TestConsoleBoxResolveQueuesBehindAPendingComposer is the rule mvp.md
// section 4 puts on the Mate's pane - the human owns that composer too, so a
// line already sitting in it is never typed over - under task 30's queue:
// the assign is not refused any more, it waits in the Mate's outbox, leaves
// no trace in sent.log (the box must never show the Mate a message no agent
// received), and the inbox row says it is queued. Once the composer is empty
// the console's sender loop types it exactly once, and the row says when.
func TestConsoleBoxResolveQueuesBehindAPendingComposer(t *testing.T) {
	f := newBoxFixture(t)
	f.rt.SetReadOutput(f.mate, claudePendingScreen)
	req := f.resolveRequest(t)

	out, err := f.action(context.Background(), req)
	if err != nil {
		t.Fatalf("resolve into a pending composer: %v", err)
	}
	if !strings.HasPrefix(out, "queued for the Mate") || !strings.Contains(out, "unsubmitted text") {
		t.Errorf("outcome = %q, want it queued and why", out)
	}
	if typed := f.mateTyped(); len(typed) != 0 {
		t.Fatalf("a queued resolve typed %q into the Mate over the captain's text", typed)
	}
	if sent := sentLines(t, f.ws); len(sent) != 0 {
		t.Fatalf("a queued resolve wrote %+v to sent.log, want nothing", sent)
	}
	items, err := f.ws.ReadOutbox("shop")
	if err != nil {
		t.Fatalf("ReadOutbox: %v", err)
	}
	if len(items) != 1 || !items[0].Queued() || items[0].Key != req.Key || items[0].Text != req.Input ||
		!strings.Contains(items[0].LastRefusal, "half typed") {
		t.Fatalf("outbox = %+v, want the resolve line queued under its entry's key with the refusal", items)
	}
	row := f.inboxRow(t)
	if row.Assigned.State != query.BoxAssignQueued {
		t.Fatalf("inbox row = %+v, want it marked assigned, queued", row)
	}

	// The captain submits their own line and the Mate's turn ends: the
	// console's loop, not another keystroke, delivers the assign.
	f.rt.SetReadOutput(f.mate, claudeEmptyScreen)
	loop := consoleOutbox(f.ws, f.deps)
	for i := 0; i < 3; i++ {
		if err := loop.Drain(context.Background()); err != nil {
			t.Fatalf("Drain: %v", err)
		}
	}
	typed := f.mateTyped()
	if len(typed) != 1 || typed[0] != send.Marker+req.Input {
		t.Fatalf("typed %q into the Mate, want the marked resolve line exactly once", typed)
	}
	sent := sentLines(t, f.ws)
	if len(sent) != 1 || sent[0].Source != store.SourceApp || sent[0].Target != store.TargetMate || sent[0].Text != req.Input {
		t.Fatalf("sent.log = %+v, want the one app -> mate line", sent)
	}
	row = f.inboxRow(t)
	if row.Assigned.State != query.BoxAssignSent || row.Assigned.SentAt.IsZero() {
		t.Fatalf("inbox row = %+v, want it marked assigned with a time - and still in the inbox", row)
	}
}

// TestConsoleBoxResolveQueuesBehindABusyMate is the case task 24 measured
// four or five times in a row: the Mate mid-turn. The assign returns at once,
// queued, instead of refusing.
func TestConsoleBoxResolveQueuesBehindABusyMate(t *testing.T) {
	f := newBoxFixture(t)
	f.rt.SetReadOutput(f.mate, "✶ Pollinating…\n"+claudeEmptyScreen)

	out, err := f.action(context.Background(), f.resolveRequest(t))
	if err != nil {
		t.Fatalf("resolve into a busy Mate: %v", err)
	}
	if !strings.Contains(out, "queued for the Mate (the Mate is mid-turn)") {
		t.Errorf("outcome = %q, want it queued behind the turn", out)
	}
	if typed := f.mateTyped(); len(typed) != 0 {
		t.Fatalf("typed %q into a Mate mid-turn", typed)
	}
	for _, call := range f.rt.Calls {
		if call == "PromptAgent" {
			t.Fatalf("the assign was handed to herdr agent prompt; calls = %v", f.rt.Calls)
		}
	}
}

// TestConsoleBoxResolveTwiceQueuesOnce is the dedup: the same inbox entry
// assigned again, while queued or after it was sent, is not queued a second
// time, and the outcome says when it was.
func TestConsoleBoxResolveTwiceQueuesOnce(t *testing.T) {
	f := newBoxFixture(t)
	f.rt.SetReadOutput(f.mate, claudePendingScreen)
	req := f.resolveRequest(t)
	if _, err := f.action(context.Background(), req); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	out, err := f.action(context.Background(), req)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if !strings.HasPrefix(out, "already assigned ") || !strings.Contains(out, "still queued") {
		t.Errorf("outcome = %q, want \"already assigned HH:MM\" and that it is still queued", out)
	}

	f.rt.SetReadOutput(f.mate, claudeEmptyScreen)
	out, err = f.action(context.Background(), req)
	if err != nil {
		t.Fatalf("third resolve: %v", err)
	}
	if !strings.HasPrefix(out, "already assigned ") || !strings.Contains(out, ", sent ") {
		t.Errorf("outcome = %q, want \"already assigned HH:MM\" and when it was sent", out)
	}
	if _, err := f.action(context.Background(), req); err != nil {
		t.Fatalf("fourth resolve: %v", err)
	}
	if typed := f.mateTyped(); len(typed) != 1 {
		t.Fatalf("typed %q into the Mate across four presses, want one line", typed)
	}
	items, err := f.ws.ReadOutbox("shop")
	if err != nil {
		t.Fatalf("ReadOutbox: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("outbox = %+v, want one item", items)
	}
}

func (f boxFixture) mateTyped() []string {
	var typed []string
	for _, s := range f.rt.SentText {
		if s.Handle.Name == f.mate.Name {
			typed = append(typed, s.Text)
		}
	}
	return typed
}

func (f boxFixture) inboxRow(t *testing.T) query.BoxEntry {
	t.Helper()
	box := query.LoadBox(f.ws, "shop")
	if !box.IsKnown() || len(box.Value.Inbox) != 1 {
		t.Fatalf("inbox = %+v (%s), want the one item still waiting", box.Value.Inbox, box.Reason)
	}
	return box.Value.Inbox[0]
}

// TestConsoleBoxReplyTypesIntoTheCrewAndRecordsTheUser is `r`: the line goes
// into the crew's own composer with no marker byte - it is the user's line,
// not the app's - and is recorded as such.
func TestConsoleBoxReplyTypesIntoTheCrewAndRecordsTheUser(t *testing.T) {
	f := newBoxFixture(t)

	out, err := f.action(context.Background(), console.ActionRequest{
		Action: console.ActionReply, Target: "shop", TargetKind: "project",
		Crew: "k3", Input: "A",
	})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if !strings.Contains(out, "crew-shop-k3") {
		t.Errorf("outcome = %q, want it to name the crew agent it replied to", out)
	}

	var typed []string
	for _, s := range f.rt.SentText {
		if s.Handle.Name == f.crew.Name {
			typed = append(typed, s.Text)
		}
	}
	if len(typed) != 1 || typed[0] != "A" {
		t.Fatalf("text typed into the crew = %q, want exactly [\"A\"] with no marker byte", typed)
	}

	sent := sentLines(t, f.ws)
	if len(sent) != 1 {
		t.Fatalf("sent.log = %+v, want exactly one line", sent)
	}
	if sent[0].Source != store.SourceUser {
		t.Errorf("sent.log source = %q, want %q: a reply typed by a person is the person's", sent[0].Source, store.SourceUser)
	}
	if sent[0].Target != store.CrewTarget("k3") || sent[0].Text != "A" {
		t.Errorf("sent.log line = %+v, want user -> crew:k3 \"A\"", sent[0])
	}

	// And the reply is in the box on the next read, which is what makes the
	// rail a log of what was actually said rather than of what was asked.
	box := query.LoadBox(f.ws, "shop")
	var found bool
	for _, e := range box.Value.Entries {
		if e.Kind == query.BoxMessage && e.Source == store.SourceUser && e.Text == "A" {
			found = true
		}
	}
	if !found {
		t.Errorf("the reply is not in the box: %+v", box.Value.Entries)
	}
}

// TestConsoleBoxReplyRefusedOnAPendingCrewComposer is the same refusal on
// the crew side. It matters as much as the Mate's: a crew mid-typing is a
// crew whose own half-line would be concatenated with the reply.
func TestConsoleBoxReplyRefusedOnAPendingCrewComposer(t *testing.T) {
	f := newBoxFixture(t)
	f.rt.SetReadOutput(f.crew, codexBoxPendingScreen)

	_, err := f.action(context.Background(), console.ActionRequest{
		Action: console.ActionReply, Target: "shop", TargetKind: "project",
		Crew: "k3", Input: "A",
	})
	if !errors.Is(err, send.ErrComposerPending) {
		t.Fatalf("reply error = %v, want send.ErrComposerPending", err)
	}
	if sent := sentLines(t, f.ws); len(sent) != 0 {
		t.Fatalf("a refused reply wrote %+v to sent.log, want nothing", sent)
	}
}

// TestConsoleBoxPeekReadsTheCrewPaneAndWritesNothing is `p`: looking at a
// screen is not communication, so it leaves no line in sent.log and types
// nothing into any pane.
func TestConsoleBoxPeekReadsTheCrewPaneAndWritesNothing(t *testing.T) {
	f := newBoxFixture(t)
	f.rt.SetReadOutput(f.crew, "needs-decision: pick A or B\n› \n")

	out, err := f.action(context.Background(), console.ActionRequest{
		Action: console.ActionPeek, Target: "shop", TargetKind: "project", Crew: "k3",
	})
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if !strings.Contains(out, "needs-decision: pick A or B") {
		t.Errorf("peek returned %q, want the crew's own pane text", out)
	}
	if len(f.rt.SentText) != 0 || len(f.rt.SentKeys) != 0 {
		t.Errorf("a peek typed into a pane: text=%+v keys=%+v", f.rt.SentText, f.rt.SentKeys)
	}
	if sent := sentLines(t, f.ws); len(sent) != 0 {
		t.Fatalf("a peek wrote %+v to sent.log, want nothing", sent)
	}
	var asked bool
	for _, call := range f.rt.ReadCalls {
		if strings.HasSuffix(call, "/crew-shop-k3:40") {
			asked = true
		}
	}
	if !asked {
		t.Errorf("peek read calls = %v, want one asking crew-shop-k3 for 40 lines", f.rt.ReadCalls)
	}
}

// TestConsoleBoxActionsRefuseAStoppedAgent: a crew whose `.meta` no longer
// names a pane is stopped, not broken, and the three actions must say so
// rather than reaching for a handle that names nothing.
func TestConsoleBoxActionsRefuseAStoppedAgent(t *testing.T) {
	f := newBoxFixture(t)
	if err := f.ws.WriteCrewMeta("shop", "k3", map[string]string{spawn.MetaHarness: "codex"}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	for _, action := range []console.Action{console.ActionReply, console.ActionPeek} {
		_, err := f.action(context.Background(), console.ActionRequest{
			Action: action, Target: "shop", TargetKind: "project", Crew: "k3", Input: "A",
		})
		if err == nil {
			t.Fatalf("%s against a stopped crew returned no error", action)
		}
		if !strings.Contains(err.Error(), "not running") {
			t.Errorf("%s refusal = %v, want it to say the crew is not running", action, err)
		}
	}
	if sent := sentLines(t, f.ws); len(sent) != 0 {
		t.Fatalf("a refused action wrote %+v to sent.log", sent)
	}
}

// A dialog over the Mate's pane and a pane that moved while it was read are
// refusals like the other three: nothing typed, the line queued, and the
// outcome says why in a few words rather than failing the action.
func TestConsoleBoxQueuesBehindTheTwoNewRefusals(t *testing.T) {
	for _, err := range []error{send.ErrDialogOpen, send.ErrPaneChanged} {
		coded := observability.WrapError(observability.CodeStateConflict, "a send refusal", err)
		if !boxSendRefusal(coded) {
			t.Errorf("%v is not a box refusal", err)
		}
		if reason := refusalReason(coded); reason == coded.Error() {
			t.Errorf("%v has no short reason", err)
		}
	}

	f := newBoxFixture(t)
	f.deps.Observer = dialogObserver{}
	out, err := consoleAction(f.ws, f.deps)(context.Background(), f.resolveRequest(t))
	if err != nil {
		t.Fatalf("resolve under a dialog: %v", err)
	}
	if !strings.Contains(out, "queued for the Mate (a dialog is open over its pane)") {
		t.Errorf("outcome = %q, want it queued behind the dialog", out)
	}
	if typed := f.mateTyped(); len(typed) != 0 {
		t.Fatalf("typed %q under a dialog", typed)
	}
}

// dialogObserver reads every pane as a trust dialog, as Jev might.
type dialogObserver struct{}

func (dialogObserver) Observe(context.Context, harness.ScreenProfile, string) (screen.Observation, error) {
	return screen.Observation{Composer: screen.ComposerUnknown, Deterministic: screen.ComposerUnknown, Dialog: screen.DialogTrust,
		Highlight: -1, Confidence: 0.99, Source: "jev"}, nil
}
