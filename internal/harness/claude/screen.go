package claude

import (
	"regexp"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// claudeScreen is Claude Code's ScreenProfile.
type claudeScreen struct{}

// Kind implements harness.ScreenProfile.
func (claudeScreen) Kind() harness.Kind { return KindClaude }

// ReadSource implements ScreenProfile.
func (claudeScreen) ReadSource() harness.ReadSource { return harness.ReadRecentUnwrapped }

// ClassifyStartup implements ScreenProfile.
func (claudeScreen) ClassifyStartup(screen string) harness.StartupScreen {
	return claudeStartup().Classify(screen)
}

// StartupAnswer implements ScreenProfile.
func (claudeScreen) StartupAnswer(dialog harness.StartupScreen) (harness.StartupDialogAnswer, error) {
	return claudeStartup().Answer(dialog)
}

// StartupTargetSelected implements ScreenProfile.
func (claudeScreen) StartupTargetSelected(dialog harness.StartupScreen, screen string) bool {
	return claudeStartup().TargetSelected(dialog, screen)
}

// ReadyScreen implements ScreenProfile: the marker alone between two rules,
// each long enough (claudeRuleMin) for the composer to be found as well.
func (claudeScreen) ReadyScreen() string {
	return "──────────\n" + ClaudeComposerMarker + "\n──────────\n"
}

// ComposerGlyph implements ScreenProfile.
func (claudeScreen) ComposerGlyph() string { return claudeComposerGlyph }

// Composer implements ScreenProfile.
func (claudeScreen) Composer(lines []string) (string, bool) { return locateClaudeComposer(lines) }

// ComposerPlaceholders implements ScreenProfile. Claude's empty composer
// holds nothing, or a faint suggestion the caller tells by its attributes.
func (claudeScreen) ComposerPlaceholders() []string { return []string{} }

// ComposerRows implements ScreenProfile.
func (claudeScreen) ComposerRows(lines []string) ([]string, bool) { return claudeComposerRows(lines) }

// Busy implements ScreenProfile.
func (claudeScreen) Busy(lines []string) (string, bool) { return claudeBusy(lines) }

const (
	// claudeComposerGlyph is the glyph Claude draws at its composer. It is
	// also its highlight glyph, which is why the composer is located by the
	// box it sits in rather than by the glyph alone. Measured: the glyph is
	// separated from the composer's contents by U+00A0, not by a space, so
	// the contents are trimmed with strings.TrimSpace (which treats U+00A0
	// as space) rather than compared byte-wise against "❯ ".
	claudeComposerGlyph = ClaudeComposerMarker
	// claudeRuleRune is the box-drawing rune Claude rules its composer with.
	claudeRuleRune = '─'
	// claudeRuleMin is how many of them a line needs to count as a rule
	// (the measured rules span the pane; a transcript em-dash does not).
	claudeRuleMin = 10
	// claudeQueuedPlaceholder is drawn in the composer while a turn is in
	// flight and a message is already queued behind it. Measured with no
	// spinner anywhere on screen, so it is busy evidence in its own right.
	claudeQueuedPlaceholder = "Press up to edit queued messages"
)

// claudeSpinnerRunes are the glyphs Claude cycles at the head of its
// in-flight line. Measured: "✶ Pollinating…", "✻ Pollinating… (1s · ↓ 25
// tokens · thinking with medium effort)", "· Pollinating…". The gerund is
// randomised per turn, so the stable part is the glyph plus the ellipsis -
// and the finished line ("✻ Sautéed for 23s · done 7:17 PM") carries no
// ellipsis, which is exactly what separates the two.
var claudeSpinnerRunes = []rune{'✻', '✽', '✶', '✳', '✢', '·', '*', '•'}

// locateClaudeComposer finds the composer by its box: Claude rules its
// composer above and below with a full-width `─` line, and nothing else on
// the measured screens is bracketed that way. The echoed prompt above the
// transcript and the highlighted option inside a dialog both carry the same
// glyph and neither is between two rules.
//
// The lines are split at their rules first (splitAtClaudeRules), because at
// the pane width the Console gives a Mate the rule is exactly as wide as the
// pane and `--source recent-unwrapped` hands the three drawn rows back as
// one line.
func locateClaudeComposer(raw []string) (string, bool) {
	lines := splitAtClaudeRules(raw)
	for i := len(lines) - 1; i >= 0; i-- {
		rest, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), claudeComposerGlyph)
		if !ok {
			continue
		}
		if i == 0 || i == len(lines)-1 {
			continue
		}
		if !isComposerRule(lines[i-1]) || !isComposerRule(lines[i+1]) {
			continue
		}
		return rest, true
	}
	return "", false
}

// splitAtClaudeRules puts every composer rule back on a line of its own.
//
// Measured 2026-09-19 (docs/mvp.md task 24, Claude Code 2.1.278, Herdr
// 0.8.2): a Mate PTY sized by the Console's stream is 65 columns wide (120
// less the 54-column rail and its divider, streamTerminalSize), Claude draws
// its composer rule at exactly that width, and a row that fills the pane is
// a wrapped row as far as `--source recent-unwrapped` is concerned - so the
// rule, the composer under it and the closing rule arrive joined into a
// single line. The box is still drawn; only the line breaks are gone, and a
// cold Mate's empty composer was refused as `a screen mate cannot name`
// because of it.
//
// A rule is a run of at least claudeRuleMin `─`, which is structure rather
// than content: nothing a reader or an agent types reaches ten of them in a
// row, and a rule that was already alone on its line comes back unchanged
// because the empty pieces either side of it are dropped. The split is local
// to this locator so the busy scan keeps reading the rows as the harness
// drew them.
func splitAtClaudeRules(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		runes := []rune(line)
		start, split := 0, false
		for i := 0; i < len(runes); {
			if runes[i] != claudeRuleRune {
				i++
				continue
			}
			end := i
			for end < len(runes) && runes[end] == claudeRuleRune {
				end++
			}
			if end-i < claudeRuleMin {
				i = end
				continue
			}
			// Whitespace either side of a rule is the padding of the row
			// the rule was drawn on, not a row of its own: emitting it
			// would put a blank line between the rule and the composer and
			// break the very adjacency this split exists to restore. A
			// `\r` left by a CRLF snapshot is exactly that case.
			if head := string(runes[start:i]); strings.TrimSpace(head) != "" {
				out = append(out, head)
			}
			out = append(out, string(runes[i:end]))
			start, i, split = end, end, true
		}
		tail := string(runes[start:])
		if !split {
			// A line with no rule in it is passed through unchanged, blank
			// lines included, so nothing else about the screen moves.
			out = append(out, tail)
			continue
		}
		if strings.TrimSpace(tail) != "" {
			out = append(out, tail)
		}
	}
	return out
}

// isComposerRule reports whether a line is one of Claude's composer rules.
func isComposerRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	n := 0
	for _, r := range trimmed {
		if r != claudeRuleRune {
			return false
		}
		n++
	}
	return n >= claudeRuleMin
}

// claudeBusy is the Claude in-flight signature: the spinner line, or the
// queued-message placeholder in the composer. Nothing else.
//
// Claude draws its spinner at column 0, and everything it quotes - a tool's
// output, its own prose - is indented under a `⏺` or `⎿`. So the spinner
// only counts unindented, and the shared literal seeds (seedBusy) are not
// consulted at all: Claude draws none of them itself (measured on 2.1.274
// and again on 2.1.281), and a Mate's screen routinely quotes another
// harness that does. Measured 2026-09-24 (task 31, Claude Code 2.1.281): an
// idle Mate whose last tool call printed a working Codex crew's pane showed
// `⎿  • Working (2s • esc to interrupt)` in its own transcript, the seed
// matched, and every digest for that Mate was refused as mid-turn until the
// 5-minute `wedged` incident opened.
func claudeBusy(lines []string) (string, bool) {
	for _, line := range harness.BusyTail(lines) {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, claudeQueuedPlaceholder) {
			return trimmed, true
		}
		if line == strings.TrimLeft(line, " \t\u00a0") && isClaudeSpinner(trimmed) {
			return trimmed, true
		}
	}
	return "", false
}

// isClaudeSpinner reports whether a trimmed line is the in-flight spinner:
// a spinner glyph, whitespace, then a word ending in the ellipsis.
func isClaudeSpinner(trimmed string) bool {
	runes := []rune(trimmed)
	if len(runes) < 3 {
		return false
	}
	if !isSpinnerRune(runes[0]) {
		return false
	}
	rest := strings.TrimLeft(string(runes[1:]), " \t")
	if rest == string(runes[1:]) {
		// No separating whitespace: a bullet list item, not the spinner.
		return false
	}
	word, _, found := strings.Cut(rest, "…")
	return found && word != "" && !strings.ContainsAny(word, " \t")
}

func isSpinnerRune(r rune) bool {
	for _, s := range claudeSpinnerRunes {
		if r == s {
			return true
		}
	}
	return false
}

// claudeComposerRows reads every row of Claude's composer: the row after the
// glyph and those under it, down to the closing rule. A composer whose
// closing rule is not on screen has no extent mate can prove.
func claudeComposerRows(lines []string) ([]string, bool) {
	lines = splitAtClaudeRules(lines)
	for i := len(lines) - 1; i >= 1; i-- {
		rest, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), claudeComposerGlyph)
		if !ok || !isComposerRule(lines[i-1]) {
			continue
		}
		rows := []string{rest}
		for _, line := range lines[i+1:] {
			if isComposerRule(line) {
				return rows, true
			}
			rows = append(rows, line)
		}
		return nil, false
	}
	return nil, false
}

// Measured 2026-09-14 (ADR 0028) and 2026-09-18 (the Codex update prompt)
// through `herdr agent read --source recent-unwrapped` against codex-cli
// 0.154.0 and Claude Code 2.1.270; Claude's captures are testdata/startup.
// Every string below is a literal from those captures, and the catalog's
// TestClassifyStartupScreenOnCapturedScreens runs the classifier over the
// captures themselves.
const (
	claudeTrustQuestion = "Is this a project you created or one you trust?"
	claudeTrustAccept   = "Yes, I trust this folder"
	claudeTrustQuit     = "No, exit"
	claudeTrustFooter   = "Enter to confirm · Esc to cancel"
	// Measured 2026-10-06 against 2.1.285
	// (testdata/startup/claude-2.1.285-bypass-dialog.txt).
	claudeBypassQuestion = "WARNING: Claude Code running in Bypass Permissions mode"
	claudeBypassAccept   = "Yes, I accept"
	// ClaudeComposerMarker is the glyph Claude draws for both its highlight
	// and its composer; alone on a line it is the empty composer.
	ClaudeComposerMarker = "❯"
)

// claudeStartup is Claude Code's measured startup screens.
func claudeStartup() harness.StartupProfile {
	return harness.StartupProfile{
		Kind: KindClaude,
		Dialogs: []harness.StartupDialog{
			{
				Screen:   harness.StartupScreenTrustDialog,
				Question: claudeTrustQuestion,
				Options: []harness.DialogOption{
					{Label: claudeTrustQuit},
					{Label: claudeTrustAccept},
				},
				Target:         1,
				Footer:         claudeTrustFooter,
				Marker:         ClaudeComposerMarker,
				QuestionWindow: 6,
				SelectKeys:     []string{"down"},
				TargetLabel:    claudeTrustAccept,
			},
			{
				// Named, never answered: the settle refuses on it with
				// no key pressed. Its explanation runs seven lines
				// between the warning and the options.
				Screen:   harness.StartupScreenBypassDialog,
				Question: claudeBypassQuestion,
				Options: []harness.DialogOption{
					{Label: claudeTrustQuit},
					{Label: claudeBypassAccept},
				},
				Target:         1,
				Footer:         claudeTrustFooter,
				Marker:         ClaudeComposerMarker,
				QuestionWindow: 10,
				SelectKeys:     []string{"down"},
				TargetLabel:    claudeBypassAccept,
			},
		},
		// The empty composer is the marker alone on its line. A shell
		// prompt ending in the same glyph ("… main ❯ claude") and a
		// highlighted option ("❯ No, exit") both carry text beside it.
		// From 2.1.282 the empty composer also shows a dim suggestion
		// after the marker; that shape counts only between the
		// composer's two rules (claudeComposerSuggestion).
		Composer: func(lines []string) bool {
			for i, l := range lines {
				if strings.TrimSpace(l) == ClaudeComposerMarker {
					return true
				}
				if i > 0 && i+1 < len(lines) && isRuleLine(lines[i-1]) && isRuleLine(lines[i+1]) &&
					claudeComposerSuggestion.MatchString(strings.TrimSpace(l)) {
					return true
				}
			}
			return false
		},
	}
}

// claudeComposerSuggestion is Claude 2.1.282's empty composer: the marker,
// then the dim placeholder `Try "<example>"` (measured 2026-09-25,
// testdata/startup/claude-2.1.282-ready.txt; the example changes between
// launches). Nothing may follow the closing quote, so typed text that merely
// starts with `Try "` is not mistaken for it.
var claudeComposerSuggestion = regexp.MustCompile(`^` + ClaudeComposerMarker + `[\s\x{00a0}]+Try "[^"]*"$`)

// isRuleLine reports whether a line is a horizontal rule: box-drawing '─'
// only, which is how Claude frames its composer above and below.
func isRuleLine(l string) bool {
	t := strings.TrimSpace(l)
	return t != "" && strings.Trim(t, "─") == ""
}
