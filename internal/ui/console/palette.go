package console

import "github.com/charmbracelet/lipgloss"

// tok is a design token (design I, "1 Tokens"). Every segment the Console
// draws carries one, never a raw colour, so the lift rule below can be
// applied in one place.
type tok uint8

const (
	tFg    tok = iota // names, values, key glyphs: the terminal's own foreground (SGR 39)
	tDim              // labels, line 2, key labels: ANSI 7
	tFaint            // rules, unavailable actions, "…": ANSI 8
	tAcc              // focused pane title and marker: ANSI 14, bold
	tAmber            // attention, unknown, review: ANSI 11
	tRed              // failed, error, the destructive verb: ANSI 9
	tGreen            // succeeded: ANSI 10
	tBold             // the terminal's own foreground, bold
)

// ansiOf is each token's ANSI index; tFg and tBold have none, because a
// light theme that maps 15 close to white would erase every name drawn in
// it ("Light terminals" in design I).
var ansiOf = map[tok]uint{
	tDim:   7,
	tFaint: 8,
	tAcc:   14,
	tAmber: 11,
	tRed:   9,
	tGreen: 10,
}

// selBG is the selected row's background: ANSI 8 across the whole width.
const selBG = 8

// palette maps tokens to styles, twice: as drawn, and lifted for a row on
// the selection background.
type palette struct {
	plain  bool
	normal map[tok]lipgloss.Style
	lifted map[tok]lipgloss.Style
	sel    lipgloss.Style
}

func defaultPalette() palette { return paletteFor(lipgloss.DefaultRenderer()) }

func paletteFor(r *lipgloss.Renderer) palette {
	base := func(t tok) lipgloss.Style {
		s := r.NewStyle()
		if n, ok := ansiOf[t]; ok {
			s = s.Foreground(lipgloss.ANSIColor(n))
		}
		if t == tAcc || t == tBold {
			s = s.Bold(true)
		}
		return s
	}
	p := palette{normal: map[tok]lipgloss.Style{}, lifted: map[tok]lipgloss.Style{}}
	bg := lipgloss.ANSIColor(selBG)
	for t := tFg; t <= tBold; t++ {
		p.normal[t] = base(t)
		p.lifted[t] = base(lift(t)).Background(bg)
	}
	p.sel = r.NewStyle().Background(bg)
	return p
}

// lift is design I's lift rule: on a sel-bg row every tint steps up once,
// faint to dim and dim to fg, because index 8 text on an index 8
// background would otherwise vanish.
func lift(t tok) tok {
	switch t {
	case tFaint:
		return tDim
	case tDim:
		return tFg
	}
	return t
}

// plainPalette draws no colour at all: the fixtures' palette, so a signal
// carried only by colour is invisible in them (doc.go).
func plainPalette() palette { return palette{plain: true} }

func (p palette) style(t tok, selected bool) (lipgloss.Style, bool) {
	if p.plain {
		return lipgloss.Style{}, false
	}
	if selected {
		return p.lifted[t], true
	}
	return p.normal[t], true
}
