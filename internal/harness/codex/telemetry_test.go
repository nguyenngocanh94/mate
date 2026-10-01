package codex

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

func telemetryLine(kind string, payload any) string {
	b, _ := json.Marshal(map[string]any{"timestamp": "2026-09-28T01:00:01Z", "type": kind, "payload": payload})
	return string(b) + "\n"
}

func TestCodexTelemetryNativeFailureAndLatePollAcrossCursor(t *testing.T) {
	start := telemetryLine("event_msg", map[string]any{"type": "task_started", "turn_id": "prompt-1"}) +
		telemetryLine("turn_context", map[string]any{"model": "gpt-6-sol", "effort": "high", "turn_id": "prompt-1"}) +
		telemetryLine("response_item", map[string]any{"type": "custom_tool_call", "call_id": "wrapper", "name": "exec", "input": "const r = await tools.write_stdin({session_id:93735,max_output_tokens:100});text(r);"})
	b := ParseCodexTelemetry(harness.TelemetryState{}, []byte(start), 0)
	if b.Error != "" || b.Consumed != int64(len(start)) {
		t.Fatalf("parse: %s %d", b.Error, b.Consumed)
	}
	encoded, _ := json.Marshal(b.State)
	var resumed harness.TelemetryState
	if err := json.Unmarshal(encoded, &resumed); err != nil {
		t.Fatal(err)
	}
	result := `{"chunk_id":"chunk-a","session_id":93735,"exit_code":1,"output":"failed: missing SDK\n"}`
	tail := telemetryLine("event_msg", map[string]any{"type": "item_completed", "turn_id": "prompt-1", "started_at_ms": 1790557200000, "completed_at_ms": 1790557201000, "item": map[string]any{"type": "CommandExecution", "id": "command-a", "process_id": "93735", "command": []string{"/bin/zsh", "-lc", "xcodebuild test"}, "cwd": "file:///work/repo", "status": "failed", "aggregated_output": "failed: missing SDK\n", "exit_code": 1, "duration": map[string]int{"secs": 1, "nanos": 0}}}) +
		telemetryLine("response_item", map[string]any{"type": "custom_tool_call_output", "call_id": "wrapper", "output": []map[string]string{{"type": "input_text", "text": "Script completed\n"}, {"type": "input_text", "text": result}}})
	b = ParseCodexTelemetry(resumed, []byte(tail), int64(len(start)))
	if b.Error != "" {
		t.Fatal(b.Error)
	}
	execution := telemetryKind(t, b.Facts, "execution")
	if execution.ExitCode == nil || *execution.ExitCode != 1 || execution.Status != "failed" || execution.Command != "xcodebuild test" || execution.CWD != "/work/repo" {
		t.Fatalf("native execution lost: %+v", execution)
	}
	if execution.WrapperRef != "" {
		t.Fatal("native command fabricated wrapper correlation")
	}
	poll := telemetryKind(t, b.Facts, "tool_result")
	if !poll.Poll || poll.ProcessID != "93735" || poll.HarnessTurnRef != "prompt-1" || poll.StartedAt == nil {
		t.Fatalf("late poll lost its opener: %+v", poll)
	}
	if poll.Output.NewBytes == nil || *poll.Output.NewBytes != 20 || poll.ExitCode == nil || *poll.ExitCode != 1 {
		t.Fatalf("poll measurement: %+v", poll)
	}
	if len(b.State.Wrappers) != 0 {
		t.Fatal("completed wrapper retained in cursor")
	}
}

func TestCodexTelemetryUsageIsEvidenceWithExactLedgerAlias(t *testing.T) {
	usage := map[string]int{"input_tokens": 100, "cached_input_tokens": 80, "cache_write_input_tokens": 0, "output_tokens": 20, "reasoning_output_tokens": 5, "total_tokens": 120}
	native := telemetryLine("token_usage_record", map[string]any{"turn_id": "prompt", "root_turn_id": "root", "response_id": "response", "usage": usage, "thread_token_usage": usage})
	b := ParseCodexTelemetry(harness.TelemetryState{}, []byte(native), 0)
	response := telemetryKind(t, b.Facts, "response")
	if response.InputTokens != 20 || response.CacheReadTokens != 80 || response.OutputTokens != 20 || response.ReasoningTokens != 5 || response.ContextTokens != 100 {
		t.Fatalf("usage buckets overlap: %+v", response)
	}
	if response.LedgerRefOffset != nil {
		t.Fatal("guessed a ledger row before token_count")
	}
	tail := telemetryLine("event_msg", map[string]any{"type": "token_count", "info": map[string]any{"total_token_usage": usage}})
	b = ParseCodexTelemetry(b.State, []byte(tail), int64(len(native)))
	alias := telemetryKind(t, b.Facts, "response")
	if alias.ID != response.ID || alias.LedgerRefOffset == nil || *alias.LedgerRefOffset != int64(len(native)) || alias.SourceOffset != 0 {
		t.Fatalf("not an exact cumulative alias: %+v", alias)
	}
	if len(b.State.Responses) != 0 {
		t.Fatal("matched response retained")
	}
}

func TestCodexTelemetryPartialMalformedAndUnknownTimestamps(t *testing.T) {
	line := telemetryLine("event_msg", map[string]any{"type": "item_completed", "turn_id": "p", "item": map[string]any{"type": "CommandExecution", "id": "cmd", "process_id": "123", "status": "inProgress", "command": []string{"test"}}})
	b := ParseCodexTelemetry(harness.TelemetryState{}, []byte(line[:len(line)-1]), 0)
	if b.Consumed != 0 || len(b.Facts) != 0 {
		t.Fatal("consumed a partial JSONL line")
	}
	b = ParseCodexTelemetry(b.State, []byte(line+"{broken}\n"), 0)
	if b.Consumed != int64(len(line)) || b.Error == "" {
		t.Fatalf("malformed cursor advanced: %+v", b)
	}
	f := telemetryKind(t, b.Facts, "execution")
	if f.StartedAt != nil || f.CompletedAt != nil || f.DurationMS != nil || f.ExitCode != nil || f.Output != nil {
		t.Fatalf("unknown measurements became zero/success: %+v", f)
	}
}

func TestCodexTelemetryReasoningContentIsNotPersisted(t *testing.T) {
	line := telemetryLine("event_msg", map[string]any{"type": "item_completed", "turn_id": "p", "item": map[string]any{"type": "Reasoning", "id": "r", "summary_text": []string{"private marker"}, "raw_content": []string{"private marker"}}})
	b := ParseCodexTelemetry(harness.TelemetryState{}, []byte(line), 0)
	data, _ := json.Marshal(b)
	if strings.Contains(string(data), "private marker") {
		t.Fatal("reasoning content leaked")
	}
}

func TestCodexTelemetryObservedInstructionsKeepOnlyMetadata(t *testing.T) {
	text := "instructions with private fixture marker"
	line := telemetryLine("session_meta", map[string]any{"cli_version": "0.156.1", "base_instructions": map[string]any{"text": text}}) + telemetryLine("world_state", map[string]any{"state": map[string]any{"agents_md": map[string]any{"directory": "/work/repo", "text": text}, "host_skills": map[string]any{"body": text}}})
	b := ParseCodexTelemetry(harness.TelemetryState{}, []byte(line), 0)
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "private fixture marker") {
		t.Fatal("instruction content persisted instead of metadata")
	}
	observed := 0
	for _, f := range b.Facts {
		if f.Phase == "instruction_input" {
			observed++
			if f.Output == nil || f.Output.Bytes != int64(len(text)) || f.Output.SHA256 == "" || f.Output.Preview != "" {
				t.Fatalf("invalid observed instruction metadata: %+v", f)
			}
		}
	}
	if observed != 3 {
		t.Fatalf("observed instruction inputs=%d", observed)
	}
}

func TestCodexTelemetryUnmatchedUsageStateIsBounded(t *testing.T) {
	var data strings.Builder
	for n := 0; n < 150; n++ {
		usage := map[string]int{"input_tokens": n, "total_tokens": n}
		data.WriteString(telemetryLine("token_usage_record", map[string]any{"response_id": fmt.Sprint(n), "usage": usage, "thread_token_usage": usage}))
	}
	b := ParseCodexTelemetry(harness.TelemetryState{}, []byte(data.String()), 0)
	if len(b.State.Responses) != 128 {
		t.Fatalf("pending correlations=%d", len(b.State.Responses))
	}
	responses, gaps := 0, 0
	for _, f := range b.Facts {
		if f.Kind == "response" {
			responses++
		}
		if f.Kind == "gap" {
			gaps++
		}
	}
	if responses != 150 || gaps != 22 {
		t.Fatalf("lost observations while bounding state: responses=%d gaps=%d", responses, gaps)
	}
}

func telemetryKind(t *testing.T, fs []telemetry.Fact, kind string) telemetry.Fact {
	t.Helper()
	for _, f := range fs {
		if f.Kind == kind {
			return f
		}
	}
	t.Fatalf("missing %s", kind)
	return telemetry.Fact{}
}

func BenchmarkCodexTelemetry(b *testing.B) {
	line := telemetryLine("event_msg", map[string]any{"type": "item_completed", "turn_id": "p", "item": map[string]any{"type": "CommandExecution", "id": "command", "process_id": "1", "status": "completed", "command": []string{"cat file"}, "aggregated_output": strings.Repeat("x", 4096), "exit_code": 0}})
	data := []byte(strings.Repeat(line, 2048))
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		ParseCodexTelemetry(harness.TelemetryState{}, data, 0)
	}
}
