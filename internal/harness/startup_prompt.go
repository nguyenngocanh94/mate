package harness

import (
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/matev2/internal/observability"
)

// StartupScreen classifies a bounded terminal snapshot of a harness pane taken
// right after `agent start`. Exactly one interactive prompt is recognised per
// harness - the directory-trust dialog measured on 2026-09-14 (ADR 0028) - and
// everything that is not that dialog and not the empty composer is
// Unrecognized. That third value is the point: the caller answers a
// recognised dialog and refuses to press anything into a screen it cannot
// name, so a reworded or unfamiliar prompt fails loudly instead of being
// driven hopefully.
type StartupScreen string

const (
	// StartupScreenUnrecognized is any screen the profile does not name: the
	// harness still drawing its banner, a shell prompt, a different dialog,
	// or a dialog whose wording moved.
	StartupScreenUnrecognized StartupScreen = "unrecognized"
	// StartupScreenReady is the harness's empty composer, waiting for input.
	StartupScreenReady StartupScreen = "ready"
	// StartupScreenTrustDialog is the per-directory trust confirmation both
	// harnesses draw on a first launch in a directory (or, for a linked
	// worktree, a repository) they have no recorded decision for.
	StartupScreenTrustDialog StartupScreen = "trust_dialog"
)

// TrustDialogAnswer is the key sequence that accepts one harness's
// directory-trust dialog. A caller sends SelectKeys one per call, re-reads the
// screen, and only sends ConfirmKey once TrustDialogAcceptSelected reports the
// highlight on the accept option: Claude puts its cursor on "No, exit" by
// default, so a blind Enter exits the agent instead of accepting.
type TrustDialogAnswer struct {
	// SelectKeys move the highlight onto the accept option. Herdr
	// `pane send-keys` key names.
	SelectKeys []string
	// ConfirmKey confirms the highlighted option.
	ConfirmKey string
	// AcceptLabel is the accept option's text as drawn, for messages.
	AcceptLabel string
}

// Measured 2026-09-14 (ADR 0028) through `herdr agent read --source
// recent-unwrapped` against codex-cli 0.154.0 and Claude Code 2.1.270; the
// captures are internal/harness/testdata/startup. Every string below is a
// literal from those captures, and TestClassifyStartupScreenOnCapturedScreens
// runs the classifier over the captures themselves.
const (
	codexTrustQuestion   = "Do you trust the contents of this directory?"
	codexTrustAccept     = "1. Yes, continue"
	codexTrustQuit       = "2. No, quit"
	codexTrustFooter     = "Press enter to continue"
	codexHighlightMarker = "›"
	// CodexComposerPlaceholder is the empty composer's placeholder text; its
	// presence is what classifies a Codex pane as ready.
	CodexComposerPlaceholder = "Ask Codex to do anything"
	claudeTrustQuestion      = "Is this a project you created or one you trust?"
	claudeTrustAccept        = "Yes, I trust this folder"
	claudeTrustQuit          = "No, exit"
	claudeTrustFooter        = "Enter to confirm · Esc to cancel"
	// ClaudeComposerMarker is the glyph Claude draws for both its highlight
	// and its composer; alone on a line it is the empty composer.
	ClaudeComposerMarker = "❯"
)

// startupProfile is one harness's measured dialog layout. The dialog is
// recognised by its shape, not by its words appearing somewhere: a modal
// drawn over the whole pane ends the screen with its footer, lists its two
// options on adjacent lines directly above that footer with exactly one of
// them highlighted, and asks its question within a few lines above them. An
// agent quoting this ADR, or `cat` of a capture, carries the same words but
// never that shape - the composer or the shell prompt follows the text.
type startupProfile struct {
	question string
	// options are the two option labels in the order the dialog draws them
	// (top to bottom); accept names which of them accepts.
	options [2]string
	accept  string
	footer  string
	marker  string
	// questionWindow is how many non-empty lines above the first option the
	// question may sit (the measured dialogs put explanatory lines between).
	questionWindow int
	answer         TrustDialogAnswer
	composer       func(lines []string) bool
}

func startupProfileFor(kind Kind) (startupProfile, error) {
	switch kind {
	case KindCodex:
		return startupProfile{
			question:       codexTrustQuestion,
			options:        [2]string{codexTrustAccept, codexTrustQuit},
			accept:         codexTrustAccept,
			footer:         codexTrustFooter,
			marker:         codexHighlightMarker,
			questionWindow: 3,
			answer: TrustDialogAnswer{
				SelectKeys:  []string{"1"},
				ConfirmKey:  "enter",
				AcceptLabel: codexTrustAccept,
			},
			composer: func(lines []string) bool {
				for _, l := range lines {
					if strings.Contains(l, CodexComposerPlaceholder) {
						return true
					}
				}
				return false
			},
		}, nil
	case KindClaude:
		return startupProfile{
			question:       claudeTrustQuestion,
			options:        [2]string{claudeTrustQuit, claudeTrustAccept},
			accept:         claudeTrustAccept,
			footer:         claudeTrustFooter,
			marker:         ClaudeComposerMarker,
			questionWindow: 6,
			answer: TrustDialogAnswer{
				SelectKeys:  []string{"down"},
				ConfirmKey:  "enter",
				AcceptLabel: claudeTrustAccept,
			},
			// The empty composer is the marker alone on its line. A shell
			// prompt ending in the same glyph ("… main ❯ claude") and a
			// highlighted option ("❯ No, exit") both carry text beside it.
			composer: func(lines []string) bool {
				for _, l := range lines {
					if strings.TrimSpace(l) == ClaudeComposerMarker {
						return true
					}
				}
				return false
			},
		}, nil
	default:
		return startupProfile{}, observability.NewError(observability.CodeUsage,
			fmt.Sprintf("no measured startup-screen profile for harness %q; mate refuses to drive a pane it cannot read", kind))
	}
}

// trustDialogView is what a structurally confirmed dialog shows.
type trustDialogView struct {
	acceptHighlighted bool
}

// optionLine reports whether a trimmed line is exactly one option label,
// optionally preceded by the highlight marker, and whether it is highlighted.
func (p startupProfile) optionLine(trimmed, label string) (isOption, highlighted bool) {
	if trimmed == label {
		return true, false
	}
	rest, ok := strings.CutPrefix(trimmed, p.marker)
	if ok && strings.TrimSpace(rest) == label {
		return true, true
	}
	return false, false
}

// parseTrustDialog confirms the measured layout and returns which option is
// highlighted. It refuses anything ambiguous: a footer or an option label
// drawn more than once (a quoted dialog above the real one), no highlight or
// two highlights, options that are not adjacent, a footer that is not the
// last thing on screen, or a question that is not where the dialog puts it.
func (p startupProfile) parseTrustDialog(screen string) (trustDialogView, bool) {
	lines := strings.Split(screen, "\n")
	var nonEmpty []int
	footerCount, firstCount, secondCount := 0, 0, 0
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		nonEmpty = append(nonEmpty, i)
		if t == p.footer {
			footerCount++
		}
		if ok, _ := p.optionLine(t, p.options[0]); ok {
			firstCount++
		}
		if ok, _ := p.optionLine(t, p.options[1]); ok {
			secondCount++
		}
	}
	if footerCount != 1 || firstCount != 1 || secondCount != 1 || len(nonEmpty) < 4 {
		return trustDialogView{}, false
	}
	last := nonEmpty[len(nonEmpty)-1]
	if strings.TrimSpace(lines[last]) != p.footer {
		return trustDialogView{}, false
	}
	// The second option is the non-empty line right above the footer (blank
	// lines between are allowed); the first option is the line immediately
	// above it, with no blank line between the two options.
	secondIdx := nonEmpty[len(nonEmpty)-2]
	isSecond, secondHighlighted := p.optionLine(strings.TrimSpace(lines[secondIdx]), p.options[1])
	if !isSecond || secondIdx == 0 {
		return trustDialogView{}, false
	}
	firstIdx := secondIdx - 1
	isFirst, firstHighlighted := p.optionLine(strings.TrimSpace(lines[firstIdx]), p.options[0])
	if !isFirst || firstHighlighted == secondHighlighted {
		return trustDialogView{}, false
	}
	// The question sits within questionWindow non-empty lines above the
	// first option.
	questionFound := false
	seen := 0
	for k := len(nonEmpty) - 3; k >= 0 && seen < p.questionWindow; k-- {
		if nonEmpty[k] >= firstIdx {
			continue
		}
		seen++
		if strings.Contains(lines[nonEmpty[k]], p.question) {
			questionFound = true
			break
		}
	}
	if !questionFound {
		return trustDialogView{}, false
	}
	acceptHighlighted := (p.options[0] == p.accept && firstHighlighted) || (p.options[1] == p.accept && secondHighlighted)
	return trustDialogView{acceptHighlighted: acceptHighlighted}, true
}

// ClassifyStartupScreen names what a harness pane is showing right after
// launch. The dialog is checked before the composer, because Claude's dialog
// and Claude's composer share the highlight glyph.
func ClassifyStartupScreen(kind Kind, screen string) (StartupScreen, error) {
	p, err := startupProfileFor(kind)
	if err != nil {
		return StartupScreenUnrecognized, err
	}
	if _, ok := p.parseTrustDialog(screen); ok {
		return StartupScreenTrustDialog, nil
	}
	if p.composer(strings.Split(screen, "\n")) {
		return StartupScreenReady, nil
	}
	return StartupScreenUnrecognized, nil
}

// TrustDialogAnswerFor returns the measured accept sequence for one harness.
func TrustDialogAnswerFor(kind Kind) (TrustDialogAnswer, error) {
	p, err := startupProfileFor(kind)
	if err != nil {
		return TrustDialogAnswer{}, err
	}
	out := p.answer
	out.SelectKeys = append([]string(nil), p.answer.SelectKeys...)
	return out, nil
}

// TrustDialogAcceptSelected reports whether the structurally confirmed trust
// dialog is on screen with its highlight marker on the accept option - the
// one state in which the confirm key accepts rather than quits. It is false
// on a screen with no dialog, so a caller cannot confirm into a composer by
// mistake.
func TrustDialogAcceptSelected(kind Kind, screen string) (bool, error) {
	p, err := startupProfileFor(kind)
	if err != nil {
		return false, err
	}
	view, ok := p.parseTrustDialog(screen)
	return ok && view.acceptHighlighted, nil
}

// StartupScreenTail returns the last n lines of a screen, for error details.
// It never returns more than the screen holds.
func StartupScreenTail(screen string, n int) string {
	if n <= 0 || screen == "" {
		return ""
	}
	lines := strings.Split(screen, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
