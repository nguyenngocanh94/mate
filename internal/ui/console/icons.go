package console

// Harness icons. The harness is drawn as its own mark, not its name, on
// every row and title (the captain's call, 2026-09-25); detail keeps the
// word beside the mark.
//
// Nerd Fonts 3.5 ships both brand marks in its Codicons set: cod-claude
// U+EC82 and cod-openai U+EC81 (Codex is OpenAI's). A terminal cannot be
// asked from inside the grid which font draws a glyph, so cmd/mate asks
// the host terminal's own font report before the Console starts and says
// so with WithHarnessIcons. Until it does, the Unicode stand-ins hold the
// cell: a private-use codepoint no font has is drawn as a box.

// iconSet names which harness marks to draw.
type iconSet int

const (
	iconsUnicode iconSet = iota
	iconsNerd
)

const (
	nerdClaude = "\uec82"
	nerdCodex  = "\uec81"
)

// withIcons returns g drawing the given harness marks. The ASCII alphabet
// keeps its own: a terminal that cannot draw box rules cannot draw a
// private-use glyph either.
func (g glyphSet) withIcons(set iconSet) glyphSet {
	if g.Name == "ascii" {
		return g
	}
	if set == iconsNerd {
		g.Claude, g.Codex = nerdClaude, nerdCodex
		return g
	}
	g.Claude, g.Codex = unicodeGlyphs.Claude, unicodeGlyphs.Codex
	return g
}

// WithHarnessIcons draws the Nerd Font brand marks when nerd is true -
// cmd/mate found a font on the host that has them.
func (m Model) WithHarnessIcons(nerd bool) Model {
	set := iconsUnicode
	if nerd {
		set = iconsNerd
	}
	m.g = m.g.withIcons(set)
	return m
}

// harnessIcon is the one-cell mark for a harness kind, or the kind's own
// word when mate does not know it: an unknown harness is never drawn as
// somebody else's logo.
func harnessIcon(kind string, g glyphSet) string {
	switch kind {
	case "claude":
		return g.Claude
	case "codex":
		return g.Codex
	case "":
		return "?"
	}
	return kind
}
