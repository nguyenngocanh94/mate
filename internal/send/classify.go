package send

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/nguyenngocanh94/mate/internal/harness"
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
//
// Where the composer is, what an empty one holds and what a turn in flight
// looks like are the harness's (harness.ScreenProfile, measured against the
// captures in testdata/screens); the order they are asked in, and the faint
// question, are this package's.
func ClassifyComposer(profile harness.ScreenProfile, screen string) Classification {
	plain := StripSGR(screen)
	// A recognised startup dialog is never a composer, whatever its lines
	// look like. Reusing the startup classifier keeps one definition of the
	// trust dialog's shape (internal/harness/startup_prompt.go).
	if profile.ClassifyStartup(plain) == harness.StartupScreenTrustDialog {
		return Classification{State: StateUnknown, Evidence: "harness directory-trust dialog"}
	}

	lines := strings.Split(plain, "\n")
	if evidence, ok := profile.Busy(lines); ok {
		return Classification{State: StateBusy, Evidence: evidence}
	}
	content, ok := profile.Composer(lines)
	if !ok {
		return Classification{State: StateUnknown}
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return Classification{State: StateEmpty, Evidence: composerEvidence(profile, "")}
	}
	for _, placeholder := range profile.ComposerPlaceholders() {
		if trimmed == placeholder {
			return Classification{State: StateEmpty, Evidence: composerEvidence(profile, trimmed)}
		}
	}
	if faintPlaceholder(screen, trimmed) {
		return Classification{State: StateEmpty, Evidence: composerEvidence(profile, trimmed) + " (faint)"}
	}
	return Classification{
		State:    StatePending,
		Evidence: composerEvidence(profile, trimmed),
		Pending:  trimmed,
	}
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
func composerEvidence(profile harness.ScreenProfile, content string) string {
	glyph := profile.ComposerGlyph()
	if content == "" {
		return glyph
	}
	return glyph + " " + content
}

// ScreenTail returns the last n lines of a screen, for error details.
func ScreenTail(screen string, n int) string {
	return harness.StartupScreenTail(screen, n)
}
