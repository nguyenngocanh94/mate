package harness

import (
	"regexp"
	"strings"
)

// The composer half of the screen profiles (screen.go): where each harness
// draws its composer, what an empty one holds, and what a turn in flight
// looks like. Moved from internal/send in plan PR 3 with the verdicts
// unchanged; send's ClassifyComposer is the policy that reads them.

// Measured 2026-09-17 against Claude Code 2.1.274 and codex-cli 0.154.0
// through `herdr agent read --source recent-unwrapped --lines 40`; the
// captures are internal/send/testdata/screens and
// TestClassifyComposerOnCapturedScreens runs this classifier over them.
//
// `--source recent-unwrapped --format text` hands mate plain text with the
// ANSI styling already gone, so firstmate's dim-ghost stripping
// (fm_tmux_strip_ghost) has nothing to strip here: a placeholder is
// recognised by its measured wording instead of by its SGR 2 attribute.
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
	// codexComposerGlyph is the glyph Codex draws at its composer.
	codexComposerGlyph = "›"
	// codexComposerScan is how far up from the bottom the Codex composer
	// is looked for, in non-empty lines. It bounds the search, not the
	// footer: Codex puts a status line under its composer, and 0.157.1 a
	// second one (the `? for shortcuts` hint, a `⚠ 1 warning` count), and
	// how many it draws there is not something to count on
	// (locateCodexComposer).
	codexComposerScan = busyTailLines
	// busyTailLines bounds the busy scan to the bottom of the snapshot, so
	// a transcript line that happens to quote a spinner or an interrupt
	// hint does not make an idle pane look busy forever.
	busyTailLines = 20
)

// claudeSpinnerRunes are the glyphs Claude cycles at the head of its
// in-flight line. Measured: "✶ Pollinating…", "✻ Pollinating… (1s · ↓ 25
// tokens · thinking with medium effort)", "· Pollinating…". The gerund is
// randomised per turn, so the stable part is the glyph plus the ellipsis -
// and the finished line ("✻ Sautéed for 23s · done 7:17 PM") carries no
// ellipsis, which is exactly what separates the two.
var claudeSpinnerRunes = []rune{'✻', '✽', '✶', '✳', '✢', '·', '*', '•'}

// busySeeds are the literal in-flight hints, case-insensitively matched.
// The first two are firstmate's FM_TMUX_BUSY_REGEX_DEFAULT reduced to
// literals; "esc to interrupt" is what Codex draws live here ("• Working
// (2s • esc to interrupt)"), and Claude 2.1.274 draws none of them at all,
// which is why its spinner shape is measured separately.
var busySeeds = []string{"esc to interrupt", "working...", "ctrl+c:cancel"}

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

// locateCodexComposer finds the composer as the last `›` line near the
// bottom with nothing below it that belongs to a menu.
//
// Codex draws no box, and what it draws under the composer is chrome that
// changes between releases: one status line on 0.154.0, a second hint and
// warning line on 0.157.1, both measured. Counting those lines is what broke
// every send to an idle crew on 0.157.1 (2026-09-26), so nothing here
// depends on what the footer says or how tall it is.
//
// What separates the composer from a `›` that marks a menu's highlighted
// option (the model picker, the trust dialog) is the menu itself: the
// highlighted option is itself drawn `› N. label`, and its sibling options
// `N. label`. A `›` line that is one, or has one below it, is not a
// composer; a person's half-typed `1. do x` is then Unknown rather than
// Pending, which refuses just the same.
//
// It is fail-closed by construction. Empty needs the text after the glyph to
// be nothing, the measured placeholder, or faint; a menu option, a quoted
// prompt in the transcript, or anything else a future Codex puts behind a
// `›` has text, so a misread can only ever be Pending - a refusal - and
// never a composer mate types into.
func locateCodexComposer(lines []string) (string, bool) {
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < codexComposerScan; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		seen++
		if isCodexMenuOption(trimmed) {
			return "", false
		}
		if rest, ok := strings.CutPrefix(trimmed, codexComposerGlyph); ok {
			if isCodexMenuOption(strings.TrimSpace(rest)) {
				// The highlighted row of a menu whose other options are
				// all above it.
				return "", false
			}
			return rest, true
		}
	}
	return "", false
}

// isCodexMenuOption reports whether a trimmed line is a sibling option of a
// Codex menu: `2. Quit`, `3. gpt-5.6-luna   Fast and affordable ...`.
func isCodexMenuOption(trimmed string) bool {
	return codexMenuOption.MatchString(trimmed)
}

// codexMenuOption is a numbered menu row, as every measured Codex menu draws
// its options: digits, a dot, a space, a label.
var codexMenuOption = regexp.MustCompile(`^[0-9]+\. \S`)

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
	for _, line := range busyTail(lines) {
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

// seedBusy matches the literal in-flight hints shared across harnesses.
func seedBusy(lines []string) (string, bool) {
	for _, line := range busyTail(lines) {
		lower := strings.ToLower(line)
		for _, seed := range busySeeds {
			if strings.Contains(lower, seed) {
				return strings.TrimSpace(line), true
			}
		}
	}
	return "", false
}

// busyTail bounds the busy scan to the bottom of the snapshot.
func busyTail(lines []string) []string {
	if len(lines) <= busyTailLines {
		return lines
	}
	return lines[len(lines)-busyTailLines:]
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

// codexComposerRows reads every row of Codex's composer, from the glyph down
// to the footer. The measured footer starts after an empty row and includes
// the model/cwd separator. Without that boundary a clipped continuation
// cannot be excluded, even if the first row matches perfectly.
func codexComposerRows(lines []string) ([]string, bool) {
	end := -1
	for i := len(lines) - 1; i >= 1; i-- {
		if strings.TrimSpace(lines[i-1]) == "" && strings.HasPrefix(lines[i], "  ") && strings.Contains(lines[i], " · ") {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, false
	}
	for i := end - 1; i >= 0; i-- {
		if rest, ok := strings.CutPrefix(lines[i], codexComposerGlyph); ok {
			return append([]string{rest}, lines[i+1:end]...), true
		}
	}
	return nil, false
}
