package console

import (
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

func TestParseTranscriptClaudeCodeRecognizesRealMarkers(t *testing.T) {
	raw := "⏺ Reading the report before continuing.\n" +
		"⎿  Read 84 lines (ctrl+o to expand)\n" +
		"\n" +
		"✻ Waiting for the completion report (esc to interrupt)\n"

	got := ParseTranscript(query.HarnessClaude, raw)
	if got.Status != SessionTranscriptParsed {
		t.Fatalf("Status = %v, want Parsed", got.Status)
	}
	want := []SessionTranscriptEntry{
		{Kind: SessionTranscriptEntryTurn, Text: "Reading the report before continuing."},
		{Kind: SessionTranscriptEntryResult, Text: "Read 84 lines (ctrl+o to expand)"},
		{Kind: SessionTranscriptEntryGap},
		{Kind: SessionTranscriptEntryStatus, Text: "Waiting for the completion report", Hint: " (esc to interrupt)"},
	}
	if len(got.Entries) != len(want) {
		t.Fatalf("Entries = %#v, want %#v", got.Entries, want)
	}
	for i := range want {
		if got.Entries[i] != want[i] {
			t.Errorf("Entries[%d] = %#v, want %#v", i, got.Entries[i], want[i])
		}
	}
	if got.Raw != raw {
		t.Errorf("Raw must always be preserved even when Parsed, got %q", got.Raw)
	}
}

func TestParseTranscriptClaudeCodeJoinsContinuationLines(t *testing.T) {
	raw := "⏺ This turn wraps across\n  two lines of real terminal output.\n"
	got := ParseTranscript(query.HarnessClaude, raw)
	if got.Status != SessionTranscriptParsed {
		t.Fatalf("Status = %v, want Parsed", got.Status)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("Entries = %#v, want exactly one joined turn", got.Entries)
	}
	want := "This turn wraps across two lines of real terminal output."
	if got.Entries[0].Text != want {
		t.Errorf("Entries[0].Text = %q, want %q", got.Entries[0].Text, want)
	}
}

func TestParseTranscriptClaudeCodeFallsBackOnUnrecognizedOutput(t *testing.T) {
	got := ParseTranscript(query.HarnessClaude, "$ some shell prompt\nwith no markers at all\n")
	if got.Status != SessionTranscriptUnknown {
		t.Fatalf("Status = %v, want Unknown for output with no claude-code marker", got.Status)
	}
	if got.Entries != nil {
		t.Errorf("Entries = %#v, want nil when unparsed", got.Entries)
	}
	if got.Raw == "" {
		t.Error("Raw must be preserved for the unknown fallback to render")
	}
}

func TestParseTranscriptNonClaudeHarnessHasNoProfileYet(t *testing.T) {
	got := ParseTranscript(query.HarnessCodex, "⏺ this looks like claude-code output but the harness is codex\n")
	if got.Status != SessionTranscriptUnknown {
		t.Fatalf("Status = %v, want Unknown: no profile exists for Codex yet", got.Status)
	}
}

func TestParseTranscriptTrimsTrailingGaps(t *testing.T) {
	got := ParseTranscript(query.HarnessClaude, "⏺ one turn\n\n\n")
	if len(got.Entries) != 1 {
		t.Fatalf("Entries = %#v, want trailing blank lines dropped", got.Entries)
	}
}
