package send

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
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
	// may sit, counted in non-empty lines: itself, the model/cwd status
	// footer, and the line codex-cli 0.157.1 draws under that footer - the
	// `? for shortcuts` hint and a `⚠ 1 warning · f2 to view` count, either
	// or both (measured 2026-09-26; 0.154.0 drew no such line, and a live
	// Mate's every send to an idle Codex crew was refused as an unnamed
	// screen until this was 3). A `›`-prefixed option inside a dialog sits
	// further up - the model picker has its sibling options and a hint
	// below it - which is what keeps it out of Empty; and the one dialog
	// short enough to fit, the trust dialog, is named before this runs.
	// Widening the window is fail-closed besides: a dialog option is never
	// an empty composer, so the worst it can read as is Pending, a refusal.
	codexComposerTailLines = 3
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
//
// screen may carry the harness's own SGR attributes
// (runtime.Adapter.ReadAgentStyled) or be the plain rendering of the same
// snapshot. Every rule below reads the plain text; the attributes decide one
// question and only one - whether text in the composer is the harness's own
// faint suggestion rather than something a person typed (faintPlaceholder).
// A plain screen therefore classifies exactly as it always did, and a styled
// one differs only where that question arises.
func ClassifyComposer(kind harness.Kind, screen string) (Classification, error) {
	profile, err := composerProfileFor(kind)
	if err != nil {
		return Classification{State: StateUnknown}, err
	}
	plain := StripSGR(screen)
	// A recognised startup dialog is never a composer, whatever its lines
	// look like. Reusing the startup classifier keeps one definition of the
	// trust dialog's shape (internal/harness/startup_prompt.go).
	startup, err := harness.ClassifyStartupScreen(kind, plain)
	if err != nil {
		return Classification{State: StateUnknown}, err
	}
	if startup == harness.StartupScreenTrustDialog {
		return Classification{State: StateUnknown, Evidence: "harness directory-trust dialog"}, nil
	}

	lines := strings.Split(plain, "\n")
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
	if faintPlaceholder(screen, trimmed) {
		return Classification{State: StateEmpty, Evidence: composerEvidence(kind, trimmed) + " (faint)"}, nil
	}
	return Classification{
		State:    StatePending,
		Evidence: composerEvidence(kind, trimmed),
		Pending:  trimmed,
	}, nil
}

// faintPlaceholder reports whether the composer's content is drawn faint,
// which is how a harness marks text it wrote itself as a suggestion rather
// than as anybody's input.
//
// Measured 2026-09-19 (docs/mvp.md task 24, Claude Code 2.1.278, Herdr
// 0.8.2): after a turn that asks the captain a question, Claude Code offers
// an answer inside the composer -
//
//	❯ \x1b[0m\x1b[2mUse checkout-express.html\x1b[0m
//
// - which `--format text` renders as `❯ Use checkout-express.html`, exactly
// what a half-typed human line looks like. mate refused to type over it, and
// Ctrl+U did not clear it, because there is nothing there to clear: the
// whole difference is SGR 2. Two live acceptance runs deadlocked on this,
// the captain's own line and the console's `[assign]` alike.
//
// It is deliberately fail-closed. The verdict needs the content to be found
// in the styled screen and every visible rune of it to be faint; a screen
// with no attributes at all, a partial match, or one non-faint rune all
// leave the classification at Pending, because typing over a person's
// unsubmitted line is the mistake this whole state exists to prevent.
func faintPlaceholder(styled, content string) bool {
	if content == "" || !strings.Contains(styled, sgrIntroducer) {
		return false
	}
	visible, faint := renderFaintMask(styled)
	at := strings.LastIndex(visible, content)
	if at < 0 {
		return false
	}
	seen := false
	for i, r := range visible[at : at+len(content)] {
		if unicode.IsSpace(r) {
			continue
		}
		if !faint[at+i] {
			return false
		}
		seen = true
	}
	return seen
}

// renderFaintMask returns the screen's visible text with the escape
// sequences removed, and a parallel mask saying, for each byte of it,
// whether SGR 2 was in force when it was drawn.
//
// It tracks only the two codes that matter - 2 turns faint on, 0 and 22 turn
// it off - and treats every other CSI sequence as a no-op on that one
// attribute, which is what a terminal does with them as far as faintness is
// concerned.
func renderFaintMask(screen string) (string, []bool) {
	var out strings.Builder
	mask := make([]bool, 0, len(screen))
	faint := false
	for i := 0; i < len(screen); {
		if screen[i] == 0x1b {
			params, end, ok := parseCSI(screen, i)
			if !ok {
				i++
				continue
			}
			if end <= len(screen) && screen[end-1] == 'm' {
				faint = applySGR(faint, params)
			}
			i = end
			continue
		}
		out.WriteByte(screen[i])
		mask = append(mask, faint)
		i++
	}
	return out.String(), mask
}

// StripSGR removes the escape sequences from a styled screen, leaving the
// text a `--format text` read would have returned.
func StripSGR(screen string) string {
	if !strings.Contains(screen, sgrIntroducer) {
		return screen
	}
	text, _ := renderFaintMask(screen)
	return text
}

// sgrIntroducer is the two bytes every sequence this package understands
// starts with. A screen without it carries no attributes at all.
const sgrIntroducer = "\x1b["

// parseCSI splits the CSI sequence starting at i into its parameter bytes,
// its final byte and the index just past it. A sequence that does not parse
// is not a sequence, and the caller steps over one byte instead.
func parseCSI(s string, i int) (params string, end int, ok bool) {
	if i+1 >= len(s) || s[i+1] != '[' {
		return "", 0, false
	}
	j := i + 2
	for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3f {
		j++
	}
	params = s[i+2 : j]
	for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
		j++
	}
	if j >= len(s) || s[j] < 0x40 || s[j] > 0x7e {
		return "", 0, false
	}
	return params, j + 1, true
}

// applySGR folds one SGR sequence's parameters into the faint attribute.
// Only 0 (reset), 2 (faint) and 22 (normal intensity) move it.
//
// The extended-colour forms have to be consumed rather than scanned past,
// and that is not a detail: Claude draws its own composer text with
// `38;2;255;255;255`, whose second parameter is the 2 that selects direct
// RGB, not the 2 that means faint. A scanner that read parameters
// independently would call a person's white typing a suggestion and type
// over it, which is the single worst thing this file can get wrong.
func applySGR(faint bool, params string) bool {
	if params == "" {
		return false // a bare ESC[m is ESC[0m
	}
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		n, err := strconv.Atoi(fields[i])
		if err != nil {
			continue
		}
		switch n {
		case 0:
			faint = false
		case 2:
			faint = true
		case 22:
			faint = false
		case 38, 48, 58:
			// 5;<n> is a palette index, 2;<r>;<g>;<b> is direct colour.
			if i+1 < len(fields) {
				switch fields[i+1] {
				case "5":
					i += 2
				case "2":
					i += 4
				default:
					i++
				}
			}
		}
	}
	return faint
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
		if !isRule(lines[i-1]) || !isRule(lines[i+1]) {
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
// box, but its composer is the last `›` line of the snapshot and only its
// footer follows it (codexComposerTailLines). A `›` marking an option inside
// the model picker has its sibling options below it, which puts it outside
// that window; the trust dialog's two options would fit, which is why
// ClassifyComposer names that dialog first.
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

// ScreenTail returns the last n lines of a screen, for error details.
func ScreenTail(screen string, n int) string {
	return harness.StartupScreenTail(screen, n)
}
