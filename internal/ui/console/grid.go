package console

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The character grid. View always returns exactly h lines of exactly w
// display cells (doc.go); every line is a gline rendered through render,
// and nothing else writes a frame line.

// cells is the display width of s, measured by grapheme cluster.
func cells(s string) int { return lipgloss.Width(s) }

// sanitizeText strips ANSI and replaces control runes with spaces: ids,
// titles and questions come from files and the database, and one stray
// escape would repaint the frame.
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

func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// seg is one run of text in one token.
type seg struct {
	text string
	t    tok
}

// gline is one frame line: segments, and whether the row sits on the
// selection background. Methods return a new line; a gline is a value.
type gline struct {
	segs []seg
	sel  bool
}

func gl() gline { return gline{} }

// add appends text in token t, sanitised. Empty text adds nothing.
func (l gline) add(text string, t tok) gline {
	text = sanitizeText(text)
	if text == "" {
		return l
	}
	out := gline{segs: make([]seg, len(l.segs), len(l.segs)+1), sel: l.sel}
	copy(out.segs, l.segs)
	out.segs = append(out.segs, seg{text: text, t: t})
	return out
}

// pad appends n blank cells.
func (l gline) pad(n int) gline {
	if n <= 0 {
		return l
	}
	return l.add(strings.Repeat(" ", n), tFg)
}

// selected puts the line on the selection background.
func (l gline) selected(on bool) gline {
	l.sel = on
	return l
}

func (l gline) width() int {
	n := 0
	for _, s := range l.segs {
		n += cells(s.text)
	}
	return n
}

// padTo pads the line with blanks to exactly w cells, or cuts it to w.
func (l gline) padTo(w int, g glyphSet) gline {
	if n := l.width(); n < w {
		return l.pad(w - n)
	}
	return l.cut(w, g)
}

// cut shortens the line to at most w cells, ending in the ellipsis in the
// token of the segment it cut into.
func (l gline) cut(w int, g glyphSet) gline {
	if l.width() <= w {
		return l
	}
	out := gline{sel: l.sel}
	if w <= 0 {
		return out
	}
	keep := w - cells(g.Ellipsis)
	used := 0
	last := tFg
	for _, s := range l.segs {
		last = s.t
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
		out.segs = append(out.segs, seg{text: text, t: s.t})
	}
	if keep < 0 {
		return out
	}
	return out.add(g.Ellipsis, last)
}

// join appends right's segments to l.
func (l gline) join(right gline) gline {
	out := gline{segs: make([]seg, 0, len(l.segs)+len(right.segs)), sel: l.sel}
	out.segs = append(out.segs, l.segs...)
	out.segs = append(out.segs, right.segs...)
	return out
}

// spread lays left and right out on w cells: right keeps its width, left is
// cut to what remains less one blank, and the gap is padded.
func spread(left, right gline, w int, g glyphSet) gline {
	rw := right.width()
	if rw >= w {
		return right.cut(w, g).selected(left.sel)
	}
	l := left.cut(w-rw-1, g)
	return l.pad(w - rw - l.width()).join(right)
}

// render draws the line on exactly w cells.
func (l gline) render(w int, p palette) string {
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, s := range l.segs {
		if used >= w {
			break
		}
		text := s.text
		if used+cells(text) > w {
			text = cutCells(text, w-used)
		}
		if text == "" {
			continue
		}
		used += cells(text)
		b.WriteString(p.paint(text, s.t, l.sel))
	}
	if used < w {
		b.WriteString(p.paint(strings.Repeat(" ", w-used), tFg, l.sel))
	}
	return b.String()
}

func (p palette) paint(text string, t tok, selected bool) string {
	st, ok := p.style(t, selected)
	if !ok {
		return text
	}
	return st.Render(text)
}

// grid is a frame under construction: exactly h lines of w cells.
type grid struct {
	w, h  int
	lines []gline
}

func newGrid(w, h int) *grid { return &grid{w: w, h: h} }

func (s *grid) push(ls ...gline) {
	s.lines = append(s.lines, ls...)
}

// String renders the frame: missing lines are blank, extra lines dropped,
// and each line is exactly w cells.
func (s *grid) String(p palette) string {
	if s.w <= 0 || s.h <= 0 {
		return ""
	}
	out := make([]string, 0, s.h)
	for i := 0; i < s.h; i++ {
		l := gl()
		if i < len(s.lines) {
			l = s.lines[i]
		}
		out = append(out, l.render(s.w, p))
	}
	return strings.Join(out, "\n")
}

// fit returns exactly h lines: ls cut, or padded with blank lines.
func fit(ls []gline, h int) []gline {
	if h <= 0 {
		return nil
	}
	if len(ls) >= h {
		return ls[:h]
	}
	out := make([]gline, 0, h)
	out = append(out, ls...)
	for len(out) < h {
		out = append(out, gl())
	}
	return out
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// padRight pads s with blanks to w cells.
func padRight(s string, w int) string {
	if n := cells(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
