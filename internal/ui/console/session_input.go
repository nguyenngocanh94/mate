package console

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// ADR 0026 step 6: stream mode's input model is raw terminal bytes, not a
// Console-owned string (session-view-contract.md, "Input model"). This file
// is the one place a tea.KeyMsg or tea.MouseMsg is turned back into the byte
// sequence a real terminal would have sent for it, so onSessionStreamKey/
// onSessionStreamMouse (session_mode.go) can write it straight to the
// SessionChannel. It never decides whether a key reaches the agent at all -
// that policy is the focus model (session_focus.go), applied in
// session_mode.go's onSessionZoneKey, which is the only caller: with the
// terminal zone focused every key is encoded here, and with the box zone
// focused nothing is.
//
// The control-key encoding below relies on a real, load-bearing fact about
// bubbletea's own KeyType values: every control key's KeyType constant (see
// key.go, "Control keys") IS the raw C0 control byte for that key -
// KeyCtrlC == 3, KeyTab == 9, KeyEnter == 13, KeyEsc == 27, KeyBackspace ==
// 127. That is not a coincidence this file invents; it is how bubbletea
// itself defines those constants, and byte(msg.Type) recovers the original
// byte without a hand-written table that could drift from bubbletea's own.
// The "Other keys" block (arrows, Home/End, function keys, ...) uses
// negative KeyType values with no such relationship, so those go through
// the explicit sequence table below instead - built from key.go's own
// `sequences` map (the bytes bubbletea itself recognises decoding a real
// terminal), so encoding one back is a faithful round-trip rather than a
// guess at what a terminal "usually" sends.
const (
	minControlByte = 0
	maxC0Byte      = 31
	deleteByte     = 127
)

// specialKeySequences covers the "Other keys" block: negative KeyType
// values with no arithmetic relationship to a control byte. Each entry is
// the unmodified (no Ctrl/Shift/Alt) sequence key.go's own `sequences` map
// decodes back into that KeyType - e.g. "\x1b[A" -> KeyUp - so this is that
// map read in reverse, not a separate invention. KeySpace is included so a
// bare space forwards as one byte 0x20 the same way a Rune would.
var specialKeySequences = map[tea.KeyType][]byte{
	tea.KeySpace:    {' '},
	tea.KeyUp:       []byte("\x1b[A"),
	tea.KeyDown:     []byte("\x1b[B"),
	tea.KeyRight:    []byte("\x1b[C"),
	tea.KeyLeft:     []byte("\x1b[D"),
	tea.KeyShiftTab: []byte("\x1b[Z"),
	tea.KeyHome:     []byte("\x1b[H"),
	tea.KeyEnd:      []byte("\x1b[F"),
	tea.KeyInsert:   []byte("\x1b[2~"),
	tea.KeyDelete:   []byte("\x1b[3~"),
	// ADR 0026 lists PageUp/PageDown as an acceptance criterion; a prior
	// live probe against Herdr 0.8.2 found it swallows the plain forms
	// below and never delivers them to the program in the pane, while the
	// Shift-modified forms are forwarded byte-exactly. That disagreement is
	// held for the captain as gomate-0026-pty-contract-calls and is not
	// resolved by this encoder (this package's own tests do not re-run that
	// probe): the bytes below are what a real terminal sends for these
	// keys, encoded correctly regardless of what the current environment
	// happens to do with them once written.
	tea.KeyPgUp:   []byte("\x1b[5~"),
	tea.KeyPgDown: []byte("\x1b[6~"),

	tea.KeyCtrlUp:     []byte("\x1b[1;5A"),
	tea.KeyCtrlDown:   []byte("\x1b[1;5B"),
	tea.KeyCtrlRight:  []byte("\x1b[1;5C"),
	tea.KeyCtrlLeft:   []byte("\x1b[1;5D"),
	tea.KeyCtrlHome:   []byte("\x1b[1;5H"),
	tea.KeyCtrlEnd:    []byte("\x1b[1;5F"),
	tea.KeyCtrlPgUp:   []byte("\x1b[5;5~"),
	tea.KeyCtrlPgDown: []byte("\x1b[6;5~"),

	tea.KeyShiftUp:    []byte("\x1b[1;2A"),
	tea.KeyShiftDown:  []byte("\x1b[1;2B"),
	tea.KeyShiftRight: []byte("\x1b[1;2C"),
	tea.KeyShiftLeft:  []byte("\x1b[1;2D"),
	tea.KeyShiftHome:  []byte("\x1b[1;2H"),
	tea.KeyShiftEnd:   []byte("\x1b[1;2F"),

	// A counter-review of this encoder's first draft found these six
	// (Ctrl+Shift+arrow, Ctrl+Shift+Home/End) silently dropped despite
	// bubbletea v1.2.4 actually decoding them (key.go's own `sequences`
	// map: "\x1b[1;6A".."\x1b[1;6D", "\x1b[1;6H", "\x1b[1;6F") - a real,
	// reachable gap the doc comment below used to claim did not exist.
	tea.KeyCtrlShiftUp:    []byte("\x1b[1;6A"),
	tea.KeyCtrlShiftDown:  []byte("\x1b[1;6B"),
	tea.KeyCtrlShiftRight: []byte("\x1b[1;6C"),
	tea.KeyCtrlShiftLeft:  []byte("\x1b[1;6D"),
	tea.KeyCtrlShiftHome:  []byte("\x1b[1;6H"),
	tea.KeyCtrlShiftEnd:   []byte("\x1b[1;6F"),

	tea.KeyF1:  []byte("\x1bOP"),
	tea.KeyF2:  []byte("\x1bOQ"),
	tea.KeyF3:  []byte("\x1bOR"),
	tea.KeyF4:  []byte("\x1bOS"),
	tea.KeyF5:  []byte("\x1b[15~"),
	tea.KeyF6:  []byte("\x1b[17~"),
	tea.KeyF7:  []byte("\x1b[18~"),
	tea.KeyF8:  []byte("\x1b[19~"),
	tea.KeyF9:  []byte("\x1b[20~"),
	tea.KeyF10: []byte("\x1b[21~"),
	tea.KeyF11: []byte("\x1b[23~"),
	tea.KeyF12: []byte("\x1b[24~"),
	// F13-F20 also dropped by the same first draft, for the same reason:
	// bubbletea decodes all eight (key.go: KeyF13..KeyF20 exist and
	// "\x1b[25~".."\x1b[34~" map to them), even though real terminals rarely
	// send them.
	tea.KeyF13: []byte("\x1b[25~"),
	tea.KeyF14: []byte("\x1b[26~"),
	tea.KeyF15: []byte("\x1b[28~"),
	tea.KeyF16: []byte("\x1b[29~"),
	tea.KeyF17: []byte("\x1b[31~"),
	tea.KeyF18: []byte("\x1b[32~"),
	tea.KeyF19: []byte("\x1b[33~"),
	tea.KeyF20: []byte("\x1b[34~"),
}

// bracketedPasteStart/End wrap a pasted KeyRunes body exactly the way a
// terminal that has bracketed paste enabled would have delivered it
// (key.go's detectBracketedPaste already stripped these markers before
// bubbletea reported the paste as a plain KeyMsg with Paste: true) - a
// counter-review found the first draft dropped Paste entirely and forwarded
// a multi-line paste as bare newline-separated runes, which a harness that
// also has bracketed paste enabled (claude-code, most shells) reads as
// several separate submitted lines instead of one pasted block.
const (
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

// encodeKeyMsg turns one tea.KeyMsg into the raw bytes a terminal would have
// sent for it. ok is false for a key type this encoder does not recognise;
// dropped rather than mis-encoded, so a key type this table has no entry
// for (bubbletea has none outside this table and the control-byte range
// today, but a future upgrade could add one) fails closed instead of
// forwarding garbage.
//
// Alt is encoded the same way for every branch: a leading ESC (0x1b) byte,
// the common terminal convention for a "meta"-modified key. This is a
// deliberate simplification, not a claim that every terminal encodes every
// Alt combination this way (xterm's own CSI modifier parameters differ per
// key); it is what this encoder produces today, and is not the criterion
// ADR 0026 or session-view-contract.md pin - PageUp/PageDown and the
// unmodified control keys are.
func encodeKeyMsg(msg tea.KeyMsg) ([]byte, bool) {
	var body []byte
	switch {
	case msg.Type == tea.KeyRunes:
		if len(msg.Runes) == 0 {
			return nil, false
		}
		if msg.Paste {
			// Re-wrap in the bracketed-paste markers bubbletea's own
			// detectBracketedPaste stripped on the way in, so a harness
			// that also has bracketed paste enabled treats this as one
			// pasted block instead of newline-separated submitted lines.
			return []byte(bracketedPasteStart + string(msg.Runes) + bracketedPasteEnd), true
		}
		body = []byte(string(msg.Runes))
	default:
		if seq, ok := specialKeySequences[msg.Type]; ok {
			body = seq
		} else if msg.Type >= minControlByte && msg.Type <= maxC0Byte {
			body = []byte{byte(msg.Type)}
		} else if msg.Type == deleteByte {
			body = []byte{byte(msg.Type)}
		} else {
			return nil, false
		}
	}
	if msg.Alt {
		out := make([]byte, 0, len(body)+1)
		out = append(out, 0x1b)
		out = append(out, body...)
		return out, true
	}
	return body, true
}

// sgrMouseFlag bits, mirroring bubbletea's own parseMouseButton (mouse.go) -
// kept in exact agreement with that decoder so an encoded event, if ever
// decoded again by bubbletea, reports the identical MouseEvent it started
// from.
const (
	sgrBitShift  = 0b0000_0100
	sgrBitAlt    = 0b0000_1000
	sgrBitCtrl   = 0b0001_0000
	sgrBitMotion = 0b0010_0000
	sgrBitWheel  = 0b0100_0000
	sgrBitAdd    = 0b1000_0000
)

// encodeMouseMsg turns one tea.MouseMsg into an SGR mouse sequence
// (`ESC [ < Cb ; Cx ; Cy M` for press/motion, trailing `m` for release),
// the format bubbletea's own parseSGRMouseEvent (mouse.go) decodes. ok is
// false only when the event names a button this encoder has no code for
// (nothing in bubbletea's MouseButton set today).
func encodeMouseMsg(msg tea.MouseMsg) ([]byte, bool) {
	m := tea.MouseEvent(msg)
	var code int
	switch {
	case m.Button >= tea.MouseButtonBackward && m.Button <= tea.MouseButton11:
		code = sgrBitAdd | int(m.Button-tea.MouseButtonBackward)
	case m.IsWheel():
		code = sgrBitWheel | int(m.Button-tea.MouseButtonWheelUp)
	case m.Button == tea.MouseButtonNone:
		// A motion-only report with no button held. Real SGR mice report
		// button field 3 ("no button") for this case; bubbletea's own
		// decoder (parseMouseButton) does not special-case it back into
		// MouseButtonNone on decode (it would read as MouseButtonLeft with
		// the release bit), so this direction is not a lossless round-trip -
		// it is still the byte sequence a real terminal emits for a bare
		// motion event, which is what the PTY on the other end expects.
		code = 3
	case m.Button >= tea.MouseButtonLeft && m.Button <= tea.MouseButtonRight:
		code = int(m.Button - tea.MouseButtonLeft)
	default:
		return nil, false
	}
	if m.Shift {
		code |= sgrBitShift
	}
	if m.Alt {
		code |= sgrBitAlt
	}
	if m.Ctrl {
		code |= sgrBitCtrl
	}
	if m.Action == tea.MouseActionMotion && !m.IsWheel() {
		code |= sgrBitMotion
	}
	suffix := byte('M')
	if m.Action == tea.MouseActionRelease {
		suffix = 'm'
	}
	return []byte(fmt.Sprintf("\x1b[<%d;%d;%d%c", code, m.X+1, m.Y+1, suffix)), true
}
