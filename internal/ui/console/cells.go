package console

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The character grid. Everything the Console draws is emitted through the
// two types in this file, and nothing else in the package writes a rendered
// line by hand. That is what makes the frame contract checkable in one
// place: View always returns exactly h lines of exactly w display cells.
//
// Width here always means display cells, never bytes and never runes. IDs,
// paths and titles come from the database and are arbitrary text, so a
// two-cell rune or a combining mark must not shift a column by one.
//
// "Arbitrary text" also means text that is not a single display line:
// lipgloss.Width measures the widest line of a multi-line string, not its
// line count, so a literal newline would otherwise reach the terminal and
// break the h-lines guarantee rather than the w-cells one. line.add is the
// one place a string becomes span text, so it is the one place that gets
// sanitised - see sanitizeText.

// cells is the one width measurement in the package: display cells, as
// lipgloss counts them (it ignores ANSI escapes, so a styled string
// measures the same as its plain text).
func cells(s string) int { return lipgloss.Width(s) }

// sanitizeText strips ANSI escape sequences and neutralises control
// characters before text becomes span content. Without this, a newline in a
// title, a path, an error string or a reason reaches the terminal as a real
// line break - lipgloss.Width reports the width of the widest line, not the
// line count, so nothing upstream of this notices - and the frame ends up
// with more than h lines. Each control character becomes one space rather
// than being dropped, so it costs a cell instead of silently collapsing two
// words together.
//
// The range neutralised is C0 (< 0x20), DEL (0x7f) and C1 (0x80..0x9f) -
// not just C0. U+0085 NEL sits in C1: it is invisible to lipgloss.Width (one
// rune, zero measured effect) but a terminal that honours C1 controls reads
// it as its own line-break function, so leaving C1 alone reopens exactly the
// h-lines defect this function exists to close, just through a control
// range one byte higher. The text this package renders comes from a
// database, a file or an error string - arbitrary text - so "arbitrary"
// has to mean the whole control-character space, not only the ASCII half
// of it.
func sanitizeText(s string) string {
	s = ansi.Strip(s)
	clean := true
	for _, r := range s {
		if isControlRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isControlRune(r) {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isControlRune reports whether r is a C0, DEL or C1 control character -
// the full control-character space sanitizeText neutralises.
func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// span is a run of text drawn in one style.
type span struct {
	text  string
	style lipgloss.Style
	// plain marks text that carries no styling of its own - padding. On a
	// selected line it is drawn through the fill style itself rather than
	// through a style layered on top of a fresh lipgloss.NewStyle(), whose
	// renderer (and therefore colour profile) may not be the one the palette
	// was built on. Without this, a selected row's background reaches its
	// glyphs but silently skips its padding.
	plain bool
}

// line accumulates spans and renders them to an exact cell width. A line
// may carry a fill style (a selected row): its background is applied to
// every span and to the trailing padding, so the highlight covers the full
// width of the pane rather than stopping at the last glyph.
type line struct {
	spans   []span
	fill    lipgloss.Style
	hasFill bool
}

func newLine() *line { return &line{} }

// background makes fill the line's background: every span and the trailing
// padding are drawn on it. Selection is a whole-row signal, so it belongs
// to the line rather than to the spans (see signals.go).
//
// The fill is a style, not a bare colour, because the padding is rendered
// through it - and a style built on a different lipgloss renderer than the
// palette's would resolve the colour profile differently and silently drop
// the background on the padding while keeping it on the glyphs.
func (l *line) background(fill lipgloss.Style) *line {
	l.fill, l.hasFill = fill, true
	return l
}

func (l *line) add(text string, style lipgloss.Style) *line {
	text = sanitizeText(text)
	if text == "" {
		return l
	}
	l.spans = append(l.spans, span{text: text, style: style})
	return l
}

func (l *line) addSpan(s span) *line { return l.add(s.text, s.style) }

func (l *line) addSpans(spans ...span) *line {
	for _, s := range spans {
		l.addSpan(s)
	}
	return l
}

// pad appends n blank cells.
func (l *line) pad(n int) *line {
	if n <= 0 {
		return l
	}
	l.spans = append(l.spans, span{text: strings.Repeat(" ", n), plain: true})
	return l
}

// width is the display width of the spans added so far, before padding or
// truncation to the render width.
func (l *line) width() int {
	n := 0
	for _, s := range l.spans {
		n += cells(s.text)
	}
	return n
}

// render draws the line as exactly w display cells: spans in order,
// truncated at the cell boundary when they overflow, and space-padded when
// they fall short.
func (l *line) render(w int) string {
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, s := range l.spans {
		if used >= w {
			break
		}
		text := s.text
		if used+cells(text) > w {
			// Truncate with no marker: a caller that wants an ellipsis asks
			// for one explicitly (truncateEnd), because a marker inserted
			// here would silently rewrite a value the caller had already
			// decided to print in full.
			text = ansi.Truncate(text, w-used, "")
		}
		if text == "" {
			continue
		}
		used += cells(text)
		b.WriteString(l.renderSpan(span{text: text, style: s.style, plain: s.plain}))
	}
	if used < w {
		b.WriteString(l.renderSpan(span{text: strings.Repeat(" ", w-used), plain: true}))
	}
	return b.String()
}

// renderSpan draws one span, on the line's fill when it has one.
func (l *line) renderSpan(s span) string {
	if s.plain {
		if l.hasFill {
			return l.fill.Render(s.text)
		}
		return s.text
	}
	return l.styleFor(s.style).Render(s.text)
}

// styleFor layers the line's fill background under a span's own style, so a
// selected row keeps each span's own foreground - a failed row that happens
// to be selected still reads as failed.
func (l *line) styleFor(s lipgloss.Style) lipgloss.Style {
	if !l.hasFill {
		return s
	}
	if bg := l.fill.GetBackground(); bg != nil {
		return s.Background(bg)
	}
	return s
}

// cut returns a copy of l truncated to at most w cells, marking a real cut
// with the glyph set's ellipsis. The marker inherits the style of the span
// it replaced, so a cut dim breadcrumb does not end in a bright ellipsis.
func (l *line) cut(w int, g glyphSet) *line {
	out := newLine()
	out.fill, out.hasFill = l.fill, l.hasFill
	if w <= 0 {
		return out
	}
	if l.width() <= w {
		out.spans = append(out.spans, l.spans...)
		return out
	}
	keep := w - cells(g.Ellipsis)
	if keep < 0 {
		keep = 0
	}
	used := 0
	marker := span{text: g.Ellipsis}
	for _, s := range l.spans {
		marker = span{text: g.Ellipsis, style: s.style, plain: s.plain}
		if used >= keep {
			break
		}
		text := s.text
		if used+cells(text) > keep {
			text = cutCells(text, keep-used)
		}
		if text == "" {
			continue
		}
		used += cells(text)
		out.spans = append(out.spans, span{text: text, style: s.style, plain: s.plain})
	}
	return out.addSpan(marker)
}

// leftRight puts right flush against the far edge of a w-cell line, with
// left starting at column 0 and at least one blank cell between them.
//
// The right side is reserved and the left side is what gets cut. On the
// header and the breadcrumb - the only two lines that use this - the right
// side carries "Recorded snapshot / As of HH:MM:SS" and the level's row
// count: the claim that everything below is a recorded snapshot of a known
// age. Dropping the tail of a long id or title is a cosmetic loss; dropping
// that claim would let the screen read as live.
func leftRight(left, right *line, w int, g glyphSet) *line {
	rw := right.width()
	if rw >= w {
		return right.cut(w, g)
	}
	out := left.cut(w-rw-1, g)
	out.pad(w - rw - out.width())
	out.spans = append(out.spans, right.spans...)
	return out
}

// screen collects rendered lines and guarantees the frame contract: exactly
// h lines, each exactly w display cells. It is deliberately the only way a
// View reaches a string, so a pane that miscounts its own width cannot
// corrupt the frame around it - the miscount is absorbed here and caught by
// the width tests rather than shifting every line below it.
type screen struct {
	w, h  int
	lines []string
}

func newScreen(w, h int) *screen { return &screen{w: w, h: h} }

// push renders l at the screen's full width.
func (s *screen) push(l *line) { s.lines = append(s.lines, l.render(s.w)) }

// pushSplit renders one body line of a two-pane frame: the list pane, the
// vertical rule the horizontal rules join with a tee, then the inspector.
// This is the only place the divider column is placed, so the tee joints in
// frame.go and the divider here cannot drift apart.
func (s *screen) pushSplit(left *line, leftWidth int, divider span, right *line, rightWidth int) {
	d := newLine().addSpan(divider)
	s.lines = append(s.lines, left.render(leftWidth)+d.render(1)+right.render(rightWidth))
}

// String renders the frame. Lines beyond h are dropped and missing lines
// are filled with blanks, so the caller's line budget is enforced here
// rather than trusted.
func (s *screen) String() string {
	if s.w <= 0 || s.h <= 0 {
		return ""
	}
	out := make([]string, 0, s.h)
	for i := 0; i < s.h; i++ {
		if i < len(s.lines) {
			out = append(out, fitCells(s.lines[i], s.w))
			continue
		}
		out = append(out, strings.Repeat(" ", s.w))
	}
	return strings.Join(out, "\n")
}

// fitCells forces an already-rendered line to exactly w display cells.
func fitCells(s string, w int) string {
	switch n := cells(s); {
	case n == w:
		return s
	case n > w:
		t := ansi.Truncate(s, w, "")
		if pad := w - cells(t); pad > 0 {
			// A truncation that landed inside a two-cell rune leaves one
			// cell short; fill it rather than returning a narrow line.
			return t + strings.Repeat(" ", pad)
		}
		return t
	default:
		return s + strings.Repeat(" ", w-n)
	}
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
