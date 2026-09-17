package console

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The focus model's own proofs (session_focus.go, session_mouse.go). What
// they pin is one sentence: at every moment the frame says which zone owns
// the next keystroke, and nothing reaches the agent's PTY from the other
// one. That is the whole reason the Ctrl+b prefix was removed - a prefix
// says nothing on screen, and a mis-typed one put box keys in the Mate's
// composer.

// mouseBoxFixture is an open Mate stream at 160x48 carrying the pinned
// fixture box, so the rail has entries to click on.
func mouseBoxFixture(t *testing.T) (Model, *controllerTestChannel) {
	t.Helper()
	m, channel := enterStreamMode(t, &controllerTestFactory{})
	m.sess.snapshot.Box = sessionTestBox()
	m.sess.snapshot.Target = m.sess.target
	if m.sess.target.Kind != SessionTargetMate {
		t.Fatalf("setup: fixture target is %v, want a Mate (only a Mate has a rail)", m.sess.target.Kind)
	}
	return m, channel
}

func press(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

// railRowOf is the frame row the rail draws entry i on.
func railRowOf(t *testing.T, m Model, i int) int {
	t.Helper()
	geo := m.sessionGeom()
	for row := 0; row < geo.railBodyH; row++ {
		if got, ok := sessionEntryAt(m.sess.snapshot.Box, m.sessionRailState().sel, geo.railBodyH, row); ok && got == i {
			return geo.railBodyTop + row
		}
	}
	t.Fatalf("entry %d is not drawn in the rail body", i)
	return 0
}

// TestClickInTheTerminalZoneFocusesItAndForwardsToThePTY: the click both
// moves focus and reaches the agent, because that is what a click on a
// terminal does everywhere else a person has ever used one.
func TestClickInTheTerminalZoneFocusesItAndForwardsToThePTY(t *testing.T) {
	m, channel := mouseBoxFixture(t)
	m.sess.zone = zoneBox
	geo := m.sessionGeom()

	m, cmd := send(t, m, press(geo.paneX+2, geo.paneTop+3))
	if cmd != nil {
		t.Fatalf("a click in the terminal zone produced a Cmd: %T", cmd())
	}
	if m.sess.zone != zoneTerminal {
		t.Fatalf("zone after a click in the terminal = %v, want the terminal", m.sess.zone)
	}
	got := waitForWrites(t, channel, 1)
	want := []byte("\x1b[<0;3;4M")
	if len(got) != 1 || !bytes.Equal(got[0], want) {
		t.Fatalf("forwarded %q, want %q (pane-relative coordinates)", got, want)
	}
}

// TestClickOnARailEntryFocusesTheBoxAndSelectsThatEntry.
func TestClickOnARailEntryFocusesTheBoxAndSelectsThatEntry(t *testing.T) {
	m, channel := mouseBoxFixture(t)
	row := railRowOf(t, m, 0)

	m, cmd := send(t, m, press(2, row))
	if cmd != nil {
		t.Fatalf("a click on a rail entry produced a Cmd: %T", cmd())
	}
	if m.sess.zone != zoneBox {
		t.Fatalf("zone after a click on the rail = %v, want the box", m.sess.zone)
	}
	if got := m.sessionRailState().sel; got != 0 {
		t.Fatalf("selection after the click = %d, want the entry that was clicked (0)", got)
	}
	if len(channel.writtenBytes()) != 0 {
		t.Fatalf("a click on the rail reached the PTY: %q", channel.writtenBytes())
	}
}

// TestDoubleClickOnARailEntryOpensPeek: the second press on the same entry
// is the gesture, and it runs the same action `p` does.
func TestDoubleClickOnARailEntryOpensPeek(t *testing.T) {
	m, _ := mouseBoxFixture(t)
	action := &recordingAction{out: "pane text"}
	m.action = action.run
	row := railRowOf(t, m, 0) // the k3 working entry, which names a crew

	m, _ = send(t, m, press(2, row))
	m, cmd := send(t, m, press(2, row))
	if cmd == nil {
		t.Fatal("a double click on a rail entry ran nothing; want the peek action")
	}
	m, _ = send(t, m, cmd())
	if !m.peek.open || m.peek.crew != "k3" {
		t.Fatalf("peek = %+v, want it open on k3", m.peek)
	}
	if len(action.reqs) != 1 || action.reqs[0].Action != ActionPeek {
		t.Fatalf("action requests = %+v, want one peek", action.reqs)
	}
}

// TestBareKeysReachThePTYUnderTerminalFocusAndNotUnderBoxFocus is the whole
// point of the change: q, j and Enter are the agent's under terminal focus,
// and none of them reaches it once the box has focus.
func TestBareKeysReachThePTYUnderTerminalFocusAndNotUnderBoxFocus(t *testing.T) {
	m, channel := mouseBoxFixture(t)

	for _, k := range []string{"q", "j", "enter"} {
		m, _ = send(t, m, key(k))
	}
	got := waitForWrites(t, channel, 3)
	want := [][]byte{[]byte("q"), []byte("j"), {0x0d}}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("write %d = %q, want %q", i, got[i], want[i])
		}
	}
	if m.quitting {
		t.Fatal("a bare q under terminal focus quit the Console; it belongs to the agent")
	}

	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	before := len(channel.writtenBytes())
	for _, k := range []string{"q", "j"} {
		m, _ = send(t, m, key(k))
	}
	if got := channel.writtenBytes(); len(got) != before {
		t.Fatalf("keys under box focus reached the PTY: %q", got[before:])
	}
	if m.quitting {
		t.Fatal("a bare q under box focus quit the Console; only Ctrl+C does")
	}
}

// TestF2TogglesTheFocusZone.
func TestF2TogglesTheFocusZone(t *testing.T) {
	m, _ := mouseBoxFixture(t)
	if m.sess.zone != zoneTerminal {
		t.Fatalf("a freshly opened session starts on %v, want the terminal", m.sess.zone)
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	if m.sess.zone != zoneBox {
		t.Fatalf("zone after F2 = %v, want the box", m.sess.zone)
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	if m.sess.zone != zoneTerminal {
		t.Fatalf("zone after a second F2 = %v, want the terminal", m.sess.zone)
	}
}

// TestSplitterDragResizesTheRailAndThePTY: the divider is grabbed, moved
// and released, and the agent is told its new geometry while the button is
// still down rather than only when it comes up.
func TestSplitterDragResizesTheRailAndThePTY(t *testing.T) {
	m, channel := mouseBoxFixture(t)
	geo := m.sessionGeom()
	before := geo.railW
	beforeCols := m.sess.terminal.Width()

	m, _ = send(t, m, press(geo.splitX, geo.bodyTop+2))
	if !m.draggingSplit {
		t.Fatal("a press on the splitter did not start a drag")
	}
	want := before + 8
	m, cmd := send(t, m, tea.MouseMsg{
		X: want, Y: geo.bodyTop + 2, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if cmd == nil {
		t.Fatal("dragging the splitter did not emit a PTY resize")
	}
	if got := m.sessionGeom().railW; got != want {
		t.Fatalf("rail width after the drag = %d, want %d", got, want)
	}
	if got := m.sess.terminal.Width(); got != beforeCols-8 {
		t.Fatalf("terminal buffer width = %d, want %d", got, beforeCols-8)
	}
	m, _ = send(t, m, cmd())
	if sizes := channel.resizedTo(); len(sizes) != 1 || sizes[0].Cols != beforeCols-8 {
		t.Fatalf("PTY resizes = %+v, want one at %d columns", sizes, beforeCols-8)
	}

	m, _ = send(t, m, tea.MouseMsg{X: want, Y: geo.bodyTop + 2, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if m.draggingSplit {
		t.Fatal("releasing the button did not end the drag")
	}
	// The width persists for the Console's run: leaving the view and coming
	// back must not snap the divider home.
	if m.railWidth != want {
		t.Fatalf("railWidth = %d, want the dragged %d to persist", m.railWidth, want)
	}
}

// TestSplitterDragIsClampedToTheRailBounds.
func TestSplitterDragIsClampedToTheRailBounds(t *testing.T) {
	m, _ := mouseBoxFixture(t)
	geo := m.sessionGeom()
	m, _ = send(t, m, press(geo.splitX, geo.bodyTop+2))
	m, _ = send(t, m, tea.MouseMsg{X: 4, Y: geo.bodyTop + 2, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if got := m.sessionGeom().railW; got != railMinWidth {
		t.Fatalf("rail width dragged to column 4 = %d, want the %d floor", got, railMinWidth)
	}
	m, _ = send(t, m, tea.MouseMsg{X: 150, Y: geo.bodyTop + 2, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if got := m.sessionGeom().railW; got != railMaxWidth {
		t.Fatalf("rail width dragged to column 150 = %d, want the %d ceiling", got, railMaxWidth)
	}
}

// TestEachHeaderLabelRunsItsOwnAction hit-tests every clickable label on the
// rail header at the coordinates the renderer drew it at. A label that is
// drawn but not clickable is decoration, and a label whose hit box has
// drifted off its text runs the wrong thing.
func TestEachHeaderLabelRunsItsOwnAction(t *testing.T) {
	for _, tc := range []struct {
		id     labelID
		assert func(t *testing.T, m Model, cmd tea.Cmd, action *recordingAction)
	}{
		{labelProject, func(t *testing.T, m Model, _ tea.Cmd, _ *recordingAction) {
			if m.sess.phase == sessionActive {
				t.Fatal("[← project] did not leave the session view")
			}
		}},
		{labelMode, func(t *testing.T, m Model, cmd tea.Cmd, action *recordingAction) {
			if cmd == nil {
				t.Fatal("[supervised] ran nothing")
			}
			send(t, m, cmd())
			if len(action.reqs) != 1 || action.reqs[0].Action != ActionMode {
				t.Fatalf("requests = %+v, want one mode flip", action.reqs)
			}
		}},
		{labelRestart, func(t *testing.T, m Model, cmd tea.Cmd, action *recordingAction) {
			if cmd != nil {
				t.Fatalf("[restart mate] ran %T before the confirmation was answered", cmd())
			}
			if !m.boxConfirm {
				t.Fatal("[restart mate] did not open its confirmation")
			}
			if len(action.reqs) != 0 {
				t.Fatalf("[restart mate] reached ActionFunc before confirmation: %+v", action.reqs)
			}
		}},
		{labelClear, func(t *testing.T, m Model, cmd tea.Cmd, action *recordingAction) {
			if cmd == nil {
				t.Fatal("[clear composer] ran nothing")
			}
			send(t, m, cmd())
			if len(action.reqs) != 1 || action.reqs[0].Action != ActionClearComposer {
				t.Fatalf("requests = %+v, want one clear_composer", action.reqs)
			}
		}},
	} {
		t.Run(labelName(tc.id), func(t *testing.T) {
			m, _ := mouseBoxFixture(t)
			action := &recordingAction{out: "done"}
			m.action = action.run
			at, ok := labelPlacement(m.sessionGeom(), tc.id)
			if !ok {
				t.Fatalf("%s is not drawn on the header", labelName(tc.id))
			}
			after, cmd := send(t, m, press(at.x, at.y))
			tc.assert(t, after, cmd, action)
		})
	}
}

// TestTheConfirmationButtonsAnswerTheRestartPrompt: [yes] runs it, [no]
// leaves the Mate alone.
func TestTheConfirmationButtonsAnswerTheRestartPrompt(t *testing.T) {
	for _, tc := range []struct {
		id   labelID
		runs bool
	}{{labelYes, true}, {labelNo, false}} {
		t.Run(labelName(tc.id), func(t *testing.T) {
			m, _ := mouseBoxFixture(t)
			action := &recordingAction{out: "restarted"}
			m.action = action.run
			m = m.beginRestartMate(m.sess.target.ProjectID)
			at, ok := labelPlacement(m.sessionGeom(), tc.id)
			if !ok {
				t.Fatalf("%s is not drawn on the confirmation line", labelName(tc.id))
			}
			m, cmd := send(t, m, press(at.x, at.y))
			if m.boxConfirm {
				t.Fatal("answering the confirmation left it open")
			}
			if tc.runs != (cmd != nil) {
				t.Fatalf("%s produced a command: %v, want %v", labelName(tc.id), cmd != nil, tc.runs)
			}
			if !tc.runs {
				return
			}
			m, _ = send(t, m, cmd())
			if len(action.reqs) != 1 || action.reqs[0].Action != ActionRestartMate {
				t.Fatalf("requests = %+v, want one restart", action.reqs)
			}
		})
	}
}

// TestTheReplyInputButtonsSendAndCancel.
func TestTheReplyInputButtonsSendAndCancel(t *testing.T) {
	for _, tc := range []struct {
		id    labelID
		sends bool
	}{{labelSend, true}, {labelCancel, false}} {
		t.Run(labelName(tc.id), func(t *testing.T) {
			m, _ := mouseBoxFixture(t)
			action := &recordingAction{out: "replied"}
			m.action = action.run
			m.sess.zone = zoneBox
			m, _ = send(t, m, key("r"))
			m, _ = send(t, m, key("A"))
			if !m.boxReply {
				t.Fatal("setup: the reply input is not open")
			}
			at, ok := labelPlacement(m.sessionGeom(), tc.id)
			if !ok {
				t.Fatalf("%s is not drawn on the reply line", labelName(tc.id))
			}
			m, cmd := send(t, m, press(at.x, at.y))
			if m.boxReply {
				t.Fatalf("%s left the reply input open", labelName(tc.id))
			}
			if tc.sends != (cmd != nil) {
				t.Fatalf("%s produced a command: %v, want %v", labelName(tc.id), cmd != nil, tc.sends)
			}
			if !tc.sends {
				return
			}
			m, _ = send(t, m, cmd())
			if len(action.reqs) != 1 || action.reqs[0].Action != ActionReply || action.reqs[0].Input != "A" {
				t.Fatalf("requests = %+v, want one reply carrying \"A\"", action.reqs)
			}
		})
	}
}

// TestEachActionStripButtonRunsItsOwnAction hit-tests the three inline
// buttons on a hovered attention entry.
func TestEachActionStripButtonRunsItsOwnAction(t *testing.T) {
	for _, tc := range []struct {
		id   labelID
		want Action
	}{{labelForward, ActionForward}, {labelPeek, ActionPeek}} {
		t.Run(labelName(tc.id), func(t *testing.T) {
			m, _ := mouseBoxFixture(t)
			action := &recordingAction{out: "done"}
			m.action = action.run
			const attention = 2 // the needs-decision entry
			m.sess.zone, m.sess.boxSel = zoneBox, attention
			row := railRowOf(t, m, attention)

			// The motion that puts the pointer on the entry is what makes its
			// strip appear, exactly as it does for a reader.
			m, _ = send(t, m, tea.MouseMsg{X: 2, Y: row, Action: tea.MouseActionMotion})
			if m.boxHover != attention {
				t.Fatalf("hover = %d, want the entry under the pointer (%d)", m.boxHover, attention)
			}
			strip, ok := sessionEntryStrip(m.sess.snapshot.Box, attention, attention, attention, 0, row, m.sessionGeom().railW, m.g)
			if !ok {
				t.Fatal("the hovered attention entry drew no action strip")
			}
			at, found := placementOf(strip, tc.id)
			if !found {
				t.Fatalf("%s is not in the strip", labelName(tc.id))
			}
			m, cmd := send(t, m, press(at.x, at.y))
			if cmd == nil {
				t.Fatalf("%s ran nothing", labelName(tc.id))
			}
			m, _ = send(t, m, cmd())
			if len(action.reqs) != 1 || action.reqs[0].Action != tc.want {
				t.Fatalf("requests = %+v, want one %s", action.reqs, tc.want)
			}
		})
	}
}

// TestTheReplyStripButtonOpensTheInput is the third button, which opens a
// field rather than running an action.
func TestTheReplyStripButtonOpensTheInput(t *testing.T) {
	m, _ := mouseBoxFixture(t)
	const attention = 2
	m.sess.zone, m.sess.boxSel, m.boxHover = zoneBox, attention, attention
	row := railRowOf(t, m, attention)
	strip, ok := sessionEntryStrip(m.sess.snapshot.Box, attention, attention, attention, 0, row, m.sessionGeom().railW, m.g)
	if !ok {
		t.Fatal("the selected attention entry drew no action strip")
	}
	at, found := placementOf(strip, labelReply)
	if !found {
		t.Fatal("[reply] is not in the strip")
	}
	m, _ = send(t, m, press(at.x, at.y))
	if !m.boxReply || m.boxReplyCrew != "k3" {
		t.Fatalf("[reply] did not open the input for k3: reply=%v crew=%q", m.boxReply, m.boxReplyCrew)
	}
}

// TestAnEntryWithoutAttentionNeverGrowsAStrip: the buttons say "this needs
// you", so putting them on a line that does not would be a lie the reader
// would learn to ignore.
func TestAnEntryWithoutAttentionNeverGrowsAStrip(t *testing.T) {
	v := sessionTestBox()
	for i, e := range v.Value.Entries {
		if got := boxEntryHasStrip(e, true, true); got != e.Attention {
			t.Errorf("entry %d (%s %s) strip = %v, want %v", i, e.Kind, e.Verb, got, e.Attention)
		}
		if boxEntryHasStrip(e, false, false) {
			t.Errorf("entry %d draws a strip while neither selected nor hovered", i)
		}
	}
}

// TestWheelOverTheRailScrollsTheBoxWithoutTakingFocus: scrolling to read is
// not a decision to type.
func TestWheelOverTheRailScrollsTheBoxWithoutTakingFocus(t *testing.T) {
	m, channel := mouseBoxFixture(t)
	geo := m.sessionGeom()
	before := m.sessionRailState().sel

	m, _ = send(t, m, tea.MouseMsg{X: 2, Y: geo.railBodyTop + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if got := m.sessionRailState().sel; got != before-1 {
		t.Fatalf("selection after a wheel up = %d, want %d", got, before-1)
	}
	if m.sess.zone != zoneTerminal {
		t.Fatalf("the wheel moved focus to %v; only a click does that", m.sess.zone)
	}
	if len(channel.writtenBytes()) != 0 {
		t.Fatalf("a wheel over the rail reached the PTY: %q", channel.writtenBytes())
	}
}

// TestBareMotionOverThePaneIsNeverForwarded: all-motion reporting is on for
// the Console's whole run so the rail can track hover, and forwarding every
// idle pointer cell to the agent would be a PTY write per cell.
func TestBareMotionOverThePaneIsNeverForwarded(t *testing.T) {
	m, channel := mouseBoxFixture(t)
	geo := m.sessionGeom()
	for i := 0; i < 5; i++ {
		m, _ = send(t, m, tea.MouseMsg{X: geo.paneX + i, Y: geo.paneTop + 1, Action: tea.MouseActionMotion})
	}
	if got := channel.writtenBytes(); len(got) != 0 {
		t.Fatalf("bare pointer motion reached the PTY: %q", got)
	}
	// A drag - motion with a button held - is a real gesture and does go.
	m, _ = send(t, m, tea.MouseMsg{
		X: geo.paneX + 1, Y: geo.paneTop + 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if got := waitForWrites(t, channel, 1); len(got) != 1 {
		t.Fatalf("a drag over the pane wrote %d times, want 1", len(got))
	}
}

// TestClickOnTheProjectFrameBoxPanelFocusesAndSelectsIt: the project frame's
// panel answers the mouse the same way the rail does (mvp.md task 15), so a
// reader does not learn one set of gestures per surface.
func TestClickOnTheProjectFrameBoxPanelFocusesAndSelectsIt(t *testing.T) {
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter")) // into the project frame
	top, h, ok := m.boxPanelBodyRegion()
	if !ok {
		t.Fatal("setup: the project frame draws no box panel at 120x36")
	}
	v, has := m.projectBox()
	if !has {
		t.Fatal("setup: the fixture project has no box entries")
	}
	// The panel pads at the top when it has fewer entries than rows, so the
	// row that certainly carries one is the oldest drawn, not the first.
	row, want := -1, 0
	for i := 0; i < h; i++ {
		if got, drawn := sessionEntryAt(v, m.projectBoxSelection(), h, i); drawn {
			row, want = i, got
			break
		}
	}
	if row < 0 {
		t.Fatal("setup: the panel draws no entries at all")
	}

	m, cmd := send(t, m, press(4, top+row))
	if cmd != nil {
		t.Fatalf("a click on the panel produced a Cmd: %T", cmd())
	}
	if m.focus != paneBox {
		t.Fatalf("focus after the click = %v, want paneBox", m.focus)
	}
	if got := m.projectBoxSelection(); got != want {
		t.Fatalf("selection after the click = %d, want the clicked entry %d", got, want)
	}
}

// TestF2MovesFocusOntoTheProjectFrameBoxPanelAndBack.
func TestF2MovesFocusOntoTheProjectFrameBoxPanelAndBack(t *testing.T) {
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	if m.focus != paneBox {
		t.Fatalf("focus after F2 = %v, want paneBox", m.focus)
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	if m.focus != paneList {
		t.Fatalf("focus after a second F2 = %v, want paneList", m.focus)
	}
}

// ---------- helpers ----------

func labelPlacement(geo sessionGeom, id labelID) (placedLabel, bool) {
	return placementOf(geo.labels, id)
}

func placementOf(labels []placedLabel, id labelID) (placedLabel, bool) {
	for _, l := range labels {
		if l.id == id {
			return l, true
		}
	}
	return placedLabel{}, false
}

func labelName(id labelID) string {
	switch id {
	case labelProject:
		return "project"
	case labelMode:
		return "mode"
	case labelRestart:
		return "restart"
	case labelClear:
		return "clear"
	case labelForward:
		return "forward"
	case labelReply:
		return "reply"
	case labelPeek:
		return "peek"
	case labelSend:
		return "send"
	case labelCancel:
		return "cancel"
	case labelYes:
		return "yes"
	case labelNo:
		return "no"
	default:
		return "none"
	}
}
