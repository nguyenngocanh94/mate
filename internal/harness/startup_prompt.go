package harness

import (
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// StartupScreen classifies a bounded terminal snapshot of a harness pane taken
// right after `agent start`. Only measured interactive prompts are recognised
// per harness - the directory-trust dialog measured on 2026-09-14 (ADR 0028)
// and, for Codex, the release-update prompt measured on 2026-09-18 - and
// everything that is not one of those and not the empty composer is
// Unrecognized. That last value is the point: the caller answers a recognised
// dialog and refuses to press anything into a screen it cannot name, so a
// reworded or unfamiliar prompt fails loudly instead of being driven
// hopefully.
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
	// StartupScreenUpdateDialog is the release-update prompt codex-cli draws
	// before anything else when a newer version is published and the
	// operator has not dismissed that version. It blocks the composer, so a
	// launch that ignores it never reaches the agent.
	StartupScreenUpdateDialog StartupScreen = "update_dialog"
	// StartupScreenHooksReview is a review of hooks the harness will not
	// run until they are trusted, such as codex-cli's "Hooks need review"
	// dialog. The settle answers it through HookInstaller.ReviewOwn.
	StartupScreenHooksReview StartupScreen = "hooks_review"
	// StartupScreenBypassDialog is Claude's one-time "Bypass Permissions
	// mode" acceptance, drawn by --dangerously-skip-permissions until the
	// operator has accepted it once. It is recognised only so the settle can
	// refuse at once with what to do; mate never answers it (the open captain
	// call gomate-claude-bypass-mode-screen).
	StartupScreenBypassDialog StartupScreen = "bypass_dialog"
)

// StartupDialogAnswer is the key sequence that answers one measured startup
// dialog. A caller sends SelectKeys one per call, re-reads the screen, and
// only sends ConfirmKey once the highlight marker is on TargetLabel: Claude
// puts its cursor on "No, exit" by default and Codex puts its update cursor
// on "Update now", so a blind Enter kills the agent or starts an npm install
// under it.
type StartupDialogAnswer struct {
	// SelectKeys move the highlight onto the target option. Herdr
	// `pane send-keys` key names.
	SelectKeys []string
	// ConfirmKey confirms the highlighted option.
	ConfirmKey string
	// TargetLabel is the option this answer selects, as drawn, for messages.
	TargetLabel string
	// Target is TargetLabel's index among the dialog's options, top to
	// bottom from 0: the highlight position at which ConfirmKey accepts.
	Target int
}

// DialogOption is one option label as the dialog draws it. prefix is set for a
// label whose tail is environment-specific (Codex spells its update command
// as `npm install -g @openai/codex` or the equivalent for another install
// method); the head still anchors at the start of the option line.
type DialogOption struct {
	Label  string
	Prefix bool
}

func (o DialogOption) matches(s string) bool {
	if o.Prefix {
		return strings.HasPrefix(s, o.Label)
	}
	return s == o.Label
}

// StartupDialog is one measured modal's layout. The dialog is recognised by
// its shape, not by its words appearing somewhere: a modal drawn over the
// whole pane ends the screen with its footer, lists its options on adjacent
// lines directly above that footer with exactly one of them highlighted, and
// states its question within a few lines above them. An agent quoting this
// ADR, `cat` of a capture, or Codex's post-launch "update available" banner
// carries the same words but never that shape - the composer or the shell
// prompt follows the text.
type StartupDialog struct {
	// Screen is what a match classifies as.
	Screen   StartupScreen
	Question string
	// Options are the option labels in the order the dialog draws them,
	// top to bottom.
	Options []DialogOption
	// Target is the index in options this harness's answer selects.
	Target int
	Footer string
	Marker string
	// QuestionWindow is how many non-empty lines above the first option the
	// question may sit (the measured dialogs put explanatory lines between).
	QuestionWindow int
	// SelectKeys move the highlight from the drawn default onto target, one
	// press at a time.
	SelectKeys []string
	// TargetLabel is the target option as drawn, for messages.
	TargetLabel string
}

func (d StartupDialog) answer() StartupDialogAnswer {
	return StartupDialogAnswer{
		SelectKeys:  append([]string(nil), d.SelectKeys...),
		ConfirmKey:  "enter",
		TargetLabel: d.TargetLabel,
		Target:      d.Target,
	}
}

// StartupProfile is one harness's measured startup screens: the modals it can
// draw, in the order they are checked, and what its empty composer looks like.
// Each harness's ScreenProfile reads through one (its package's screen.go).
type StartupProfile struct {
	// Kind names the harness in refusals.
	Kind     Kind
	Dialogs  []StartupDialog
	Composer func(lines []string) bool
}

// dialogsFor returns every profile that classifies as one screen: a harness
// can draw the same dialog in more than one measured layout (Codex's trust
// dialog before and after 0.156.1). Every layout of one screen is answered
// with the same keys (TestStartupDialogLayoutsOfOneScreenShareTheirAnswer).
func (p StartupProfile) dialogsFor(screen StartupScreen) []StartupDialog {
	var out []StartupDialog
	for _, d := range p.Dialogs {
		if d.Screen == screen {
			out = append(out, d)
		}
	}
	return out
}

// optionLine reports whether a trimmed line is exactly one option label,
// optionally preceded by the highlight marker, and whether it is highlighted.
func (d StartupDialog) optionLine(trimmed string, o DialogOption) (isOption, highlighted bool) {
	if o.matches(trimmed) {
		return true, false
	}
	rest, ok := strings.CutPrefix(trimmed, d.Marker)
	if ok && o.matches(strings.TrimSpace(rest)) {
		return true, true
	}
	return false, false
}

// parse confirms the measured layout and returns the index of the highlighted
// option. It refuses anything ambiguous: a footer or an option label drawn
// more than once (a quoted dialog above the real one), no highlight or two
// highlights, options that are not on adjacent rows, a footer that is not the
// last thing on screen, or a question that is not where the dialog puts it.
func (d StartupDialog) parse(screen string) (highlighted int, ok bool) {
	lines := strings.Split(screen, "\n")
	var nonEmpty []int
	footerCount := 0
	counts := make([]int, len(d.Options))
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		nonEmpty = append(nonEmpty, i)
		if t == d.Footer {
			footerCount++
		}
		for k, o := range d.Options {
			if isOpt, _ := d.optionLine(t, o); isOpt {
				counts[k]++
			}
		}
	}
	if footerCount != 1 || len(nonEmpty) < len(d.Options)+2 {
		return 0, false
	}
	for _, c := range counts {
		if c != 1 {
			return 0, false
		}
	}
	last := nonEmpty[len(nonEmpty)-1]
	if strings.TrimSpace(lines[last]) != d.Footer {
		return 0, false
	}
	// The last option is the non-empty line right above the footer (blank
	// lines between are allowed); the options above it sit on the rows
	// directly above, with no blank line anywhere between them.
	lastOpt := nonEmpty[len(nonEmpty)-2]
	firstOpt := lastOpt - (len(d.Options) - 1)
	if firstOpt <= 0 {
		return 0, false
	}
	highlighted = -1
	for k, o := range d.Options {
		isOpt, hl := d.optionLine(strings.TrimSpace(lines[firstOpt+k]), o)
		if !isOpt {
			return 0, false
		}
		if hl {
			if highlighted >= 0 {
				return 0, false
			}
			highlighted = k
		}
	}
	if highlighted < 0 {
		return 0, false
	}
	// The question sits within questionWindow non-empty lines above the
	// first option.
	seen := 0
	for k := len(nonEmpty) - 1; k >= 0 && seen < d.QuestionWindow; k-- {
		if nonEmpty[k] >= firstOpt {
			continue
		}
		seen++
		if strings.Contains(lines[nonEmpty[k]], d.Question) {
			return highlighted, true
		}
	}
	return 0, false
}

// Classify names what a harness pane is showing right after launch. Dialogs
// are checked before the composer, because Claude's dialog and Claude's
// composer share the highlight glyph, and because Codex keeps an "update
// available" banner in its scrollback above the live composer.
func (p StartupProfile) Classify(screen string) StartupScreen {
	for _, d := range p.Dialogs {
		if _, ok := d.parse(screen); ok {
			return d.Screen
		}
	}
	if p.Composer(strings.Split(screen, "\n")) {
		return StartupScreenReady
	}
	return StartupScreenUnrecognized
}

// Answer is the measured key sequence for one dialog: the trust dialog's
// accept, or the update prompt's "3. Skip until next version" - the only
// option that both leaves the installed harness alone and stops the prompt
// returning before the next release.
func (p StartupProfile) Answer(screen StartupScreen) (StartupDialogAnswer, error) {
	layouts := p.dialogsFor(screen)
	if len(layouts) == 0 {
		return StartupDialogAnswer{}, observability.NewError(observability.CodeUsage,
			fmt.Sprintf("harness %q has no measured %s; mate refuses to invent one", p.Kind, screen))
	}
	return layouts[0].answer(), nil
}

// TargetSelected reports whether the structurally confirmed dialog is on
// screen with its highlight marker on the option the answer confirms - the
// one state in which the confirm key accepts rather than quits, or skips
// rather than runs a package install under the agent. It is false on a
// screen with no such dialog, so a caller cannot confirm into a composer by
// mistake.
func (p StartupProfile) TargetSelected(screen StartupScreen, snapshot string) bool {
	for _, d := range p.dialogsFor(screen) {
		if highlighted, parsed := d.parse(snapshot); parsed {
			return highlighted == d.Target
		}
	}
	return false
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
