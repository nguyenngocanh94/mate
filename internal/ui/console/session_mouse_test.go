package console

import (
	"bytes"
	"context"
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
		b, _ := m.sessionBoxList()
		if got, ok := sessionEntryAt(b, m.sessionRailState().sel, geo.railBodyH, row, geo.railW); ok && got == i {
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

// TestClickOnARailEntrySelectsItWithoutReachingThePTY. The entry it clicks
// is the Mate's own `wedged` incident, the one row that names the view
// already on screen: it selects and opens nothing, so what this measures is
// the press itself rather than the open (which is
// TestClickOnARailEntryOpensThatCrewsSession's job).
func TestClickOnARailEntrySelectsItWithoutReachingThePTY(t *testing.T) {
	m, channel := mouseBoxFixture(t)
	m.sess.snapshot.Box = boxMateWedged()
	row := railRowOf(t, m, 0)

	m, cmd := send(t, m, press(2, row))
	if cmd != nil {
		t.Fatalf("a click on a rail entry produced a Cmd: %T", cmd())
	}
	if m.sess.zone != zoneTerminal {
		t.Fatalf("zone after clicking the Mate's own row = %v, want the terminal it opened", m.sess.zone)
	}
	if got := m.sessionRailState().sel; got != 0 {
		t.Fatalf("selection after the click = %d, want the entry that was clicked (0)", got)
	}
	if len(channel.writtenBytes()) != 0 {
		t.Fatalf("a click on the rail reached the PTY: %q", channel.writtenBytes())
	}
}

// TestClickOnARailEntryOpensThatCrewsSession is the 2026-09-19 gesture: one
// press on the row body, anywhere but the button, and the reader is looking
// at that crew's own pane. The Mate's stream is closed first - two PTY
// streams are never open at once - so the crew's own open begins from the
// close, not from the click.
func TestClickOnARailEntryOpensThatCrewsSession(t *testing.T) {
	m, channel := mouseBoxFixture(t)
	m.sess.snapshot.Box = boxAsking(fixtureCrewID)
	row := railRowOf(t, m, 0)

	m, cmd := send(t, m, press(2, row))
	if cmd == nil {
		t.Fatal("a click on a rail row opened nothing")
	}
	if m.sess.phase != sessionClosing {
		t.Fatalf("phase after the click = %v, want the Mate's stream closing first", m.sess.phase)
	}
	m, _ = send(t, m, cmd())
	if !channel.isClosed() {
		t.Fatal("the Mate's channel is still open after its session was left")
	}
	if m.sess.target.Kind != SessionTargetCrew || m.sess.target.ID != fixtureCrewID {
		t.Fatalf("session target after the click = %+v, want crew %s", m.sess.target, fixtureCrewID)
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

// TestTheHeaderCarriesTheTwoFiltersAndNothingElse is the 2026-09-19 header:
// the box is a filter and a list of rows, so the only things above the rows
// are the two filters. A label that came back - the view, the mode, a
// recovery action - would be something to read before the first row again.
func TestTheHeaderCarriesTheTwoFiltersAndNothingElse(t *testing.T) {
	specs := sessionHeaderLabels()
	if len(specs) != 2 {
		t.Fatalf("header labels = %+v, want exactly the two filters", specs)
	}
	if specs[0].id != labelWaiting || specs[0].text != "[waiting]" {
		t.Fatalf("first label = %+v, want [waiting]", specs[0])
	}
	if specs[1].id != labelAll || specs[1].text != "[all]" {
		t.Fatalf("second label = %+v, want [all]", specs[1])
	}
	// The header is one line at every rail width the splitter allows, which
	// is the other half of the complaint it answers.
	for w := railMinWidth; w <= railMaxWidth; w++ {
		if rows := packLabels(specs, w); len(rows) != 1 {
			t.Fatalf("the header wraps onto %d lines at %d cells", len(rows), w)
		}
	}
}

// TestTheFilterLabelsSelectTheirOwnList hit-tests both filters at the
// coordinates the renderer drew them at, and pins that each one names the
// list it wants rather than toggling: two presses on [all] leave [all] on.
func TestTheFilterLabelsSelectTheirOwnList(t *testing.T) {
	for _, tc := range []struct {
		id      labelID
		wantAll bool
	}{{labelAll, true}, {labelWaiting, false}} {
		t.Run(labelName(tc.id), func(t *testing.T) {
			m, _ := mouseBoxFixture(t)
			at, ok := labelPlacement(m.sessionGeom(), tc.id)
			if !ok {
				t.Fatalf("%s is not drawn on the header", labelName(tc.id))
			}
			m, cmd := send(t, m, press(at.x, at.y))
			if cmd != nil {
				t.Fatalf("a filter ran %T; it only changes what the rail shows", cmd())
			}
			if m.boxAll != tc.wantAll {
				t.Fatalf("[all] = %v after pressing %s, want %v", m.boxAll, labelName(tc.id), tc.wantAll)
			}
			m, _ = send(t, m, press(at.x, at.y))
			if m.boxAll != tc.wantAll {
				t.Fatalf("a second press on %s flipped the filter to %v", labelName(tc.id), m.boxAll)
			}
		})
	}
}

// TestTheBoxZoneOpensTheActionsMenu: `o` is where the Mate's restart and
// clear-composer went when they left the header, and the menu it opens is
// the Mate's own - not whatever row the tree's cursor is sitting on.
func TestTheBoxZoneOpensTheActionsMenu(t *testing.T) {
	m, channel := mouseBoxFixture(t)
	action := &recordingAction{out: "done"}
	m.action = action.run
	m.sess.zone = zoneBox

	m, cmd := send(t, m, key("o"))
	if cmd != nil {
		t.Fatalf("o ran %T; it only opens the menu", cmd())
	}
	if !m.actions {
		t.Fatal("o under box focus did not open the Actions menu")
	}
	if len(channel.writtenBytes()) != 0 {
		t.Fatalf("o under box focus reached the PTY: %q", channel.writtenBytes())
	}
	frame := renderFrame(t, m)
	for _, want := range []string{"ACTIONS", string(ActionRestartMate), string(ActionClearComposer)} {
		if !containsLine(frame, want) {
			t.Fatalf("the menu does not offer %q:\n%s", want, frame)
		}
	}

	// The restart is dangerous, so the menu's own confirmation stands in
	// front of it, and nothing reaches ActionFunc until it is answered.
	m = selectMenuAction(t, m, ActionRestartMate)
	m, cmd = send(t, m, key("enter"))
	if cmd != nil || m.confirm == nil {
		t.Fatalf("restart ran without its confirmation (cmd=%v confirm=%v)", cmd != nil, m.confirm != nil)
	}
	if len(action.reqs) != 0 {
		t.Fatalf("restart reached ActionFunc before the confirmation: %+v", action.reqs)
	}
	m, cmd = send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("the answered confirmation ran nothing")
	}
	m, _ = send(t, m, cmd())
	if len(action.reqs) != 1 || action.reqs[0].Action != ActionRestartMate {
		t.Fatalf("requests = %+v, want one restart", action.reqs)
	}
	if m.sess.phase != sessionActive {
		t.Fatalf("the menu left the session view (phase %v); only Esc does", m.sess.phase)
	}
}

// TestTheBoxZoneActionsMenuClearsTheComposerWithoutAConfirmation: the other
// entry writes one Ctrl+U into a composer and asks nothing first, exactly as
// the `u` key it replaces did.
func TestTheBoxZoneActionsMenuClearsTheComposerWithoutAConfirmation(t *testing.T) {
	m, _ := mouseBoxFixture(t)
	action := &recordingAction{out: "cleared"}
	m.action = action.run
	m.sess.zone = zoneBox

	m, _ = send(t, m, key("o"))
	m = selectMenuAction(t, m, ActionClearComposer)
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("clear_composer ran nothing")
	}
	if m.confirm != nil {
		t.Fatal("clear_composer asked for a confirmation; it changes nothing that cannot be retyped")
	}
	m, _ = send(t, m, cmd())
	if len(action.reqs) != 1 || action.reqs[0].Action != ActionClearComposer {
		t.Fatalf("requests = %+v, want one clear_composer", action.reqs)
	}
}

// selectMenuAction moves the menu cursor onto one action by name, the way a
// reader would with j.
func selectMenuAction(t *testing.T, m Model, want Action) Model {
	t.Helper()
	for i, c := range m.actionChoices {
		if c.action == want {
			m.actionIndex = i
			return m
		}
	}
	t.Fatalf("the menu does not offer %s: %+v", want, m.actionChoices)
	return m
}

// TestTheAssignButtonHandsTheEntryToTheMate hit-tests the row's one inline
// button at the coordinates the renderer drew it at, and proves the press
// assigns rather than opening the crew's pane the rest of the row opens.
func TestTheAssignButtonHandsTheEntryToTheMate(t *testing.T) {
	m, channel := mouseBoxFixture(t)
	action := &recordingAction{out: "done"}
	m.action = action.run
	const attention = 0 // the needs-decision item, first in the inbox
	m.sess.zone, m.sess.boxSel = zoneBox, attention
	row := railRowOf(t, m, attention)

	// The motion that puts the pointer on the entry is what makes its strip
	// appear, exactly as it does for a reader.
	m, _ = send(t, m, tea.MouseMsg{X: 2, Y: row, Action: tea.MouseActionMotion})
	if m.boxHover != attention {
		t.Fatalf("hover = %d, want the entry under the pointer (%d)", m.boxHover, attention)
	}
	inbox, _ := m.sessionBoxList()
	strip, ok := sessionEntryStrip(inbox, attention, attention, attention, 0, row, m.sessionGeom().railW, m.g)
	if !ok {
		t.Fatal("the hovered attention entry drew no action strip")
	}
	at, found := placementOf(strip, labelAssign)
	if !found {
		t.Fatal("[assign] is not in the strip")
	}
	m, cmd := send(t, m, press(at.x, at.y))
	if cmd == nil {
		t.Fatal("[assign] ran nothing")
	}
	m, _ = send(t, m, cmd())
	if len(action.reqs) != 1 || action.reqs[0].Action != ActionResolve {
		t.Fatalf("requests = %+v, want one resolve", action.reqs)
	}
	if channel.isClosed() || m.sess.phase != sessionActive {
		t.Fatalf("[assign] left the Mate's session (phase %v, closed %v); only the row body opens a crew",
			m.sess.phase, channel.isClosed())
	}
}

// TestTheStripIsTheOnlyButtonOnTheRow: one button, so a reader never has to
// aim at the right word out of three (2026-09-19).
func TestTheStripIsTheOnlyButtonOnTheRow(t *testing.T) {
	buttons := boxStripButtons(unicodeGlyphs)
	if len(buttons) != 1 || buttons[0].id != labelAssign || buttons[0].text != "[assign]" {
		t.Fatalf("strip buttons = %+v, want exactly [assign]", buttons)
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
// reader does not learn one set of gestures per surface. The click both
// selects the row and opens the crew it names, which is one gesture with one
// outcome - the panel's own proof that the two surfaces agree.
func TestClickOnTheProjectFrameBoxPanelFocusesAndSelectsIt(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Box = boxAsking(fixtureCrewID)
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	m = m.WithSession(func(context.Context, SessionTarget) (SessionSnapshot, error) {
		return SessionSnapshot{}, nil
	}, nil, nil)
	m, _ = send(t, m, key("enter")) // into the project frame
	top, h, ok := m.boxPanelBodyRegion()
	if !ok {
		t.Fatal("setup: the project frame draws no box panel at 120x36")
	}
	b, has := m.projectBox()
	if !has {
		t.Fatal("setup: the fixture project has no box entries")
	}
	// The panel pads at the top when it has fewer entries than rows, so the
	// row that certainly carries one is the oldest drawn, not the first.
	row, want := -1, 0
	for i := 0; i < h; i++ {
		if got, drawn := sessionEntryAt(b, m.projectBoxSelection(), h, i, m.w); drawn {
			row, want = i, got
			break
		}
	}
	if row < 0 {
		t.Fatal("setup: the panel draws no entries at all")
	}

	m, _ = send(t, m, press(4, top+row))
	if m.focus != paneBox {
		t.Fatalf("focus after the click = %v, want paneBox", m.focus)
	}
	if got := m.projectBoxSelection(); got != want {
		t.Fatalf("selection after the click = %d, want the clicked entry %d", got, want)
	}
	if m.sess.target.Kind != SessionTargetCrew || m.sess.target.ID != fixtureCrewID {
		t.Fatalf("session target after the click = %+v, want crew %s", m.sess.target, fixtureCrewID)
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
	case labelAll:
		return "all"
	case labelAssign:
		return "assign"
	case labelWaiting:
		return "waiting"
	default:
		return "none"
	}
}
