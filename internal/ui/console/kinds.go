package console

// KindGlyphs is how the Mate and Crew kind marks are drawn. The emoji are
// the design's; a terminal without grapheme clustering (tmux, older
// Terminal.app, some SSH setups) draws 👨‍💻 as two emoji, four cells, and
// pushes the rest of every row right. cmd/mate measures that at startup
// (CSI 6n) and tells the Console which alphabet held (design I, "Emoji
// width rule"). Emoji keep their own colours, so kind never carries status
// or focus.
type KindGlyphs int

const (
	// KindEmoji draws 👨‍💻 and 🤖.
	KindEmoji KindGlyphs = iota
	// KindSymbol draws ◆ and ◇, padded to two cells.
	KindSymbol
	// KindASCII draws @ and o, padded to two cells.
	KindASCII
)

// withKinds returns g drawing the given kind marks.
func (g glyphSet) withKinds(k KindGlyphs) glyphSet {
	switch k {
	case KindSymbol:
		g.Mate, g.Crew = "◆ ", "◇ "
	case KindASCII:
		g.Mate, g.Crew = "@ ", "o "
	}
	return g
}

// WithKindGlyphs sets the kind marks cmd/mate's startup probe settled on.
func (m Model) WithKindGlyphs(k KindGlyphs) Model {
	if m.g.Name == "ascii" {
		return m
	}
	m.g = m.g.withKinds(k)
	return m
}
