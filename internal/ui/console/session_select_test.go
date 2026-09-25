package console

import (
	"encoding/base64"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The Console's own text selection in the terminal zone. Through
// `herdr agent attach` neither the agent's mouse input nor its OSC 52 copy
// get through (measured 2026-09-25, Herdr 0.8.2), so an agent that selects
// and copies by itself - Claude Code does - cannot be copied from inside
// the Console. The Console has the agent's screen in its own emulator, so
// it selects from that and copies to the real terminal with OSC 52.

func selectionFixture(t *testing.T) (Model, *controllerTestChannel, *[]string) {
	t.Helper()
	m, channel := mouseBoxFixture(t)
	_, _ = m.sess.terminal.Write([]byte("\x1b[2J\x1b[H" +
		"first line   \r\n" +
		"second: Tiếng Việt 字\r\n" +
		"third"))
	m.sess.terminal.Flush()
	copied := &[]string{}
	m = m.WithClipboard(func(seq []byte) { *copied = append(*copied, string(seq)) })
	return m, channel, copied
}

func drag(t *testing.T, m Model, fromX, fromY, toX, toY int) Model {
	t.Helper()
	geo := m.sessionGeom()
	m, _ = send(t, m, tea.MouseMsg{X: geo.paneX + fromX, Y: geo.paneTop + fromY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m, _ = send(t, m, tea.MouseMsg{X: geo.paneX + toX, Y: geo.paneTop + toY, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m, cmd := send(t, m, tea.MouseMsg{X: geo.paneX + toX, Y: geo.paneTop + toY, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if cmd != nil {
		cmd()
	}
	return m
}

func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

func TestDragInTheTerminalCopiesTheSelectedText(t *testing.T) {
	cases := []struct {
		name                   string
		fromX, fromY, toX, toY int
		want                   string
	}{
		{"within one line", 0, 0, 4, 0, "first"},
		{"backwards", 4, 0, 0, 0, "first"},
		{"across lines, trailing spaces trimmed", 6, 0, 5, 1, "line\nsecond"},
		{"wide and combined characters are whole", 8, 1, 21, 1, "Tiếng Việt 字"},
		{"three lines", 0, 0, 4, 2, "first line\nsecond: Tiếng Việt 字\nthird"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, channel, copied := selectionFixture(t)
			m = drag(t, m, tc.fromX, tc.fromY, tc.toX, tc.toY)
			if len(*copied) != 1 || (*copied)[0] != osc52(tc.want) {
				var got []string
				for _, c := range *copied {
					raw := strings.TrimSuffix(strings.TrimPrefix(c, "\x1b]52;c;"), "\x07")
					b, _ := base64.StdEncoding.DecodeString(raw)
					got = append(got, string(b))
				}
				t.Fatalf("copied %q, want %q", got, tc.want)
			}
			if got := channel.writtenBytes(); len(got) != 0 {
				t.Fatalf("the selection gesture reached the PTY: %q", got)
			}
			view := m.View()
			if !strings.Contains(view, "copied") {
				t.Fatalf("the hint line does not say the text was copied:\n%s", view)
			}
		})
	}
}

// A click without a drag selects nothing and copies nothing; typing into
// the terminal clears a selection and its notice.
func TestClickAloneCopiesNothingAndTypingClearsTheSelection(t *testing.T) {
	m, _, copied := selectionFixture(t)
	m = drag(t, m, 3, 1, 3, 1)
	if len(*copied) != 0 || m.sess.sel.active() {
		t.Fatalf("a plain click copied %q / left a selection %+v", *copied, m.sess.sel)
	}
	m = drag(t, m, 0, 0, 4, 0)
	if !m.sess.sel.active() {
		t.Fatal("a drag left no selection to show")
	}
	m, _ = send(t, m, key("x"))
	if m.sess.sel.active() || strings.Contains(m.View(), "copied") {
		t.Fatalf("a keystroke kept the selection %+v or its notice", m.sess.sel)
	}
}

// Dragging out of the pane keeps selecting, clamped to its edge, so a
// selection to the end of a line does not need pixel aim.
func TestDragPastThePaneEdgeClampsTheSelection(t *testing.T) {
	m, _, copied := selectionFixture(t)
	geo := m.sessionGeom()
	m, _ = send(t, m, tea.MouseMsg{X: geo.paneX + 7, Y: geo.paneTop + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m, _ = send(t, m, tea.MouseMsg{X: geo.paneX - 5, Y: geo.paneTop + 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	_, cmd := send(t, m, tea.MouseMsg{X: geo.paneX - 5, Y: geo.paneTop + 1, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if cmd != nil {
		cmd()
	}
	if len(*copied) != 1 || (*copied)[0] != osc52("second: Tiếng Việt 字\nthird") {
		t.Fatalf("copied %q", *copied)
	}
}

// The selected cells are drawn reversed, and only while selected.
func TestSelectionIsDrawnReversed(t *testing.T) {
	m, _, _ := selectionFixture(t)
	snap := m.sess.terminal.Snapshot()
	sel := termSelection{anchor: cellPos{0, 0}, head: cellPos{4, 0}, set: true}
	applySelection(&snap, sel)
	for x := 0; x < 8; x++ {
		c := snap.Cells[0][x]
		selected := x <= 4
		if got := c.emulatorStyle.Attrs&reverseAttr != 0; got != selected {
			t.Fatalf("cell %d reversed = %v, want %v", x, got, selected)
		}
	}
	if m.sess.terminal.Snapshot().Cells[0][0].emulatorStyle.Attrs&reverseAttr != 0 {
		t.Fatal("applySelection changed the buffer, not just the snapshot it was given")
	}
}
