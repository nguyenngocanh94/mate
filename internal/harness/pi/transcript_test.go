package pi

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

const (
	maxSession = "2026-10-01T03-48-20-697Z_c954e7e3-b011-47e5-8ec7-bec609f00f90.jsonl"
	lowSession = "2026-10-01T03-54-06-652Z_3f0c5a8e-6b1d-4c2e-9a7f-2d4b8e1c0a11.jsonl"
)

func sessionPath(name string) string { return filepath.Join("testdata", "session", name) }

func readSession(t *testing.T, name string) harness.TranscriptBatch {
	t.Helper()
	b, err := piTranscripts{}.Read(harness.TranscriptReadRequest{Path: sessionPath(name)})
	if err != nil {
		t.Fatal(err)
	}
	if b.Malformed != nil {
		t.Fatalf("malformed: %+v", *b.Malformed)
	}
	return b
}

// The measured session with a tool call: three model calls, the first
// running bash, each with the usage the session recorded
// (`jq '.message.usage'`).
func TestPiTranscriptReadsTheMeasuredSessions(t *testing.T) {
	b := readSession(t, lowSession)
	want := []harness.TokenUsage{
		{Input: 1719, CacheRead: 384, Output: 61},
		{Input: 260, CacheRead: 1920, Output: 4},
		{Input: 167, CacheRead: 2048, Output: 223, Reasoning: 195},
	}
	if len(b.UsageTurns) != len(want) {
		t.Fatalf("%d usage turns, want %d", len(b.UsageTurns), len(want))
	}
	for i, u := range b.UsageTurns {
		if u.Usage != want[i] {
			t.Errorf("turn %d usage = %+v, want %+v", i, u.Usage, want[i])
		}
		if u.Model != "deepseek/deepseek-flash" {
			t.Errorf("turn %d model = %q", i, u.Model)
		}
		if u.EndedAt.Before(u.StartedAt) {
			t.Errorf("turn %d ends %v before it starts %v", i, u.EndedAt, u.StartedAt)
		}
	}
	if len(b.ToolCalls) != 1 || b.ToolCalls[0].ToolName != "bash" || b.ToolCalls[0].CommandClass != harness.CommandShell {
		t.Fatalf("tool calls = %+v, want one bash call", b.ToolCalls)
	}
	if len(b.ToolResults) != 1 || b.ToolResults[0].CallID != b.ToolCalls[0].SourceRef {
		t.Fatalf("tool results = %+v, want the bash call's", b.ToolResults)
	}
	if got := b.UsageTurns[0].Outcome; got != stopToolUse {
		t.Errorf("first call stopped for %q, want toolUse", got)
	}
	if b.ConsumedBytes != b.TotalBytes || b.PendingBytes != 0 {
		t.Errorf("consumed %d of %d", b.ConsumedBytes, b.TotalBytes)
	}
}

// A line pi is still writing is left for the next read.
func TestPiTranscriptLeavesAPartialLine(t *testing.T) {
	data, err := os.ReadFile(sessionPath(maxSession))
	if err != nil {
		t.Fatal(err)
	}
	whole := parseSession(data).batch
	cut := parseSession(data[:len(data)-10]).batch
	if cut.Malformed != nil || cut.PendingBytes == 0 || len(cut.Turns) != len(whole.Turns)-1 {
		t.Fatalf("cut read: %d turns of %d, %d pending, malformed %v", len(cut.Turns), len(whole.Turns), cut.PendingBytes, cut.Malformed)
	}
}

// pi clamps the requested thinking level without saying so; the level it
// applied is read back from the session and carried as the runtime's
// effort. Session c954e7e3 asked for xhigh and recorded max.
func TestPiTelemetryReportsTheAppliedThinkingLevel(t *testing.T) {
	for _, tc := range []struct{ file, level string }{{maxSession, "max"}, {lowSession, "low"}} {
		b := readSession(t, tc.file)
		up, err := piTranscripts{}.Telemetry(harness.TelemetryRequest{Path: sessionPath(tc.file), Changed: true, Batch: b})
		if err != nil {
			t.Fatal(err)
		}
		effort, model := "", ""
		for _, f := range up.Facts {
			if f.Kind == "context" {
				if f.Effort != "" {
					effort = f.Effort
				}
				if f.Model != "" {
					model = f.Model
				}
			}
		}
		if effort != tc.level || model != "deepseek/deepseek-flash" {
			t.Errorf("%s: applied effort %q on %q, want %q on deepseek/deepseek-flash", tc.file, effort, model, tc.level)
		}
		if up.Offset != b.ConsumedBytes {
			t.Errorf("%s: cursor %d, want %d", tc.file, up.Offset, b.ConsumedBytes)
		}
		again, err := piTranscripts{}.Telemetry(harness.TelemetryRequest{Path: sessionPath(tc.file), Offset: up.Offset, Batch: b})
		if err != nil || len(again.Facts) != 0 {
			t.Errorf("%s: an unchanged session gave %d facts (%v)", tc.file, len(again.Facts), err)
		}
	}
}

// A turn ends on an assistant message that did not stop to run tools.
func TestPiTurnEndsInTheSession(t *testing.T) {
	data, err := os.ReadFile(sessionPath(lowSession))
	if err != nil {
		t.Fatal(err)
	}
	turns := parseSession(data).batch.Turns
	last := turns[len(turns)-1].OccurredAt
	if !sessionTurnEnded(data, last.Add(-time.Millisecond)) {
		t.Fatal("the last turn's end was not seen")
	}
	if sessionTurnEnded(data, last) {
		t.Fatal("a turn end was seen after the last one")
	}
	// The first call stopped to run a tool: through its message and the
	// tool's result, no turn has ended.
	first := turns[0]
	if first.StopReason != stopToolUse {
		t.Fatalf("first call stopped with %q, want %q", first.StopReason, stopToolUse)
	}
	if sessionTurnEnded(data[:first.Offset], time.Time{}) {
		t.Fatal("a session holding only the prompt read as ended")
	}
	toolResult := lineEnd(data, lineEnd(data, first.Offset))
	if sessionTurnEnded(data[:toolResult], first.OccurredAt.Add(-time.Millisecond)) {
		t.Fatal("an assistant message that stopped to run a tool read as a turn end")
	}
}

// lineEnd is the offset just past the JSONL record that starts at off.
func lineEnd(data []byte, off int64) int64 {
	i := bytes.IndexByte(data[off:], '\n')
	if i < 0 {
		return int64(len(data))
	}
	return off + int64(i) + 1
}

func TestPiLocatesTheSessionByID(t *testing.T) {
	root := t.TempDir()
	src := piTranscripts{}
	if _, reason := src.Locate(harness.TranscriptLocateRequest{Root: root}); reason != harness.LocateNoSession {
		t.Fatalf("no session id: reason %q", reason)
	}
	req := harness.TranscriptLocateRequest{Root: root, SessionID: testSessionID, Cwd: "/w"}
	if _, reason := src.Locate(req); reason != harness.LocateNotFound {
		t.Fatalf("before the first prompt: reason %q", reason)
	}
	dir := SessionDir(root, "/w")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, lowSession)
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loc, reason := src.Locate(req)
	if reason != "" || loc.Path != path {
		t.Fatalf("Locate = %+v, %q; want %s", loc, reason, path)
	}
}
