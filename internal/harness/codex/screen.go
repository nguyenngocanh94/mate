package codex

import (
	"regexp"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// codexScreen is codex-cli's ScreenProfile.
type codexScreen struct{}

// ReadSource implements ScreenProfile.
func (codexScreen) ReadSource() harness.ReadSource { return harness.ReadRecentUnwrapped }

// ClassifyStartup implements ScreenProfile.
func (codexScreen) ClassifyStartup(screen string) harness.StartupScreen {
	return codexStartup().Classify(screen)
}

// StartupAnswer implements ScreenProfile.
func (codexScreen) StartupAnswer(dialog harness.StartupScreen) (harness.StartupDialogAnswer, error) {
	return codexStartup().Answer(dialog)
}

// StartupTargetSelected implements ScreenProfile.
func (codexScreen) StartupTargetSelected(dialog harness.StartupScreen, screen string) bool {
	return codexStartup().TargetSelected(dialog, screen)
}

// ReadyScreen implements ScreenProfile: the composer holding its
// placeholder.
func (codexScreen) ReadyScreen() string {
	return "› " + CodexComposerPlaceholder + "\n"
}

// ComposerGlyph implements ScreenProfile.
func (codexScreen) ComposerGlyph() string { return codexComposerGlyph }

// Composer implements ScreenProfile.
func (codexScreen) Composer(lines []string) (string, bool) { return locateCodexComposer(lines) }

// ComposerPlaceholders implements ScreenProfile.
func (codexScreen) ComposerPlaceholders() []string { return []string{CodexComposerPlaceholder} }

// ComposerRows implements ScreenProfile.
func (codexScreen) ComposerRows(lines []string) ([]string, bool) { return codexComposerRows(lines) }

// Busy implements ScreenProfile.
func (codexScreen) Busy(lines []string) (string, bool) { return seedBusy(lines) }

const (
	// codexComposerGlyph is the glyph Codex draws at its composer.
	codexComposerGlyph = "›"
	// codexComposerScan is how far up from the bottom the Codex composer
	// is looked for, in non-empty lines. It bounds the search, not the
	// footer: Codex puts a status line under its composer, and 0.157.1 a
	// second one (the `? for shortcuts` hint, a `⚠ 1 warning` count), and
	// how many it draws there is not something to count on
	// (locateCodexComposer).
	codexComposerScan = harness.BusyTailLines
)

// busySeeds are the literal in-flight hints, case-insensitively matched.
// The first two are firstmate's FM_TMUX_BUSY_REGEX_DEFAULT reduced to
// literals; "esc to interrupt" is what Codex draws live here ("• Working
// (2s • esc to interrupt)"), and Claude 2.1.274 draws none of them at all,
// which is why its spinner shape is measured separately.
var busySeeds = []string{"esc to interrupt", "working...", "ctrl+c:cancel"}

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

// seedBusy matches the literal in-flight hints shared across harnesses.
func seedBusy(lines []string) (string, bool) {
	for _, line := range harness.BusyTail(lines) {
		lower := strings.ToLower(line)
		for _, seed := range busySeeds {
			if strings.Contains(lower, seed) {
				return strings.TrimSpace(line), true
			}
		}
	}
	return "", false
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

const (
	codexTrustQuestion   = "Do you trust the contents of this directory?"
	codexTrustAccept     = "1. Yes, continue"
	codexTrustQuit       = "2. No, quit"
	codexDialogFooter    = "Press enter to continue"
	codexHighlightMarker = "›"
	// codex-cli 0.156.1 redrew the directory-trust dialog (captured
	// 2026-09-24, task 35, after the installed codex moved from 0.154.0 to
	// 0.156.1 mid-session): a "Folder access" header, the path, a wrapped
	// paragraph that opens with this question, two new option labels and a
	// new footer. The highlight still opens on option 1 and "1" still moves
	// it there without confirming, so the answer is the same sequence.
	codexTrustQuestionV156 = "Trust this folder?"
	codexTrustAcceptV156   = "1. Trust and continue"
	codexTrustQuitV156     = "2. Quit"
	codexDialogFooterV156  = "enter continue · esc quit"
	// CodexComposerPlaceholder is the empty composer's placeholder text; its
	// presence is what classifies a Codex pane as ready.
	CodexComposerPlaceholder = "Ask Codex to do anything"
	// codexUpdateNotice is the update prompt's headline. The version pair
	// after it moves with every release, so only the constant part is
	// matched, and only as the dialog's question line.
	codexUpdateNotice = "Update available!"
	// codexUpdateNow is matched as a line prefix: the parenthetical names
	// the install command, which differs by install method.
	codexUpdateNow      = "1. Update now"
	codexUpdateSkip     = "2. Skip"
	codexUpdateSkipNext = "3. Skip until next version"
)

// codexStartup is codex-cli's measured startup screens.
func codexStartup() harness.StartupProfile {
	return harness.StartupProfile{
		Kind: KindCodex,
		// The hook review (hooks.go) is recognised here but
		// answered only by the settle's own walk through it, and only
		// for hooks mate names as its own.
		Dialogs: append([]harness.StartupDialog{
			{
				Screen:   harness.StartupScreenTrustDialog,
				Question: codexTrustQuestion,
				Options: []harness.DialogOption{
					{Label: codexTrustAccept},
					{Label: codexTrustQuit},
				},
				Target:         0,
				Footer:         codexDialogFooter,
				Marker:         codexHighlightMarker,
				QuestionWindow: 3,
				// The dialog opens with "1. Yes, continue" highlighted,
				// but the digit is pressed rather than trusted: the
				// re-read after it is what the accept check reads.
				SelectKeys:  []string{"1"},
				TargetLabel: codexTrustAccept,
			},
			{
				Screen:   harness.StartupScreenTrustDialog,
				Question: codexTrustQuestionV156,
				Options: []harness.DialogOption{
					{Label: codexTrustAcceptV156},
					{Label: codexTrustQuitV156},
				},
				Target: 0,
				Footer: codexDialogFooterV156,
				Marker: codexHighlightMarker,
				// The question opens a paragraph that wraps to three
				// lines at 93 columns; a narrower pane wraps it to more.
				QuestionWindow: 6,
				SelectKeys:     []string{"1"},
				TargetLabel:    codexTrustAcceptV156,
			},
			{
				Screen:   harness.StartupScreenUpdateDialog,
				Question: codexUpdateNotice,
				Options: []harness.DialogOption{
					{Label: codexUpdateNow, Prefix: true},
					{Label: codexUpdateSkip},
					{Label: codexUpdateSkipNext},
				},
				// "3. Skip until next version" is the only option that
				// does not draw the prompt again on the next launch:
				// "2. Skip" returns immediately, and "1. Update now"
				// runs a package install under the agent.
				Target:         2,
				Footer:         codexDialogFooter,
				Marker:         codexHighlightMarker,
				QuestionWindow: 3,
				// Measured 2026-09-18: the highlight opens on option 1
				// and `down` moves it one option at a time.
				SelectKeys:  []string{"down", "down"},
				TargetLabel: codexUpdateSkipNext,
			},
		}, hooksReviewDialogs()...),
		Composer: func(lines []string) bool {
			for _, l := range lines {
				if strings.Contains(l, CodexComposerPlaceholder) {
					return true
				}
			}
			return false
		},
	}
}
