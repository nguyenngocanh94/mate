package console

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// Where the three box keys live, per mode (mvp.md task 15). The routing is
// the part a reader gets wrong at no cost to the tests and at real cost to
// them: an unprefixed `r` in stream mode is a letter in the harness's
// composer, not a reply.

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

// TestStreamModeBoxKeysNeedThePrefix: unprefixed, all three keys are bytes
// for the agent's own terminal; behind Ctrl+b they are the box's.
func TestStreamModeBoxKeysNeedThePrefix(t *testing.T) {
	action := &recordingAction{out: "delivered"}
	m := streamBoxFixture(t, action)

	for _, k := range []string{"r", "p", "enter"} {
		m, _ = send(t, m, key(k))
	}
	if len(action.reqs) != 0 {
		t.Fatalf("unprefixed keys ran %+v; in stream mode they belong to the agent's terminal", action.reqs)
	}
	if m.boxReply {
		t.Fatal("an unprefixed r opened the reply input instead of reaching the harness")
	}

	// Ctrl+b Enter forwards the selected entry, which defaults to the newest
	// - the needs-decision line.
	m, cmd := send(t, m, key("ctrl+b"))
	m, cmd = send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Ctrl+b Enter issued no command")
	}
	msg, ok := cmd().(actionDoneMsg)
	if !ok {
		t.Fatalf("Ctrl+b Enter produced %T, want actionDoneMsg", cmd())
	}
	if msg.choice.action != ActionForward {
		t.Fatalf("Ctrl+b Enter ran %s, want forward", msg.choice.action)
	}
	if len(action.reqs) != 1 {
		t.Fatalf("action requests = %+v, want exactly the forward", action.reqs)
	}
	req := action.reqs[0]
	if req.Crew != "k3" || req.Input != query.BoxStatusSignal("k3") {
		t.Errorf("forward request = %+v, want crew k3 and its status signal", req)
	}
}

// TestStreamModeCtrlBMovesTheRailSelection pins Ctrl+b j/k, the movement
// half of the same rule.
func TestStreamModeCtrlBMovesTheRailSelection(t *testing.T) {
	m := streamBoxFixture(t, &recordingAction{})
	if got := m.sessionRailState().sel; got != 2 {
		t.Fatalf("initial selection = %d, want the newest entry (2)", got)
	}
	m, _ = send(t, m, key("ctrl+b"))
	m, _ = send(t, m, key("k"))
	if got := m.sessionRailState().sel; got != 1 {
		t.Fatalf("selection after Ctrl+b k = %d, want 1", got)
	}
	m, _ = send(t, m, key("k"))
	if got := m.sessionRailState().sel; got != 1 {
		t.Fatalf("a bare k moved the selection to %d; unprefixed keys belong to the agent", got)
	}
	m, _ = send(t, m, key("ctrl+b"))
	m, _ = send(t, m, key("j"))
	if got := m.sessionRailState().sel; got != 2 {
		t.Fatalf("selection after Ctrl+b j = %d, want 2", got)
	}
}

// TestBoxForwardRefusesAMessageEntry: a message is something the Mate
// already sent or was sent, so forwarding it says nothing. The refusal is on
// the outcome line rather than silent, which is the only way a reader can
// tell it from a lost keystroke.
func TestBoxForwardRefusesAMessageEntry(t *testing.T) {
	action := &recordingAction{}
	m := streamBoxFixture(t, action)
	m.sess.boxSel = 1 // the user->mate message

	m, _ = send(t, m, key("ctrl+b"))
	m, cmd := send(t, m, key("enter"))
	if cmd != nil {
		t.Fatalf("forwarding a message issued a command: %T", cmd())
	}
	if len(action.reqs) != 0 {
		t.Fatalf("forwarding a message reached ActionFunc: %+v", action.reqs)
	}
	if m.boxMsg.tone != toneError || m.boxMsg.text == "" {
		t.Fatalf("outcome = %+v, want a refusal naming why", m.boxMsg)
	}
	frame := RenderSessionFrame(m.sess.snapshot, "", m.sessionRailState(), 120, 36, unicodeGlyphs, plainPalette())
	if !containsLine(frame, "Send refused") {
		t.Errorf("the rail does not show the refusal:\n%s", frame)
	}
}

// TestStreamModeReplyInputTakesUnprefixedKeys: once the input is open it is
// a Console-drawn field with a visible caret, so every keystroke is its own -
// otherwise a reply could not contain the letters q, r or p.
func TestStreamModeReplyInputTakesUnprefixedKeys(t *testing.T) {
	action := &recordingAction{out: "replied"}
	m := streamBoxFixture(t, action)

	m, _ = send(t, m, key("ctrl+b"))
	m, _ = send(t, m, key("r"))
	if !m.boxReply || m.boxReplyCrew != "k3" {
		t.Fatalf("Ctrl+b r did not open the reply input for k3: reply=%v crew=%q", m.boxReply, m.boxReplyCrew)
	}
	for _, r := range "prq A" {
		m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.boxReplyText != "prq A" {
		t.Fatalf("reply text = %q, want every typed character", m.boxReplyText)
	}
	m, _ = send(t, m, key("backspace"))
	if m.boxReplyText != "prq " {
		t.Fatalf("reply text after backspace = %q", m.boxReplyText)
	}

	m, cmd := send(t, m, key("enter"))
	if m.boxReply {
		t.Fatal("submitting left the reply input open")
	}
	if cmd == nil {
		t.Fatal("submitting the reply issued no command")
	}
	if _, ok := cmd().(actionDoneMsg); !ok {
		t.Fatalf("submitting produced %T, want actionDoneMsg", cmd())
	}
	if len(action.reqs) != 1 || action.reqs[0].Action != ActionReply {
		t.Fatalf("action requests = %+v, want one reply", action.reqs)
	}
	if action.reqs[0].Crew != "k3" || action.reqs[0].Input != "prq" {
		t.Errorf("reply request = %+v, want crew k3 and the trimmed line", action.reqs[0])
	}
}

// TestBoxPeekOpensAndClosesTheOverlay covers `p` end to end through the
// action machinery: the pane text lands in the overlay rather than on the
// outcome line, the overlay is modal, and Esc closes it.
func TestBoxPeekOpensAndClosesTheOverlay(t *testing.T) {
	action := &recordingAction{out: "line one\nline two\nline three"}
	m := streamBoxFixture(t, action)

	m, _ = send(t, m, key("ctrl+b"))
	m, cmd := send(t, m, key("p"))
	if cmd == nil {
		t.Fatal("Ctrl+b p issued no command")
	}
	m, _ = send(t, m, cmd())
	if !m.peek.open || m.peek.crew != "k3" {
		t.Fatalf("peek overlay = %+v, want it open on k3", m.peek)
	}
	if m.boxMsg.tone != toneNone {
		t.Errorf("a peek left %+v on the outcome line; its whole result is the overlay", m.boxMsg)
	}

	frame := m.View()
	assertFrameShape(t, frame, m.w, m.h)
	for _, want := range []string{"PEEK", "crew k3", "line two"} {
		if !containsLine(frame, want) {
			t.Errorf("the peek overlay does not show %q:\n%s", want, frame)
		}
	}

	// Modal: j scrolls it rather than moving the rail behind it.
	before := m.sessionRailState().sel
	m, _ = send(t, m, key("j"))
	if m.peek.top != 1 {
		t.Errorf("j did not scroll the overlay: top=%d", m.peek.top)
	}
	if got := m.sessionRailState().sel; got != before {
		t.Errorf("a key aimed at the overlay moved the rail selection to %d", got)
	}
	m, _ = send(t, m, key("esc"))
	if m.peek.open {
		t.Fatal("Esc did not close the peek overlay")
	}
	if m.sess.phase != sessionActive {
		t.Fatalf("Esc out of the overlay also left session mode: phase=%v", m.sess.phase)
	}
}

// TestProjectFrameBoxKeysAreBare is the other half of the rule: on the
// project frame the Console owns the keyboard, so the three keys need no
// prefix - but only once Tab has focused the panel, because Enter and `r`
// already mean something on the list.
func TestProjectFrameBoxKeysAreBare(t *testing.T) {
	action := &recordingAction{out: "delivered"}
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m.action = action.run
	m, _ = send(t, m, key("enter")) // into the project frame

	// On the list, `r` is still Refresh and Enter still opens the agent view.
	m, _ = send(t, m, key("r"))
	if len(action.reqs) != 0 {
		t.Fatalf("`r` on the list ran %+v; it is Refresh there", action.reqs)
	}

	m, _ = send(t, m, key("tab")) // list -> inspector
	m, _ = send(t, m, key("tab")) // inspector -> box
	if m.focus != paneBox {
		t.Fatalf("focus = %v, want paneBox", m.focus)
	}
	// The panel's selection defaults to the newest entry, which in the
	// sample box is a message; k steps back onto the needs-decision line.
	m, _ = send(t, m, key("k"))
	if got := m.projectBoxSelection(); got != 2 {
		t.Fatalf("selection after k = %d, want the needs-decision entry (2)", got)
	}
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("bare Enter on the focused panel issued no command")
	}
	if _, ok := cmd().(actionDoneMsg); !ok {
		t.Fatalf("bare Enter produced %T, want actionDoneMsg", cmd())
	}
	if len(action.reqs) != 1 || action.reqs[0].Action != ActionForward {
		t.Fatalf("action requests = %+v, want one forward", action.reqs)
	}
	if action.reqs[0].Target != "proj_01J9M1F8K2Q7C4H6N0R3V5T8YZ" || action.reqs[0].Crew != "k3" {
		t.Errorf("forward request = %+v, want this project and crew k3", action.reqs[0])
	}
}
