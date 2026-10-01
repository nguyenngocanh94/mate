package console

import "github.com/nguyenngocanh94/mate/internal/query"

// Harness icons. The harness is drawn as its own mark, not its name, on
// every row and title (the captain's call, 2026-09-25); detail keeps the
// word beside the mark. Each harness names its marks itself
// (query.HarnessIcon, from its profile's Info), so the Console names no
// harness.
//
// The Nerd marks are private-use codepoints. A terminal cannot be asked
// from inside the grid which font draws a glyph, so cmd/mate asks the host
// terminal's own font report before the Console starts and says so with
// WithHarnessIcons. Until it does, the Unicode stand-ins hold the cell: a
// private-use codepoint no font has is drawn as a box.

// iconSet names which harness marks to draw.
type iconSet int

const (
	iconsUnicode iconSet = iota
	iconsNerd
	iconsASCII
)

// withIcons returns g drawing the given harness marks. The ASCII alphabet
// keeps its own: a terminal that cannot draw box rules cannot draw a
// private-use glyph either.
func (g glyphSet) withIcons(set iconSet) glyphSet {
	if g.Name == "ascii" {
		return g
	}
	g.Icons = set
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

// harnessIcon is the one-cell mark for a harness kind, as the catalog the
// snapshot carries draws it, or the kind's own word when the catalog does
// not hold it: an unknown harness is never drawn as somebody else's logo.
func harnessIcon(kind string, g glyphSet, harnesses []query.Harness) string {
	if kind == "" {
		return "?"
	}
	for _, h := range harnesses {
		if string(h.Kind) != kind {
			continue
		}
		icon := h.Icon.Unicode
		switch g.Icons {
		case iconsNerd:
			icon = h.Icon.Nerd
		case iconsASCII:
			icon = h.Icon.ASCII
		}
		if icon != "" {
			return icon
		}
	}
	return kind
}

// harnessIcon is the package function over the model's own alphabet and
// the catalog of the snapshot on screen.
func (m Model) harnessIcon(kind string) string { return harnessIcon(kind, m.g, m.tree.Harnesses) }
