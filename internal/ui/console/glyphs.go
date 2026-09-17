package console

import "strings"

// glyphSet is the drawing alphabet. Two sets exist: box-drawing Unicode,
// and an ASCII fallback for a terminal whose locale is not UTF-8. There is
// no third tier - no Nerd Font glyph and no emoji appears in either set,
// because both would render as a replacement box on a plain terminal while
// still occupying the cells the layout counted on.
type glyphSet struct {
	// Name is "unicode" or "ascii"; golden fixtures are keyed by it.
	Name string

	HRule   string // horizontal rule
	VRule   string // the inspector's divider column
	TeeDown string // rule above the body, joining the divider
	TeeUp   string // rule below the body, joining the divider

	Selected  string // selected row, focused pane
	Unfocused string // selected row, unfocused pane

	Crumb    string // breadcrumb separator
	Ellipsis string // truncation marker
	Up       string // "n more above" indicator
	Down     string // "n more below" indicator
	UpDown   string // the key-line name for the movement keys
	Dot      string // inline separator between items

	// Transcript glyphs. The Console does not draw a transcript itself
	// (that surface belongs to the harness after attach), but the set
	// carries them so nothing has to invent an ASCII fallback later.
	Bullet string // turn marker
	Elbow  string // tool result continuation
	Spark  string // the running line
	Cycle  string // mode indicator

	CornerTL, CornerTR, CornerBL, CornerBR string // rounded box corners
}

var unicodeGlyphs = glyphSet{
	Name:      "unicode",
	HRule:     "─",
	VRule:     "│",
	TeeDown:   "┬",
	TeeUp:     "┴",
	Selected:  "▌",
	Unfocused: "▏",
	Crumb:     "›",
	Ellipsis:  "…",
	Up:        "↑",
	Down:      "↓",
	UpDown:    "↑↓",
	Dot:       "·",
	Bullet:    "⏺",
	Elbow:     "⎟",
	Spark:     "✻",
	Cycle:     "⏵⏵",
	CornerTL:  "╭",
	CornerTR:  "╮",
	CornerBL:  "╰",
	CornerBR:  "╯",
}

var asciiGlyphs = glyphSet{
	Name:      "ascii",
	HRule:     "-",
	VRule:     "|",
	TeeDown:   "+",
	TeeUp:     "+",
	Selected:  ">",
	Unfocused: ":",
	Crumb:     ">",
	Ellipsis:  "..",
	Up:        "^",
	Down:      "v",
	UpDown:    "j/k",
	Dot:       ".",
	Bullet:    "*",
	Elbow:     "\\_",
	Spark:     "*",
	Cycle:     ">>",
	CornerTL:  "+",
	CornerTR:  "+",
	CornerBL:  "+",
	CornerBR:  "+",
}

// glyphsFor picks the set from the environment. This is the only place the
// package reads the environment.
//
//	MATEV2_ASCII truthy  -> ASCII, whatever the locale says
//	MATEV2_ASCII falsy   -> Unicode, whatever the locale says (the escape
//	                      hatch for a terminal that draws box glyphs fine
//	                      but reports a C locale)
//	MATEV2_ASCII unset   -> the locale decides: UTF-8 gets Unicode, anything
//	                      else - including an unset locale, which is the C
//	                      locale - gets ASCII.
func glyphsFor(getenv func(string) string) glyphSet {
	switch strings.ToLower(strings.TrimSpace(getenv("MATEV2_ASCII"))) {
	case "1", "true", "yes", "on":
		return asciiGlyphs
	case "0", "false", "no", "off":
		return unicodeGlyphs
	}
	if localeIsUTF8(getenv) {
		return unicodeGlyphs
	}
	return asciiGlyphs
}

// localeIsUTF8 reads the POSIX locale precedence: LC_ALL overrides
// LC_CTYPE, which overrides LANG. The first one that is set decides; an
// unset chain is the C locale, which is not UTF-8.
func localeIsUTF8(getenv func(string) string) bool {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := strings.TrimSpace(getenv(key))
		if v == "" {
			continue
		}
		v = strings.ToLower(v)
		return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
	}
	return false
}
