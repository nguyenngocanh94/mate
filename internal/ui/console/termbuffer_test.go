package console

import (
	"image/color"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// rowText reads row y back as plain content, one string per cell (a wide
// glyph's continuation cell reads as "" - callers that care about columns
// use Cell/Line directly instead).
func rowText(t *testing.T, b *TerminalBuffer, y int) []string {
	t.Helper()
	b.Flush()
	w := b.Width()
	out := make([]string, w)
	for x := 0; x < w; x++ {
		out[x] = b.Cell(x, y).Content
	}
	return out
}

func TestWritePreservesGraphemesAcrossEveryPTYBoundary(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"VietnameseNFC", "Nguyễn"},
		{"VietnameseNFD", "Nguyễn"},
		{"ZWJEmoji", "👨‍💻"},
		{"SkinToneEmoji", "👍🏽"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for split := 1; split < len(tt.input); split++ {
				b := NewTerminalBuffer(40, 2)
				b.Write([]byte(tt.input[:split]))
				b.Write([]byte(tt.input[split:]))
				b.Flush()
				var got strings.Builder
				for x := 0; x < b.Width(); x++ {
					cell := b.Cell(x, 0)
					if cell.Width > 0 {
						got.WriteString(cell.Content)
					}
				}
				if got := strings.TrimRight(got.String(), " "); got != tt.input {
					t.Fatalf("split %d: got %q, want %q", split, got, tt.input)
				}
			}
		})
	}
}

func TestWideGraphemesDoNotLoseCellsAtAnyWidthParity(t *testing.T) {
	for _, glyph := range []string{"中", "👨‍💻", "👍🏽"} {
		for width := 2; width <= 12; width++ {
			t.Run(glyph+"/width-"+strconv.Itoa(width), func(t *testing.T) {
				b := NewTerminalBuffer(width, 300)
				const want = 300
				b.Write([]byte(strings.Repeat(glyph, want)))
				b.Flush()
				got := 0
				for y := 0; y < b.Height(); y++ {
					for x := 0; x < b.Width(); x++ {
						if b.Cell(x, y).Content == glyph {
							got++
						}
					}
				}
				if got != want {
					t.Fatalf("got %d %q cells, want %d", got, glyph, want)
				}
			})
		}
		for _, width := range []int{80, 120, 160} {
			t.Run(glyph+"/width-"+strconv.Itoa(width), func(t *testing.T) {
				b := NewTerminalBuffer(width, 20)
				const want = 300
				b.Write([]byte(strings.Repeat(glyph, want)))
				b.Flush()
				got := 0
				for y := 0; y < b.Height(); y++ {
					for x := 0; x < b.Width(); x++ {
						if b.Cell(x, y).Content == glyph {
							got++
						}
					}
				}
				if got != want {
					t.Fatalf("got %d %q cells, want %d", got, glyph, want)
				}
			})
		}
	}
}

func TestConcealedCellsRenderAsSpaces(t *testing.T) {
	b := NewTerminalBuffer(20, 2)
	b.Write([]byte("visible\x1b[8m中SECRET\x1b[0m"))
	b.Flush()
	if got := b.Cell(7, 0); got.Content != " " || got.Width != 2 {
		t.Fatalf("concealed wide cell = %+v, want a two-cell space", got)
	}
	if got := b.Cell(9, 0); got.Content != " " || got.Width != 1 {
		t.Fatalf("concealed narrow cell = %+v, want a space", got)
	}
	if got := b.Line(0).render(20); got != "visible             " {
		t.Fatalf("concealed Line = %q, want hidden content as spaces", got)
	}
}

func TestRISRestoresCursorVisibility(t *testing.T) {
	b := NewTerminalBuffer(10, 2)
	b.Write([]byte(ansi.ResetModeTextCursorEnable))
	if _, _, visible := b.Cursor(); visible {
		t.Fatal("cursor should be hidden")
	}
	b.Write([]byte("\x1bc"))
	if _, _, visible := b.Cursor(); !visible {
		t.Fatal("RIS should restore cursor visibility")
	}
}

func TestTerminalBufferClampsNonPositiveDimensions(t *testing.T) {
	b := NewTerminalBuffer(0, -1)
	if b.Width() != 1 || b.Height() != 1 {
		t.Fatalf("clamped dimensions = %dx%d, want 1x1", b.Width(), b.Height())
	}
	b.Resize(-3, 0)
	if b.Width() != 1 || b.Height() != 1 {
		t.Fatalf("resized dimensions = %dx%d, want 1x1", b.Width(), b.Height())
	}
}

func TestSnapshotIsAnAtomicCopy(t *testing.T) {
	b := NewTerminalBuffer(8, 2)
	b.Write([]byte("before"))
	b.Flush()
	frame := b.Snapshot()
	b.Write([]byte("!"))
	b.Flush()
	if got := frame.Cells[0][0].Content; got != "b" {
		t.Fatalf("snapshot mutated to %q after write", got)
	}
	if got := frame.Cells[0][6].Content; got != " " {
		t.Fatalf("snapshot includes later write %q", got)
	}
}

func rowString(t *testing.T, b *TerminalBuffer, y int) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range rowText(t, b, y) {
		if c == "" {
			sb.WriteString("_")
			continue
		}
		sb.WriteString(c)
	}
	return sb.String()
}

// TestWideGlyphAtRightEdge_CJK is the exact reproduction described in
// ADR 0026's step-4 library record: "abcd" then a
// 2-cell-wide CJK ideograph in a 5-column grid. Unpatched x/vt drops the
// ideograph silently - it appears nowhere on the grid and the cursor
// advances as if it had been placed. This pins the fix: the glyph wraps
// whole onto the next row, the way a real terminal wraps it, and nothing is
// lost.
func TestWideGlyphAtRightEdge_CJK(t *testing.T) {
	b := NewTerminalBuffer(5, 3)
	b.Write([]byte("abcd中")) // 中

	if got := rowString(t, b, 0); got != "abcd " {
		t.Fatalf("row 0 = %q, want %q (last column left blank, not the dropped glyph)", got, "abcd ")
	}
	row1 := rowText(t, b, 1)
	if row1[0] != "中" {
		t.Fatalf("row 1 col 0 = %q, want the wrapped ideograph %q", row1[0], "中")
	}
	if row1[1] != "" {
		t.Fatalf("row 1 col 1 = %q, want the ideograph's empty continuation cell", row1[1])
	}

	x, y, _ := b.Cursor()
	if x != 2 || y != 1 {
		t.Fatalf("cursor = (%d,%d), want (2,1) - just after the wrapped glyph", x, y)
	}

	// A follow-up write must land beside the wrapped glyph, not overwrite it
	// or land somewhere the dropped-glyph defect would have left the cursor.
	b.Write([]byte("Z"))
	if got := rowString(t, b, 1); got != "中_Z  " {
		t.Fatalf("row 1 after follow-up = %q, want %q", got, "中_Z  ")
	}
}

// TestWideGlyphAtRightEdge_Emoji covers a ZWJ family emoji and a skin-tone
// modifier emoji - both width-2 grapheme clusters made of more than one
// rune, per the spike's semantic #1 findings - in the same one-column-left
// shape as the CJK case.
func TestWideGlyphAtRightEdge_Emoji(t *testing.T) {
	tests := []struct {
		name  string
		emoji string
	}{
		{"ZWJFamily", "\U0001F468‍\U0001F469‍\U0001F467‍\U0001F466"}, // 👨‍👩‍👧‍👦
		{"SkinTone", "\U0001F44D\U0001F3FD"},                         // 👍🏽
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewTerminalBuffer(5, 3)
			b.Write([]byte("abcd" + tt.emoji))

			if got := rowString(t, b, 0); got != "abcd " {
				t.Fatalf("row 0 = %q, want %q", got, "abcd ")
			}
			row1 := rowText(t, b, 1)
			if row1[0] != tt.emoji {
				t.Fatalf("row 1 col 0 = %q, want the wrapped emoji %q", row1[0], tt.emoji)
			}
			if row1[1] != "" {
				t.Fatalf("row 1 col 1 = %q, want the emoji's empty continuation cell", row1[1])
			}
			x, y, _ := b.Cursor()
			if x != 2 || y != 1 {
				t.Fatalf("cursor = (%d,%d), want (2,1)", x, y)
			}
		})
	}
}

// TestWideGlyphExactFit_NotAffected pins that the fix does not touch the
// case that already worked: a wide glyph that fits in the last two columns
// exactly is drawn in place, on the same row, with no forced blank inserted
// ahead of it.
func TestWideGlyphExactFit_NotAffected(t *testing.T) {
	b := NewTerminalBuffer(5, 3)
	b.Write([]byte("abc中"))

	if got := rowString(t, b, 0); got != "abc中_" {
		t.Fatalf("row 0 = %q, want %q (glyph placed in the last two columns, no forced wrap)", got, "abc中_")
	}
	if got := rowString(t, b, 1); got != "     " {
		t.Fatalf("row 1 = %q, want a blank row (nothing wrapped)", got)
	}
}

// TestWideGlyphAtRightEdge_MidRowUnaffected pins that the workaround only
// fires at the true last column: a wide glyph anywhere else on the row
// (including the second-to-last column, i.e. the exact-fit case above from
// a different starting width) is never preceded by an injected space.
func TestWideGlyphAtRightEdge_MidRowUnaffected(t *testing.T) {
	b := NewTerminalBuffer(10, 3)
	b.Write([]byte("ab中cd"))
	if got := rowString(t, b, 0); got != "ab中_cd    " {
		t.Fatalf("row 0 = %q, want %q", got, "ab中_cd    ")
	}
}

// TestWideGlyphAfterExactLineFill_AlreadyPhantom is the regression a
// counter-review found: a single-width run that exactly fills the row
// (the emulator's own phantom-wrap already pending, unlike the other cases
// above where the wide glyph itself first reaches the last column) must
// still wrap the following wide glyph correctly, with no extra blank cell
// inserted and no one-column shift - an earlier version of writeGrapheme
// injected a space unconditionally whenever the cursor merely read as
// "last column", which fired here too even though the emulator was already
// about to wrap this glyph on its own.
func TestWideGlyphAfterExactLineFill_AlreadyPhantom(t *testing.T) {
	b := NewTerminalBuffer(5, 3)
	b.Write([]byte("abcde")) // exactly fills the row; phantom-wrap pending
	b.Write([]byte("中"))

	if got := rowString(t, b, 0); got != "abcde" {
		t.Fatalf("row 0 = %q, want %q (the full line, untouched)", got, "abcde")
	}
	row1 := rowText(t, b, 1)
	if row1[0] != "中" || row1[1] != "" {
		t.Fatalf("row 1 = %#v, want the glyph at column 0 with its continuation cell at column 1, no leading blank", row1)
	}
	x, y, _ := b.Cursor()
	if x != 2 || y != 1 {
		t.Fatalf("cursor = (%d,%d), want (2,1)", x, y)
	}
}

// TestWideGlyphAfterExactLineFill_ScrollsAtBottomRow is the same regression
// at the bottom of the screen, where the wrap must also scroll the region -
// exercised through IND in writeGrapheme's repair path rather than the
// emulator's own phantom-wrap mechanism.
func TestWideGlyphAfterExactLineFill_ScrollsAtBottomRow(t *testing.T) {
	b := NewTerminalBuffer(5, 3)
	b.Write([]byte("row0\r\n"))
	b.Write([]byte("row1\r\n"))
	b.Write([]byte("abcde")) // bottom row, exactly full
	b.Write([]byte("中"))

	if got := rowString(t, b, 0); got != "row1 " {
		t.Fatalf("row 0 after scroll = %q, want %q (row0 scrolled off, row1 shifted up)", got, "row1 ")
	}
	if got := rowString(t, b, 1); got != "abcde" {
		t.Fatalf("row 1 after scroll = %q, want %q", got, "abcde")
	}
	row2 := rowText(t, b, 2)
	if row2[0] != "中" || row2[1] != "" {
		t.Fatalf("row 2 = %#v, want the wrapped glyph at column 0", row2)
	}
}

// TestWidePlainTextWrap pins the plain-text half of spike semantic #6,
// which already passed before this fix: a run of single-width characters
// wraps the overflowing character onto the next row.
func TestWidePlainTextWrap(t *testing.T) {
	b := NewTerminalBuffer(5, 3)
	b.Write([]byte("abcdef"))
	if got := rowString(t, b, 0); got != "abcde" {
		t.Fatalf("row 0 = %q, want %q", got, "abcde")
	}
	if got := rowString(t, b, 1); got != "f    " {
		t.Fatalf("row 1 = %q, want %q", got, "f    ")
	}
}

// TestSGRColorTranslation exercises spike semantic #2: basic, 256-color and
// truecolor SGR must each translate through cellColor's type-switch,
// including the truecolor case the spike found surprising - x/vt normalises
// it to the stdlib color.RGBA, not ansi.RGBColor.
func TestSGRColorTranslation(t *testing.T) {
	b := NewTerminalBuffer(20, 3)
	b.Write([]byte("\x1b[1;31mA\x1b[0m\x1b[38;5;208mB\x1b[0m\x1b[38;2;10;200;250mC\x1b[0m"))

	a := b.Cell(0, 0)
	if !a.Style.GetBold() {
		t.Fatalf("cell A: want bold")
	}
	if got := a.Style.GetForeground(); got != cellColor(ansi.BasicColor(ansi.Red)) {
		t.Fatalf("cell A foreground = %#v, want the basic-red translation", got)
	}

	bb := b.Cell(1, 0)
	if got := bb.Style.GetForeground(); got != cellColor(ansi.IndexedColor(208)) {
		t.Fatalf("cell B foreground = %#v, want the 256-color(208) translation", got)
	}

	c := b.Cell(2, 0)
	want := cellColor(color.RGBA{R: 10, G: 200, B: 250, A: 0xff})
	if got := c.Style.GetForeground(); got != want {
		t.Fatalf("cell C foreground = %#v, want %#v (truecolor via stdlib color.RGBA)", got, want)
	}
}

// TestCellColor_UnknownConcreteTypeStillRenders pins the default branch of
// cellColor: a color.Color implementation that is none of the three the
// spike found (ansi.BasicColor, ansi.IndexedColor, color.RGBA) still
// produces a color rather than being silently dropped, by sampling RGBA().
type fakeColor struct{ r, g, b uint32 }

func (f fakeColor) RGBA() (r, g, bl, a uint32) {
	return f.r * 0x101, f.g * 0x101, f.b * 0x101, 0xffff
}

func TestCellColor_UnknownConcreteTypeStillRenders(t *testing.T) {
	got := cellColor(fakeColor{r: 0x11, g: 0x22, b: 0x33})
	want := cellColor(color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xff})
	if got != want {
		t.Fatalf("cellColor(fakeColor) = %#v, want %#v", got, want)
	}
}

func TestCellColor_NilIsNoColor(t *testing.T) {
	if _, ok := cellColor(nil).(interface {
		RGBA() (uint32, uint32, uint32, uint32)
	}); !ok {
		t.Fatalf("cellColor(nil) does not implement color.Color")
	}
}

// TestCursorPositionAndVisibility exercises spike semantic #3: CUP lands
// 0-indexed, and cursor visibility - which the emulator exposes only as a
// change callback, never a poll - is tracked correctly across a hide/show.
func TestCursorPositionAndVisibility(t *testing.T) {
	b := NewTerminalBuffer(10, 5)
	if _, _, visible := b.Cursor(); !visible {
		t.Fatalf("cursor should start visible (ansi.ModeTextCursorEnable defaults set)")
	}

	b.Write([]byte("\x1b[3;5H")) // CUP row 3, col 5 (1-indexed) -> (4,2) 0-indexed
	x, y, _ := b.Cursor()
	if x != 4 || y != 2 {
		t.Fatalf("cursor after CUP = (%d,%d), want (4,2)", x, y)
	}

	b.Write([]byte(ansi.ResetModeTextCursorEnable))
	if _, _, visible := b.Cursor(); visible {
		t.Fatalf("cursor should be hidden after DECRST 25")
	}
	b.Write([]byte(ansi.SetModeTextCursorEnable))
	if _, _, visible := b.Cursor(); !visible {
		t.Fatalf("cursor should be visible again after DECSET 25")
	}
}

// TestAltScreenEnterExit exercises spike semantic #4.
func TestAltScreenEnterExit(t *testing.T) {
	b := NewTerminalBuffer(10, 3)
	b.Write([]byte("main"))
	if b.AltScreen() {
		t.Fatalf("should not start in alt screen")
	}

	b.Write([]byte("\x1b[?1049h"))
	if !b.AltScreen() {
		t.Fatalf("should be in alt screen after DECSET 1049")
	}
	b.Write([]byte("alt"))

	b.Write([]byte("\x1b[?1049l"))
	if b.AltScreen() {
		t.Fatalf("should be back on the main screen after DECRST 1049")
	}
	if got := rowString(t, b, 0); got != "main      " {
		t.Fatalf("main screen content after alt-screen round trip = %q, want %q", got, "main      ")
	}
}

// TestEraseCommands exercises spike semantic #7.
func TestEraseCommands(t *testing.T) {
	b := NewTerminalBuffer(6, 2)
	b.Write([]byte("abcdef"))
	b.Write([]byte("\x1b[3G")) // cursor to column 3 (1-indexed) -> x=2
	b.Write([]byte("\x1b[K"))  // EL: erase to end of line
	if got := rowString(t, b, 0); got != "ab    " {
		t.Fatalf("row after EL = %q, want %q", got, "ab    ")
	}
}

// TestResizeGrowPreservesShrinkTruncates exercises spike semantic #8: no
// reflow, by design (see the TerminalBuffer doc comment).
func TestResizeGrowPreservesShrinkTruncates(t *testing.T) {
	b := NewTerminalBuffer(10, 3)
	b.Write([]byte("row1xxxxxx"))

	b.Resize(15, 5)
	if got := rowString(t, b, 0); got != "row1xxxxxx     " {
		t.Fatalf("after grow, row 0 = %q, want the original content preserved verbatim", got)
	}

	b.Resize(4, 2)
	if got := rowString(t, b, 0); got != "row1" {
		t.Fatalf("after shrink, row 0 = %q, want truncated to %q (no reflow)", got, "row1")
	}
}

// TestWriteAcrossChunkBoundary_ANSISequence pins that an escape sequence
// split across two Write calls (as an arbitrary PTY read boundary can do)
// still applies correctly - ansi.DecodeSequence's state is carried in
// decodeState across the split.
func TestWriteAcrossChunkBoundary_ANSISequence(t *testing.T) {
	b := NewTerminalBuffer(10, 3)
	b.Write([]byte("\x1b[1;3")) // split mid-CSI-parameter
	b.Write([]byte("1mX"))
	cell := b.Cell(0, 0)
	if cell.Content != "X" || !cell.Style.GetBold() {
		t.Fatalf("cell after split CSI = %+v, want bold red X", cell)
	}
}

// TestWriteAcrossChunkBoundary_MultibyteRune pins that a multi-byte UTF-8
// rune split across two Write calls is reassembled rather than misdecoded -
// the pending-buffer half of the feeding boundary, distinct from the
// escape-sequence case above (ansi.DecodeSequence itself has no cross-call
// state for this, unlike CSI/OSC parsing).
func TestWriteAcrossChunkBoundary_MultibyteRune(t *testing.T) {
	b := NewTerminalBuffer(10, 3)
	full := []byte("中") // 中, 3 UTF-8 bytes
	b.Write(full[:1])
	b.Write(full[1:])
	if got := b.Cell(0, 0).Content; got != "中" {
		t.Fatalf("cell after split rune = %q, want %q", got, "中")
	}
}

// TestLineGroupsRunsByStyle pins Line's run-length translation: adjacent
// cells sharing one style become one span, and a style change starts a new
// one - the reuse of the Console's existing cell grid without transliteration.
func TestLineGroupsRunsByStyle(t *testing.T) {
	b := NewTerminalBuffer(10, 2)
	b.Write([]byte("ab\x1b[1mCD\x1b[0mef"))

	l := b.Line(0)
	if len(l.spans) != 3 {
		t.Fatalf("spans = %d, want 3 (plain run, bold run, plain run), got %#v", len(l.spans), l.spans)
	}
	if l.spans[0].text != "ab" || l.spans[1].text != "CD" || l.spans[2].text != "ef    " {
		t.Fatalf("unexpected span texts: %#v", l.spans)
	}
	if !l.spans[1].style.GetBold() {
		t.Fatalf("middle span should be bold")
	}
}

// TestLineSkipsWideGlyphContinuationCell pins that Line's row walk advances
// by each cell's own width, never assuming one column per cell - a naive
// per-column walk would emit the wide glyph's empty continuation cell as
// its own space, splitting one glyph into two spans of wrong total width.
func TestLineSkipsWideGlyphContinuationCell(t *testing.T) {
	b := NewTerminalBuffer(5, 2)
	b.Write([]byte("a中"))
	l := b.Line(0)
	if got := l.render(5); got != "a中  " {
		t.Fatalf("rendered line = %q, want %q", got, "a中  ")
	}
}

// TestScrollRegion exercises spike semantic #5.
func TestScrollRegion(t *testing.T) {
	b := NewTerminalBuffer(6, 5)
	b.Write([]byte("row0\r\n"))
	b.Write([]byte("row1\r\n"))
	b.Write([]byte("row2\r\n"))
	b.Write([]byte("row3\r\n"))
	b.Write([]byte("\x1b[2;4r")) // DECSTBM: scroll region rows 2-4 (1-indexed)
	b.Write([]byte("\x1b[4;1H")) // cursor to bottom margin
	b.Write([]byte("\x1bD"))     // IND: scroll the region up by one

	if got := rowString(t, b, 0); got != "row0  " {
		t.Fatalf("row 0 (outside region) = %q, want untouched %q", got, "row0  ")
	}
	if got := rowString(t, b, 1); got != "row2  " {
		t.Fatalf("row 1 after scroll = %q, want %q (row2 shifted up)", got, "row2  ")
	}
}
