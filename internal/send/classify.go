package send

import (
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
)

// ComposerState names what a harness pane is showing, as far as typing one
// line into it is concerned. Four values, because the three a caller may not
// type into are genuinely different and the caller answers them differently:
// Busy waits, Pending belongs to whoever typed it, and Unknown is a screen
// mate cannot read at all.
type ComposerState string

const (
	// StateEmpty is the harness composer drawn with nothing in it (or only
	// its placeholder). It is the one state a line may be typed into.
	StateEmpty ComposerState = "empty"
	// StatePending is the composer drawn with text after the prompt glyph:
	// a human mid-typing, or an earlier send whose Enter was swallowed.
	// Typing here concatenates two messages into one (docs/mvp.md section 7).
	StatePending ComposerState = "pending"
	// StateBusy is the harness's own mid-turn signature on screen. The
	// composer may look empty underneath it; a line typed now is queued or
	// dropped rather than answered.
	StateBusy ComposerState = "busy"
	// StateUnknown is any screen with no recognised composer: a trust or
	// model dialog, a scrolled transcript, a harness still drawing itself.
	StateUnknown ComposerState = "unknown"
)

func (s ComposerState) String() string { return string(s) }

// Classification is what ClassifyComposer saw and the line it saw it on.
// Evidence is quoted back in errors and logs so a refusal names the screen
// text it was decided from rather than asserting a verdict.
type Classification struct {
	State ComposerState
	// Evidence is the trimmed screen line the verdict was read from. It is
	// empty only when no line supported any verdict (StateUnknown).
	Evidence string
	// Pending is the text sitting after the prompt glyph. It is set only
	// for StatePending, and is what a caller shows the user whose
	// half-typed line the send refused to overwrite.
	Pending string
}

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
	claudeComposerGlyph = harness.ClaudeComposerMarker
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
	// codexComposerTailLines is how far from the bottom the Codex composer
	// sits, counted in non-empty lines: itself, then the model/cwd status
	// footer. A `›`-prefixed option inside a dialog sits further up, which
	// is what keeps the model picker and the trust dialog out of Empty.
	codexComposerTailLines = 2
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

// composerProfile is one harness's measured composer layout.
type composerProfile struct {
	// locate returns the composer line's content after the prompt glyph,
	// and whether a composer was found at all.
	locate func(lines []string) (content string, ok bool)
	// placeholders are the measured empty-composer placeholder texts. The
	// composer holding one of these is empty, not pending.
	placeholders []string
	// busy returns the line proving the harness is mid-turn.
	busy func(lines []string) (evidence string, ok bool)
}

func composerProfileFor(kind harness.Kind) (composerProfile, error) {
	switch kind {
	case harness.KindClaude:
		return composerProfile{
			locate:       locateClaudeComposer,
			placeholders: []string{},
			busy:         claudeBusy,
		}, nil
	case harness.KindCodex:
		return composerProfile{
			locate:       locateCodexComposer,
			placeholders: []string{harness.CodexComposerPlaceholder},
			busy:         seedBusy,
		}, nil
	default:
		return composerProfile{}, observability.NewError(observability.CodeUsage,
			fmt.Sprintf("no measured composer profile for harness %q; mate refuses to type into a pane it cannot read", kind))
	}
}

// ClassifyComposer names what one harness pane is showing. The order is the
// point: a screen that is both mid-turn and drawing an empty composer -
// which is what Claude does for most of a turn - must classify as Busy, and
// a dialog that puts the highlight glyph on an option must never be read as
// a composer holding that option's text.
func ClassifyComposer(kind harness.Kind, screen string) (Classification, error) {
	profile, err := composerProfileFor(kind)
	if err != nil {
		return Classification{State: StateUnknown}, err
	}
	// A recognised startup dialog is never a composer, whatever its lines
	// look like. Reusing the startup classifier keeps one definition of the
	// trust dialog's shape (internal/harness/startup_prompt.go).
	startup, err := harness.ClassifyStartupScreen(kind, screen)
	if err != nil {
		return Classification{State: StateUnknown}, err
	}
	if startup == harness.StartupScreenTrustDialog {
		return Classification{State: StateUnknown, Evidence: "harness directory-trust dialog"}, nil
	}

	lines := strings.Split(screen, "\n")
	if evidence, ok := profile.busy(lines); ok {
		return Classification{State: StateBusy, Evidence: evidence}, nil
	}
	content, ok := profile.locate(lines)
	if !ok {
		return Classification{State: StateUnknown}, nil
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return Classification{State: StateEmpty, Evidence: composerEvidence(kind, "")}, nil
	}
	for _, placeholder := range profile.placeholders {
		if trimmed == placeholder {
			return Classification{State: StateEmpty, Evidence: composerEvidence(kind, trimmed)}, nil
		}
	}
	return Classification{
		State:    StatePending,
		Evidence: composerEvidence(kind, trimmed),
		Pending:  trimmed,
	}, nil
}

// composerEvidence renders the composer line the way the harness drew it.
func composerEvidence(kind harness.Kind, content string) string {
	glyph := claudeComposerGlyph
	if kind == harness.KindCodex {
		glyph = codexComposerGlyph
	}
	if content == "" {
		return glyph
	}
	return glyph + " " + content
}

// locateClaudeComposer finds the composer by its box: Claude rules its
// composer above and below with a full-width `─` line, and nothing else on
// the measured screens is bracketed that way. The echoed prompt above the
// transcript and the highlighted option inside a dialog both carry the same
// glyph and neither is between two rules.
func locateClaudeComposer(lines []string) (string, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		rest, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), claudeComposerGlyph)
		if !ok {
			continue
		}
		if i == 0 || i == len(lines)-1 {
			continue
		}
		if !isRule(lines[i-1]) || !isRule(lines[i+1]) {
			continue
		}
		return rest, true
	}
	return "", false
}

// isRule reports whether a line is one of Claude's composer rules.
func isRule(line string) bool {
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

// locateCodexComposer finds the composer by where it sits: Codex draws no
// box, but its composer is the last `›` line of the snapshot and only the
// model/cwd status footer follows it. A `›` marking an option inside the
// model picker or the trust dialog has its sibling options below it, which
// puts it outside that window.
func locateCodexComposer(lines []string) (string, bool) {
	seen := 0
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		seen++
		if seen > codexComposerTailLines {
			return "", false
		}
		if rest, ok := strings.CutPrefix(trimmed, codexComposerGlyph); ok {
			return rest, true
		}
	}
	return "", false
}

// claudeBusy is the Claude in-flight signature: the spinner line, or the
// queued-message placeholder in the composer, or one of the shared literal
// seeds for a version that draws them.
func claudeBusy(lines []string) (string, bool) {
	for _, line := range busyTail(lines) {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, claudeQueuedPlaceholder) {
			return trimmed, true
		}
		if isClaudeSpinner(trimmed) {
			return trimmed, true
		}
	}
	return seedBusy(lines)
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

// ScreenTail returns the last n lines of a screen, for error details.
func ScreenTail(screen string, n int) string {
	return harness.StartupScreenTail(screen, n)
}
