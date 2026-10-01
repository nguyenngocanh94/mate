package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
)

// screenOf is the ScreenProfile of a kind's profile in the catalog.
func screenOf(kind harness.Kind) harness.ScreenProfile {
	p, err := Default().Lookup(kind)
	if err != nil {
		panic(err)
	}
	return p.Screen()
}

// startupFixture reads a captured startup screen from the package of the
// harness that drew it, which its name begins with.
func startupFixture(t *testing.T, name string) string {
	t.Helper()
	kind, _, _ := strings.Cut(strings.ReplaceAll(name, "_", "-"), "-")
	raw, err := os.ReadFile(filepath.Join("..", kind, "testdata", "startup", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The classifier is measured against real screens, not against text invented
// for the test: every fixture under testdata/startup is a live capture (see
// its README) or a one-marker edit of one.
func TestClassifyStartupScreenOnCapturedScreens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind    harness.Kind
		fixture string
		want    harness.StartupScreen
	}{
		{codex.KindCodex, "codex-0.154.0-trust-dialog.txt", harness.StartupScreenTrustDialog},
		{codex.KindCodex, "codex-0.154.0-trust-dialog-quit-selected.txt", harness.StartupScreenTrustDialog},
		{codex.KindCodex, "codex-0.154.0-ready.txt", harness.StartupScreenReady},
		// codex-cli 0.156.1 redrew the trust dialog (task 35).
		{codex.KindCodex, "codex-0.156.1-trust-dialog.txt", harness.StartupScreenTrustDialog},
		{codex.KindCodex, "codex-0.156.1-trust-dialog-quit-selected.txt", harness.StartupScreenTrustDialog},
		{codex.KindCodex, "codex-0.156.1-ready.txt", harness.StartupScreenReady},
		{claude.KindClaude, "claude-2.1.270-trust-dialog.txt", harness.StartupScreenTrustDialog},
		{claude.KindClaude, "claude-2.1.270-trust-dialog-accept-selected.txt", harness.StartupScreenTrustDialog},
		{claude.KindClaude, "claude-2.1.270-ready.txt", harness.StartupScreenReady},
		// 2.1.282 draws a suggestion inside the empty composer.
		{claude.KindClaude, "claude-2.1.282-ready.txt", harness.StartupScreenReady},
		// `codex resume <id>` (task 35): the old conversation is replayed
		// above the composer, prompt lines and all.
		{codex.KindCodex, "codex-0.154.0-resume-ready.txt", harness.StartupScreenReady},
		// Codex's hook-trust review (task 37): recognised in both measured
		// layouts, and answered only by the settle's walk through the
		// review, for mate's own hook. The screens behind it are not
		// startup screens of their own.
		{codex.KindCodex, "codex-0.154.0-hooks-review.txt", harness.StartupScreenHooksReview},
		{codex.KindCodex, "codex-0.156.1-hooks-review.txt", harness.StartupScreenHooksReview},
		{codex.KindCodex, "codex-0.156.1-hooks-review-two.txt", harness.StartupScreenHooksReview},
		{codex.KindCodex, "codex-0.156.1-hooks-table-review.txt", harness.StartupScreenUnrecognized},
		{codex.KindCodex, "codex-0.156.1-hooks-sessionstart-own.txt", harness.StartupScreenUnrecognized},
		{codex.KindCodex, "codex-0.156.1-hooks-closed-ready.txt", harness.StartupScreenReady},
		// A harness's dialog is not another harness's dialog: the wording is
		// matched per profile, never as "anything with Yes/No on it".
		{claude.KindClaude, "codex-0.154.0-trust-dialog.txt", harness.StartupScreenUnrecognized},
		{codex.KindCodex, "claude-2.1.270-trust-dialog.txt", harness.StartupScreenUnrecognized},
	}
	for _, tc := range cases {
		got := screenOf(tc.kind).ClassifyStartup(startupFixture(t, tc.fixture))
		if got != tc.want {
			t.Fatalf("%s/%s = %s, want %s", tc.kind, tc.fixture, got, tc.want)
		}
	}
}

func TestClassifyStartupScreenIsUnrecognizedForAnythingElse(t *testing.T) {
	t.Parallel()
	for _, screen := range []string{
		"",
		"   \n\n",
		"  /Users/x/proj   main ❯ codex\n", // the shell prompt before the harness draws anything
		"╭──╮\n│ >_ OpenAI Codex (v0.154.0) │\n│ model: loading │\n╰──╯\n",          // the Codex banner still loading
		"Do you want to enable bypass permissions mode?\n ❯ No\n   Yes, I accept\n", // a different interactive prompt
		"Press Enter to continue\n",
	} {
		for _, kind := range []harness.Kind{claude.KindClaude, codex.KindCodex} {
			got := screenOf(kind).ClassifyStartup(screen)
			if got != harness.StartupScreenUnrecognized {
				t.Fatalf("%s classified %q as %s, want unrecognized", kind, screen, got)
			}
		}
	}
}

// A dialog with the wording changed is unrecognized, not "close enough": the
// answer sends keys into a pane, so the profile must match exactly what was
// measured.
func TestClassifyStartupScreenRefusesRewordedDialogs(t *testing.T) {
	t.Parallel()
	claudeDialog := startupFixture(t, "claude-2.1.270-trust-dialog.txt")
	reworded := strings.Replace(claudeDialog, "Yes, I trust this folder", "Yes, trust it", 1)
	got := screenOf(claude.KindClaude).ClassifyStartup(reworded)
	if got != harness.StartupScreenUnrecognized {
		t.Fatalf("reworded Claude dialog = %s, want unrecognized", got)
	}
	codexDialog := startupFixture(t, "codex-0.154.0-trust-dialog.txt")
	reworded = strings.Replace(codexDialog, "Do you trust the contents of this directory?", "Trust this directory?", 1)
	got = screenOf(codex.KindCodex).ClassifyStartup(reworded)
	if got != harness.StartupScreenUnrecognized {
		t.Fatalf("reworded Codex dialog = %s, want unrecognized", got)
	}
}

// Claude puts the cursor on "No, exit"; Codex puts it on "1. Yes, continue".
// The answer therefore differs per harness, and neither is a bare Enter.
func TestTrustDialogAnswersSelectBeforeConfirming(t *testing.T) {
	t.Parallel()
	claude, err := screenOf(claude.KindClaude).StartupAnswer(harness.StartupScreenTrustDialog)
	if err != nil {
		t.Fatal(err)
	}
	if len(claude.SelectKeys) != 1 || claude.SelectKeys[0] != "down" || claude.ConfirmKey != "enter" || claude.TargetLabel != "Yes, I trust this folder" {
		t.Fatalf("claude answer = %+v", claude)
	}
	codex, err := screenOf(codex.KindCodex).StartupAnswer(harness.StartupScreenTrustDialog)
	if err != nil {
		t.Fatal(err)
	}
	if len(codex.SelectKeys) != 1 || codex.SelectKeys[0] != "1" || codex.ConfirmKey != "enter" || codex.TargetLabel != "1. Yes, continue" {
		t.Fatalf("codex answer = %+v", codex)
	}
}

func TestTrustDialogAcceptSelectedReadsTheHighlightMarker(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind    harness.Kind
		fixture string
		want    bool
	}{
		{claude.KindClaude, "claude-2.1.270-trust-dialog.txt", false},
		{claude.KindClaude, "claude-2.1.270-trust-dialog-accept-selected.txt", true},
		{codex.KindCodex, "codex-0.154.0-trust-dialog.txt", true},
		{codex.KindCodex, "codex-0.154.0-trust-dialog-quit-selected.txt", false},
		{codex.KindCodex, "codex-0.156.1-trust-dialog.txt", true},
		{codex.KindCodex, "codex-0.156.1-trust-dialog-quit-selected.txt", false},
		{codex.KindCodex, "codex-0.156.1-ready.txt", false},
		// The ready screen has no dialog, so nothing is "selected" on it.
		{claude.KindClaude, "claude-2.1.270-ready.txt", false},
		{codex.KindCodex, "codex-0.154.0-ready.txt", false},
	}
	for _, tc := range cases {
		got := screenOf(tc.kind).StartupTargetSelected(harness.StartupScreenTrustDialog, startupFixture(t, tc.fixture))
		if got != tc.want {
			t.Fatalf("%s/%s accept selected = %v, want %v", tc.kind, tc.fixture, got, tc.want)
		}
	}
}

// The shell prompt in the captured Claude screens ends in the same "❯" glyph
// Claude uses for its highlight and its composer. Neither the prompt line
// ("… main ❯ claude") nor a highlight line ("❯ No, exit") is the composer.
func TestClaudeComposerIsALoneMarkerLine(t *testing.T) {
	t.Parallel()
	dialog := startupFixture(t, "claude-2.1.270-trust-dialog.txt")
	if strings.Count(dialog, "❯") < 2 {
		t.Fatalf("fixture no longer carries both the shell prompt and the highlight marker")
	}
	got := screenOf(claude.KindClaude).ClassifyStartup(dialog)
	if got != harness.StartupScreenTrustDialog {
		t.Fatalf("dialog with two ❯ glyphs = %s, want trust_dialog", got)
	}
	shellOnly := "  /Users/x/proj   main ❯ \n"
	got = screenOf(claude.KindClaude).ClassifyStartup(shellOnly)
	if got != harness.StartupScreenUnrecognized {
		t.Fatalf("shell prompt alone = %s, want unrecognized", got)
	}
}

func TestStartupScreenTailIsBoundedAndKeepsTheEnd(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line\n")
	}
	b.WriteString("LAST")
	tail := harness.StartupScreenTail(b.String(), 3)
	if tail != "line\nline\nLAST" {
		t.Fatalf("tail = %q", tail)
	}
	if harness.StartupScreenTail("", 3) != "" {
		t.Fatal("empty screen must have an empty tail")
	}
}

// PR 96 counter-review B1: the three dialog strings appearing somewhere on a
// screen is not the dialog. An agent quoting ADR 0028, or a transcript that
// repeats the wording, carries the same literals; pressing a key into that
// pane would be a destructive keypress into ordinary content. The dialog is
// recognised by its shape - footer last, adjacent options directly above it,
// exactly one highlighted, question within the measured window - and every
// screen below carries the words but not the shape.
func TestClassifyStartupScreenRefusesTheWordsWithoutTheShape(t *testing.T) {
	t.Parallel()
	codexDialog := startupFixture(t, "codex-0.154.0-trust-dialog.txt")
	claudeDialog := startupFixture(t, "claude-2.1.270-trust-dialog.txt")
	cases := []struct {
		name   string
		kind   harness.Kind
		screen string
	}{
		// The reviewer's reproduction, verbatim, for each harness.
		{"codex reviewer reproduction", codex.KindCodex, "Agent quoted ADR:\nDo you trust the contents of this directory?\n1. Yes, continue\n2. No, quit\n"},
		{"claude reviewer reproduction", claude.KindClaude, "Agent quoted ADR:\nIs this a project you created or one you trust?\nNo, exit\nYes, I trust this folder\n"},
		// The captured dialog followed by a shell prompt: `cat` of the fixture.
		{"codex capture then shell prompt", codex.KindCodex, codexDialog + "\n  /Users/x/proj   main ❯ \n"},
		{"claude capture then shell prompt", claude.KindClaude, claudeDialog + "\n  /Users/x/proj   main ❯ cat fixture\n"},
		// Two dialogs' worth of words: ambiguous, so refused.
		{"codex dialog drawn twice", codex.KindCodex, codexDialog + "\n" + codexDialog},
		{"claude dialog drawn twice", claude.KindClaude, claudeDialog + "\n" + claudeDialog},
		// No highlight at all, or both options highlighted.
		{"codex no highlight", codex.KindCodex, strings.Replace(codexDialog, "› 1. Yes, continue", "  1. Yes, continue", 1)},
		{"codex both highlighted", codex.KindCodex, strings.Replace(codexDialog, "  2. No, quit", "› 2. No, quit", 1)},
		{"claude no highlight", claude.KindClaude, strings.Replace(claudeDialog, " ❯ No, exit", "   No, exit", 1)},
		{"claude both highlighted", claude.KindClaude, strings.Replace(claudeDialog, "   Yes, I trust this folder", " ❯ Yes, I trust this folder", 1)},
		// Options not adjacent, or the footer missing.
		{"codex options separated", codex.KindCodex, strings.Replace(codexDialog, "› 1. Yes, continue\n  2. No, quit", "› 1. Yes, continue\n\n  2. No, quit", 1)},
		{"codex footer missing", codex.KindCodex, strings.Replace(codexDialog, "Press enter to continue", "", 1)},
		{"claude footer missing", claude.KindClaude, strings.Replace(claudeDialog, "Enter to confirm · Esc to cancel", "", 1)},
		// Option text embedded in a longer line is not an option line.
		{"codex options inside prose", codex.KindCodex, "Do you trust the contents of this directory?\nthe options were › 1. Yes, continue and\n2. No, quit as before\nPress enter to continue\n"},
	}
	for _, tc := range cases {
		got := screenOf(tc.kind).ClassifyStartup(tc.screen)
		if got == harness.StartupScreenTrustDialog {
			t.Fatalf("%s: classified as trust_dialog; a key would have been pressed into it:\n%s", tc.name, tc.screen)
		}
		selected := screenOf(tc.kind).StartupTargetSelected(harness.StartupScreenTrustDialog, tc.screen)
		if selected {
			t.Fatalf("%s: accept reported selected on a screen that is not the dialog", tc.name)
		}
	}
}

// A quoted dialog above a live composer is the composer: ready, never the
// dialog. Codex's placeholder and Claude's lone marker are what settle the
// screen, and neither harness draws its composer while its modal is up.
func TestClassifyStartupScreenQuotedDialogAboveTheComposerIsReady(t *testing.T) {
	t.Parallel()
	codexScreen := startupFixture(t, "codex-0.154.0-trust-dialog.txt") + "\n› Ask Codex to do anything\n"
	if got := screenOf(codex.KindCodex).ClassifyStartup(codexScreen); got != harness.StartupScreenReady {
		t.Fatalf("codex quoted dialog over composer = %s, want ready", got)
	}
	claudeScreen := startupFixture(t, "claude-2.1.270-trust-dialog.txt") + "\n──────\n❯ \n──────\n"
	if got := screenOf(claude.KindClaude).ClassifyStartup(claudeScreen); got != harness.StartupScreenReady {
		t.Fatalf("claude quoted dialog over composer = %s, want ready", got)
	}
}

// Codex's release-update prompt, measured 2026-09-18 with 0.154.0 installed
// and 0.155.0 published. It is drawn before the directory-trust dialog, so a
// launch that cannot name it never reaches the composer at all.
func TestClassifyStartupScreenOnCapturedUpdateDialogScreens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind    harness.Kind
		fixture string
		want    harness.StartupScreen
	}{
		{codex.KindCodex, "codex_update_dialog.txt", harness.StartupScreenUpdateDialog},
		{codex.KindCodex, "codex_update_dialog_skip_selected.txt", harness.StartupScreenUpdateDialog},
		// The same prompt drawn by `codex resume <id>` without the update
		// flag (task 35): history above, the dialog's shape at the bottom.
		{codex.KindCodex, "codex-0.154.0-resume-update-dialog.txt", harness.StartupScreenUpdateDialog},
		// What the pane showed after Enter on "3. Skip until next version":
		// the directory-trust dialog for a directory codex had not seen.
		{codex.KindCodex, "codex_update_dialog_after_enter.txt", harness.StartupScreenTrustDialog},
		// The composer, with the update notice still in the scrollback as a
		// banner. The words are there; the shape is not.
		{codex.KindCodex, "codex_update_banner_ready.txt", harness.StartupScreenReady},
		// Claude has no measured update prompt, so Codex's is not its dialog.
		{claude.KindClaude, "codex_update_dialog.txt", harness.StartupScreenUnrecognized},
	}
	for _, tc := range cases {
		got := screenOf(tc.kind).ClassifyStartup(startupFixture(t, tc.fixture))
		if got != tc.want {
			t.Fatalf("%s/%s = %s, want %s", tc.kind, tc.fixture, got, tc.want)
		}
	}
}

// "2. Skip" returns on the very next launch and "1. Update now" runs a
// package install under the agent, so the answer is "3. Skip until next
// version" and the presses are measured, not guessed.
func TestUpdateDialogAnswerSkipsUntilTheNextVersion(t *testing.T) {
	t.Parallel()
	answer, err := screenOf(codex.KindCodex).StartupAnswer(harness.StartupScreenUpdateDialog)
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.SelectKeys) != 2 || answer.SelectKeys[0] != "down" || answer.SelectKeys[1] != "down" {
		t.Fatalf("select keys = %v, want two downs (the highlight opens on option 1)", answer.SelectKeys)
	}
	if answer.ConfirmKey != "enter" || answer.TargetLabel != "3. Skip until next version" {
		t.Fatalf("update answer = %+v", answer)
	}
	if _, err := screenOf(claude.KindClaude).StartupAnswer(harness.StartupScreenUpdateDialog); err == nil {
		t.Fatal("Claude has no measured update prompt; no answer may be invented for it")
	}
}

func TestUpdateDialogSkipSelectedReadsTheHighlightMarker(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind    harness.Kind
		fixture string
		want    bool
	}{
		// As drawn: the highlight is on "1. Update now".
		{codex.KindCodex, "codex_update_dialog.txt", false},
		// After the two measured downs.
		{codex.KindCodex, "codex_update_dialog_skip_selected.txt", true},
		// Neither the trust dialog nor the composer has anything selected.
		{codex.KindCodex, "codex_update_dialog_after_enter.txt", false},
		{codex.KindCodex, "codex_update_banner_ready.txt", false},
		{claude.KindClaude, "codex_update_dialog.txt", false},
	}
	for _, tc := range cases {
		got := screenOf(tc.kind).StartupTargetSelected(harness.StartupScreenUpdateDialog, startupFixture(t, tc.fixture))
		if got != tc.want {
			t.Fatalf("%s/%s skip selected = %v, want %v", tc.kind, tc.fixture, got, tc.want)
		}
	}
}

// The update prompt is recognised by the same shape rule as the trust
// dialog: the footer is the last non-empty line, the three numbered options
// sit on the rows directly above it with exactly one highlighted, and the
// "Update available!" headline is within the measured window above them.
// Every screen below carries the words without the shape, and a keypress
// into any of them would land in ordinary content.
func TestClassifyStartupScreenUpdateRefusesTheWordsWithoutTheShape(t *testing.T) {
	t.Parallel()
	dialog := startupFixture(t, "codex_update_dialog.txt")
	cases := []struct {
		name   string
		screen string
	}{
		// The dialog quoted in a transcript above a live composer: ready.
		{"quoted above the composer", dialog + "\n› Ask Codex to do anything\n"},
		{"capture then shell prompt", dialog + "\n  /Users/x/proj   main ❯ cat capture\n"},
		{"drawn twice", dialog + "\n" + dialog},
		{"no highlight", strings.Replace(dialog, "› 1. Update now", "  1. Update now", 1)},
		{"two highlights", strings.Replace(dialog, "  3. Skip until next version", "› 3. Skip until next version", 1)},
		{"options separated", strings.Replace(dialog, "  2. Skip\n", "\n  2. Skip\n", 1)},
		{"footer missing", strings.Replace(dialog, "Press enter to continue", "", 1)},
		{"headline missing", strings.Replace(dialog, "Update available!", "News!", 1)},
		{"an option reworded", strings.Replace(dialog, "3. Skip until next version", "3. Skip for now", 1)},
		{"options inside prose", "✨ Update available! 0.154.0 -> 0.155.0\nit offered › 1. Update now and\n2. Skip, or 3. Skip until next version\nPress enter to continue\n"},
	}
	for _, tc := range cases {
		got := screenOf(codex.KindCodex).ClassifyStartup(tc.screen)
		if got == harness.StartupScreenUpdateDialog {
			t.Fatalf("%s: classified as update_dialog; a key would have been pressed into it:\n%s", tc.name, tc.screen)
		}
		selected := screenOf(codex.KindCodex).StartupTargetSelected(harness.StartupScreenUpdateDialog, tc.screen)
		if selected {
			t.Fatalf("%s: skip reported selected on a screen that is not the dialog", tc.name)
		}
	}
}

// Option 1's tail names the install command, which differs by install
// method (npm here, a package manager elsewhere). The head anchors the
// option line; the tail is not matched.
func TestClassifyStartupScreenUpdateDialogToleratesTheInstallCommand(t *testing.T) {
	t.Parallel()
	dialog := startupFixture(t, "codex_update_dialog.txt")
	brew := strings.Replace(dialog, "1. Update now (runs `npm install -g @openai/codex`)", "1. Update now (runs `brew upgrade codex`)", 1)
	if brew == dialog {
		t.Fatal("fixture no longer carries the npm install command")
	}
	got := screenOf(codex.KindCodex).ClassifyStartup(brew)
	if got != harness.StartupScreenUpdateDialog {
		t.Fatalf("update dialog with another install command = %s, want update_dialog", got)
	}
}

// TestClaudeComposerSuggestionIsStillEmpty: from 2.1.282 the empty composer
// carries a dim `Try "..."` suggestion after the marker. That is still the
// empty composer, whatever the suggestion says; anything else after the
// marker is text someone typed, and a composer with text in it is not ready.
// The line must sit inside the composer's two rules, so a shell prompt or a
// quoted capture that happens to start with the marker is not mistaken for it.
func TestClaudeComposerSuggestionIsStillEmpty(t *testing.T) {
	t.Parallel()
	ready := startupFixture(t, "claude-2.1.282-ready.txt")
	const suggestion = "❯\u00a0Try \"edit <filepath> to...\""
	if !strings.Contains(ready, suggestion) {
		t.Fatalf("fixture no longer carries the measured suggestion line %q", suggestion)
	}
	for _, tc := range []struct {
		name   string
		screen string
		want   harness.StartupScreen
	}{
		{"another suggestion", strings.Replace(ready, suggestion, "❯\u00a0Try \"refactor <filepath>\"", 1), harness.StartupScreenReady},
		{"typed text", strings.Replace(ready, suggestion, "❯\u00a0fix the login bug", 1), harness.StartupScreenUnrecognized},
		{"typed text that starts like a suggestion", strings.Replace(ready, suggestion, "❯\u00a0Try \"edit\" and then run the tests", 1), harness.StartupScreenUnrecognized},
		{"suggestion outside the composer rules", "  /tmp/x main ❯ Try \"edit <filepath> to...\"\n", harness.StartupScreenUnrecognized},
	} {
		if got := screenOf(claude.KindClaude).ClassifyStartup(tc.screen); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Claude never draws Codex's hook review, so its profile does not name it.
func TestClaudeDoesNotClassifyCodexsHookReview(t *testing.T) {
	t.Parallel()
	if got := screenOf(claude.KindClaude).ClassifyStartup(startupFixture(t, "codex-0.156.1-hooks-review.txt")); got != harness.StartupScreenUnrecognized {
		t.Errorf("claude classified Codex's hook review as %s", got)
	}
}
