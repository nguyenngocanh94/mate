package grok

import (
	"strings"
	"unicode/utf8"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// grokScreen is grok's ScreenProfile, measured on grok 1.0.46 in a 93x39
// pane read as visible text (testdata/screens). The same read through
// recent-unwrapped was not what the measurement used, so the source is
// visible.
//
// The composer is the box at the bottom of the pane, not every ❯: scrollback
// repeats the glyph on the user's own lines. A turn in flight leaves that
// box empty and draws a braille spinner with an ellipsis above it, plus the
// hint Ctrl+c:cancel. A startup spinner such as "⠴ MCP (5/10)" has no
// ellipsis, and the privacy banner above the box takes no keys.
type grokScreen struct{}

// Kind implements harness.ScreenProfile.
func (grokScreen) Kind() harness.Kind { return KindGrok }

// ReadSource implements ScreenProfile.
func (grokScreen) ReadSource() harness.ReadSource { return harness.ReadVisible }

// ClassifyStartup implements ScreenProfile. Ready is the empty composer box
// and no turn in flight. grok's measured launches did not draw a trust,
// update or hooks dialog, so anything else is unrecognised and no answer is
// invented for it.
func (grokScreen) ClassifyStartup(screen string) harness.StartupScreen {
	return grokStartup().Classify(screen)
}

// StartupAnswer implements ScreenProfile.
func (grokScreen) StartupAnswer(dialog harness.StartupScreen) (harness.StartupDialogAnswer, error) {
	return grokStartup().Answer(dialog)
}

// StartupTargetSelected implements ScreenProfile.
func (grokScreen) StartupTargetSelected(dialog harness.StartupScreen, screen string) bool {
	return grokStartup().TargetSelected(dialog, screen)
}

// ReadyScreen implements ScreenProfile: the smallest screen that classifies
// as an idle empty composer. A fake runtime shows it for a clean start.
func (grokScreen) ReadyScreen() string {
	return "╭─╮\n│ " + grokGlyph + " │\n╰─╯\n"
}

// ComposerGlyph implements ScreenProfile.
func (grokScreen) ComposerGlyph() string { return grokGlyph }

// Composer implements ScreenProfile.
func (grokScreen) Composer(lines []string) (string, bool) {
	c, ok := locateComposer(lines)
	if !ok {
		return "", false
	}
	return c.content(), true
}

// ComposerPlaceholders implements ScreenProfile. The empty box holds the
// glyph and nothing else.
func (grokScreen) ComposerPlaceholders() []string { return []string{} }

// ComposerRows implements ScreenProfile.
func (grokScreen) ComposerRows(lines []string) ([]string, bool) {
	c, ok := locateComposer(lines)
	if !ok {
		return nil, false
	}
	return c.rows, true
}

// Busy implements ScreenProfile. The spinner line is the evidence when it
// is on screen; the cancel hint alone is enough on a frame that drops the
// spinner, and it is not the quit toast "Ctrl+c:press again to quit".
func (grokScreen) Busy(lines []string) (string, bool) {
	return busyEvidence(lines)
}

const (
	// grokGlyph is the one-cell prompt grok draws at the start of the
	// composer box (U+276F).
	grokGlyph = "❯"
	// grokEllipsis is the one grok puts in a status line while a turn runs
	// (U+2026). A spinner without it is startup, not a turn.
	grokEllipsis = "…"
	// grokCancelHint is the footer grok draws only while a turn can be
	// cancelled. The quit toast says "press again to quit" instead.
	grokCancelHint = "Ctrl+c:cancel"
	// grokBoxScan bounds how far above the box's bottom its top is looked for.
	grokBoxScan = harness.BusyTailLines
)

// composer is the bottom box, as located on one screen.
type composer struct {
	// rows are the text rows inside the box, glyph stripped and trimmed.
	rows []string
}

func (c composer) content() string {
	var parts []string
	for _, r := range c.rows {
		if r != "" {
			parts = append(parts, r)
		}
	}
	return strings.Join(parts, " ")
}

// locateComposer finds the last box on the screen: the last line that closes
// with ╰ and ─, the ╭ above it within grokBoxScan lines, and the │ rows
// between them. It is fail-closed. A shell after grok exited, a menu, or a
// capture that joined the box into one line has no composer.
func locateComposer(lines []string) (composer, bool) {
	bottom := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "╰") && strings.Contains(lines[i], "─") {
			bottom = i
			break
		}
	}
	if bottom < 0 {
		return composer{}, false
	}
	top := -1
	for i := bottom - 1; i >= 0 && bottom-i <= grokBoxScan; i-- {
		if strings.Contains(lines[i], "╭") {
			top = i
			break
		}
		if !strings.Contains(lines[i], "│") {
			return composer{}, false
		}
	}
	if top < 0 || top >= bottom-1 {
		return composer{}, false
	}
	var rows []string
	for i, line := range lines[top+1 : bottom] {
		inner, ok := boxInner(line)
		if !ok {
			return composer{}, false
		}
		text := strings.TrimSpace(inner)
		if i == 0 {
			if rest, ok := strings.CutPrefix(text, grokGlyph); ok {
				text = strings.TrimSpace(rest)
			}
		}
		rows = append(rows, text)
	}
	return composer{rows: rows}, true
}

// boxInner is the text between a row's first and last │.
func boxInner(line string) (string, bool) {
	open := strings.Index(line, "│")
	close := strings.LastIndex(line, "│")
	if open < 0 || close <= open {
		return "", false
	}
	return line[open+len("│") : close], true
}

// busyEvidence is the trimmed line that shows a turn in flight: the last
// braille spinner that is followed by a space and an ellipsis, or else the
// line that carries grokCancelHint.
func busyEvidence(lines []string) (string, bool) {
	spinner, cancel := "", ""
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if isTurnSpinner(t) {
			spinner = t
		}
		if strings.Contains(t, grokCancelHint) {
			cancel = t
		}
	}
	if spinner != "" {
		return spinner, true
	}
	if cancel != "" {
		return cancel, true
	}
	return "", false
}

// isTurnSpinner reports whether a trimmed line is grok's in-flight status:
// one braille cell (U+2800–U+28FF), a space, and an ellipsis somewhere after.
func isTurnSpinner(t string) bool {
	r, size := utf8.DecodeRuneInString(t)
	if size == 0 || r < 0x2800 || r > 0x28FF {
		return false
	}
	rest := t[size:]
	return strings.HasPrefix(rest, " ") && strings.Contains(rest, grokEllipsis)
}

// grokStartup is grok's startup profile. No dialog was measured, so Answer
// refuses every one. The composer predicate is the empty box with no turn
// in flight, which is what Classify calls ready.
func grokStartup() harness.StartupProfile {
	return harness.StartupProfile{
		Kind: KindGrok,
		Composer: func(lines []string) bool {
			if _, busy := busyEvidence(lines); busy {
				return false
			}
			c, ok := locateComposer(lines)
			return ok && c.content() == ""
		},
	}
}
