package console

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	vt "github.com/charmbracelet/x/vt"
)

// TerminalBuffer is the VT-parsing cell buffer ADR 0026 step 4 adds: the
// Agent View's stream controller (step 5, not built here) will draw from one
// of these instead of the bounded SessionTranscript snapshot. It wraps
// github.com/charmbracelet/x/vt's Emulator at the pinned commit
// 3986e9119cf98efcf5809969e11ad369fddb5522 - see ADR 0026's in-repo
// emulator selection record for why this untagged commit, why this library,
// and the eight semantics it was checked against - and translates its
// per-cell output into this package's own line/span
// primitives (cells.go) so no other file in this package needs to import
// vt/ultraviolet or know the emulator exists. Nothing here talks to Herdr,
// runs a PTY, or forwards input: the landed step 3 Herdr adapter owns the
// PTY stream outside this package, and step 6 will own input encoding.
//
// Width is always the emulator's own cell model, never re-derived from
// runes or bytes. The pinned emulator groups grapheme clusters (decomposed
// Vietnamese, ZWJ emoji, skin-tone modifiers) into single cells. Write feeds
// raw PTY chunks through the patch's streaming API, which keeps the trailing
// grapheme candidate until Flush establishes a rendering boundary; this never
// re-derives width from runes or bytes.
//
// Resize does not reflow, by inheritance from the emulator (spike semantic
// #8): growing keeps existing rows exactly as they were, and shrinking
// truncates whatever no longer fits rather than re-wrapping it into
// scrollback. That matches most real terminals' own behaviour, and is a
// known, deliberate limitation rather than something this wrapper works
// around.
//
// Snapshot takes an atomic copy for the renderer, so a frame cannot contain
// rows from different emulator states. x/vt's scrollback is not exposed here
// (ScrollbackLen, ScrollbackCellAt, SetScrollbackSize and ClearScrollback);
// content above the visible screen remains unreachable until step 5 adds the
// transcript-facing surface.
type TerminalBuffer struct {
	mu  sync.Mutex
	emu *vt.Emulator

	// stateMu guards cursorVisible/autoWrap separately from mu. Both fields
	// are written from vt.Emulator's own callbacks, which fire synchronously
	// from inside emu.Write/emu.WriteString - i.e. while mu is already held
	// by this file's own Write. Locking mu again from the callback would
	// deadlock (sync.Mutex is not reentrant); a second, narrower lock that
	// is never held across a call into emu avoids that without requiring
	// the callbacks to run outside the call that triggered them.
	stateMu sync.Mutex

	// cursorVisible mirrors emulator state that has no public getter - only a
	// change callback. vt.Callbacks.CursorVisibility is the
	// only way to learn the cursor's visibility; a caller that wants "is it
	// visible right now" without having watched every change must track it
	// itself, which is what this field (and its default, matching the
	// emulator's own reset default of ansi.ModeTextCursorEnable == ansi.ModeSet)
	// does.
	cursorVisible bool
}

// NewTerminalBuffer creates a buffer of the given size. cols and rows are
// display cells, matching this package's own width convention (cells.go)
// and vt.Emulator's. A terminal viewport can temporarily calculate to zero or
// below while chrome is subtracted; clamp it here, before it reaches x/vt,
// whose allocation requires positive dimensions.
func NewTerminalBuffer(cols, rows int) *TerminalBuffer {
	cols, rows = terminalDimensions(cols, rows)
	b := &TerminalBuffer{
		cursorVisible: true,
	}
	b.emu = vt.NewEmulator(cols, rows)
	b.emu.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) {
			b.stateMu.Lock()
			b.cursorVisible = visible
			b.stateMu.Unlock()
		},
	})
	return b
}

func terminalDimensions(cols, rows int) (int, int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

func (b *TerminalBuffer) isCursorVisible() bool {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	return b.cursorVisible
}

// Write feeds raw bytes - exactly what a PTY read returns, ANSI/VT escape
// sequences included - into the terminal emulator. It always reports having
// consumed the whole input (never a short write, never an error). The patched
// emulator retains a trailing grapheme candidate until a following chunk or a
// rendering boundary makes it safe to commit.
//
// It deliberately does not split its input into ANSI tokens or calculate
// grapheme widths. The temporary vt patch named in ADR 0026 owns both
// wide-glyph wrap corrections (charmbracelet/x#975 and #977) and streaming
// grapheme assembly; remove the replace only after #977 lands upstream and
// the pinned upstream commit passes this package's regression suite.
func (b *TerminalBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.emu.WritePending(p) //nolint:wrapcheck // Emulator.Write never fails on open input.
}

// Flush commits the trailing grapheme candidate retained by Write. Call this
// at a rendering or end-of-stream boundary; flushing between arbitrary PTY
// reads would again split a decomposed character or ZWJ emoji.
func (b *TerminalBuffer) Flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.emu.Flush()
}

// Resize changes the buffer's size. See the type doc: this does not reflow
// existing content.
func (b *TerminalBuffer) Resize(cols, rows int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cols, rows = terminalDimensions(cols, rows)
	b.emu.Resize(cols, rows)
}

// Width and Height are the buffer's current size in display cells.
func (b *TerminalBuffer) Width() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.emu.Width()
}

func (b *TerminalBuffer) Height() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.emu.Height()
}

// Cursor returns the cursor's position and visibility. visible is tracked
// from the CursorVisibility callback (see the type doc) rather than polled,
// since the emulator exposes no getter for it.
func (b *TerminalBuffer) Cursor() (x, y int, visible bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.emu.Flush()
	pos := b.emu.CursorPosition()
	return pos.X, pos.Y, b.isCursorVisible()
}

// AltScreen reports whether the buffer is currently showing the alternate
// screen.
func (b *TerminalBuffer) AltScreen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.emu.IsAltScreen()
}

// TerminalCell is one cell of a TerminalBuffer. Cell returns its style
// translated into this package's own drawing primitive (a lipgloss.Style,
// per cells.go's span), while Snapshot retains the emulator style privately
// so the renderer can coalesce runs before doing that translation.
//
// A wide glyph's continuation cell (the second column of a 2-wide
// character) has zero Width and empty Content, while its Style is populated
// from that continuation cell's own style. This mirrors vt.Emulator.CellAt:
// a caller must walk a row by advancing x by each cell's own Width, the way
// Line already does, rather than assuming every column is one cell.
type TerminalCell struct {
	Content string
	Width   int
	Style   lipgloss.Style
	// emulatorStyle is retained in snapshots so the renderer can compare
	// adjacent cells with ultraviolet's cheap semantic equality before it
	// translates a run into a lipgloss style. It is intentionally private:
	// callers use Style, while the stream renderer uses the atomic snapshot's
	// cheaper key.
	emulatorStyle    uv.Style
	hasEmulatorStyle bool
}

// TerminalSnapshot is an immutable, one-lock view of a complete terminal
// frame. Cells are indexed [row][column]; callers render this rather than
// taking individual Line/Cell reads while another goroutine writes PTY output.
type TerminalSnapshot struct {
	Width, Height    int
	CursorX, CursorY int
	CursorVisible    bool
	AltScreen        bool
	Cells            [][]TerminalCell
}

func terminalCell(c *uv.Cell) TerminalCell {
	if c == nil {
		return TerminalCell{}
	}
	return terminalCellWithStyle(c, cellLipglossStyle(c.Style))
}

func terminalCellWithStyle(c *uv.Cell, style lipgloss.Style) TerminalCell {
	if c == nil {
		return TerminalCell{}
	}
	content := c.Content
	// Lipgloss has no SGR 8 conceal support. Never give its renderer the
	// concealed content; a width-two grapheme becomes two spaces in Line while
	// Cell preserves the emulator's Width for callers walking the grid.
	if c.Style.Attrs&uv.AttrConceal != 0 && c.Width > 0 {
		content = " "
	}
	return TerminalCell{Content: content, Width: c.Width, Style: style,
		emulatorStyle: c.Style, hasEmulatorStyle: true}
}

func terminalSnapshotCell(c *uv.Cell) TerminalCell {
	// A snapshot keeps the emulator style instead of translating every cell
	// to lipgloss up front. terminalSnapshotLines performs that conversion
	// once per coalesced run, while the snapshot remains one atomic copy.
	return terminalCellWithStyle(c, lipgloss.Style{})
}

// Cell returns the cell at (x, y). A position outside the buffer, or a
// continuation column of a wide glyph, has zero Content and Width; its Style
// is still translated from the continuation cell's own style.
func (b *TerminalBuffer) Cell(x, y int) TerminalCell {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.emu.Flush()
	c := b.emu.CellAt(x, y)
	return terminalCell(c)
}

// Snapshot flushes and copies the complete visible terminal state while
// holding the emulator lock once. It is the rendering boundary for a complete
// frame; arbitrary PTY writes themselves never force a grapheme split.
func (b *TerminalBuffer) Snapshot() TerminalSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.emu.Flush()

	width, height := b.emu.Width(), b.emu.Height()
	pos := b.emu.CursorPosition()
	b.stateMu.Lock()
	visible := b.cursorVisible
	b.stateMu.Unlock()
	frame := TerminalSnapshot{
		Width:         width,
		Height:        height,
		CursorX:       pos.X,
		CursorY:       pos.Y,
		CursorVisible: visible,
		AltScreen:     b.emu.IsAltScreen(),
		Cells:         make([][]TerminalCell, height),
	}
	for y := 0; y < height; y++ {
		frame.Cells[y] = make([]TerminalCell, width)
		for x := 0; x < width; x++ {
			frame.Cells[y][x] = terminalSnapshotCell(b.emu.CellAt(x, y))
		}
	}
	return frame
}

// Line renders row y as one of this package's own *line values (cells.go):
// a run of adjacent cells sharing an identical style becomes one span. This
// reuses the Console's existing cell grid without transliterating
// cells.go/palette.go/glyphs.go - the step-5 stream renderer can draw a
// TerminalBuffer exactly like every other pane in this package, through the
// same line/span/screen primitives, without needing to know vt or ultraviolet
// exist.
func (b *TerminalBuffer) Line(y int) *line {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.emu.Flush()

	l := newLine()
	width := b.emu.Width()
	var run strings.Builder
	var runStyle uv.Style
	haveRun := false

	flush := func() {
		if !haveRun {
			return
		}
		l.add(run.String(), cellLipglossStyle(runStyle))
		run.Reset()
		haveRun = false
	}

	for x := 0; x < width; {
		c := b.emu.CellAt(x, y)
		if c == nil || c.Width <= 0 {
			x++
			continue
		}
		content := c.Content
		if c.Style.Attrs&uv.AttrConceal != 0 {
			content = strings.Repeat(" ", c.Width)
		}
		if content == "" {
			content = " "
		}
		if !haveRun || !c.Style.Equal(&runStyle) {
			flush()
			runStyle = c.Style
			haveRun = true
		}
		run.WriteString(content)
		x += c.Width
	}
	flush()
	return l
}

// cellStyle translates one cell's style into a lipgloss.Style. Attrs and
// Underline are the emulator's own bitset/enum (ultraviolet.Style); colors
// need cellColor's type-switch (see there for why).
func cellLipglossStyle(s uv.Style) lipgloss.Style {
	st := lipgloss.NewStyle()
	if s.Fg != nil {
		st = st.Foreground(cellColor(s.Fg))
	}
	if s.Bg != nil {
		st = st.Background(cellColor(s.Bg))
	}
	if s.Attrs&uv.AttrBold != 0 {
		st = st.Bold(true)
	}
	if s.Attrs&uv.AttrFaint != 0 {
		st = st.Faint(true)
	}
	if s.Attrs&uv.AttrItalic != 0 {
		st = st.Italic(true)
	}
	if s.Attrs&(uv.AttrBlink|uv.AttrRapidBlink) != 0 {
		st = st.Blink(true)
	}
	if s.Attrs&uv.AttrReverse != 0 {
		st = st.Reverse(true)
	}
	if s.Attrs&uv.AttrStrikethrough != 0 {
		st = st.Strikethrough(true)
	}
	if s.Underline != uv.UnderlineNone {
		st = st.Underline(true)
	}
	return st
}

// cellColor translates one color the emulator returns into a lipgloss
// color. Colour is not one type here: the emulator spike
// found truecolor normalised to the stdlib color.RGBA, not ansi.RGBColor as
// the name might suggest, alongside ansi.BasicColor (0-15) and ansi.IndexedColor/
// ExtendedColor (0-255, the same type under two names). This type-switches
// on what x/vt actually returns rather than assuming one shape; the default
// case samples RGBA() so an unrecognised concrete color.Color still renders
// rather than being dropped.
func cellColor(c color.Color) lipgloss.TerminalColor {
	switch v := c.(type) {
	case nil:
		return lipgloss.NoColor{}
	case ansi.BasicColor:
		return lipgloss.Color(strconv.Itoa(int(v)))
	case ansi.IndexedColor: // ansi.ExtendedColor is an alias for this type.
		return lipgloss.Color(strconv.Itoa(int(v)))
	case color.RGBA:
		return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", v.R, v.G, v.B))
	default:
		r, g, b, _ := c.RGBA()
		return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8))
	}
}
