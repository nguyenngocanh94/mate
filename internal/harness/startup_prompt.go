package harness

import (
	"fmt"
	"regexp"
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
}

// Measured 2026-09-14 (ADR 0028) and 2026-09-18 (the Codex update prompt)
// through `herdr agent read --source recent-unwrapped` against codex-cli
// 0.154.0 and Claude Code 2.1.270; the captures are
// internal/harness/testdata/startup. Every string below is a literal from
// those captures, and TestClassifyStartupScreenOnCapturedScreens runs the
// classifier over the captures themselves.
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
	claudeTrustQuestion = "Is this a project you created or one you trust?"
	claudeTrustAccept   = "Yes, I trust this folder"
	claudeTrustQuit     = "No, exit"
	claudeTrustFooter   = "Enter to confirm · Esc to cancel"
	// ClaudeComposerMarker is the glyph Claude draws for both its highlight
	// and its composer; alone on a line it is the empty composer.
	ClaudeComposerMarker = "❯"
)

// optionSpec is one option label as the dialog draws it. prefix is set for a
// label whose tail is environment-specific (Codex spells its update command
// as `npm install -g @openai/codex` or the equivalent for another install
// method); the head still anchors at the start of the option line.
type optionSpec struct {
	label  string
	prefix bool
}

func (o optionSpec) matches(s string) bool {
	if o.prefix {
		return strings.HasPrefix(s, o.label)
	}
	return s == o.label
}

// dialogProfile is one measured modal's layout. The dialog is recognised by
// its shape, not by its words appearing somewhere: a modal drawn over the
// whole pane ends the screen with its footer, lists its options on adjacent
// lines directly above that footer with exactly one of them highlighted, and
// states its question within a few lines above them. An agent quoting this
// ADR, `cat` of a capture, or Codex's post-launch "update available" banner
// carries the same words but never that shape - the composer or the shell
// prompt follows the text.
type dialogProfile struct {
	// screen is what a match classifies as.
	screen   StartupScreen
	question string
	// options are the option labels in the order the dialog draws them,
	// top to bottom.
	options []optionSpec
	// target is the index in options this harness's answer selects.
	target int
	footer string
	marker string
	// questionWindow is how many non-empty lines above the first option the
	// question may sit (the measured dialogs put explanatory lines between).
	questionWindow int
	// selectKeys move the highlight from the drawn default onto target, one
	// press at a time.
	selectKeys []string
	// targetLabel is the target option as drawn, for messages.
	targetLabel string
}

func (d dialogProfile) answer() StartupDialogAnswer {
	return StartupDialogAnswer{
		SelectKeys:  append([]string(nil), d.selectKeys...),
		ConfirmKey:  "enter",
		TargetLabel: d.targetLabel,
	}
}

// startupProfile is one harness's measured startup screens: the modals it can
// draw, in the order they are checked, and what its empty composer looks like.
// Each harness's ScreenProfile reads through one (screen.go).
type startupProfile struct {
	// kind names the harness in refusals.
	kind     Kind
	dialogs  []dialogProfile
	composer func(lines []string) bool
}

// dialogsFor returns every profile that classifies as one screen: a harness
// can draw the same dialog in more than one measured layout (Codex's trust
// dialog before and after 0.156.1). Every layout of one screen is answered
// with the same keys (TestStartupDialogLayoutsOfOneScreenShareTheirAnswer).
func (p startupProfile) dialogsFor(screen StartupScreen) []dialogProfile {
	var out []dialogProfile
	for _, d := range p.dialogs {
		if d.screen == screen {
			out = append(out, d)
		}
	}
	return out
}

// codexStartup is codex-cli's measured startup screens.
func codexStartup() startupProfile {
	return startupProfile{
		kind: KindCodex,
		// The hook review (codex_hooks.go) is recognised here but
		// answered only by the settle's own walk through it, and only
		// for hooks mate names as its own.
		dialogs: append([]dialogProfile{
			{
				screen:   StartupScreenTrustDialog,
				question: codexTrustQuestion,
				options: []optionSpec{
					{label: codexTrustAccept},
					{label: codexTrustQuit},
				},
				target:         0,
				footer:         codexDialogFooter,
				marker:         codexHighlightMarker,
				questionWindow: 3,
				// The dialog opens with "1. Yes, continue" highlighted,
				// but the digit is pressed rather than trusted: the
				// re-read after it is what the accept check reads.
				selectKeys:  []string{"1"},
				targetLabel: codexTrustAccept,
			},
			{
				screen:   StartupScreenTrustDialog,
				question: codexTrustQuestionV156,
				options: []optionSpec{
					{label: codexTrustAcceptV156},
					{label: codexTrustQuitV156},
				},
				target: 0,
				footer: codexDialogFooterV156,
				marker: codexHighlightMarker,
				// The question opens a paragraph that wraps to three
				// lines at 93 columns; a narrower pane wraps it to more.
				questionWindow: 6,
				selectKeys:     []string{"1"},
				targetLabel:    codexTrustAcceptV156,
			},
			{
				screen:   StartupScreenUpdateDialog,
				question: codexUpdateNotice,
				options: []optionSpec{
					{label: codexUpdateNow, prefix: true},
					{label: codexUpdateSkip},
					{label: codexUpdateSkipNext},
				},
				// "3. Skip until next version" is the only option that
				// does not draw the prompt again on the next launch:
				// "2. Skip" returns immediately, and "1. Update now"
				// runs a package install under the agent.
				target:         2,
				footer:         codexDialogFooter,
				marker:         codexHighlightMarker,
				questionWindow: 3,
				// Measured 2026-09-18: the highlight opens on option 1
				// and `down` moves it one option at a time.
				selectKeys:  []string{"down", "down"},
				targetLabel: codexUpdateSkipNext,
			},
		}, hooksReviewDialogs()...),
		composer: func(lines []string) bool {
			for _, l := range lines {
				if strings.Contains(l, CodexComposerPlaceholder) {
					return true
				}
			}
			return false
		},
	}
}

// claudeStartup is Claude Code's measured startup screens.
func claudeStartup() startupProfile {
	return startupProfile{
		kind: KindClaude,
		dialogs: []dialogProfile{
			{
				screen:   StartupScreenTrustDialog,
				question: claudeTrustQuestion,
				options: []optionSpec{
					{label: claudeTrustQuit},
					{label: claudeTrustAccept},
				},
				target:         1,
				footer:         claudeTrustFooter,
				marker:         ClaudeComposerMarker,
				questionWindow: 6,
				selectKeys:     []string{"down"},
				targetLabel:    claudeTrustAccept,
			},
		},
		// The empty composer is the marker alone on its line. A shell
		// prompt ending in the same glyph ("… main ❯ claude") and a
		// highlighted option ("❯ No, exit") both carry text beside it.
		// From 2.1.282 the empty composer also shows a dim suggestion
		// after the marker; that shape counts only between the
		// composer's two rules (claudeComposerSuggestion).
		composer: func(lines []string) bool {
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

// optionLine reports whether a trimmed line is exactly one option label,
// optionally preceded by the highlight marker, and whether it is highlighted.
func (d dialogProfile) optionLine(trimmed string, o optionSpec) (isOption, highlighted bool) {
	if o.matches(trimmed) {
		return true, false
	}
	rest, ok := strings.CutPrefix(trimmed, d.marker)
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
func (d dialogProfile) parse(screen string) (highlighted int, ok bool) {
	lines := strings.Split(screen, "\n")
	var nonEmpty []int
	footerCount := 0
	counts := make([]int, len(d.options))
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		nonEmpty = append(nonEmpty, i)
		if t == d.footer {
			footerCount++
		}
		for k, o := range d.options {
			if isOpt, _ := d.optionLine(t, o); isOpt {
				counts[k]++
			}
		}
	}
	if footerCount != 1 || len(nonEmpty) < len(d.options)+2 {
		return 0, false
	}
	for _, c := range counts {
		if c != 1 {
			return 0, false
		}
	}
	last := nonEmpty[len(nonEmpty)-1]
	if strings.TrimSpace(lines[last]) != d.footer {
		return 0, false
	}
	// The last option is the non-empty line right above the footer (blank
	// lines between are allowed); the options above it sit on the rows
	// directly above, with no blank line anywhere between them.
	lastOpt := nonEmpty[len(nonEmpty)-2]
	firstOpt := lastOpt - (len(d.options) - 1)
	if firstOpt <= 0 {
		return 0, false
	}
	highlighted = -1
	for k, o := range d.options {
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
	for k := len(nonEmpty) - 1; k >= 0 && seen < d.questionWindow; k-- {
		if nonEmpty[k] >= firstOpt {
			continue
		}
		seen++
		if strings.Contains(lines[nonEmpty[k]], d.question) {
			return highlighted, true
		}
	}
	return 0, false
}

// classify names what a harness pane is showing right after launch. Dialogs
// are checked before the composer, because Claude's dialog and Claude's
// composer share the highlight glyph, and because Codex keeps an "update
// available" banner in its scrollback above the live composer.
func (p startupProfile) classify(screen string) StartupScreen {
	for _, d := range p.dialogs {
		if _, ok := d.parse(screen); ok {
			return d.screen
		}
	}
	if p.composer(strings.Split(screen, "\n")) {
		return StartupScreenReady
	}
	return StartupScreenUnrecognized
}

// answer is the measured key sequence for one dialog: the trust dialog's
// accept, or the update prompt's "3. Skip until next version" - the only
// option that both leaves the installed harness alone and stops the prompt
// returning before the next release.
func (p startupProfile) answer(screen StartupScreen) (StartupDialogAnswer, error) {
	layouts := p.dialogsFor(screen)
	if len(layouts) == 0 {
		return StartupDialogAnswer{}, observability.NewError(observability.CodeUsage,
			fmt.Sprintf("harness %q has no measured %s; mate refuses to invent one", p.kind, screen))
	}
	return layouts[0].answer(), nil
}

// targetSelected reports whether the structurally confirmed dialog is on
// screen with its highlight marker on the option the answer confirms - the
// one state in which the confirm key accepts rather than quits, or skips
// rather than runs a package install under the agent. It is false on a
// screen with no such dialog, so a caller cannot confirm into a composer by
// mistake.
func (p startupProfile) targetSelected(screen StartupScreen, snapshot string) bool {
	for _, d := range p.dialogsFor(screen) {
		if highlighted, parsed := d.parse(snapshot); parsed {
			return highlighted == d.target
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
