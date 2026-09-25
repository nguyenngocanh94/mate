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
		{name: "no locale at all", env: map[string]string{}, want: "ascii"},
		{name: "MATE_ASCII forces ascii", env: map[string]string{"MATE_ASCII": "1", "LANG": "en_US.UTF-8"}, want: "ascii"},
		{name: "MATE_ASCII true", env: map[string]string{"MATE_ASCII": "TRUE", "LANG": "en_US.UTF-8"}, want: "ascii"},
		{name: "MATE_ASCII=0 forces unicode", env: map[string]string{"MATE_ASCII": "0", "LANG": "C"}, want: "unicode"},
		{name: "MATE_ASCII empty defers to locale", env: map[string]string{"MATE_ASCII": "", "LANG": "en_US.UTF-8"}, want: "unicode"},
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

// TestHarnessIconsAreTheCallersChoice: the harness is drawn as its own
// mark. Nerd Fonts 3.5 has cod-claude U+EC82 and cod-openai U+EC81, but a
// private-use codepoint no font has draws as a box, so the Console uses
// them only when cmd/mate found a font that has them (WithHarnessIcons);
// the environment alone never switches them on. ASCII keeps its own marks.
func TestHarnessIconsAreTheCallersChoice(t *testing.T) {
	for _, env := range []map[string]string{
		{"LANG": "en_US.UTF-8"},
		{"LANG": "en_US.UTF-8", "TERM_PROGRAM": "ghostty"},
		{"LANG": "en_US.UTF-8", "WEZTERM_PANE": "3"},
		{"LANG": "en_US.UTF-8", "MATE_ICONS": "nerd"},
	} {
		g := glyphsFor(func(k string) string { return env[k] })
		if g.Claude != "✻" || g.Codex != "⌬" {
			t.Errorf("glyphsFor(%v) icons = %q %q, want the Unicode stand-ins by default", env, g.Claude, g.Codex)
		}
	}
	cases := []struct {
		name          string
		g             glyphSet
		nerd          bool
		claude, codex string
	}{
		{"unicode, no nerd font", unicodeGlyphs, false, "✻", "⌬"},
		{"unicode, nerd font found", unicodeGlyphs, true, "\uec82", "\uec81"},
		{"ascii ignores a nerd font", asciiGlyphs, true, "*", "#"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{g: tc.g}.WithHarnessIcons(tc.nerd)
			if got := harnessIcon("claude", m.g); got != tc.claude {
				t.Fatalf("claude = %q, want %q", got, tc.claude)
			}
			if got := harnessIcon("codex", m.g); got != tc.codex {
				t.Fatalf("codex = %q, want %q", got, tc.codex)
			}
		})
	}
	// Switching back is honoured too.
	m := Model{g: unicodeGlyphs}.WithHarnessIcons(true).WithHarnessIcons(false)
	if m.g.Claude != "✻" {
		t.Fatalf("WithHarnessIcons(false) after true = %q, want ✻", m.g.Claude)
	}
}

// An unknown harness is never drawn as somebody else's logo.
func TestAnUnknownHarnessIsItsOwnWord(t *testing.T) {
	if got := harnessIcon("gemini", unicodeGlyphs); got != "gemini" {
		t.Fatalf("harnessIcon(gemini) = %q, want the word", got)
	}
	if got := harnessIcon("", unicodeGlyphs); got != "?" {
		t.Fatalf("harnessIcon(\"\") = %q, want ?", got)
	}
}

// TestAsciiSetIsTheDesignsFallbackTable, glyph for glyph (design I, "9").
func TestAsciiSetIsTheDesignsFallbackTable(t *testing.T) {
	want := map[string][2]string{
		"hrule":     {unicodeGlyphs.HRule, "-"},
		"selected":  {unicodeGlyphs.Selected, ">"},
		"unfocused": {unicodeGlyphs.Unfocused, ":"},
		"collapsed": {unicodeGlyphs.Collapsed, "+"},
		"expanded":  {unicodeGlyphs.Expanded, "-"},
		"ellipsis":  {unicodeGlyphs.Ellipsis, "~"},
		"dot":       {unicodeGlyphs.Dot, "."},
		"crumb":     {unicodeGlyphs.Crumb, ">"},
		"arrow":     {unicodeGlyphs.Arrow, ">"},
		"updown":    {unicodeGlyphs.UpDown, "^v"},
		"cursor":    {unicodeGlyphs.Cursor, "_"},
	}
	got := map[string]string{
		"hrule": asciiGlyphs.HRule, "selected": asciiGlyphs.Selected, "unfocused": asciiGlyphs.Unfocused,
		"collapsed": asciiGlyphs.Collapsed, "expanded": asciiGlyphs.Expanded, "ellipsis": asciiGlyphs.Ellipsis,
		"dot": asciiGlyphs.Dot, "crumb": asciiGlyphs.Crumb, "arrow": asciiGlyphs.Arrow,
		"updown": asciiGlyphs.UpDown, "cursor": asciiGlyphs.Cursor,
	}
	for k, w := range want {
		if got[k] != w[1] {
			t.Errorf("ascii %s (for %q) = %q, want %q", k, w[0], got[k], w[1])
		}
	}
}

func bxAllGlyphs(g glyphSet) map[string]string {
	return map[string]string{
		"HRule": g.HRule, "Selected": g.Selected, "Unfocused": g.Unfocused,
		"Collapsed": g.Collapsed, "Expanded": g.Expanded, "Cursor": g.Cursor,
		"Crumb": g.Crumb, "Ellipsis": g.Ellipsis, "Up": g.Up, "Down": g.Down,
		"UpDown": g.UpDown, "Dot": g.Dot, "Arrow": g.Arrow, "Bang": g.Bang,
		"Mate": g.Mate, "Crew": g.Crew, "Claude": g.Claude, "Codex": g.Codex,
	}
}

// TestAsciiSetIsActuallyASCII: a stray box-drawing glyph in the fallback
// would be the only broken cell on a terminal that cannot draw it.
func TestAsciiSetIsActuallyASCII(t *testing.T) {
	for name, glyph := range bxAllGlyphs(asciiGlyphs) {
		for _, r := range glyph {
			if r > 127 {
				t.Errorf("ascii %s = %q contains a non-ASCII rune %q", name, glyph, r)
			}
		}
	}
}

// TestSingleCellGlyphsAreSingleCell: the grid assumes every glyph but the
// kind marks and UpDown is one cell, and the kind marks exactly two, in
// every alphabet - otherwise a name no longer starts at col 5.
func TestSingleCellGlyphsAreSingleCell(t *testing.T) {
	sets := []glyphSet{
		unicodeGlyphs, asciiGlyphs,
		unicodeGlyphs.withIcons(iconsNerd),
		unicodeGlyphs.withKinds(KindSymbol),
		unicodeGlyphs.withKinds(KindASCII),
	}
	for _, g := range sets {
		for name, glyph := range bxAllGlyphs(g) {
			want := 1
			switch name {
			case "Mate", "Crew", "UpDown":
				want = 2
			}
			if n := cells(glyph); n != want {
				t.Errorf("%s %s = %q is %d cells, want %d", g.Name, name, glyph, n, want)
			}
		}
	}
}

// The kind marks fall back to ◆ ◇ then @ o (design I, "Emoji width rule");
// ascii keeps its own whatever the probe said.
func TestKindGlyphsFallBackInOrder(t *testing.T) {
	cases := []struct {
		k          KindGlyphs
		mate, crew string
	}{
		{KindEmoji, "👨‍💻", "🤖"},
		{KindSymbol, "◆ ", "◇ "},
		{KindASCII, "@ ", "o "},
	}
	for _, tc := range cases {
		g := unicodeGlyphs.withKinds(tc.k)
		if g.Mate != tc.mate || g.Crew != tc.crew {
			t.Errorf("withKinds(%d) = %q, %q; want %q, %q", tc.k, g.Mate, g.Crew, tc.mate, tc.crew)
		}
	}
	m := Model{g: asciiGlyphs}.WithKindGlyphs(KindEmoji)
	if m.g.Mate != "@ " {
		t.Fatalf("ascii Model took emoji kinds: %q", m.g.Mate)
	}
	m = Model{g: unicodeGlyphs}.WithKindGlyphs(KindSymbol)
	if m.g.Mate != "◆ " {
		t.Fatalf("WithKindGlyphs(KindSymbol) = %q", m.g.Mate)
	}
}

// TestOnlyTheMarksNeedAnEmojiOrANerdFont: the rules, markers and separators
// render on any terminal; only the kind marks (emoji, with a probed
// fallback) and the Nerd harness marks (opt-in by host) do not.
func TestOnlyTheMarksNeedAnEmojiOrANerdFont(t *testing.T) {
	for _, g := range []glyphSet{unicodeGlyphs, asciiGlyphs} {
		var b strings.Builder
		for name, glyph := range bxAllGlyphs(g) {
			switch name {
			case "Mate", "Crew", "Claude", "Codex":
				continue
			}
			b.WriteString(glyph)
		}
		for _, r := range b.String() {
			switch {
			case r >= 0xE000 && r <= 0xF8FF:
				t.Errorf("%s glyph set uses a private-use rune %U", g.Name, r)
			case r >= 0x1F300 && r <= 0x1FAFF:
				t.Errorf("%s glyph set uses an emoji %U", g.Name, r)
			case r == 0xFE0F:
				t.Errorf("%s glyph set requests emoji presentation %U", g.Name, r)
			}
		}
	}
}
