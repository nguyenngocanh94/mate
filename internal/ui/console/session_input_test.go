package console

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ADR 0026 step 6: encodeKeyMsg/encodeMouseMsg are the byte-exact boundary
// between a decoded Bubble Tea event and the raw stream a PTY expects. Every
// case here is checked against a literal byte sequence, not a symbolic
// comparison, because a wrong byte here is exactly the defect this step
// exists to prevent (the composer's "just append runes" model silently
// tolerates a wrong byte; a raw PTY does not).

func TestEncodeKeyMsgPrintableUTF8IncludingVietnamese(t *testing.T) {
	cases := []struct {
		name  string
		runes []rune
		want  string
	}{
		{"ascii", []rune("a"), "a"},
		{"vietnamese single", []rune("ệ"), "ệ"},
		{"vietnamese phrase", []rune("Tiếng Việt"), "Tiếng Việt"},
		{"vietnamese with tone and horn", []rune("ườ"), "ườ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := encodeKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: tc.runes})
			if !ok {
				t.Fatalf("encodeKeyMsg(%q) ok = false, want true", tc.name)
			}
			if !bytes.Equal(got, []byte(tc.want)) {
				t.Fatalf("encodeKeyMsg(%q) = %x, want %x", tc.name, got, []byte(tc.want))
			}
		})
	}
}

func TestEncodeKeyMsgEmptyRunesIsNotForwarded(t *testing.T) {
	if _, ok := encodeKeyMsg(tea.KeyMsg{Type: tea.KeyRunes}); ok {
		t.Fatalf("encodeKeyMsg with no runes: ok = true, want false")
	}
}

// TestEncodeKeyMsgBracketedPasteIsReWrapped is a counter-review regression:
// the first draft forwarded a paste's runes bare, which a harness with its
// own bracketed-paste mode enabled reads as several separately-submitted
// lines instead of one pasted block, since bubbletea already stripped the
// \x1b[200~/\x1b[201~ markers before reporting the paste as a KeyMsg.
func TestEncodeKeyMsgBracketedPasteIsReWrapped(t *testing.T) {
	got, ok := encodeKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line1\nline2"), Paste: true})
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	want := []byte("\x1b[200~line1\nline2\x1b[201~")
	if !bytes.Equal(got, want) {
		t.Fatalf("pasted bytes = %q, want %q", got, want)
	}
}

func TestEncodeKeyMsgNonPasteRunesAreNotBracketed(t *testing.T) {
	got, ok := encodeKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if !bytes.Equal(got, []byte("hi")) {
		t.Fatalf("got = %q, want %q (no bracketed-paste markers on ordinary typed input)", got, "hi")
	}
}

// TestEncodeKeyMsgCtrlShiftAndFunctionKeysBeyondF12 is a counter-review
// regression: the first draft silently dropped these even though bubbletea
// v1.2.4 actually decodes them (key.go's own `sequences` map), while its
// doc comment claimed nothing in the KeyType set fell into that case.
func TestEncodeKeyMsgCtrlShiftAndFunctionKeysBeyondF12(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyMsg
		want []byte
	}{
		{"ctrl+shift+up", tea.KeyMsg{Type: tea.KeyCtrlShiftUp}, []byte("\x1b[1;6A")},
		{"ctrl+shift+down", tea.KeyMsg{Type: tea.KeyCtrlShiftDown}, []byte("\x1b[1;6B")},
		{"ctrl+shift+right", tea.KeyMsg{Type: tea.KeyCtrlShiftRight}, []byte("\x1b[1;6C")},
		{"ctrl+shift+left", tea.KeyMsg{Type: tea.KeyCtrlShiftLeft}, []byte("\x1b[1;6D")},
		{"ctrl+shift+home", tea.KeyMsg{Type: tea.KeyCtrlShiftHome}, []byte("\x1b[1;6H")},
		{"ctrl+shift+end", tea.KeyMsg{Type: tea.KeyCtrlShiftEnd}, []byte("\x1b[1;6F")},
		{"f13", tea.KeyMsg{Type: tea.KeyF13}, []byte("\x1b[25~")},
		{"f20", tea.KeyMsg{Type: tea.KeyF20}, []byte("\x1b[34~")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := encodeKeyMsg(tc.msg)
			if !ok {
				t.Fatalf("encodeKeyMsg(%s) ok = false, want true", tc.name)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("encodeKeyMsg(%s) = %x, want %x", tc.name, got, tc.want)
			}
		})
	}
}

func TestEncodeKeyMsgControlAndNavigationKeys(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyMsg
		want []byte
	}{
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, []byte{0x7f}},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, []byte{0x0d}},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}, []byte{0x09}},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}, []byte{0x1b}},
		{"up", tea.KeyMsg{Type: tea.KeyUp}, []byte("\x1b[A")},
		{"down", tea.KeyMsg{Type: tea.KeyDown}, []byte("\x1b[B")},
		{"right", tea.KeyMsg{Type: tea.KeyRight}, []byte("\x1b[C")},
		{"left", tea.KeyMsg{Type: tea.KeyLeft}, []byte("\x1b[D")},
		{"home", tea.KeyMsg{Type: tea.KeyHome}, []byte("\x1b[H")},
		{"end", tea.KeyMsg{Type: tea.KeyEnd}, []byte("\x1b[F")},
		{"pgup", tea.KeyMsg{Type: tea.KeyPgUp}, []byte("\x1b[5~")},
		{"pgdown", tea.KeyMsg{Type: tea.KeyPgDown}, []byte("\x1b[6~")},
		{"insert", tea.KeyMsg{Type: tea.KeyInsert}, []byte("\x1b[2~")},
		{"delete", tea.KeyMsg{Type: tea.KeyDelete}, []byte("\x1b[3~")},
		{"shift+tab", tea.KeyMsg{Type: tea.KeyShiftTab}, []byte("\x1b[Z")},
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}, []byte{0x03}},
		{"ctrl+d", tea.KeyMsg{Type: tea.KeyCtrlD}, []byte{0x04}},
		{"ctrl+l", tea.KeyMsg{Type: tea.KeyCtrlL}, []byte{0x0c}},
		{"ctrl+o", tea.KeyMsg{Type: tea.KeyCtrlO}, []byte{0x0f}},
		{"ctrl+a", tea.KeyMsg{Type: tea.KeyCtrlA}, []byte{0x01}},
		{"ctrl+up", tea.KeyMsg{Type: tea.KeyCtrlUp}, []byte("\x1b[1;5A")},
		{"f1", tea.KeyMsg{Type: tea.KeyF1}, []byte("\x1bOP")},
		{"f12", tea.KeyMsg{Type: tea.KeyF12}, []byte("\x1b[24~")},
		{"space", tea.KeyMsg{Type: tea.KeySpace}, []byte{0x20}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := encodeKeyMsg(tc.msg)
			if !ok {
				t.Fatalf("encodeKeyMsg(%s) ok = false, want true", tc.name)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("encodeKeyMsg(%s) = %x, want %x", tc.name, got, tc.want)
			}
		})
	}
}

func TestEncodeKeyMsgAltPrefixesEscape(t *testing.T) {
	got, ok := encodeKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a"), Alt: true})
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	want := []byte{0x1b, 'a'}
	if !bytes.Equal(got, want) {
		t.Fatalf("alt+a = %x, want %x", got, want)
	}
}

func TestEncodeMouseMsgLeftClickPressAndRelease(t *testing.T) {
	press, ok := encodeMouseMsg(tea.MouseMsg{X: 4, Y: 9, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !ok {
		t.Fatalf("press: ok = false")
	}
	if want := []byte("\x1b[<0;5;10M"); !bytes.Equal(press, want) {
		t.Fatalf("press = %q, want %q", press, want)
	}

	release, ok := encodeMouseMsg(tea.MouseMsg{X: 4, Y: 9, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if !ok {
		t.Fatalf("release: ok = false")
	}
	if want := []byte("\x1b[<0;5;10m"); !bytes.Equal(release, want) {
		t.Fatalf("release = %q, want %q", release, want)
	}
}

func TestEncodeMouseMsgWheelAndModifiers(t *testing.T) {
	wheel, ok := encodeMouseMsg(tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonWheelUp, Ctrl: true})
	if !ok {
		t.Fatalf("ok = false")
	}
	// wheel up (64) | ctrl (16) = 80.
	if want := []byte("\x1b[<80;1;1M"); !bytes.Equal(wheel, want) {
		t.Fatalf("wheel = %q, want %q", wheel, want)
	}
}

func TestEncodeMouseMsgMotionWithNoButton(t *testing.T) {
	motion, ok := encodeMouseMsg(tea.MouseMsg{X: 9, Y: 19, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	if !ok {
		t.Fatalf("ok = false")
	}
	// no-button (3) | motion (32) = 35.
	if want := []byte("\x1b[<35;10;20M"); !bytes.Equal(motion, want) {
		t.Fatalf("motion = %q, want %q", motion, want)
	}
}
