package console

import "strings"

// glyphSet is the drawing alphabet. Every glyph but the kind marks is one
// cell; Mate and Crew are exactly two, whatever alphabet draws them, so a
// name always starts at column 5 (design I, "Row anatomy").
type glyphSet struct {
	Name string

	HRule     string // pane rules
	Selected  string // selected row, focused pane
	Unfocused string // selected row, unfocused pane
	Collapsed string // a closed group (Completed)
	Expanded  string // an open group
	Cursor    string // the text cursor in a sheet's input field

	Crumb    string // breadcrumb separator
	Ellipsis string // truncation marker
	Up       string // "n more above"
	Down     string // "n more below"
	UpDown   string // the key-line name for the movement keys
	Dot      string // inline separator between items
	Arrow    string // the status line's "next pane" arrow
	Bang     string // the attention mark

	// Mate and Crew are the kind marks, two cells each (kinds.go).
	Mate, Crew string
	// Icons is which of a harness's marks this alphabet draws (icons.go):
	// each harness names its own (query.HarnessIcon), one cell each.
	Icons iconSet
}

var unicodeGlyphs = glyphSet{
	Name:      "unicode",
	HRule:     "─",
	Selected:  "▌",
	Unfocused: "▏",
	Collapsed: "▸",
	Expanded:  "▾",
	Cursor:    "█",
	Crumb:     "›",
	Ellipsis:  "…",
	Up:        "↑",
	Down:      "↓",
	UpDown:    "↑↓",
	Dot:       "·",
	Arrow:     "→",
	Bang:      "!",
	Mate:      "👨‍💻",
	Crew:      "🤖",
	Icons:     iconsUnicode,
}

// asciiGlyphs is design I's fallback table, one cell each ("9 ASCII
// fallback"), with the kind marks padded to their two cells.
var asciiGlyphs = glyphSet{
	Name:      "ascii",
	HRule:     "-",
	Selected:  ">",
	Unfocused: ":",
	Collapsed: "+",
	Expanded:  "-",
	Cursor:    "_",
	Crumb:     ">",
	Ellipsis:  "~",
	Up:        "^",
	Down:      "v",
	UpDown:    "^v",
	Dot:       ".",
	Arrow:     ">",
	Bang:      "!",
	Mate:      "@ ",
	Crew:      "o ",
	Icons:     iconsASCII,
}

// glyphsFor picks the alphabet from the environment - the only place this
// package reads it. MATE_ASCII forces either alphabet; otherwise a UTF-8
// locale gets Unicode.
func glyphsFor(getenv func(string) string) glyphSet {
	switch strings.ToLower(strings.TrimSpace(getenv("MATE_ASCII"))) {
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

// localeIsUTF8 reads the POSIX locale precedence: the first of LC_ALL,
// LC_CTYPE and LANG that is set decides.
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
