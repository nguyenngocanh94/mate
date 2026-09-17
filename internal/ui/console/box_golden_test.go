package console

import (
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The message box's own fixtures (mvp.md task 15). The session-mate-* and
// project-* goldens already cover the rail and the panel in their default
// state; these pin what only exists because of this task and has no other
// fixture: an inbox item selected with its question wrapped underneath, an
// empty inbox, the `[all]` debugging view, the reply input open, and the
// peek overlay.

// boxRailSnapshot is a Mate session frame carrying the pinned fixture box.
func boxRailSnapshot() SessionSnapshot {
	snap := sessionTestSnapshot(SessionTargetMate, unicodeGlyphs)
	snap.Box = sessionTestBox()
	return snap
}

// TestGoldenBoxRailSelectedNeedsDecision renders the rail with the cursor on
// the needs-decision entry: the entry that is both highlighted (the "!"
// attention mark) and selected (the marker glyph). Both signals have to be
// visible with colour stripped, which is exactly what a plainPalette fixture
// proves.
// It is also the fixture for the inbox itself: the box behind it holds five
// entries - a `working`, a message and a `done` among them - and the rail
// shows the two that are still waiting on somebody, with the selected one's
// question wrapped underneath it so it is readable without `p`.
func TestGoldenBoxRailSelectedNeedsDecision(t *testing.T) {
	frame := RenderSessionFrame(boxRailSnapshot(), "", boxRail{sel: 0, zone: zoneBox, mode: query.ModeSupervised}, 120, 36, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 120, 36)
	assertGolden(t, "box-rail-selected-120x36-unicode", frame)
}

// TestGoldenBoxRailEmptyInbox is the state the reader asked for and the one
// the old rail could never show: nothing is waiting. The log behind it is not
// empty - the same five entries are there - so this fixture is also the proof
// that the filter, not the merge, is what emptied the rail. The placeholder
// is quiet on purpose: "nothing to resolve" is a state, not a fault.
func TestGoldenBoxRailEmptyInbox(t *testing.T) {
	snap := sessionTestSnapshot(SessionTargetMate, unicodeGlyphs)
	snap.Box = sessionTestEmptyBox()
	rail := boxRail{sel: -1, zone: zoneBox, mode: query.ModeSupervised}
	frame := RenderSessionFrame(snap, "", rail, 120, 36, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 120, 36)
	assertGolden(t, "box-rail-empty-120x36-unicode", frame)
}

// TestGoldenBoxRailAllMode is the `[all]` toggle: the whole merged log, the
// way the rail used to look, with the header saying so. The header label
// reads "[all on]" rather than relying on the accent colour, which is what a
// plainPalette fixture proves - a reader on a monochrome terminal can still
// tell the debugging view from a broken filter.
func TestGoldenBoxRailAllMode(t *testing.T) {
	rail := boxRail{all: true, sel: 4, zone: zoneBox, mode: query.ModeSupervised}
	frame := RenderSessionFrame(boxRailSnapshot(), "", rail, 120, 36, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 120, 36)
	assertGolden(t, "box-rail-all-120x36-unicode", frame)
}

// TestGoldenSessionTerminalFocus is the Mate session view as it opens: the
// terminal has focus, so the hint line names the agent's keys and nothing
// else, and the rail draws no selection marker of its own. This is the
// fixture that would fail if the view ever again reserved a keystroke out of
// the agent's own alphabet.
func TestGoldenSessionTerminalFocus(t *testing.T) {
	rail := boxRail{sel: 1, zone: zoneTerminal, mode: query.ModeSupervised}
	frame := RenderStreamSessionFrame(boxRailSnapshot(), sessionGoldenTerminal(t), false, rail, 120, 36, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 120, 36)
	assertGolden(t, "session-focus-terminal-120x36-unicode", frame)
}

// TestGoldenSessionBoxFocusWithHoverStrip is the other focus state, with the
// pointer resting on the needs-decision entry: the hint line names the box's
// keys, the entry under the pointer grows its action strip, and the header
// carries the four clickable labels. Rendered with plainPalette, so nothing
// here is carried by the accent colour alone - the words "BOX" and the
// buttons themselves are what a monochrome reader sees.
func TestGoldenSessionBoxFocusWithHoverStrip(t *testing.T) {
	rail := boxRail{sel: 1, hover: 1, zone: zoneBox, mode: query.ModeSupervised}
	frame := RenderStreamSessionFrame(boxRailSnapshot(), sessionGoldenTerminal(t), false, rail, 120, 36, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 120, 36)
	assertGolden(t, "session-focus-box-120x36-unicode", frame)
}

// TestGoldenSessionHeaderLabelsAutoMode pins the header's clickable labels
// at the widest rail a reader can drag to, in auto mode - the one state the
// mode label reports rather than merely offers, and the width at which all
// four labels fit on one line.
func TestGoldenSessionHeaderLabelsAutoMode(t *testing.T) {
	rail := boxRail{sel: 1, zone: zoneBox, mode: query.ModeAuto, railW: railMaxWidth}
	snap := boxRailSnapshot()
	snap.Target.Mode = query.ModeAuto
	frame := RenderStreamSessionFrame(snap, sessionGoldenTerminal(t), false, rail, 160, 48, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 160, 48)
	assertGolden(t, "session-header-labels-160x48-unicode", frame)
}

// sessionGoldenTerminal is a small, deterministic PTY frame so the focus
// goldens show a real terminal beside the rail rather than an empty pane.
func sessionGoldenTerminal(t *testing.T) *TerminalBuffer {
	t.Helper()
	b := NewTerminalBuffer(83, 33)
	if _, err := b.Write([]byte("shop-mate $ matev2 crew list\r\nk3  needs-decision\r\nshop-mate $ ")); err != nil {
		t.Fatalf("seed the terminal buffer: %v", err)
	}
	b.Flush()
	return b
}

// TestGoldenBoxReplyInputOpen renders the one-line reply input in the rail,
// with its [send]/[cancel] buttons and a refusal already on the outcome line
// - the pieces of rail chrome that only appear once a reader has pressed
// something.
func TestGoldenBoxReplyInputOpen(t *testing.T) {
	rail := boxRail{
		sel: 0, zone: zoneBox, mode: query.ModeSupervised,
		reply: true, replyCrew: "k3", replyText: "A",
		outcome: errMsg("Send refused: composer holds unsubmitted text · nothing was sent"),
	}
	frame := RenderSessionFrame(boxRailSnapshot(), "", rail, 120, 36, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 120, 36)
	assertGolden(t, "box-reply-input-120x36-unicode", frame)
}

// TestGoldenBoxRestartConfirmation pins the one-line recovery prompt: a
// restart stops a live agent, so it is never one keystroke away, and the
// question sits in the rail with its own [yes]/[no] buttons rather than in a
// modal drawn over the terminal being restarted.
func TestGoldenBoxRestartConfirmation(t *testing.T) {
	rail := boxRail{
		sel: 1, zone: zoneBox, mode: query.ModeSupervised,
		confirm: true, confirmText: "restart Mate payments-api?",
	}
	frame := RenderStreamSessionFrame(boxRailSnapshot(), sessionGoldenTerminal(t), false, rail, 120, 36, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 120, 36)
	assertGolden(t, "session-restart-confirm-120x36-unicode", frame)
}

// TestGoldenBoxPeekOverlay renders `p`'s overlay over the project frame. The
// pane text is a crew's own screen, reproduced verbatim: nothing is wrapped,
// because a terminal scrape is already laid out in columns.
func TestGoldenBoxPeekOverlay(t *testing.T) {
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter")) // into the project frame
	m.peek = peekFlow{
		open: true,
		crew: "k3",
		text: "> read internal/webhook/handler.go\n" +
			"  Read 212 lines\n" +
			"\n" +
			"needs-decision: migration for idempotency_keys, or key off stripe_events?\n" +
			"\n" +
			"╭──────────────────────────────────────╮\n" +
			"│ >                                    │\n" +
			"╰──────────────────────────────────────╯",
	}
	assertGolden(t, "box-peek-overlay-120x36-unicode", renderFrame(t, m))
}

// TestGoldenBoxPanelFocused renders the project frame with Tab moved onto
// the box panel: the panel's title goes accent, its selection marker becomes
// the focused one, and the key line names the three bare keys.
func TestGoldenBoxPanelFocused(t *testing.T) {
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("tab")) // list -> inspector
	m, _ = send(t, m, key("tab")) // inspector -> box
	if m.focus != paneBox {
		t.Fatalf("focus = %v, want paneBox after two Tabs", m.focus)
	}
	// The cursor and the pointer both on the one inbox item, so the panel's
	// own action strip is in the fixture too: it is the same strip the rail
	// draws, and the project frame is the surface where it is easiest to
	// lose. The panel shows the inbox, not the log - the sample project's
	// box holds four entries and exactly one of them is waiting.
	m.boxSel = 0
	m.boxHover = 0
	assertGolden(t, "box-panel-focused-120x36-unicode", renderFrame(t, m))
}

// TestBoxDigestReplacesThePanelWhenTheTableWouldLoseTooMuch pins the one
// layout decision the panel makes: at 80x24 the crews table cannot spare
// eight rows, so the box collapses to the one-line digest instead.
func TestBoxDigestReplacesThePanelWhenTheTableWouldLoseTooMuch(t *testing.T) {
	wide := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	wide, _ = send(t, wide, key("enter"))
	if h, panel := wide.boxRegion(layout(120, 36)); !panel || h != boxPanelRows {
		t.Fatalf("at 120x36 boxRegion = (%d, %v), want the full panel", h, panel)
	}
	narrow := newFixture(t, sampleTree(), 80, 24, unicodeGlyphs)
	narrow, _ = send(t, narrow, key("enter"))
	h, panel := narrow.boxRegion(layout(80, 24))
	if panel || h != 1 {
		t.Fatalf("at 80x24 boxRegion = (%d, %v), want the one-line digest", h, panel)
	}
	// And the digest must actually carry Summarize's figures, or the frame
	// that dropped the panel says less than the panel it replaced.
	line := boxDigestLine(sampleBox(), unicodeGlyphs, plainPalette()).render(80)
	for _, want := range []string{"1 to resolve", "4 entries", "1 crew"} {
		if !containsLine(line, want) {
			t.Errorf("the digest line %q does not carry %q", line, want)
		}
	}
}

// TestBoxSelectionFollowsTheNewestUntilMoved pins the rail's default: a
// reader who has not touched j/k is looking at the newest item, and stays on
// it as a crew asks something new - but an absolute selection, once made,
// keeps naming the same item rather than sliding.
func TestBoxSelectionFollowsTheNewestUntilMoved(t *testing.T) {
	b := boxList{field: sessionTestBox()}
	if got := boxDefaultSelection(b); got != 1 {
		t.Fatalf("default selection = %d, want the newest inbox item (1)", got)
	}
	grown := b
	grown.field.Value.Inbox = append(append([]query.BoxEntry{}, b.rows()...),
		query.BoxEntry{Seq: 9, Kind: query.BoxStatus, Crew: "k4", Verb: "blocked", Text: "no credentials", Attention: true})
	if got := boxDefaultSelection(grown); got != 2 {
		t.Fatalf("default selection after a new question = %d, want the new newest (2)", got)
	}
	if e, ok := boxSelectedEntry(grown, 0); !ok || e.Verb != "needs-decision" {
		t.Fatalf("index 0 after a new question = %+v, want the same needs-decision item", e)
	}
}

// TestBoxInboxIsTheDefaultAndAllShowsTheLog pins the one thing the `[all]`
// toggle is for: the rail draws the inbox, the toggle draws the merge, and
// nothing in between drops a line from the record.
func TestBoxInboxIsTheDefaultAndAllShowsTheLog(t *testing.T) {
	v := sessionTestBox()
	inbox := boxList{field: v}
	all := boxList{field: v, all: true}
	if got := len(inbox.rows()); got != 2 {
		t.Fatalf("inbox rows = %d, want the two open questions", got)
	}
	if got := len(all.rows()); got != len(v.Value.Entries) {
		t.Fatalf("[all] rows = %d, want the whole log (%d)", got, len(v.Value.Entries))
	}
	for _, e := range inbox.rows() {
		if e.Verb != "needs-decision" && e.Verb != "blocked" {
			t.Fatalf("inbox holds a %q entry; only an open question or an incident belongs there", e.Verb)
		}
		if !e.Resolvable() {
			t.Fatalf("inbox item %+v carries no resolve line", e)
		}
	}
	if !inbox.wraps() || all.wraps() {
		t.Fatalf("wraps() = inbox %v, all %v; only the inbox expands its selection", inbox.wraps(), all.wraps())
	}
}

// TestBoxEmptyInboxSaysNothingToResolve pins the placeholder and the header
// count: an empty inbox is a state, not a failed read, and the header must
// say so in words rather than by going blank.
func TestBoxEmptyInboxSaysNothingToResolve(t *testing.T) {
	b := boxList{field: sessionTestEmptyBox()}
	if got := len(b.rows()); got != 0 {
		t.Fatalf("empty-inbox fixture has %d rows", got)
	}
	body := boxBodyLines(b, -1, -1, true, unicodeGlyphs, plainPalette(), 44, 4)
	if !containsLine(body[0].render(44), "nothing to resolve") {
		t.Fatalf("empty inbox body = %q, want the placeholder", body[0].render(44))
	}
	head := boxCountLine(b, unicodeGlyphs, plainPalette()).render(44)
	if !containsLine(head, "nothing to resolve") {
		t.Fatalf("empty inbox header = %q, want the count line to say so", head)
	}
	two := boxCountLine(boxList{field: sessionTestBox()}, unicodeGlyphs, plainPalette()).render(44)
	if !containsLine(two, "2 to resolve") {
		t.Fatalf("header = %q, want \"2 to resolve\"", two)
	}
}
