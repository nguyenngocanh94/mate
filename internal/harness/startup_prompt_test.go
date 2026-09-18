package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func startupFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "startup", name))
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
		kind    Kind
		fixture string
		want    StartupScreen
	}{
		{KindCodex, "codex-0.154.0-trust-dialog.txt", StartupScreenTrustDialog},
		{KindCodex, "codex-0.154.0-trust-dialog-quit-selected.txt", StartupScreenTrustDialog},
		{KindCodex, "codex-0.154.0-ready.txt", StartupScreenReady},
		{KindClaude, "claude-2.1.270-trust-dialog.txt", StartupScreenTrustDialog},
		{KindClaude, "claude-2.1.270-trust-dialog-accept-selected.txt", StartupScreenTrustDialog},
		{KindClaude, "claude-2.1.270-ready.txt", StartupScreenReady},
		// A harness's dialog is not another harness's dialog: the wording is
		// matched per profile, never as "anything with Yes/No on it".
		{KindClaude, "codex-0.154.0-trust-dialog.txt", StartupScreenUnrecognized},
		{KindCodex, "claude-2.1.270-trust-dialog.txt", StartupScreenUnrecognized},
	}
	for _, tc := range cases {
		got, err := ClassifyStartupScreen(tc.kind, startupFixture(t, tc.fixture))
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.kind, tc.fixture, err)
		}
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
		for _, kind := range []Kind{KindClaude, KindCodex} {
			got, err := ClassifyStartupScreen(kind, screen)
			if err != nil {
				t.Fatal(err)
			}
			if got != StartupScreenUnrecognized {
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
	claude := startupFixture(t, "claude-2.1.270-trust-dialog.txt")
	reworded := strings.Replace(claude, "Yes, I trust this folder", "Yes, trust it", 1)
	got, err := ClassifyStartupScreen(KindClaude, reworded)
	if err != nil {
		t.Fatal(err)
	}
	if got != StartupScreenUnrecognized {
		t.Fatalf("reworded Claude dialog = %s, want unrecognized", got)
	}
	codex := startupFixture(t, "codex-0.154.0-trust-dialog.txt")
	reworded = strings.Replace(codex, "Do you trust the contents of this directory?", "Trust this directory?", 1)
	got, err = ClassifyStartupScreen(KindCodex, reworded)
	if err != nil {
		t.Fatal(err)
	}
	if got != StartupScreenUnrecognized {
		t.Fatalf("reworded Codex dialog = %s, want unrecognized", got)
	}
}

func TestClassifyStartupScreenRefusesUnknownHarness(t *testing.T) {
	t.Parallel()
	if _, err := ClassifyStartupScreen(Kind("gemini"), "anything"); err == nil {
		t.Fatal("a harness with no measured profile must be refused, not classified")
	}
	if _, err := TrustDialogAnswerFor(Kind("gemini")); err == nil {
		t.Fatal("no answer may exist for a harness with no measured dialog")
	}
}

// Claude puts the cursor on "No, exit"; Codex puts it on "1. Yes, continue".
// The answer therefore differs per harness, and neither is a bare Enter.
func TestTrustDialogAnswersSelectBeforeConfirming(t *testing.T) {
	t.Parallel()
	claude, err := TrustDialogAnswerFor(KindClaude)
	if err != nil {
		t.Fatal(err)
	}
	if len(claude.SelectKeys) != 1 || claude.SelectKeys[0] != "down" || claude.ConfirmKey != "enter" || claude.TargetLabel != "Yes, I trust this folder" {
		t.Fatalf("claude answer = %+v", claude)
	}
	codex, err := TrustDialogAnswerFor(KindCodex)
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
		kind    Kind
		fixture string
		want    bool
	}{
		{KindClaude, "claude-2.1.270-trust-dialog.txt", false},
		{KindClaude, "claude-2.1.270-trust-dialog-accept-selected.txt", true},
		{KindCodex, "codex-0.154.0-trust-dialog.txt", true},
		{KindCodex, "codex-0.154.0-trust-dialog-quit-selected.txt", false},
		// The ready screen has no dialog, so nothing is "selected" on it.
		{KindClaude, "claude-2.1.270-ready.txt", false},
		{KindCodex, "codex-0.154.0-ready.txt", false},
	}
	for _, tc := range cases {
		got, err := TrustDialogAcceptSelected(tc.kind, startupFixture(t, tc.fixture))
		if err != nil {
			t.Fatal(err)
		}
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
	got, err := ClassifyStartupScreen(KindClaude, dialog)
	if err != nil {
		t.Fatal(err)
	}
	if got != StartupScreenTrustDialog {
		t.Fatalf("dialog with two ❯ glyphs = %s, want trust_dialog", got)
	}
	shellOnly := "  /Users/x/proj   main ❯ \n"
	got, err = ClassifyStartupScreen(KindClaude, shellOnly)
	if err != nil {
		t.Fatal(err)
	}
	if got != StartupScreenUnrecognized {
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
	tail := StartupScreenTail(b.String(), 3)
	if tail != "line\nline\nLAST" {
		t.Fatalf("tail = %q", tail)
	}
	if StartupScreenTail("", 3) != "" {
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
		kind   Kind
		screen string
	}{
		// The reviewer's reproduction, verbatim, for each harness.
		{"codex reviewer reproduction", KindCodex, "Agent quoted ADR:\nDo you trust the contents of this directory?\n1. Yes, continue\n2. No, quit\n"},
		{"claude reviewer reproduction", KindClaude, "Agent quoted ADR:\nIs this a project you created or one you trust?\nNo, exit\nYes, I trust this folder\n"},
		// The captured dialog followed by a shell prompt: `cat` of the fixture.
		{"codex capture then shell prompt", KindCodex, codexDialog + "\n  /Users/x/proj   main ❯ \n"},
		{"claude capture then shell prompt", KindClaude, claudeDialog + "\n  /Users/x/proj   main ❯ cat fixture\n"},
		// Two dialogs' worth of words: ambiguous, so refused.
		{"codex dialog drawn twice", KindCodex, codexDialog + "\n" + codexDialog},
		{"claude dialog drawn twice", KindClaude, claudeDialog + "\n" + claudeDialog},
		// No highlight at all, or both options highlighted.
		{"codex no highlight", KindCodex, strings.Replace(codexDialog, "› 1. Yes, continue", "  1. Yes, continue", 1)},
		{"codex both highlighted", KindCodex, strings.Replace(codexDialog, "  2. No, quit", "› 2. No, quit", 1)},
		{"claude no highlight", KindClaude, strings.Replace(claudeDialog, " ❯ No, exit", "   No, exit", 1)},
		{"claude both highlighted", KindClaude, strings.Replace(claudeDialog, "   Yes, I trust this folder", " ❯ Yes, I trust this folder", 1)},
		// Options not adjacent, or the footer missing.
		{"codex options separated", KindCodex, strings.Replace(codexDialog, "› 1. Yes, continue\n  2. No, quit", "› 1. Yes, continue\n\n  2. No, quit", 1)},
		{"codex footer missing", KindCodex, strings.Replace(codexDialog, "Press enter to continue", "", 1)},
		{"claude footer missing", KindClaude, strings.Replace(claudeDialog, "Enter to confirm · Esc to cancel", "", 1)},
		// Option text embedded in a longer line is not an option line.
		{"codex options inside prose", KindCodex, "Do you trust the contents of this directory?\nthe options were › 1. Yes, continue and\n2. No, quit as before\nPress enter to continue\n"},
	}
	for _, tc := range cases {
		got, err := ClassifyStartupScreen(tc.kind, tc.screen)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got == StartupScreenTrustDialog {
			t.Fatalf("%s: classified as trust_dialog; a key would have been pressed into it:\n%s", tc.name, tc.screen)
		}
		selected, err := TrustDialogAcceptSelected(tc.kind, tc.screen)
		if err != nil {
			t.Fatal(err)
		}
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
	codex := startupFixture(t, "codex-0.154.0-trust-dialog.txt") + "\n› Ask Codex to do anything\n"
	got, err := ClassifyStartupScreen(KindCodex, codex)
	if err != nil || got != StartupScreenReady {
		t.Fatalf("codex quoted dialog over composer = %s err=%v, want ready", got, err)
	}
	claude := startupFixture(t, "claude-2.1.270-trust-dialog.txt") + "\n──────\n❯ \n──────\n"
	got, err = ClassifyStartupScreen(KindClaude, claude)
	if err != nil || got != StartupScreenReady {
		t.Fatalf("claude quoted dialog over composer = %s err=%v, want ready", got, err)
	}
}

// Codex's release-update prompt, measured 2026-09-18 with 0.154.0 installed
// and 0.155.0 published. It is drawn before the directory-trust dialog, so a
// launch that cannot name it never reaches the composer at all.
func TestClassifyStartupScreenOnCapturedUpdateDialogScreens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind    Kind
		fixture string
		want    StartupScreen
	}{
		{KindCodex, "codex_update_dialog.txt", StartupScreenUpdateDialog},
		{KindCodex, "codex_update_dialog_skip_selected.txt", StartupScreenUpdateDialog},
		// What the pane showed after Enter on "3. Skip until next version":
		// the directory-trust dialog for a directory codex had not seen.
		{KindCodex, "codex_update_dialog_after_enter.txt", StartupScreenTrustDialog},
		// The composer, with the update notice still in the scrollback as a
		// banner. The words are there; the shape is not.
		{KindCodex, "codex_update_banner_ready.txt", StartupScreenReady},
		// Claude has no measured update prompt, so Codex's is not its dialog.
		{KindClaude, "codex_update_dialog.txt", StartupScreenUnrecognized},
	}
	for _, tc := range cases {
		got, err := ClassifyStartupScreen(tc.kind, startupFixture(t, tc.fixture))
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.kind, tc.fixture, err)
		}
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
	answer, err := UpdateDialogAnswerFor(KindCodex)
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.SelectKeys) != 2 || answer.SelectKeys[0] != "down" || answer.SelectKeys[1] != "down" {
		t.Fatalf("select keys = %v, want two downs (the highlight opens on option 1)", answer.SelectKeys)
	}
	if answer.ConfirmKey != "enter" || answer.TargetLabel != "3. Skip until next version" {
		t.Fatalf("update answer = %+v", answer)
	}
	if _, err := UpdateDialogAnswerFor(KindClaude); err == nil {
		t.Fatal("Claude has no measured update prompt; no answer may be invented for it")
	}
	if _, err := UpdateDialogAnswerFor(Kind("gemini")); err == nil {
		t.Fatal("a harness with no profile must be refused")
	}
}

func TestUpdateDialogSkipSelectedReadsTheHighlightMarker(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind    Kind
		fixture string
		want    bool
	}{
		// As drawn: the highlight is on "1. Update now".
		{KindCodex, "codex_update_dialog.txt", false},
		// After the two measured downs.
		{KindCodex, "codex_update_dialog_skip_selected.txt", true},
		// Neither the trust dialog nor the composer has anything selected.
		{KindCodex, "codex_update_dialog_after_enter.txt", false},
		{KindCodex, "codex_update_banner_ready.txt", false},
		{KindClaude, "codex_update_dialog.txt", false},
	}
	for _, tc := range cases {
		got, err := UpdateDialogSkipSelected(tc.kind, startupFixture(t, tc.fixture))
		if err != nil {
			t.Fatal(err)
		}
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
		got, err := ClassifyStartupScreen(KindCodex, tc.screen)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got == StartupScreenUpdateDialog {
			t.Fatalf("%s: classified as update_dialog; a key would have been pressed into it:\n%s", tc.name, tc.screen)
		}
		selected, err := UpdateDialogSkipSelected(KindCodex, tc.screen)
		if err != nil {
			t.Fatal(err)
		}
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
	got, err := ClassifyStartupScreen(KindCodex, brew)
	if err != nil {
		t.Fatal(err)
	}
	if got != StartupScreenUpdateDialog {
		t.Fatalf("update dialog with another install command = %s, want update_dialog", got)
	}
}
