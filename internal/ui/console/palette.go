package console

import "github.com/charmbracelet/lipgloss"

// The palette: the ten tokens of design/mate-tui.css as lipgloss styles.
//
// Every token is an ANSI index and nothing else. That file says so itself -
// "a real terminal substitutes its own theme, so only the index mapping is
// a contract" - and its oklch values are previews of one theme, not values
// the Console is entitled to impose. Three consequences, all deliberate:
//
//   - No colour exists only at truecolor. A 16-colour terminal and a
//     truecolor one draw the same hierarchy, so the layout survives either,
//     which is the property the design asked for.
//   - The hierarchy travels as indices: bright white (15) over white (7)
//     over bright black (8). A user who has themed those three keeps their
//     own ramp instead of having ours painted over it.
//   - rule and faint both map to index 8 and are indistinguishable on a
//     16-colour terminal. That is the CSS's own mapping, kept rather than
//     "improved", because faint is decoration only - nothing readable
//     depends on telling the two apart.
//
// bg is in the table below for completeness but is never painted: the token
// is described as the terminal background, so the Console leaves it to the
// terminal. The design is a dark-background design; on a light terminal the
// user's own theme decides what index 15 and index 7 look like.
var paletteANSI = map[string]uint{
	"bg":     0,  // black - the terminal's own background, never painted
	"bg-sel": 8,  // bright black - selected row background
	"rule":   8,  // bright black - box-drawing rules
	"fg":     15, // bright white - primary text, values, key names
	"dim":    7,  // white - labels, column headers, key descriptions
	"faint":  8,  // bright black - non-text decoration only
	"acc":    14, // bright cyan - focus and selection, nothing else
	"amber":  11, // bright yellow - needs attention, unknown
	"red":    9,  // bright red - failed, missing, errors
	"green":  10, // bright green - succeeded, confirmations
}

// palette is the drawing styles for one frame. A Model carries one, so a
// test can render with plainPalette and diff readable text.
type palette struct {
	Fg    lipgloss.Style
	Bold  lipgloss.Style
	Dim   lipgloss.Style
	Faint lipgloss.Style
	Rule  lipgloss.Style
	Acc   lipgloss.Style
	Amber lipgloss.Style
	Red   lipgloss.Style
	Green lipgloss.Style

	// Sel is the selected row's fill: background bg-sel, no foreground of
	// its own, built on the same renderer as every other token so a padded
	// cell carries the same background a glyph does (see line.background).
	Sel lipgloss.Style
	// SelBG is that background on its own, for a caller that layers it
	// under a span's own style. nil in plainPalette.
	SelBG lipgloss.TerminalColor
}

// defaultPalette builds the palette on lipgloss's default renderer, which
// resolves the colour profile from the terminal the program is drawing on.
func defaultPalette() palette { return paletteFor(lipgloss.DefaultRenderer()) }

// paletteFor builds the palette on an explicit renderer. Production uses
// the default one; a test that asserts colour uses a renderer with a pinned
// profile, so the assertion does not depend on whether `go test` happened
// to inherit a terminal.
func paletteFor(r *lipgloss.Renderer) palette {
	fg := func(token string) lipgloss.Style {
		return r.NewStyle().Foreground(lipgloss.ANSIColor(paletteANSI[token]))
	}
	return palette{
		Fg:    fg("fg"),
		Bold:  fg("fg").Bold(true),
		Dim:   fg("dim"),
		Faint: fg("faint"),
		Rule:  fg("rule"),
		Acc:   fg("acc"),
		Amber: fg("amber"),
		Red:   fg("red"),
		Green: fg("green"),
		Sel:   r.NewStyle().Background(lipgloss.ANSIColor(paletteANSI["bg-sel"])),
		SelBG: lipgloss.ANSIColor(paletteANSI["bg-sel"]),
	}
}

// plainPalette draws no colour at all: every token is the zero style and
// selection carries no background. Golden fixtures are rendered with it so
// a diff shows the frame rather than escape sequences - which is also why
// no signal in this package may be carried by colour alone. If a state is
// only distinguishable in defaultPalette, it is invisible in a fixture, and
// that is the bug the rule exists to prevent.
func plainPalette() palette {
	s := lipgloss.NewStyle()
	return palette{Fg: s, Bold: s, Dim: s, Faint: s, Rule: s, Acc: s, Amber: s, Red: s, Green: s, Sel: s}
}
