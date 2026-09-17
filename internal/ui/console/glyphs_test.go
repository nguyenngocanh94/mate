package console

import (
	"strings"
	"testing"
)

func TestGlyphsForPicksTheSetFromMateAsciiAndTheLocale(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "utf-8 locale", env: map[string]string{"LANG": "en_US.UTF-8"}, want: "unicode"},
		{name: "utf8 spelling", env: map[string]string{"LC_ALL": "en_US.utf8"}, want: "unicode"},
		{name: "lc_ctype wins over lang", env: map[string]string{"LC_CTYPE": "C", "LANG": "en_US.UTF-8"}, want: "ascii"},
		{name: "lc_all wins over lc_ctype", env: map[string]string{"LC_ALL": "en_US.UTF-8", "LC_CTYPE": "C"}, want: "unicode"},
		{name: "c locale", env: map[string]string{"LANG": "C"}, want: "ascii"},
		{name: "posix locale", env: map[string]string{"LANG": "POSIX"}, want: "ascii"},
		{name: "no locale at all", env: map[string]string{}, want: "ascii"},
		{name: "MATEV2_ASCII forces ascii", env: map[string]string{"MATEV2_ASCII": "1", "LANG": "en_US.UTF-8"}, want: "ascii"},
		{name: "MATEV2_ASCII true", env: map[string]string{"MATEV2_ASCII": "TRUE", "LANG": "en_US.UTF-8"}, want: "ascii"},
		{name: "MATEV2_ASCII=0 forces unicode", env: map[string]string{"MATEV2_ASCII": "0", "LANG": "C"}, want: "unicode"},
		{name: "MATEV2_ASCII empty defers to locale", env: map[string]string{"MATEV2_ASCII": "", "LANG": "en_US.UTF-8"}, want: "unicode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := glyphsFor(func(k string) string { return tc.env[k] })
			if got.Name != tc.want {
				t.Fatalf("glyphsFor(%v) = %s, want %s", tc.env, got.Name, tc.want)
			}
		})
	}
}

// TestAsciiSetIsTheMappingTheDesignNames, glyph for glyph.
func TestAsciiSetIsTheMappingTheDesignNames(t *testing.T) {
	want := map[string]string{
		"selected":  ">",
		"unfocused": ":",
		"hrule":     "-",
		"vrule":     "|",
		"tee down":  "+",
		"tee up":    "+",
		"ellipsis":  "..",
		"up/down":   "j/k",
		"bullet":    "*",
		"elbow":     "\\_",
		"spark":     "*",
		"cycle":     ">>",
		"corner tl": "+",
		"corner br": "+",
	}
	got := map[string]string{
		"selected":  asciiGlyphs.Selected,
		"unfocused": asciiGlyphs.Unfocused,
		"hrule":     asciiGlyphs.HRule,
		"vrule":     asciiGlyphs.VRule,
		"tee down":  asciiGlyphs.TeeDown,
		"tee up":    asciiGlyphs.TeeUp,
		"ellipsis":  asciiGlyphs.Ellipsis,
		"up/down":   asciiGlyphs.UpDown,
		"bullet":    asciiGlyphs.Bullet,
		"elbow":     asciiGlyphs.Elbow,
		"spark":     asciiGlyphs.Spark,
		"cycle":     asciiGlyphs.Cycle,
		"corner tl": asciiGlyphs.CornerTL,
		"corner br": asciiGlyphs.CornerBR,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("ascii %s = %q, want %q", k, got[k], w)
		}
	}
}

// TestAsciiSetIsActuallyASCII: the point of the fallback is a terminal that
// cannot render anything else. A stray box-drawing glyph would be worse
// than the Unicode set, because it would be the only broken cell.
func TestAsciiSetIsActuallyASCII(t *testing.T) {
	for name, g := range map[string]string{
		"HRule": asciiGlyphs.HRule, "VRule": asciiGlyphs.VRule,
		"TeeDown": asciiGlyphs.TeeDown, "TeeUp": asciiGlyphs.TeeUp,
		"Selected": asciiGlyphs.Selected, "Unfocused": asciiGlyphs.Unfocused,
		"Crumb": asciiGlyphs.Crumb, "Ellipsis": asciiGlyphs.Ellipsis,
		"Up": asciiGlyphs.Up, "Down": asciiGlyphs.Down, "UpDown": asciiGlyphs.UpDown,
		"Dot": asciiGlyphs.Dot, "Bullet": asciiGlyphs.Bullet, "Elbow": asciiGlyphs.Elbow,
		"Spark": asciiGlyphs.Spark, "Cycle": asciiGlyphs.Cycle,
		"CornerTL": asciiGlyphs.CornerTL, "CornerTR": asciiGlyphs.CornerTR,
		"CornerBL": asciiGlyphs.CornerBL, "CornerBR": asciiGlyphs.CornerBR,
	} {
		for _, r := range g {
			if r > 127 {
				t.Errorf("ascii %s = %q contains a non-ASCII rune %q", name, g, r)
			}
		}
	}
}

// TestSingleCellGlyphsAreSingleCell: the frame's arithmetic assumes the
// rule, divider, tee and markers are one cell each, in both sets. A
// two-cell glyph would shift the divider column by one per line.
func TestSingleCellGlyphsAreSingleCell(t *testing.T) {
	for _, g := range []glyphSet{unicodeGlyphs, asciiGlyphs} {
		for name, glyph := range map[string]string{
			"HRule": g.HRule, "VRule": g.VRule, "TeeDown": g.TeeDown, "TeeUp": g.TeeUp,
			"Selected": g.Selected, "Unfocused": g.Unfocused, "Crumb": g.Crumb,
		} {
			if n := cells(glyph); n != 1 {
				t.Errorf("%s %s = %q is %d cells, want 1", g.Name, name, glyph, n)
			}
		}
	}
}

// TestNoGlyphNeedsANerdFontOrIsAnEmoji: both would render as a replacement
// box on a plain terminal while still taking the cells the layout counted.
func TestNoGlyphNeedsANerdFontOrIsAnEmoji(t *testing.T) {
	for _, g := range []glyphSet{unicodeGlyphs, asciiGlyphs} {
		all := strings.Join([]string{
			g.HRule, g.VRule, g.TeeDown, g.TeeUp, g.Selected, g.Unfocused, g.Crumb,
			g.Ellipsis, g.Up, g.Down, g.UpDown, g.Dot, g.Bullet, g.Elbow, g.Spark,
			g.Cycle, g.CornerTL, g.CornerTR, g.CornerBL, g.CornerBR,
		}, "")
		for _, r := range all {
			switch {
			case r >= 0xE000 && r <= 0xF8FF: // Private Use Area: Nerd Fonts live here
				t.Errorf("%s glyph set uses a private-use rune %U", g.Name, r)
			case r >= 0x1F300 && r <= 0x1FAFF: // pictographs and emoji
				t.Errorf("%s glyph set uses an emoji %U", g.Name, r)
			case r == 0xFE0F: // variation selector-16, the emoji presentation request
				t.Errorf("%s glyph set requests emoji presentation %U", g.Name, r)
			}
		}
	}
}
