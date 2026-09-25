package console

import (
	"encoding/base64"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
)

// The terminal zone's text selection. A left-button drag over the agent's
// screen selects the cells it passes, line by line the way a terminal does,
// and the release copies them to the real terminal's clipboard with OSC 52.
//
// It is the Console's own because nothing else can do it: the Console
// captures the mouse for the whole run, so the terminal emulator the
// Console is drawn in never sees the drag, and `herdr agent attach` - the
// PTY the Console streams the agent through - delivers no mouse input to
// the agent and passes no OSC 52 back out (both measured 2026-09-25 on
// Herdr 0.8.2), so an agent that selects and copies by itself, as Claude
// Code does, cannot do it from inside the Console either. The Console
// already holds the agent's screen in its emulator (termbuffer.go), which
// is all a selection needs.

// cellPos is a cell of the agent's screen: column and row, 0-based.
type cellPos struct{ x, y int }

// termSelection is the current selection. anchor is where the press was,
// head where the pointer is now; either may be the earlier of the two.
type termSelection struct {
	anchor, head cellPos
	// set is true from the press until the selection is cleared.
	set bool
	// dragging is true between the press and the release.
	dragging bool
}

// active reports whether there is a selection to draw: a press that has
// moved off its own cell.
func (s termSelection) active() bool { return s.set && s.anchor != s.head }

// bounds returns the selection's two ends in reading order.
func (s termSelection) bounds() (cellPos, cellPos) {
	a, b := s.anchor, s.head
	if b.y < a.y || (b.y == a.y && b.x < a.x) {
		a, b = b, a
	}
	return a, b
}

// covers reports whether cell (x, y) is inside the selection.
func (s termSelection) covers(x, y int) bool {
	if !s.active() {
		return false
	}
	a, b := s.bounds()
	switch {
	case y < a.y || y > b.y:
		return false
	case a.y == b.y:
		return x >= a.x && x <= b.x
	case y == a.y:
		return x >= a.x
	case y == b.y:
		return x <= b.x
	default:
		return true
	}
}

// reverseAttr is the attribute a selected cell is drawn with.
const reverseAttr = uv.AttrReverse

// applySelection draws sel onto a snapshot: every selected cell has its
// reverse attribute flipped, so selected reverse-video text reads as normal
// the way terminals show it. The snapshot is the renderer's own copy; the
// buffer is never touched.
func applySelection(snap *TerminalSnapshot, sel termSelection) {
	if !sel.active() {
		return
	}
	for y := range snap.Cells {
		for x := range snap.Cells[y] {
			if !sel.covers(x, y) {
				continue
			}
			c := &snap.Cells[y][x]
			if c.hasEmulatorStyle {
				c.emulatorStyle.Attrs ^= reverseAttr
			} else {
				c.Style = c.Style.Reverse(!c.Style.GetReverse())
			}
		}
	}
}

// selectionText is the text under sel: each row's selected cells, trailing
// blanks trimmed, rows joined with newlines. A wide character is one cell
// with its continuation after it, so it is taken whole or not at all.
func selectionText(snap TerminalSnapshot, sel termSelection) string {
	if !sel.active() {
		return ""
	}
	a, b := sel.bounds()
	rows := make([]string, 0, b.y-a.y+1)
	for y := a.y; y <= b.y && y < len(snap.Cells); y++ {
		var row strings.Builder
		for x, c := range snap.Cells[y] {
			if !sel.covers(x, y) || c.Width <= 0 {
				continue
			}
			if c.Content == "" {
				row.WriteString(" ")
				continue
			}
			row.WriteString(c.Content)
		}
		rows = append(rows, strings.TrimRight(row.String(), " "))
	}
	return strings.Join(rows, "\n")
}

// clipboardSequence is OSC 52's clipboard write for text.
func clipboardSequence(text string) []byte {
	return []byte("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07")
}

// paneCell maps a frame position to the agent screen cell under it,
// clamped to the screen, so a drag that leaves the pane keeps selecting up
// to its edge.
func (m Model) paneCell(x, y int, geo sessionGeom) cellPos {
	w, h := geo.paneW, geo.paneH
	if m.sess.terminal != nil {
		w, h = m.sess.terminal.Width(), m.sess.terminal.Height()
	}
	return cellPos{x: clampInt(x-geo.paneX, 0, max0(w-1)), y: clampInt(y-geo.paneTop, 0, max0(h-1))}
}

// onSelectionMouse is the left button over the terminal zone, and any
// event while a selection drag is under way. The press starts a
// selection, motion extends it, the release copies it.
func (m Model) onSelectionMouse(ev tea.MouseEvent, geo sessionGeom) (Model, tea.Cmd) {
	at := m.paneCell(ev.X, ev.Y, geo)
	switch {
	case ev.Action == tea.MouseActionPress:
		m.sess.sel = termSelection{anchor: at, head: at, set: true, dragging: true}
		m.sess.copied = ""
		return m, nil
	case ev.Action == tea.MouseActionMotion:
		m.sess.sel.head = at
		return m, nil
	default: // release
		m.sess.sel.head, m.sess.sel.dragging = at, false
		if !m.sess.sel.active() {
			m.sess.sel = termSelection{}
			return m, nil
		}
		return m.copySelection()
	}
}

// copySelection writes the selected text to the real terminal's clipboard.
// The hint line says what happened; it is cleared by the next press or key.
func (m Model) copySelection() (Model, tea.Cmd) {
	if m.sess.terminal == nil {
		return m, nil
	}
	text := selectionText(m.sess.terminal.Snapshot(), m.sess.sel)
	if text == "" {
		return m, nil
	}
	if m.clipboard == nil {
		m.sess.copied = "copy unavailable: this Console has no terminal clipboard"
		return m, nil
	}
	n := len([]rune(text))
	m.sess.copied = fmt.Sprintf("copied %d character(s) to the clipboard", n)
	write, seq := m.clipboard, clipboardSequence(text)
	return m, func() tea.Msg {
		write(seq)
		return nil
	}
}

// clearSelection drops the selection and its notice, as any keystroke into
// the terminal does: the reader has moved on from what they copied.
func (m Model) clearSelection() Model {
	m.sess.sel, m.sess.copied = termSelection{}, ""
	return m
}
