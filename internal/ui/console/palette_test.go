package console

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestPaletteUsesTheDocumentedANSIIndices is the contract half of
// design/mate-tui.css: the oklch values there are previews of one theme,
// but the index mapping is what the Console promises. The literals below
// are written out rather than read from paletteANSI, so this test can
// actually disagree with the production table.
func TestPaletteUsesTheDocumentedANSIIndices(t *testing.T) {
	p := defaultPalette()
	cases := []struct {
		token string
		style lipgloss.Style
		index uint
	}{
		{"fg", p.Fg, 15},
		{"fg (bold)", p.Bold, 15},
		{"dim", p.Dim, 7},
		{"faint", p.Faint, 8},
		{"rule", p.Rule, 8},
		{"acc", p.Acc, 14},
		{"amber", p.Amber, 11},
		{"red", p.Red, 9},
		{"green", p.Green, 10},
	}
	for _, tc := range cases {
		if got := tc.style.GetForeground(); got != lipgloss.ANSIColor(tc.index) {
			t.Errorf("%s foreground = %v, want ANSI %d", tc.token, got, tc.index)
		}
	}
	if got := p.SelBG; got != lipgloss.ANSIColor(8) {
		t.Errorf("bg-sel = %v, want ANSI 8", got)
	}
	if !p.Bold.GetBold() {
		t.Errorf("the bold token is not bold")
	}
}

// TestNoColourExistsOnlyAtTruecolor: every token is an ANSI index, so a
// 16-colour terminal draws the same hierarchy a truecolor one does. If a
// token ever gains a truecolor-only value, the layout stops surviving a
// plain terminal and this test says so.
func TestNoColourExistsOnlyAtTruecolor(t *testing.T) {
	p := defaultPalette()
	for token, style := range map[string]lipgloss.Style{
		"fg": p.Fg, "dim": p.Dim, "faint": p.Faint, "rule": p.Rule,
		"acc": p.Acc, "amber": p.Amber, "red": p.Red, "green": p.Green,
	} {
		if _, ok := style.GetForeground().(lipgloss.ANSIColor); !ok {
			t.Errorf("%s foreground is %T, want lipgloss.ANSIColor", token, style.GetForeground())
		}
	}
	if _, ok := p.SelBG.(lipgloss.ANSIColor); !ok {
		t.Errorf("bg-sel is %T, want lipgloss.ANSIColor", p.SelBG)
	}
}

// TestPaletteKeepsTheLightnessRamp: hierarchy is carried by lightness, so
// the three text tiers must be three different indices - a palette where
// fg and dim collapse to the same colour has no hierarchy left, whatever
// theme the terminal supplies.
func TestPaletteKeepsTheLightnessRamp(t *testing.T) {
	p := defaultPalette()
	fg, dim, faint := p.Fg.GetForeground(), p.Dim.GetForeground(), p.Faint.GetForeground()
	if fg == dim || dim == faint || fg == faint {
		t.Fatalf("the text ramp collapsed: fg=%v dim=%v faint=%v", fg, dim, faint)
	}
	// One accent, and it is not one of the status hues: the accent means
	// focus and selection, never a state.
	acc := p.Acc.GetForeground()
	for token, style := range map[string]lipgloss.Style{"amber": p.Amber, "red": p.Red, "green": p.Green} {
		if acc == style.GetForeground() {
			t.Fatalf("the accent shares a colour with %s; focus and status would be indistinguishable", token)
		}
	}
}

// TestPlainPaletteEmitsNoEscapes: golden fixtures are rendered with it, so
// it must produce literal text - and nothing in the package may depend on
// colour to be legible.
func TestPlainPaletteEmitsNoEscapes(t *testing.T) {
	p := plainPalette()
	for token, style := range map[string]lipgloss.Style{
		"fg": p.Fg, "bold": p.Bold, "dim": p.Dim, "faint": p.Faint, "rule": p.Rule,
		"acc": p.Acc, "amber": p.Amber, "red": p.Red, "green": p.Green,
	} {
		if got := style.Render("x"); got != "x" {
			t.Errorf("plain %s rendered %q, want %q", token, got, "x")
		}
	}
	if p.SelBG != nil {
		t.Errorf("plain SelBG = %v, want nil so a selected row carries no background", p.SelBG)
	}
	if got := selectRow(newLine(), true, p).add("row", p.Fg).render(6); got != "row   " {
		t.Errorf("plain selected row rendered %q, want %q", got, "row   ")
	}
}
