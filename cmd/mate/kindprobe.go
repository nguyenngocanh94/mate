package main

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// The Console marks a Mate with 👨‍💻 and a Crew with 🤖, two cells each. 👨‍💻
// is a joined sequence (👨 + joiner + 💻): a terminal without grapheme
// clustering - tmux, older Terminal.app, some SSH setups - draws it as two
// emoji, four cells, and pushes the rest of every row right. So before the
// TUI starts, mate draws the mark at the start of the current line, asks
// the terminal where the cursor went (CSI 6n), and erases the line again;
// if the mark moved anything but two cells it falls back to ◆ / ◇, then to
// @ / o (the console design, "Emoji width rule").

// kindProbeWait is how long mate waits for a cursor-position report. A
// terminal that does not answer keeps the design's emoji.
const kindProbeWait = 300 * time.Millisecond

// probeKindGlyphs picks the kind marks: MATE_KINDS (emoji | symbol |
// ascii) when set, else what the terminal measures.
func probeKindGlyphs(getenv func(string) string) console.KindGlyphs {
	switch strings.ToLower(strings.TrimSpace(getenv("MATE_KINDS"))) {
	case "emoji":
		return console.KindEmoji
	case "symbol", "symbols":
		return console.KindSymbol
	case "ascii":
		return console.KindASCII
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return console.KindEmoji
	}
	defer tty.Close()
	if err := tty.SetReadDeadline(time.Now().Add(kindProbeWait)); err != nil {
		// Not pollable: a read could block past the probe and steal the
		// first key meant for the Console.
		return console.KindEmoji
	}
	state, err := term.MakeRaw(tty.Fd())
	if err != nil {
		return console.KindEmoji
	}
	defer func() { _ = term.Restore(tty.Fd(), state) }()
	return chooseKindGlyphs(func(s string) (int, bool) { return measureCells(tty, s) })
}

// chooseKindGlyphs is the fallback order, over a measure that reports how
// many cells a string advanced the cursor (ok false when unknown).
func chooseKindGlyphs(measure func(string) (int, bool)) console.KindGlyphs {
	if n, ok := measure("👨‍💻"); !ok || n == 2 {
		return console.KindEmoji
	}
	if n, ok := measure("◆"); !ok || n == 1 {
		return console.KindSymbol
	}
	return console.KindASCII
}

// measureCells draws s at column 1, reads the cursor-position report, and
// erases the line.
func measureCells(tty *os.File, s string) (int, bool) {
	if _, err := tty.WriteString("\r" + s + "\x1b[6n"); err != nil {
		return 0, false
	}
	defer func() { _, _ = tty.WriteString("\r\x1b[2K") }()
	_ = tty.SetReadDeadline(time.Now().Add(kindProbeWait))
	var buf bytes.Buffer
	chunk := make([]byte, 32)
	for buf.Len() < 64 {
		n, err := tty.Read(chunk)
		buf.Write(chunk[:n])
		if col, ok := parseCursorColumn(buf.Bytes()); ok {
			return col - 1, true
		}
		if err != nil {
			return 0, false
		}
	}
	return 0, false
}

// parseCursorColumn reads the column out of a cursor-position report,
// ESC [ row ; col R, anywhere in b.
func parseCursorColumn(b []byte) (int, bool) {
	start := bytes.LastIndex(b, []byte("\x1b["))
	if start < 0 {
		return 0, false
	}
	rest := b[start+2:]
	end := bytes.IndexByte(rest, 'R')
	if end < 0 {
		return 0, false
	}
	parts := strings.Split(string(rest[:end]), ";")
	if len(parts) != 2 {
		return 0, false
	}
	col, err := strconv.Atoi(parts[1])
	if err != nil || col < 1 {
		return 0, false
	}
	return col, true
}
