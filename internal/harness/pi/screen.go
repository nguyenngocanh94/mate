package pi

import (
	"strings"
	"unicode/utf8"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// piScreen is pi's ScreenProfile, measured on pi 0.99.1 in a 93x39 Herdr
// 0.8.2 pane (docs/evidence/pi-contract-2026-10-01.md, "Capture cho
// ScreenProfile"; the captures are testdata/screens).
//
// pi's pane is read through ReadVisible. Through recent-unwrapped, which
// Claude and Codex are read through, pi's composer rules span the pane, so a
// rule, the composer row and the closing rule can come back joined into one
// 664-character line with no composer row left to find
// (testdata/screens/run6-draft.recent-unwrapped.txt, the same moment as
// run6-draft.visible.txt). It did not happen on every read, which is why
// the source is fixed rather than retried.
type piScreen struct{}

// ReadSource implements ScreenProfile.
func (piScreen) ReadSource() harness.ReadSource { return harness.ReadVisible }

// ClassifyStartup implements ScreenProfile. The trust dialog is checked
// before the composer, because it is drawn between two rules as well.
func (piScreen) ClassifyStartup(screen string) harness.StartupScreen {
	if piStartup().Classify(dialogView(screen)) == harness.StartupScreenTrustDialog {
		return harness.StartupScreenTrustDialog
	}
	lines := strings.Split(screen, "\n")
	c, ok := locateComposer(lines)
	if ok && !c.busy && c.content() == "" {
		return harness.StartupScreenReady
	}
	return harness.StartupScreenUnrecognized
}

// StartupAnswer implements ScreenProfile.
func (piScreen) StartupAnswer(dialog harness.StartupScreen) (harness.StartupDialogAnswer, error) {
	return piStartup().Answer(dialog)
}

// StartupTargetSelected implements ScreenProfile.
func (piScreen) StartupTargetSelected(dialog harness.StartupScreen, screen string) bool {
	return piStartup().TargetSelected(dialog, dialogView(screen))
}

// ReadyScreen implements ScreenProfile: the empty composer between its two
// rules, over pi's two footer lines.
func (piScreen) ReadyScreen() string {
	rule := strings.Repeat(piRule, piRuleMin)
	return rule + "\n\n" + rule + "\n" + "/work (main)\n" + "0.0%/200k (auto)\n"
}

// ComposerGlyph implements ScreenProfile. pi draws no prompt glyph: a draft
// starts at column 0, under the rule that opens the composer, so the rule
// is the mark a reading is quoted with.
func (piScreen) ComposerGlyph() string { return piRule }

// Composer implements ScreenProfile: every row of the composer, trimmed and
// joined by a space.
func (piScreen) Composer(lines []string) (string, bool) {
	c, ok := locateComposer(lines)
	if !ok {
		return "", false
	}
	return c.content(), true
}

// ComposerPlaceholders implements ScreenProfile. pi's empty composer holds
// nothing; its cursor is a reverse-video space, blank as text.
func (piScreen) ComposerPlaceholders() []string { return []string{} }

// ComposerRows implements ScreenProfile.
func (piScreen) ComposerRows(lines []string) ([]string, bool) {
	c, ok := locateComposer(lines)
	if !ok {
		return nil, false
	}
	return c.rows, true
}

// Busy implements ScreenProfile: the composer's opening rule carries pi's
// spinner while a turn runs (`── ⠴ Working ───`), with the composer itself
// still drawn, and empty, below it.
func (piScreen) Busy(lines []string) (string, bool) {
	c, ok := locateComposer(lines)
	if !ok || !c.busy {
		return "", false
	}
	return c.top, true
}

const (
	// piRule is the box-drawing rune pi rules its composer and its dialogs
	// with.
	piRule = "─"
	// piRuleMin is how many of them a line needs to count as a rule. The
	// measured rules span the pane; a transcript em-dash does not.
	piRuleMin = 10
	// piFooterLines is how many lines pi draws under its composer: the cwd
	// with its git branch, then usage and `(provider) model • thinking`.
	// Each is cut to the pane width with `...` rather than wrapped.
	piFooterLines = 2
	// piComposerScan bounds how far above the closing rule the opening one
	// is looked for.
	piComposerScan = harness.BusyTailLines
)

// composer is pi's composer as located on one screen.
type composer struct {
	// top is the trimmed opening line: a rule, or the busy line.
	top  string
	busy bool
	// rows are the lines between the two rules, as drawn.
	rows []string
}

func (c composer) content() string {
	var parts []string
	for _, r := range c.rows {
		if t := strings.TrimSpace(r); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// locateComposer finds the composer by its frame: the last thing on the
// screen is pi's two footer lines, right above them the composer's closing
// rule, and above that, within piComposerScan lines, its opening rule or the
// busy line that replaces it. The composer is the rows between.
//
// It is fail-closed. A screen that does not end that way - a dialog, whose
// own closing rule is the last line; a shell after pi exited; a menu drawn
// under the composer - has no composer, and a send refuses it as a screen
// mate cannot name. The update banner and the trust dialog are boxes of the
// same rules, but neither sits directly above the footer.
func locateComposer(lines []string) (composer, bool) {
	end := len(lines) - 1
	for end >= 0 && strings.TrimSpace(lines[end]) == "" {
		end--
	}
	bottom := end - piFooterLines
	if bottom < 1 {
		return composer{}, false
	}
	for _, l := range lines[bottom+1 : end+1] {
		if t := strings.TrimSpace(l); t == "" || isRule(t) {
			return composer{}, false
		}
	}
	if !isRule(strings.TrimSpace(lines[bottom])) {
		return composer{}, false
	}
	for i := bottom - 1; i >= 0 && bottom-i <= piComposerScan; i-- {
		t := strings.TrimSpace(lines[i])
		busy := isBusyRule(t)
		if !busy && !isRule(t) {
			continue
		}
		if i == bottom-1 {
			// A composer always draws at least one row.
			return composer{}, false
		}
		return composer{top: t, busy: busy, rows: append([]string(nil), lines[i+1:bottom]...)}, true
	}
	return composer{}, false
}

// isRule reports whether a trimmed line is a rule: piRuleMin or more `─`
// and nothing else.
func isRule(t string) bool {
	return utf8.RuneCountInString(t) >= piRuleMin && strings.Trim(t, piRule) == ""
}

// isBusyRule reports whether a trimmed line is the composer's opening rule
// with pi's in-flight spinner on it: `── `, one braille spinner cell, a
// space, the working message, and the rule again. The message is pi's
// default "Working" on every measured screen, but an extension may set its
// own, so only the frame is matched.
func isBusyRule(t string) bool {
	rest, ok := strings.CutPrefix(t, piRule+piRule+" ")
	if !ok {
		return false
	}
	r, size := utf8.DecodeRuneInString(rest)
	if r < 0x2801 || r > 0x28FF {
		return false
	}
	rest = rest[size:]
	return strings.HasPrefix(rest, " ") && strings.HasSuffix(rest, strings.Repeat(piRule, 3))
}

// The trust dialog, measured on pi 0.99.1 (screens/trust-c5-startup.txt and
// trust-c5-dialog-third-option.visible.txt). pi draws it only in a directory
// holding project resources it protects (`.pi/settings.json`,
// `.pi/extensions`), and a launch passes --no-approve, which skips it; it is
// recognised anyway, because a flag is a prediction and the pane is the
// measurement.
const (
	piTrustQuestion = "Trust project folder?"
	piTrustAccept   = "Trust"
	// piTrustParent is followed by the parent folder's path in parentheses,
	// on a row of its own (more than one when it wraps); dialogView drops
	// those rows so the options read as adjacent.
	piTrustParent        = "Trust parent folder"
	piTrustSession       = "Trust (this session only)"
	piTrustReject        = "Do not trust"
	piTrustRejectSession = "Do not trust (this session only)"
	piDialogFooter       = "↑↓ navigate  enter select  escape/ctrl+c cancel"
	piHighlightMarker    = "→"
)

// piStartup is pi's measured startup dialogs. Its composer is located by
// ClassifyStartup itself, so the profile's own never matches.
func piStartup() harness.StartupProfile {
	return harness.StartupProfile{
		Kind: KindPi,
		Dialogs: []harness.StartupDialog{{
			Screen:   harness.StartupScreenTrustDialog,
			Question: piTrustQuestion,
			Options: []harness.DialogOption{
				{Label: piTrustAccept},
				{Label: piTrustParent},
				{Label: piTrustSession},
				{Label: piTrustReject},
				{Label: piTrustRejectSession},
			},
			// "Trust (this session only)" is the one measured answer: two
			// presses of `down` from the default "Trust" put the highlight
			// on it, Enter reached the composer, and ~/.pi/agent/trust.json
			// was the same before and after. "Trust", the default, is saved
			// to the operator's trust.json; the options that do not trust
			// were never measured past the highlight.
			Target:      2,
			Footer:      piDialogFooter,
			Marker:      piHighlightMarker,
			SelectKeys:  []string{"down", "down"},
			TargetLabel: piTrustSession,
			// The question, the folder, then a paragraph that wraps to two
			// lines at 93 columns and to more in a narrower pane.
			QuestionWindow: 8,
		}},
		Composer: func([]string) bool { return false },
	}
}

// dialogView is the screen as the trust dialog's layout is parsed: without
// the rules pi frames the dialog in (the closing one comes after the
// footer, which the shared parser expects to end the screen), and without
// the rows of the parent folder's path under "Trust parent folder", up to
// the option that follows it. A screen where that option never follows
// loses everything after the path, and so is no dialog at all.
func dialogView(screen string) string {
	var out []string
	skipping := false
	for _, l := range strings.Split(screen, "\n") {
		t := strings.TrimSpace(l)
		if isRule(t) {
			continue
		}
		label := strings.TrimSpace(strings.TrimPrefix(t, piHighlightMarker))
		if skipping {
			if label != piTrustSession {
				continue
			}
			skipping = false
		}
		out = append(out, l)
		if label == piTrustParent {
			skipping = true
		}
	}
	return strings.Join(out, "\n")
}
