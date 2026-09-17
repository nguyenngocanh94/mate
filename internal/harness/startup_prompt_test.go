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
	if len(claude.SelectKeys) != 1 || claude.SelectKeys[0] != "down" || claude.ConfirmKey != "enter" || claude.AcceptLabel != "Yes, I trust this folder" {
		t.Fatalf("claude answer = %+v", claude)
	}
	codex, err := TrustDialogAnswerFor(KindCodex)
	if err != nil {
		t.Fatal(err)
	}
	if len(codex.SelectKeys) != 1 || codex.SelectKeys[0] != "1" || codex.ConfirmKey != "enter" || codex.AcceptLabel != "1. Yes, continue" {
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
