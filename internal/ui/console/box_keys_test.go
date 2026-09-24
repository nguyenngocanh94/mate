package console

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// fixtureCrewID is the running crew of sampleTree: the one a box row can
// actually open, because opening a crew goes through the same snapshot the
// tree row would (box_keys.go's boxEntryCrewRow).
const fixtureCrewID = "crew_01J9P6Q6W0E5V8XK2M4B8DT"

// boxAsking is a one-item inbox: crew asked something and is waiting.
func boxAsking(crew string) query.Field[query.BoxView] {
	e := query.BoxEntry{
		Seq: 0, At: time.Date(2026, 9, 10, 14, 1, 0, 0, time.UTC),
		Kind: query.BoxStatus, Source: "crew", Target: "crew:" + crew, Crew: crew,
		Verb: "needs-decision", Text: "pick A or B", Attention: true,
		Resolve: testResolveLine(crew, "pick A or B"),
	}
	return query.KnownField(query.BoxView{
		Entries: []query.BoxEntry{e}, Inbox: []query.BoxEntry{e},
		Crews: 1, Awaiting: 1, LastAt: e.At,
	})
}

// boxMateWedged is the daemon's own incident: an inbox item whose crew is
// the literal "mate" (internal/autopilot/doc.go).
func boxMateWedged() query.Field[query.BoxView] {
	e := query.BoxEntry{
		Seq: 0, At: time.Date(2026, 9, 10, 14, 4, 0, 0, time.UTC),
		Kind: query.BoxIncident, Source: "observer", Crew: "mate",
		Verb: "wedged", Text: "the composer has held unsent text for 6m",
		Attention: true, Resolve: "resolve: incident wedged mate - the composer has held unsent text for 6m",
	}
	return query.KnownField(query.BoxView{
		Entries: []query.BoxEntry{e}, Inbox: []query.BoxEntry{e},
		Crews: 1, Awaiting: 1, LastAt: e.At,
	})
}

// Where the box keys live, per focus zone (mvp.md task 15,
// session_focus.go). The routing is the part a reader gets wrong at no cost
// to the tests and at real cost to them: an `a` typed with the terminal
// focused is a letter in the harness's composer, not an assign.
//
// The vocabulary itself is the 2026-09-19 decision: the box is a place to
// act, not to read. Enter opens the pane of the crew the row names, `a`
// hands the row to the Mate, `l` swaps the inbox for the whole log.

// recordingAction captures what the Console asked its ActionFunc for.
type recordingAction struct {
	reqs []ActionRequest
	out  string
	err  error
}

func (r *recordingAction) run(_ context.Context, req ActionRequest) (string, error) {
	r.reqs = append(r.reqs, req)
	return r.out, r.err
}

// streamBoxFixture is an open Mate stream carrying the pinned fixture box.
func streamBoxFixture(t *testing.T, action *recordingAction) Model {
	t.Helper()
	m, _, _ := narrowMateStreamFixture(t, nil)
	m.action = action.run
	m.w, m.h = 120, 36
	m.sess.snapshot.Target = SessionTarget{Kind: SessionTargetMate, ID: "mate_1", ProjectID: "shop"}
	m.sess.target = m.sess.snapshot.Target
	m.sess.snapshot.Box = sessionTestBox()
	return m
}

// TestStreamModeBoxKeysNeedBoxFocus: with the terminal focused the box's
// keys are bytes for the agent's own terminal; with the box focused they
// are the box's, bare.
func TestStreamModeBoxKeysNeedBoxFocus(t *testing.T) {
	action := &recordingAction{out: "delivered"}
	m := streamBoxFixture(t, action)

	for _, k := range []string{"a", "l", "enter"} {
		m, _ = send(t, m, key(k))
	}
	if len(action.reqs) != 0 {
		t.Fatalf("keys under terminal focus ran %+v; they belong to the agent's terminal", action.reqs)
	}
	if m.boxAll {
		t.Fatal("an l under terminal focus toggled [all] instead of reaching the harness")
	}

	// `a` under box focus assigns the selected item, which defaults to the
	// newest - k9's blocked line.
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	m, cmd := send(t, m, key("a"))
	if cmd == nil {
		t.Fatal("`a` under box focus issued no command")
	}
	msg, ok := cmd().(actionDoneMsg)
	if !ok {
		t.Fatalf("`a` under box focus produced %T, want actionDoneMsg", cmd())
	}
	if msg.choice.action != ActionResolve {
		t.Fatalf("`a` under box focus ran %s, want the resolve request", msg.choice.action)
	}
	if len(action.reqs) != 1 {
		t.Fatalf("action requests = %+v, want exactly the assign", action.reqs)
	}
	req := action.reqs[0]
	if req.Crew != "k9" || req.Input != m.sess.snapshot.Box.Value.Inbox[1].Resolve {
		t.Errorf("assign request = %+v, want crew k9 and its resolve line", req)
	}
}

// TestBoxEnterFromTheRailOpensTheCrewAfterTheStreamCloses is the whole of
// the 2026-09-19 decision on the rail: Enter on a row is navigation, not a
// message, and it never opens a second PTY on top of the first. The Mate's
// stream is closed first, and the crew's session only begins once that
// close has reported back.
func TestBoxEnterFromTheRailOpensTheCrewAfterTheStreamCloses(t *testing.T) {
	m, channel := mouseBoxFixture(t)
	m.sess.zone = zoneBox
	m.sess.snapshot.Box = boxAsking(fixtureCrewID)
	m.sess.boxSel = 0

	m, cmd := send(t, m, key("enter"))
	if m.sess.phase != sessionClosing {
		t.Fatalf("phase after Enter on a rail row = %v, want the Mate's stream closing first", m.sess.phase)
	}
	if m.pendingBoxOpen.Crew != fixtureCrewID {
		t.Fatalf("pending open = %+v, want the crew the row names", m.pendingBoxOpen)
	}
	if cmd == nil {
		t.Fatal("Enter on a rail row issued no close command")
	}
	// The close runs inside the Cmd; running it is what shuts the PTY, and
	// only its result starts the crew's own open.
	m, cmd = send(t, m, cmd())
	if !channel.isClosed() {
		t.Fatal("the Mate's channel is still open while the crew's session begins")
	}
	if m.pendingBoxOpen.Crew != "" {
		t.Fatalf("pending open survived the close: %+v", m.pendingBoxOpen)
	}
	if m.sess.target.Kind != SessionTargetCrew || m.sess.target.ID != fixtureCrewID {
		t.Fatalf("session target after the close = %+v, want crew %s", m.sess.target, fixtureCrewID)
	}
	if m.sess.phase != sessionOpening {
		t.Fatalf("phase after the close = %v, want the crew's own open", m.sess.phase)
	}
	if cmd == nil {
		t.Fatal("the pending open issued no command of its own")
	}
}

// TestBoxEnterOnTheMateIncidentJustFocusesTheTerminal: the daemon's own
// `wedged` incident names the crew "mate", and the Mate is the view already
// on screen. Closing and reopening it would be a flicker that answers
// nothing, so the row moves focus to the pane instead.
func TestBoxEnterOnTheMateIncidentJustFocusesTheTerminal(t *testing.T) {
	m, channel := mouseBoxFixture(t)
	m.sess.zone = zoneBox
	m.sess.snapshot.Box = boxMateWedged()
	m.sess.boxSel = 0

	m, cmd := send(t, m, key("enter"))
	if cmd != nil {
		t.Fatalf("Enter on the Mate's own incident ran %T; the Mate is already open", cmd())
	}
	if channel.isClosed() {
		t.Fatal("Enter on the Mate's own incident closed the stream it was looking at")
	}
	if m.sess.zone != zoneTerminal {
		t.Fatalf("zone after Enter on the Mate's own incident = %v, want the terminal", m.sess.zone)
	}
	if m.sess.phase != sessionActive {
		t.Fatalf("phase = %v, want the session still active", m.sess.phase)
	}
}

// TestBoxRailShowsOnlyTheInboxUntilAllIsToggled is the user's whole
// complaint, pinned: the rail draws the two open questions out of a
// five-entry log, `l` swaps in the log, and `l` again goes back.
func TestBoxRailShowsOnlyTheInboxUntilAllIsToggled(t *testing.T) {
	m := streamBoxFixture(t, &recordingAction{})
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	b, _ := m.sessionBoxList()
	if got := len(b.rows()); got != 2 {
		t.Fatalf("rail rows = %d, want only the two unresolved items", got)
	}
	m, _ = send(t, m, key("l"))
	if !m.boxAll {
		t.Fatal("`l` under box focus did not turn [all] on")
	}
	b, _ = m.sessionBoxList()
	if got := len(b.rows()); got != len(m.sess.snapshot.Box.Value.Entries) {
		t.Fatalf("[all] rows = %d, want the whole log", got)
	}
	if got := m.sessionRailState().sel; got != len(b.rows())-1 {
		t.Fatalf("selection after the toggle = %d, want the newest row of the new list", got)
	}
	m, _ = send(t, m, key("l"))
	if m.boxAll {
		t.Fatal("a second `l` did not turn [all] off again")
	}
}

// TestBoxFocusMovesTheRailSelectionWithBareKeys pins j/k, the movement half
// of the same rule: bare under box focus, the agent's under terminal focus.
func TestBoxFocusMovesTheRailSelectionWithBareKeys(t *testing.T) {
	m := streamBoxFixture(t, &recordingAction{})
	if got := m.sessionRailState().sel; got != 1 {
		t.Fatalf("initial selection = %d, want the newest inbox item (1)", got)
	}
	m, _ = send(t, m, key("k"))
	if got := m.sessionRailState().sel; got != 1 {
		t.Fatalf("a k under terminal focus moved the selection to %d; it belongs to the agent", got)
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	m, _ = send(t, m, key("k"))
	if got := m.sessionRailState().sel; got != 0 {
		t.Fatalf("selection after k under box focus = %d, want 0", got)
	}
	m, _ = send(t, m, key("j"))
	if got := m.sessionRailState().sel; got != 1 {
		t.Fatalf("selection after j under box focus = %d, want 1", got)
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	if m.sess.zone != zoneTerminal {
		t.Fatalf("F2 did not toggle back to the terminal (zone %v)", m.sess.zone)
	}
}

// TestBoxAssignRefusesAMessageEntry: a message is something the Mate
// already sent or was sent, so there is nothing in it to hand over. The
// inbox never holds one, so the row has to be reached through `[all]` -
// which is exactly the case this refusal exists for. It is on the outcome
// line rather than silent, the only way a reader can tell it from a lost
// keystroke.
func TestBoxAssignRefusesAMessageEntry(t *testing.T) {
	action := &recordingAction{}
	m := streamBoxFixture(t, action)

	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	m, _ = send(t, m, key("l")) // the whole log, messages included
	m.sess.boxSel = 1           // the user->mate message
	m, cmd := send(t, m, key("a"))
	if cmd != nil {
		t.Fatalf("assigning a message issued a command: %T", cmd())
	}
	if len(action.reqs) != 0 {
		t.Fatalf("assigning a message reached ActionFunc: %+v", action.reqs)
	}
	if m.boxMsg.tone != toneError || m.boxMsg.text == "" {
		t.Fatalf("outcome = %+v, want a refusal naming why", m.boxMsg)
	}
	frame := RenderSessionFrame(m.sess.snapshot, "", m.sessionRailState(), 120, 36, unicodeGlyphs, plainPalette())
	if !containsLine(frame, "Assign refused") {
		t.Errorf("the rail does not show the refusal:\n%s", frame)
	}
}

// TestBoxEnterRefusesAnEntryThatNamesNoCrew: a message to the Mate's own
// pane has no crew to open, and Enter says so rather than doing nothing.
func TestBoxEnterRefusesAnEntryThatNamesNoCrew(t *testing.T) {
	m := streamBoxFixture(t, &recordingAction{})
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	m, _ = send(t, m, key("l"))
	m.sess.boxSel = 1 // the user->mate message

	m, cmd := send(t, m, key("enter"))
	if cmd != nil {
		t.Fatalf("Enter on a crewless entry ran %T", cmd())
	}
	if m.sess.phase != sessionActive {
		t.Fatalf("phase = %v, want the open session untouched", m.sess.phase)
	}
	if m.boxMsg.tone != toneError || !containsLine(m.boxMsg.text, "names no crew") {
		t.Fatalf("outcome = %+v, want a refusal naming why", m.boxMsg)
	}
}

// TestProjectFrameBoxKeysAreBare is the other half of the rule: on the
// project frame the Console owns the keyboard, so the keys need no prefix -
// but only once Tab has focused the panel, because `a` and Enter already
// mean something on the list.
func TestProjectFrameBoxKeysAreBare(t *testing.T) {
	action := &recordingAction{out: "delivered"}
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m.action = action.run
	m, _ = send(t, m, key("enter")) // into the project frame

	// On the list, `a` is still the action menu and `r` still refreshes.
	m, _ = send(t, m, key("a"))
	if len(action.reqs) != 0 {
		t.Fatalf("`a` on the list ran %+v; it opens the action menu there", action.reqs)
	}
	m, _ = send(t, m, key("esc"))

	m, _ = send(t, m, key("tab")) // list -> inspector
	m, _ = send(t, m, key("tab")) // inspector -> box
	if m.focus != paneBox {
		t.Fatalf("focus = %v, want paneBox", m.focus)
	}
	// The panel draws the inbox, so its one row is already the
	// needs-decision line - no stepping back past a message to reach it,
	// which is the whole point of the filter.
	if got := m.projectBoxSelection(); got != 0 {
		t.Fatalf("panel selection = %d, want the single inbox item (0)", got)
	}
	m, cmd := send(t, m, key("a"))
	if cmd == nil {
		t.Fatal("bare `a` on the focused panel issued no command")
	}
	if _, ok := cmd().(actionDoneMsg); !ok {
		t.Fatalf("bare `a` produced %T, want actionDoneMsg", cmd())
	}
	if len(action.reqs) != 1 || action.reqs[0].Action != ActionResolve {
		t.Fatalf("action requests = %+v, want one resolve", action.reqs)
	}
	if action.reqs[0].Target != "proj_01J9M1F8K2Q7C4H6N0R3V5T8YZ" || action.reqs[0].Crew != "k3" {
		t.Errorf("assign request = %+v, want this project and crew k3", action.reqs[0])
	}
}

// TestProjectFrameBoxEnterOpensTheCrew: from the project frame there is no
// stream to close, so Enter on a row goes straight to the same place Enter
// on that crew's tree row goes.
func TestProjectFrameBoxEnterOpensTheCrew(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Box = boxAsking(fixtureCrewID)
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	m = m.WithSession(func(context.Context, SessionTarget) (SessionSnapshot, error) {
		return SessionSnapshot{}, nil
	}, nil, nil)
	m, _ = send(t, m, key("enter")) // into the project frame
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	if m.focus != paneBox {
		t.Fatalf("focus = %v, want paneBox", m.focus)
	}
	e, ok := boxSelectedEntry(m.projectBoxList(), m.projectBoxSelection())
	if !ok || e.Crew != fixtureCrewID {
		t.Fatalf("setup: the panel's selected entry is %+v, want the fixture crew", e)
	}

	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter on the panel opened nothing")
	}
	if m.sess.target.Kind != SessionTargetCrew || m.sess.target.ID != e.Crew {
		t.Fatalf("session target after Enter = %+v, want crew %s", m.sess.target, e.Crew)
	}
}

// TestProjectFrameBoxEnterRefusesACrewThatLeftTheSnapshot: a crew closed
// since the last read has no pane to open, and the refusal says so on the
// frame's own message line rather than opening nothing in silence.
func TestProjectFrameBoxEnterRefusesACrewThatLeftTheSnapshot(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Box = boxAsking(fixtureCrewID)
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	// The box still carries the crew's question; the crew itself is gone.
	m.tree.Projects[0].Crews = nil
	m, _ = send(t, m, key("enter"))

	if m.sess.phase != sessionIdle {
		t.Fatalf("phase = %v, want no session at all", m.sess.phase)
	}
	if m.msg.tone != toneError || !containsLine(m.msg.text, "not in this snapshot") {
		t.Fatalf("message = %+v, want a refusal saying the crew is gone", m.msg)
	}
}
