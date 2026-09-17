package send_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/send"
)

// capture loads one committed live capture. Every screen under
// testdata/screens is the verbatim stdout of `herdr agent read --source
// recent-unwrapped --lines 40 --format text` taken on 2026-09-17 against
// Claude Code 2.1.274 and codex-cli 0.154.0.
func capture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "screens", name+".txt"))
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
			kind:     harness.KindClaude,
			screen:   "claude_empty",
			want:     send.StateEmpty,
			evidence: "❯",
		},
		{
			name:     "claude composer holding a half typed line",
			kind:     harness.KindClaude,
			screen:   "claude_pending",
			want:     send.StatePending,
			pending:  "half typed",
			evidence: "❯ half typed",
		},
		{
			// The composer under the spinner is empty; only the ordering
			// of the checks keeps this out of StateEmpty.
			name:     "claude mid turn spinner over an empty composer",
			kind:     harness.KindClaude,
			screen:   "claude_busy",
			want:     send.StateBusy,
			evidence: "✶ Pollinating…",
		},
		{
			// Measured with no spinner on screen at all: the placeholder
			// is the only in-flight evidence this snapshot carries.
			name:     "claude mid turn with a queued message",
			kind:     harness.KindClaude,
			screen:   "claude_busy_queued",
			want:     send.StateBusy,
			evidence: "❯\u00a0Press up to edit queued messages",
		},
		{
			name:     "claude trust dialog is not a composer",
			kind:     harness.KindClaude,
			screen:   "claude_trust_dialog",
			want:     send.StateUnknown,
			evidence: "harness directory-trust dialog",
		},
		{
			name:     "codex empty composer shows its placeholder",
			kind:     harness.KindCodex,
			screen:   "codex_empty",
			want:     send.StateEmpty,
			evidence: "› Ask Codex to do anything",
		},
		{
			name:     "codex composer holding a half typed line",
			kind:     harness.KindCodex,
			screen:   "codex_pending",
			want:     send.StatePending,
			pending:  "half typed",
			evidence: "› half typed",
		},
		{
			// Codex redraws its placeholder while working, so the busy
			// line is what must decide this screen.
			name:     "codex mid turn",
			kind:     harness.KindCodex,
			screen:   "codex_busy",
			want:     send.StateBusy,
			evidence: "• Working (2s • esc to interrupt)",
		},
		{
			name:     "codex trust dialog is not a composer",
			kind:     harness.KindCodex,
			screen:   "codex_trust_dialog",
			want:     send.StateUnknown,
			evidence: "harness directory-trust dialog",
		},
		{
			// The model picker marks its current option with the same
			// glyph the composer uses.
			name:   "codex model picker modal is not a composer",
			kind:   harness.KindCodex,
			screen: "codex_modal",
			want:   send.StateUnknown,
		},
		{
			// A slash command typed but not yet submitted is the caller's
			// own pending text, popup and all.
			name:     "codex slash command popup is pending text",
			kind:     harness.KindCodex,
			screen:   "codex_slash_popup",
			want:     send.StatePending,
			pending:  "/model",
			evidence: "› /model",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := send.ClassifyComposer(tc.kind, capture(t, tc.screen))
			if err != nil {
				t.Fatalf("ClassifyComposer: %v", err)
			}
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

// TestClassifyComposerRefusesAnUnmeasuredHarness pins the fail-closed edge:
// a kind with no measured profile is an error, not a hopeful verdict.
func TestClassifyComposerRefusesAnUnmeasuredHarness(t *testing.T) {
	got, err := send.ClassifyComposer(harness.Kind("pi"), "❯\n")
	if err == nil {
		t.Fatalf("classifying an unmeasured harness succeeded with %q", got.State)
	}
	if got.State != send.StateUnknown {
		t.Fatalf("state on error = %q, want unknown", got.State)
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
			kind:   harness.KindClaude,
			screen: "some output\nmore output\n",
			want:   send.StateUnknown,
		},
		{
			name:   "claude echoed prompt is not the composer",
			kind:   harness.KindClaude,
			screen: "❯ an earlier prompt\nsome answer\n",
			want:   send.StateUnknown,
		},
		{
			name:   "claude composer inside its rules",
			kind:   harness.KindClaude,
			screen: rule + "\n❯ \n" + rule + "\n",
			want:   send.StateEmpty,
		},
		{
			name:   "a finished claude turn is not busy",
			kind:   harness.KindClaude,
			screen: "✻ Sautéed for 23s · done 7:17 PM\n" + rule + "\n❯ \n" + rule + "\n",
			want:   send.StateEmpty,
		},
		{
			name:   "a bullet list item is not a spinner",
			kind:   harness.KindClaude,
			screen: "· a point, and then some more…\n" + rule + "\n❯ \n" + rule + "\n",
			want:   send.StateEmpty,
		},
		{
			name:   "an interrupt hint far above the tail does not make a pane busy",
			kind:   harness.KindCodex,
			screen: "quoting: esc to interrupt\n" + manyLines(25) + "› Ask Codex to do anything\n\n  model · cwd\n",
			want:   send.StateEmpty,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := send.ClassifyComposer(tc.kind, tc.screen)
			if err != nil {
				t.Fatalf("ClassifyComposer: %v", err)
			}
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
