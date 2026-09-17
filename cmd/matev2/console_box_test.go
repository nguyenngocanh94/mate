package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/ui/console"
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
	claudeEmptyScreen     = claudeRule + "\n" + harness.ClaudeComposerMarker + "\n" + claudeRule + "\n"
	claudePendingScreen   = claudeRule + "\n" + harness.ClaudeComposerMarker + " half typed\n" + claudeRule + "\n"
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
		spawn.MetaHarness:   string(harness.KindCodex),
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
		Session: session, Name: "crew-shop-k3", RawID: "k3", Kind: harness.KindCodex,
		Tab: runtime.TabHandle{Session: session, TabID: "tab-k3", PaneID: "pane-k3", Label: "crew-k3"},
	}
	rt.PutAgent(crew, runtime.AgentIdle)

	if err := ws.AppendStatus("shop", "k3", "needs-decision: pick A or B"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	rt.SetReadOutput(mate, claudeEmptyScreen)
	rt.SetReadOutput(crew, codexEmptyScreen)
	return boxFixture{ws: ws, rt: rt, action: action, mate: mate, crew: crew}
}

// forwardRequest is the request the rail builds for the needs-decision entry
// (internal/ui/console/box_keys.go's boxForwardChoice), read off the same
// query.BoxView the rail draws so the test cannot invent a signal line the
// console would never send.
func (f boxFixture) forwardRequest(t *testing.T) console.ActionRequest {
	t.Helper()
	box := query.LoadBox(f.ws, "shop")
	if !box.IsKnown() {
		t.Fatalf("LoadBox: %s", box.Reason)
	}
	for _, e := range box.Value.Entries {
		if e.Kind == query.BoxStatus && e.Verb == "needs-decision" {
			if !e.Forwardable() {
				t.Fatalf("the needs-decision entry is not forwardable: %+v", e)
			}
			return console.ActionRequest{
				Action: console.ActionForward, Target: "shop", TargetKind: "project",
				Crew: e.Crew, Input: e.Signal,
			}
		}
	}
	t.Fatalf("no needs-decision entry in the box: %+v", box.Value.Entries)
	return console.ActionRequest{}
}

func sentLines(t *testing.T, ws *store.Workspace) []store.SentEntry {
	t.Helper()
	entries, _, err := ws.ReadSent("shop", 0)
	if err != nil {
		t.Fatalf("ReadSent: %v", err)
	}
	return entries
}

// TestConsoleBoxForwardTypesTheSignalAndRecordsIt is Enter on a status
// entry: the marked `signal:` line reaches the Mate's composer, and only
// that line - never the status text itself, which the Mate reads from the
// file the signal points at.
func TestConsoleBoxForwardTypesTheSignalAndRecordsIt(t *testing.T) {
	f := newBoxFixture(t)
	wantSignal := query.BoxStatusSignal(f.ws.CrewStatus("shop", "k3"))
	req := f.forwardRequest(t)
	if req.Input != wantSignal {
		t.Fatalf("signal = %q, want %q", req.Input, wantSignal)
	}

	out, err := f.action(context.Background(), req)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if !strings.Contains(out, wantSignal) {
		t.Errorf("outcome = %q, want it to name the line it delivered", out)
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
	if typed[0] != send.Marker+wantSignal {
		t.Errorf("typed %q, want the marker then the signal line", typed[0])
	}

	sent := sentLines(t, f.ws)
	if len(sent) != 1 {
		t.Fatalf("sent.log = %+v, want exactly one line", sent)
	}
	if sent[0].Source != store.SourceApp || sent[0].Target != store.TargetMate {
		t.Errorf("sent.log line = %+v, want app -> mate", sent[0])
	}
	if sent[0].Text != wantSignal {
		t.Errorf("sent.log text = %q, want the signal line without the marker", sent[0].Text)
	}
	// The signal must be an absolute path: the Mate's cwd is its own
	// workspace directory, not the project's, so `crews/k3.status` (relative
	// to the project) resolves to nothing there.
	if !strings.HasPrefix(wantSignal, "signal: "+f.ws.Root()) {
		t.Fatalf("signal %q is not rooted at the workspace (%s)", wantSignal, f.ws.Root())
	}
}

// TestConsoleBoxForwardRefusedOnAPendingComposerRecordsNothing is the rule
// mvp.md section 4 puts on the Mate's pane specifically: the human owns that
// composer too, so a line already sitting in it is never typed over - and a
// send that did not happen must leave no trace in sent.log, or the box would
// show the Mate a message no agent ever received.
func TestConsoleBoxForwardRefusedOnAPendingComposerRecordsNothing(t *testing.T) {
	f := newBoxFixture(t)
	f.rt.SetReadOutput(f.mate, claudePendingScreen)

	out, err := f.action(context.Background(), f.forwardRequest(t))
	if err == nil {
		t.Fatalf("forward into a pending composer returned %q and no error", out)
	}
	if !errors.Is(err, send.ErrComposerPending) {
		t.Fatalf("forward error = %v, want send.ErrComposerPending", err)
	}
	if !boxSendRefusal(err) {
		t.Errorf("boxSendRefusal(%v) = false, want a refusal the console reports on its outcome line", err)
	}
	if !strings.Contains(err.Error(), "half typed") {
		t.Errorf("refusal %q does not quote the pending text it refused to overwrite", err)
	}

	for _, s := range f.rt.SentText {
		if s.Handle.Name == f.mate.Name {
			t.Errorf("a refused forward typed %q into the Mate", s.Text)
		}
	}
	if sent := sentLines(t, f.ws); len(sent) != 0 {
		t.Fatalf("a refused forward wrote %+v to sent.log, want nothing", sent)
	}
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
