package send_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/send"
)

// screenOf is the screen profile catalog.Default() registers for a kind.
func screenOf(kind harness.Kind) harness.ScreenProfile {
	profile, err := catalog.Default().Lookup(kind)
	if err != nil {
		panic(err)
	}
	return profile.Screen()
}

// capture loads one committed live capture. Every screen under
// testdata/screens is the verbatim stdout of `herdr agent read --source
// recent-unwrapped --lines 40 --format text` taken on 2026-09-17 against
// Claude Code 2.1.274 and codex-cli 0.154.0, except the three
// `claude_startup_splash*` captures, taken on 2026-09-19 against Claude Code
// 2.1.278 through the Console's own session stream (docs/mvp.md task 24), and
// the `codex_*_v157` captures, taken on 2026-09-26 against codex-cli 0.157.1
// and Herdr 0.8.2 in a lab session - `codex_busy_visible_v157` with
// `--source visible`, the read mate falls back to while Codex works.
func capture(t *testing.T, name string) string {
	t.Helper()
	return captureFile(t, name+".txt")
}

// captureFile loads a capture by its own file name, for the `.ansi` screens
// that carry the harness's styling.
func captureFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "screens", name))
	if err != nil {
		t.Fatalf("read capture %s: %v", name, err)
	}
	return string(data)
}

func TestClassifyComposerOnCapturedScreens(t *testing.T) {
	tests := []struct {
		name     string
		kind     harness.Kind
		screen   string
		want     send.ComposerState
		pending  string
		evidence string
	}{
		{
			name:     "claude empty composer",
			kind:     claude.KindClaude,
			screen:   "claude_empty",
			want:     send.StateEmpty,
			evidence: "❯",
		},
		{
			name:     "claude composer holding a half typed line",
			kind:     claude.KindClaude,
			screen:   "claude_pending",
			want:     send.StatePending,
			pending:  "half typed",
			evidence: "❯ half typed",
		},
		{
			// The composer under the spinner is empty; only the ordering
			// of the checks keeps this out of StateEmpty.
			name:     "claude mid turn spinner over an empty composer",
			kind:     claude.KindClaude,
			screen:   "claude_busy",
			want:     send.StateBusy,
			evidence: "✶ Pollinating…",
		},
		{
			// Measured with no spinner on screen at all: the placeholder
			// is the only in-flight evidence this snapshot carries.
			name:     "claude mid turn with a queued message",
			kind:     claude.KindClaude,
			screen:   "claude_busy_queued",
			want:     send.StateBusy,
			evidence: "❯\u00a0Press up to edit queued messages",
		},
		{
			name:     "claude trust dialog is not a composer",
			kind:     claude.KindClaude,
			screen:   "claude_trust_dialog",
			want:     send.StateUnknown,
			evidence: "harness directory-trust dialog",
		},
		{
			// docs/mvp.md task 24. A Mate that has not had a turn yet is
			// still showing Claude's welcome box, and the Console's stream
			// sizes its PTY to 65 columns (120 less the rail and its
			// divider, streamTerminalSize). At that width Claude's composer
			// rule is exactly as wide as the pane, so `--source
			// recent-unwrapped` joins the rule, the composer and the
			// closing rule into one line - and the composer under the
			// splash is an empty composer all the same.
			name:     "claude startup splash at the console's own pane width",
			kind:     claude.KindClaude,
			screen:   "claude_startup_splash",
			want:     send.StateEmpty,
			evidence: "❯",
		},
		{
			// The same screen with the reader's own half-typed line in it:
			// the joined rules must not turn somebody's text into an empty
			// composer that mate would type over.
			name:     "claude startup splash holding a half typed line",
			kind:     claude.KindClaude,
			screen:   "claude_startup_splash_pending",
			want:     send.StatePending,
			pending:  "half typed",
			evidence: "❯ half typed",
		},
		{
			// The same cold Mate at 80x24, where the rules happened to
			// survive on their own lines: the splash's banner is still
			// joined, and the verdict must be the same one.
			name:     "claude startup splash at 80x24",
			kind:     claude.KindClaude,
			screen:   "claude_startup_splash_80x24",
			want:     send.StateEmpty,
			evidence: "❯",
		},
		{
			name:     "codex empty composer shows its placeholder",
			kind:     codex.KindCodex,
			screen:   "codex_empty",
			want:     send.StateEmpty,
			evidence: "› Ask Codex to do anything",
		},
		{
			name:     "codex composer holding a half typed line",
			kind:     codex.KindCodex,
			screen:   "codex_pending",
			want:     send.StatePending,
			pending:  "half typed",
			evidence: "› half typed",
		},
		{
			// Codex redraws its placeholder while working, so the busy
			// line is what must decide this screen.
			name:     "codex mid turn",
			kind:     codex.KindCodex,
			screen:   "codex_busy",
			want:     send.StateBusy,
			evidence: "• Working (2s • esc to interrupt)",
		},
		{
			name:     "codex trust dialog is not a composer",
			kind:     codex.KindCodex,
			screen:   "codex_trust_dialog",
			want:     send.StateUnknown,
			evidence: "harness directory-trust dialog",
		},
		{
			// The model picker marks its current option with the same
			// glyph the composer uses.
			name:   "codex model picker modal is not a composer",
			kind:   codex.KindCodex,
			screen: "codex_modal",
			want:   send.StateUnknown,
		},
		{
			// codex-cli 0.157.1 draws a second footer line under the
			// model/cwd one: the shortcuts hint and a warning count. A
			// live Mate's every send to an idle Codex crew was refused as
			// an unnamed screen because of it (2026-09-26).
			name:     "codex 0.157 empty composer above a two-line footer",
			kind:     codex.KindCodex,
			screen:   "codex_empty_v157",
			want:     send.StateEmpty,
			evidence: "› Ask Codex to do anything",
		},
		{
			// The screen that crew showed: a finished turn, its time and a
			// tip above the composer.
			name:     "codex 0.157 composer after a finished turn",
			kind:     codex.KindCodex,
			screen:   "codex_after_turn_v157",
			want:     send.StateEmpty,
			evidence: "› Ask Codex to do anything",
		},
		{
			// Its two options and hint fit inside the composer window, so
			// only naming the dialog first keeps it from reading as a
			// composer holding "1. Trust and continue".
			name:     "codex 0.157 trust dialog is not a composer",
			kind:     codex.KindCodex,
			screen:   "codex_trust_dialog_v157",
			want:     send.StateUnknown,
			evidence: "harness directory-trust dialog",
		},
		{
			// Typing drops the hint and keeps the warning on that line.
			name:     "codex 0.157 composer holding a half typed line",
			kind:     codex.KindCodex,
			screen:   "codex_pending_v157",
			want:     send.StatePending,
			pending:  "half typed",
			evidence: "› half typed",
		},
		{
			// herdr refuses a recent-unwrapped read of a working Codex, so
			// this one is the `--source visible` read mate falls back to.
			name:     "codex 0.157 mid turn, read from the visible screen",
			kind:     codex.KindCodex,
			screen:   "codex_busy_visible_v157",
			want:     send.StateBusy,
			evidence: "• Working (5s • esc to interrupt) · 1 background terminal running · /ps to view · /stop to c…",
		},
		{
			// A slash command typed but not yet submitted is the caller's
			// own pending text, popup and all.
			name:     "codex slash command popup is pending text",
			kind:     codex.KindCodex,
			screen:   "codex_slash_popup",
			want:     send.StatePending,
			pending:  "/model",
			evidence: "› /model",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := send.ClassifyComposer(screenOf(tc.kind), capture(t, tc.screen))
			if got.State != tc.want {
				t.Fatalf("state = %q, want %q (evidence %q)", got.State, tc.want, got.Evidence)
			}
			if got.Pending != tc.pending {
				t.Fatalf("pending = %q, want %q", got.Pending, tc.pending)
			}
			if tc.evidence != "" && got.Evidence != tc.evidence {
				t.Fatalf("evidence = %q, want %q", got.Evidence, tc.evidence)
			}
		})
	}
}

// TestClassifyComposerReadsAFaintSuggestionAsAnEmptyComposer is the second
// debt docs/mvp.md task 24 pays, and the one the acceptance run found.
//
// The capture is `herdr agent read --format ansi` of a real Claude Code
// 2.1.278 Mate taken 2026-09-19, moments after it finished a turn that
// asked the captain which of two checkout pages a button should link to.
// Claude Code offered an answer inside the composer, drawn faint:
//
//	❯ \x1b[0m\x1b[2mUse checkout-express.html\x1b[0m
//
// The plain rendering of that same screen is indistinguishable from a
// half-typed human line, so `send.Send` refused to type over it and Ctrl+U
// could not clear it - there was nothing there to clear. Two live runs of
// the two-project acceptance deadlocked on exactly this.
// TestClassifyComposerIgnoresAnotherHarnessBusyLineQuotedByClaude is the
// capture of task 31's acceptance runs (Claude Code 2.1.281, Herdr 0.8.2,
// 2026-09-24): an idle Mate, finished ("Sautéed for 26s · done"), with a
// faint suggestion in its composer and, a few lines up, the output of a tool
// call that printed a working Codex crew's pane - `⎿  • Working (2s • esc
// to interrupt)`. The shared busy seed matched that quoted line, so every
// digest was refused as mid-turn until the 5-minute `wedged` incident, and
// the Mate was never woken.
func TestClassifyComposerIgnoresAnotherHarnessBusyLineQuotedByClaude(t *testing.T) {
	styled := captureFile(t, "claude_idle_quoting_codex_busy.ansi")
	plain := send.StripSGR(styled)
	if !strings.Contains(plain, "• Working (2s • esc to interrupt)") {
		t.Fatal("the capture no longer holds the quoted Codex busy line this test is about")
	}
	got := send.ClassifyComposer(claude.Claude{}.Screen(), styled)
	if got.State != send.StateEmpty || !strings.Contains(got.Evidence, "faint") {
		t.Fatalf("state = %q (evidence %q), want empty: an idle Claude quoting a busy Codex is idle", got.State, got.Evidence)
	}

	// A quoted Claude spinner - indented, as every quoted line is - is not
	// this pane's spinner either.
	quoted := strings.Replace(plain, "• Working (2s • esc to interrupt)", "✻ Pollinating…", 1)
	if got := send.ClassifyComposer(claude.Claude{}.Screen(), quoted); got.State == send.StateBusy {
		t.Fatalf("a quoted, indented spinner classified busy (evidence %q)", got.Evidence)
	}

	// The pane's own spinner, drawn at column 0 above the composer, is.
	own := strings.Replace(plain, "✻ Sautéed for 26s · done 11:20 AM", "✽ Transmuting… (42s · ↓ 1.4k tokens)", 1)
	if own == plain {
		t.Fatal("the capture no longer holds the finished line this test replaces")
	}
	if got := send.ClassifyComposer(claude.Claude{}.Screen(), own); got.State != send.StateBusy {
		t.Fatalf("the pane's own spinner classified %q, want busy", got.State)
	}
}

func TestClassifyComposerReadsAFaintSuggestionAsAnEmptyComposer(t *testing.T) {
	styled := captureFile(t, "claude_ghost_suggestion.ansi")

	got := send.ClassifyComposer(claude.Claude{}.Screen(), styled)
	if got.State != send.StateEmpty {
		t.Fatalf("state = %q, want empty (evidence %q, pending %q)", got.State, got.Evidence, got.Pending)
	}
	if got.Pending != "" {
		t.Fatalf("pending = %q; a suggestion is nobody's unsubmitted text", got.Pending)
	}
	if !strings.Contains(got.Evidence, "faint") {
		t.Fatalf("evidence = %q, want it to say the composer text was faint", got.Evidence)
	}

	// The same screen without its attributes is the read that could not
	// tell the difference, and it must still classify the conservative way:
	// unrecognised styling means the text might be a person's.
	plain := send.StripSGR(styled)
	if strings.Contains(plain, "\x1b") {
		t.Fatalf("StripSGR left escape bytes in the screen:\n%q", plain)
	}
	blind := send.ClassifyComposer(claude.Claude{}.Screen(), plain)
	if blind.State != send.StatePending || blind.Pending != "Use checkout-express.html" {
		t.Fatalf("the plain screen classifies %q/%q; without the attributes mate must assume the text is somebody's",
			blind.State, blind.Pending)
	}
}

// TestClassifyComposerKeepsTypedTextThatIsNotFaint is the other half of the
// same rule, and the one that matters: a line drawn in ordinary intensity is
// somebody's, whatever else is faint on the screen.
func TestClassifyComposerKeepsTypedTextThatIsNotFaint(t *testing.T) {
	rule := "──────────────────────────────"
	// A screen whose status footer is faint - as Claude's really is - and
	// whose composer holds plain white text.
	screen := rule + "\r\n❯ \x1b[0m\x1b[38;2;255;255;255mhalf typed\x1b[0m\r\n" + rule +
		"\r\n  \x1b[0m\x1b[2mFable 5.1 · high | tok 0 in / 0 out\x1b[0m\r\n"
	got := send.ClassifyComposer(claude.Claude{}.Screen(), screen)
	if got.State != send.StatePending || got.Pending != "half typed" {
		t.Fatalf("state = %q, pending = %q, want pending/half typed", got.State, got.Pending)
	}

	// And a composer holding both: one faint rune is not enough to make the
	// whole line the harness's.
	mixed := rule + "\r\n❯ \x1b[2mUse \x1b[22mthe classic page\x1b[0m\r\n" + rule + "\r\n"
	got = send.ClassifyComposer(claude.Claude{}.Screen(), mixed)
	if got.State != send.StatePending {
		t.Fatalf("state = %q, want pending: only a wholly faint line is a suggestion", got.State)
	}
}

// TestClassifyComposerOnSyntheticEdges covers the shapes the live captures
// cannot be made to hold on demand.
func TestClassifyComposerOnSyntheticEdges(t *testing.T) {
	rule := "──────────────────────────────"
	tests := []struct {
		name   string
		kind   harness.Kind
		screen string
		want   send.ComposerState
	}{
		{
			name:   "claude scrolled transcript with no composer box",
			kind:   claude.KindClaude,
			screen: "some output\nmore output\n",
			want:   send.StateUnknown,
		},
		{
			name:   "claude echoed prompt is not the composer",
			kind:   claude.KindClaude,
			screen: "❯ an earlier prompt\nsome answer\n",
			want:   send.StateUnknown,
		},
		{
			name:   "claude composer inside its rules",
			kind:   claude.KindClaude,
			screen: rule + "\n❯ \n" + rule + "\n",
			want:   send.StateEmpty,
		},
		{
			name:   "a finished claude turn is not busy",
			kind:   claude.KindClaude,
			screen: "✻ Sautéed for 23s · done 7:17 PM\n" + rule + "\n❯ \n" + rule + "\n",
			want:   send.StateEmpty,
		},
		{
			name:   "a bullet list item is not a spinner",
			kind:   claude.KindClaude,
			screen: "· a point, and then some more…\n" + rule + "\n❯ \n" + rule + "\n",
			want:   send.StateEmpty,
		},
		{
			// The shape the unwrapped read hands back at the Console's own
			// pane width: one line carrying both rules and the composer.
			name:   "claude composer whose rules were joined onto its own line",
			kind:   claude.KindClaude,
			screen: "  " + rule + "❯   " + rule + "\n  status line\n",
			want:   send.StateEmpty,
		},
		{
			name:   "claude joined rules around a composer holding text",
			kind:   claude.KindClaude,
			screen: "  " + rule + "❯ half typed  " + rule + "\n  status line\n",
			want:   send.StatePending,
		},
		{
			// A rule is structure, not content: a short run of the same
			// rune inside a sentence must not split a line and manufacture
			// a composer out of an echoed prompt.
			name:   "a short run of the rule rune is not a rule",
			kind:   claude.KindClaude,
			screen: "a ───── b ❯ an earlier prompt ───── c\nmore output\n",
			want:   send.StateUnknown,
		},
		{
			// The highlighted option of a dialog carries the composer's own
			// glyph, and a dialog drawn under a rule must still not be read
			// as a composer: the option below it is what a composer never
			// has.
			name:   "a dialog option under a rule is not a composer",
			kind:   claude.KindClaude,
			screen: rule + "\n❯ No, exit\n  Yes, I trust this folder\n" + rule + "\n",
			want:   send.StateUnknown,
		},
		{
			name:   "an interrupt hint far above the tail does not make a pane busy",
			kind:   codex.KindCodex,
			screen: "quoting: esc to interrupt\n" + manyLines(25) + "› Ask Codex to do anything\n\n  model · cwd\n",
			want:   send.StateEmpty,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := send.ClassifyComposer(screenOf(tc.kind), tc.screen)
			if got.State != tc.want {
				t.Fatalf("state = %q, want %q (evidence %q)", got.State, tc.want, got.Evidence)
			}
		})
	}
}

func manyLines(n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += "filler\n"
	}
	return out
}

// TestClassifyCodexComposerIgnoresFooterHeight is the property the 0.157.1
// break was missing: whatever chrome a future Codex stacks under its
// composer, an idle composer is still found, and a menu is still not one.
// The screens are the measured 0.157.1 capture with lines added under its
// footer, so everything above the footer is real.
func TestClassifyCodexComposerIgnoresFooterHeight(t *testing.T) {
	base := strings.TrimRight(capture(t, "codex_empty_v157"), "\r\n")
	for extra := 0; extra <= 4; extra++ {
		screen := base
		for i := 0; i < extra; i++ {
			screen += "\n  some future footer line " + strings.Repeat("·", i+1)
		}
		got := send.ClassifyComposer(codex.Codex{}.Screen(), screen)
		if got.State != send.StateEmpty {
			t.Fatalf("%d extra footer line(s): state = %q, want empty", extra, got.State)
		}
	}
}

// TestClassifyCodexComposerFailsClosedOnMenus pins the other half: a `›`
// with menu options below it is never a composer, and a `›` line of any
// other kind can at worst be read as pending text, which is a refusal.
func TestClassifyCodexComposerFailsClosedOnMenus(t *testing.T) {
	tests := []struct {
		name   string
		screen string
		want   send.ComposerState
	}{
		{
			name:   "a two-option menu with its hint",
			screen: "  Pick one\n\n› 1. Continue\n  2. Stop\n\n  enter confirm · esc back",
			want:   send.StateUnknown,
		},
		{
			name:   "a menu whose highlighted row is its last option",
			screen: "  Pick one\n\n  1. Continue\n› 2. Stop\n\n  enter confirm · esc back\n  status line",
			want:   send.StateUnknown,
		},
		{
			name:   "an unnumbered dialog option",
			screen: "  Allow this command?\n\n› Yes, run it\n  No, tell Codex what to do\n\n  enter confirm",
			want:   send.StatePending,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := send.ClassifyComposer(codex.Codex{}.Screen(), tc.screen)
			if got.State != tc.want {
				t.Fatalf("state = %q, want %q (evidence %q)", got.State, tc.want, got.Evidence)
			}
			if got.State == send.StateEmpty {
				t.Fatal("a menu was read as an empty composer")
			}
		})
	}
}
