package console

import (
	"strings"
	"testing"
)

// TestTokensUseTheDocumentedANSIIndices is design I's token table: dim 7,
// faint 8, acc 14 bold, amber 11, red 9, green 10, sel-bg background 8.
func TestTokensUseTheDocumentedANSIIndices(t *testing.T) {
	p := ansiPalette()
	for tk, want := range map[tok]string{
		tDim: "37", tFaint: "90", tAcc: "96", tAmber: "93", tRed: "91", tGreen: "92",
	} {
		got := p.paint("x", tk, false)
		if !strings.Contains(got, want) {
			t.Errorf("token %d renders %q, want SGR %s", tk, got, want)
		}
	}
	if got := p.paint("x", tAcc, false); !strings.Contains(got, "1;") && !strings.Contains(got, ";1") && !strings.Contains(got, "[1m") {
		t.Errorf("acc renders %q, want bold", got)
	}
	if got := p.paint("x", tDim, true); !strings.Contains(got, "100") {
		t.Errorf("a selected segment renders %q, want background 8 (SGR 100)", got)
	}
}

// TestFgIsTheTerminalsOwnForeground: many light themes map 15 close to
// white, so fg is drawn as the default foreground (SGR 39), never an index.
func TestFgIsTheTerminalsOwnForeground(t *testing.T) {
	p := ansiPalette()
	for _, tk := range []tok{tFg, tBold} {
		got := p.paint("x", tk, false)
		for _, sgr := range []string{"97", "37", "38;5"} {
			if strings.Contains(got, "["+sgr) || strings.Contains(got, ";"+sgr) {
				t.Errorf("token %d renders %q: fg must carry no foreground colour", tk, got)
			}
		}
	}
}

// TestLiftRuleStepsTintsUpOnTheSelection: on a sel-bg row faint becomes
// dim and dim becomes fg, because index 8 on index 8 would vanish.
func TestLiftRuleStepsTintsUpOnTheSelection(t *testing.T) {
	for in, want := range map[tok]tok{tFaint: tDim, tDim: tFg, tFg: tFg, tAcc: tAcc, tAmber: tAmber, tRed: tRed} {
		if got := lift(in); got != want {
			t.Errorf("lift(%d) = %d, want %d", in, got, want)
		}
	}
	p := ansiPalette()
	if got := p.paint("x", tFaint, true); strings.Contains(got, "90") {
		t.Errorf("faint on the selection renders %q, still index 8", got)
	}
	if got := p.paint("x", tFaint, true); !strings.Contains(got, "37") {
		t.Errorf("faint on the selection renders %q, want it lifted to dim (7)", got)
	}
	if got := p.paint("x", tDim, true); strings.Contains(got, "[37") || strings.Contains(got, ";37") {
		t.Errorf("dim on the selection renders %q, want it lifted to fg", got)
	}
}

func TestPlainPaletteEmitsNoEscapes(t *testing.T) {
	p := plainPalette()
	for tk := tFg; tk <= tBold; tk++ {
		for _, sel := range []bool{false, true} {
			if got := p.paint("x", tk, sel); got != "x" {
				t.Errorf("plain token %d sel=%v renders %q", tk, sel, got)
			}
		}
	}
	frame := newFixture(t, designTree(), 40, 36, unicodeGlyphs).View()
	if strings.Contains(frame, "\x1b") {
		t.Fatal("a plain-palette frame contains an escape")
	}
}
