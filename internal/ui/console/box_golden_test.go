package console

import (
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The message box's own fixtures (mvp.md task 15). The session-mate-* and
// project-* goldens already cover the rail and the panel in their default
// state; these pin the three things that only exist because of this task and
// have no other fixture: a moved selection on an attention entry, the reply
// input open, and the peek overlay.

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
func TestGoldenBoxRailSelectedNeedsDecision(t *testing.T) {
	frame := RenderSessionFrame(boxRailSnapshot(), "", boxRail{sel: 2, zone: zoneBox, mode: query.ModeSupervised}, 120, 36, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 120, 36)
	assertGolden(t, "box-rail-selected-120x36-unicode", frame)
}

// TestGoldenSessionTerminalFocus is the Mate session view as it opens: the
// terminal has focus, so the hint line names the agent's keys and nothing
// else, and the rail draws no selection marker of its own. This is the
// fixture that would fail if the view ever again reserved a keystroke out of
// the agent's own alphabet.
func TestGoldenSessionTerminalFocus(t *testing.T) {
	rail := boxRail{sel: 2, zone: zoneTerminal, mode: query.ModeSupervised}
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
	rail := boxRail{sel: 2, hover: 2, zone: zoneBox, mode: query.ModeSupervised}
	frame := RenderStreamSessionFrame(boxRailSnapshot(), sessionGoldenTerminal(t), false, rail, 120, 36, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 120, 36)
	assertGolden(t, "session-focus-box-120x36-unicode", frame)
}

// TestGoldenSessionHeaderLabelsAutoMode pins the header's clickable labels
// at the widest rail a reader can drag to, in auto mode - the one state the
// mode label reports rather than merely offers, and the width at which all
// four labels fit on one line.
func TestGoldenSessionHeaderLabelsAutoMode(t *testing.T) {
	rail := boxRail{sel: 2, zone: zoneBox, mode: query.ModeAuto, railW: railMaxWidth}
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
		sel: 2, zone: zoneBox, mode: query.ModeSupervised,
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
		sel: 2, zone: zoneBox, mode: query.ModeSupervised,
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
	// The cursor and the pointer both on the needs-decision entry, so the
	// panel's own action strip is in the fixture too: it is the same strip
	// the rail draws, and the project frame is the surface where it is
	// easiest to lose.
	m.boxSel = 2
	m.boxHover = 2
	assertGolden(t, "box-panel-focused-120x36-unicode", renderFrame(t, m))
}

// TestBoxDigestReplacesThePanelWhenTheTableWouldLoseTooMuch pins the one
// layout decision the panel makes: at 80x24 the crews table cannot spare
// eight rows, so the box collapses to the Summarize digest line instead.
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
	for _, want := range []string{"1 attention", "4 entries", "1 awaiting", "1 crew"} {
		if !containsLine(line, want) {
			t.Errorf("the digest line %q does not carry %q", line, want)
		}
	}
}

// TestBoxSelectionFollowsTheNewestUntilMoved pins the rail's default: a
// reader who has not touched j/k is looking at the newest entry, and stays
// on it as a crew appends - but an absolute selection, once made, keeps
// naming the same entry rather than sliding.
func TestBoxSelectionFollowsTheNewestUntilMoved(t *testing.T) {
	v := sessionTestBox()
	if got := boxDefaultSelection(v); got != 2 {
		t.Fatalf("default selection = %d, want the newest (2)", got)
	}
	grown := v
	grown.Value.Entries = append(append([]query.BoxEntry{}, v.Value.Entries...),
		query.BoxEntry{Seq: 3, Kind: query.BoxStatus, Crew: "k3", Verb: "done", Text: "shipped", Attention: true})
	if got := boxDefaultSelection(grown); got != 3 {
		t.Fatalf("default selection after an append = %d, want the new newest (3)", got)
	}
	if e, ok := boxSelectedEntry(grown, 2); !ok || e.Verb != "needs-decision" {
		t.Fatalf("index 2 after an append = %+v, want the same needs-decision entry", e)
	}
}
