package grok

import (
	"os"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/session/updates.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGrokTranscriptReadsTheMeasuredTurn(t *testing.T) {
	b, err := grokTranscripts{}.Read(harness.TranscriptReadRequest{Path: "testdata/session/updates.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Malformed != nil {
		t.Fatalf("malformed: %+v", *b.Malformed)
	}
	if b.Source != TranscriptGrok || b.ConsumedBytes != b.TotalBytes || b.PendingBytes != 0 {
		t.Fatalf("batch source %q consumed %d of %d", b.Source, b.ConsumedBytes, b.TotalBytes)
	}
	if len(b.UsageTurns) != 1 {
		t.Fatalf("%d usage turns, want 1", len(b.UsageTurns))
	}
	u := b.UsageTurns[0]
	want := harness.TokenUsage{Input: 20231, CacheRead: 1664, CacheWrite: 0, Output: 137, Reasoning: 136}
	if u.Usage != want {
		t.Fatalf("usage = %+v, want %+v", u.Usage, want)
	}
	if u.ContextTokens != 21895 || u.Usage.Input+u.Usage.CacheRead+u.Usage.CacheWrite+u.Usage.Output != 22032 {
		t.Fatalf("context %d, four-bucket sum %d", u.ContextTokens, u.Usage.Input+u.Usage.CacheRead+u.Usage.CacheWrite+u.Usage.Output)
	}
	if u.Model != "grok-4.7-build" || u.Outcome != "end_turn" || u.SourceRef != "6569252e-7d3f-4222-b27a-871e6d00b3b9" || u.HarnessTurnRef != u.SourceRef {
		t.Fatalf("turn = %+v", u)
	}
	if len(b.Turns) != 1 || b.Turns[0].Text != "pong" {
		t.Fatalf("turns = %+v, want the answer pong", b.Turns)
	}
	ended := time.UnixMilli(1791085829886).UTC()
	if !u.EndedAt.Equal(ended) || !u.StartedAt.Equal(ended.Add(-3986*time.Millisecond)) {
		t.Fatalf("started %s ended %s", u.StartedAt, u.EndedAt)
	}
	up, err := grokTranscripts{}.Telemetry(harness.TelemetryRequest{Changed: true, Batch: b})
	if err != nil {
		t.Fatal(err)
	}
	if len(up.Facts) != 1 || up.Facts[0].Kind != "response" || up.Facts[0].OutputTokens != 137 || up.Offset != b.ConsumedBytes {
		t.Fatalf("telemetry = %+v", up)
	}
	for _, gap := range []string{"tool_events_not_read", "model_calls_are_turn_totals"} {
		found := false
		for _, g := range up.Gaps {
			if g == gap {
				found = true
			}
		}
		if !found {
			t.Errorf("gaps %q missing %s", up.Gaps, gap)
		}
	}
	again, err := grokTranscripts{}.Telemetry(harness.TelemetryRequest{Offset: up.Offset, Batch: b})
	if err != nil || len(again.Facts) != 0 || len(again.Gaps) != 2 {
		t.Fatalf("unchanged telemetry = %+v, %v", again, err)
	}
}

func TestGrokTurnEndsOnTurnCompleted(t *testing.T) {
	data := fixture(t)
	ended := time.UnixMilli(1791085829886).UTC()
	if !sessionTurnEnded(data, ended.Add(-time.Millisecond)) {
		t.Fatal("the turn's end was not seen")
	}
	if sessionTurnEnded(data, ended) {
		t.Fatal("a turn end was seen after the only one")
	}
	if !(grokTurnEnd{}).EndsInTranscript() || (grokTurnEnd{}).LogsAnswers() {
		t.Fatal("turn-end evidence flags drifted")
	}
}

func TestGrokTranscriptStopsBeforeMalformedJSON(t *testing.T) {
	data := fixture(t)
	bad := append(append([]byte{}, data...), []byte("{not json}\n")...)
	b := parseUpdates(bad)
	if b.Malformed == nil || b.Malformed.Reason != harness.MalformedInvalidJSON || b.Malformed.Offset != int64(len(data)) {
		t.Fatalf("malformed = %+v, want invalid json at %d", b.Malformed, len(data))
	}
	if b.ConsumedBytes != int64(len(data)) || len(b.UsageTurns) != 1 {
		t.Fatalf("consumed %d, turns %d; the bad line was consumed or the good turn was dropped", b.ConsumedBytes, len(b.UsageTurns))
	}
}

func TestGrokTranscriptLeavesAPartialLine(t *testing.T) {
	data := fixture(t)
	whole := parseUpdates(data)
	cut := parseUpdates(data[:len(data)-8])
	if cut.Malformed != nil || cut.PendingBytes == 0 || len(cut.UsageTurns) != len(whole.UsageTurns)-1 {
		t.Fatalf("cut read: %d turns of %d, %d pending, malformed %v", len(cut.UsageTurns), len(whole.UsageTurns), cut.PendingBytes, cut.Malformed)
	}
}

func TestGrokTranscriptRefusesCacheLargerThanInput(t *testing.T) {
	const line = `{"timestamp":1791085829,"params":{"update":{"sessionUpdate":"turn_completed","prompt_id":"6569252e-7d3f-4222-b27a-871e6d00b3b9","stop_reason":"end_turn","elapsed_ms":1,"usage":{"inputTokens":10,"outputTokens":1,"totalTokens":11,"cachedReadTokens":20,"cacheCreationTokens":0,"reasoningTokens":0,"modelUsage":{"m":{"inputTokens":10,"outputTokens":1,"totalTokens":11,"cachedReadTokens":20,"cacheCreationTokens":0,"reasoningTokens":0}}}},"_meta":{"agentTimestampMs":1791085829886}}}
`
	b := parseUpdates([]byte(line))
	if b.Malformed == nil || b.Malformed.Reason != harness.MalformedUsageSnapshot || b.ConsumedBytes != 0 || len(b.UsageTurns) != 0 {
		t.Fatalf("cache overrun: malformed %+v, consumed %d, turns %d", b.Malformed, b.ConsumedBytes, len(b.UsageTurns))
	}
}
