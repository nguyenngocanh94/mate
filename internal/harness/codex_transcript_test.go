package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var _ TranscriptParser = Codex{}

const codexRolloutFixture = `{"timestamp":"2026-09-13T08:00:00Z","ordinal":0,"type":"session_meta","payload":{"session_id":"thread-1","cwd":"/work/repo","cli_version":"0.152.1","model_provider":"openai"}}
{"timestamp":"2026-09-13T08:00:01Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-09-13T08:00:02Z","ordinal":2,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}
{"timestamp":"2026-09-13T08:00:03Z","ordinal":3,"type":"response_item","payload":{"type":"custom_tool_call","call_id":"call-1","name":"exec","input":"printf hello","status":"completed"}}
{"timestamp":"2026-09-13T08:00:04Z","ordinal":4,"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-1","output":[{"type":"input_text","text":"hello"}]}}
{"timestamp":"2026-09-13T08:00:05Z","ordinal":5,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":2,"cache_write_input_tokens":1,"output_tokens":3,"reasoning_output_tokens":1,"total_tokens":13}}}}
{"timestamp":"2026-09-13T08:00:06Z","ordinal":6,"type":"response_item","payload":{"type":"custom_tool_call","call_id":"call-2","name":"Read","input":{"path":"README.md"}}}
{"timestamp":"2026-09-13T08:00:07Z","ordinal":7,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":15,"cached_input_tokens":2,"cache_write_input_tokens":1,"output_tokens":5,"reasoning_output_tokens":2,"total_tokens":20}}}}
{"timestamp":"2026-09-13T08:00:08Z","ordinal":8,"type":"compacted","payload":{"window_id":"window-2"}}
{"timestamp":"2026-09-13T08:00:09Z","ordinal":9,"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}
`

func TestCodexParseTranscriptNormalizesRolloutFactsAndCumulativeDeltas(t *testing.T) {
	batch := (Codex{}).ParseTranscript(TranscriptParseState{}, []byte(codexRolloutFixture))
	if batch.Source != TranscriptCodex {
		t.Fatalf("source = %q, want %q", batch.Source, TranscriptCodex)
	}
	if batch.Malformed != nil {
		t.Fatalf("unexpected malformed record: %+v", batch.Malformed)
	}
	if batch.ConsumedBytes != int64(len(codexRolloutFixture)) {
		t.Fatalf("consumed bytes = %d, want %d", batch.ConsumedBytes, len(codexRolloutFixture))
	}
	if len(batch.Records) != 10 {
		t.Fatalf("records = %d, want 10 copied records", len(batch.Records))
	}
	if batch.Skipped["response_item:message"] != 0 || batch.Skipped["compacted"] != 0 {
		t.Fatalf("skipped = %#v, want no dropped activity", batch.Skipped)
	}
	if len(batch.Turns) != 1 {
		t.Fatalf("turns = %d, want one real assistant response", len(batch.Turns))
	}
	if batch.Turns[0].SourceRef != "2" || batch.Turns[0].Text != "hello" || batch.Turns[0].Usage != (TokenUsage{}) {
		t.Fatalf("assistant turn = %+v, want message identity/text and no fabricated usage", batch.Turns[0])
	}
	wantUsage := []TokenUsage{
		{Input: 10, CacheRead: 2, CacheWrite: 1, Output: 3, Reasoning: 1},
		{Input: 5, Output: 2, Reasoning: 1},
	}
	if len(batch.CodexUsageSnapshots) != len(wantUsage) {
		t.Fatalf("usage snapshots = %d, want %d", len(batch.CodexUsageSnapshots), len(wantUsage))
	}
	for i, snapshot := range batch.CodexUsageSnapshots {
		if snapshot.SourceRef != []string{"5", "7"}[i] {
			t.Errorf("snapshot %d source ref = %q, want %q", i, snapshot.SourceRef, []string{"5", "7"}[i])
		}
		if !reflect.DeepEqual(snapshot.Delta, wantUsage[i]) {
			t.Errorf("snapshot %d delta = %+v, want %+v", i, snapshot.Delta, wantUsage[i])
		}
		if snapshot.HarnessTurnRef != "turn-1" {
			t.Errorf("snapshot %d harness turn = %q, want turn-1", i, snapshot.HarnessTurnRef)
		}
		if i == 0 && snapshot.HasBaseline {
			t.Errorf("first snapshot unexpectedly has a baseline")
		}
		if i == 1 && !snapshot.HasBaseline {
			t.Errorf("second snapshot has no baseline")
		}
	}
	if len(batch.ToolCalls) != 2 || batch.ToolCalls[0].SourceRef != "call-1" || batch.ToolCalls[1].CommandClass != CommandRead {
		t.Fatalf("tool calls = %+v, want both calls with classified names", batch.ToolCalls)
	}
	if batch.ToolCalls[0].InputJSON != `"printf hello"` || batch.ToolCalls[1].InputJSON != `{"path":"README.md"}` {
		t.Fatalf("tool inputs = %q, %q", batch.ToolCalls[0].InputJSON, batch.ToolCalls[1].InputJSON)
	}
	if len(batch.ToolResults) != 1 || batch.ToolResults[0].CallID != "call-1" || batch.ToolResults[0].OutputText != "hello" {
		t.Fatalf("tool results = %+v, want call-1 with full text", batch.ToolResults)
	}
	if batch.NextState.HarnessTurnRef != "" {
		t.Fatalf("next harness turn = %q, want cleared by task_complete", batch.NextState.HarnessTurnRef)
	}
	var cumulative map[string]int64
	if err := json.Unmarshal([]byte(batch.NextState.LastCumulativeJSON), &cumulative); err != nil {
		t.Fatalf("last cumulative json = %q: %v", batch.NextState.LastCumulativeJSON, err)
	}
	if cumulative["input_tokens"] != 15 || cumulative["output_tokens"] != 5 {
		t.Fatalf("last cumulative = %#v, want final snapshot", cumulative)
	}
}

func TestCodexParseTranscriptCarriesTurnAndCumulativeStateAcrossRanges(t *testing.T) {
	cut := strings.Index(codexRolloutFixture, `{"timestamp":"2026-09-13T08:00:06Z","ordinal":6`)
	if cut < 0 {
		t.Fatal("fixture cut not found")
	}
	first := (Codex{}).ParseTranscript(TranscriptParseState{}, []byte(codexRolloutFixture[:cut]))
	if first.NextState.HarnessTurnRef != "turn-1" {
		t.Fatalf("first next harness turn = %q, want turn-1", first.NextState.HarnessTurnRef)
	}
	if first.NextState.LastCumulativeJSON == "" {
		t.Fatal("first parse did not return cumulative cursor state")
	}
	second := (Codex{}).ParseTranscript(first.NextState, []byte(codexRolloutFixture[cut:]))
	if len(second.Turns) != 0 {
		t.Fatalf("resumed turns = %+v, want no fabricated assistant turn", second.Turns)
	}
	if len(second.CodexUsageSnapshots) != 1 || second.CodexUsageSnapshots[0].Delta.Input != 5 {
		t.Fatalf("resumed usage snapshots = %+v, want the 5-token input delta", second.CodexUsageSnapshots)
	}
}

func TestCodexParseTranscriptFinalIsOrdinaryParsing(t *testing.T) {
	state := TranscriptParseState{HarnessTurnRef: "turn-1"}
	got := (Codex{}).ParseTranscript(state, []byte(codexRolloutFixture))
	final := (Codex{}).ParseTranscriptFinal(state, []byte(codexRolloutFixture))
	if !reflect.DeepEqual(got, final) {
		t.Fatalf("final parse differs from ordinary parse:\nordinary=%+v\nfinal=%+v", got, final)
	}
}

func TestCodexParseTranscriptStallsOnMalformedLineAndLeavesFragmentUnconsumed(t *testing.T) {
	valid := strings.Split(codexRolloutFixture, "\n")[0] + "\n"
	fragment := valid + `{"timestamp":"2026-09-13T08:00:01Z"}`
	partial := (Codex{}).ParseTranscript(TranscriptParseState{}, []byte(fragment))
	if partial.Malformed != nil || partial.ConsumedBytes != int64(len(valid)) {
		t.Fatalf("partial batch = %+v, want no failure and cursor at valid prefix", partial)
	}
	malformedInput := valid + "not-json\n" + strings.TrimPrefix(valid, "")
	malformed := (Codex{}).ParseTranscript(TranscriptParseState{}, []byte(malformedInput))
	if malformed.Malformed == nil || malformed.Malformed.Reason != MalformedInvalidJSON {
		t.Fatalf("malformed = %+v, want invalid_json", malformed.Malformed)
	}
	if malformed.ConsumedBytes != int64(len(valid)) {
		t.Fatalf("malformed consumed bytes = %d, want %d", malformed.ConsumedBytes, len(valid))
	}
	if malformed.Malformed.EndOffset != int64(len(valid)+len("not-json\n")) {
		t.Fatalf("malformed end offset = %d, want %d", malformed.Malformed.EndOffset, len(valid)+len("not-json\n"))
	}
}

func TestCodexParseTranscriptRefusesRegressingCumulativeUsage(t *testing.T) {
	input := strings.Replace(codexRolloutFixture, `"input_tokens":15`, `"input_tokens":9`, 1)
	batch := (Codex{}).ParseTranscript(TranscriptParseState{}, []byte(input))
	if batch.Malformed != nil {
		t.Fatalf("regression became a permanent malformed stall: %+v", batch.Malformed)
	}
	if len(batch.UsageFailures) != 1 || batch.UsageFailures[0].Reason != MalformedCumulativeUsage {
		t.Fatalf("usage failures = %+v, want one cumulative regression", batch.UsageFailures)
	}
	if len(batch.CodexUsageSnapshots) != 1 || batch.CodexUsageSnapshots[0].Delta.Input != 10 {
		t.Fatalf("usage snapshots before regression = %+v, want first delta only", batch.CodexUsageSnapshots)
	}
}

func TestCodexTokenCountInfoNullDoesNotBlockLaterUsage(t *testing.T) {
	input := strings.Join([]string{
		strings.Split(codexRolloutFixture, "\n")[0],
		`{"timestamp":"2026-09-13T08:00:01Z","ordinal":1,"type":"event_msg","payload":{"type":"token_count","info":null}}`,
		`{"timestamp":"2026-09-13T08:00:02Z","ordinal":2,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":2,"cache_write_input_tokens":1,"output_tokens":3,"reasoning_output_tokens":1,"total_tokens":13}}}}`,
	}, "\n") + "\n"
	batch := (Codex{}).ParseTranscript(TranscriptParseState{}, []byte(input))
	if batch.Malformed != nil || len(batch.UsageFailures) != 0 {
		t.Fatalf("info:null batch = %+v, want no failure", batch)
	}
	if len(batch.CodexUsageSnapshots) != 1 || batch.CodexUsageSnapshots[0].Delta.Input != 10 {
		t.Fatalf("snapshots = %+v, want later valid snapshot", batch.CodexUsageSnapshots)
	}
}

func TestCodexRegressionProgressesAcrossPersistedCursorRestart(t *testing.T) {
	prefix := strings.Join([]string{
		`{"timestamp":"2026-09-13T08:00:00Z","ordinal":0,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":0,"total_tokens":11}}}}`,
		`{"timestamp":"2026-09-13T08:00:01Z","ordinal":1,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":9,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":0,"total_tokens":10}}}}`,
	}, "\n") + "\n"
	tail := `{"timestamp":"2026-09-13T08:00:02Z","ordinal":2,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":15,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":2,"reasoning_output_tokens":0,"total_tokens":17}}}}` + "\n"
	first := (Codex{}).ParseTranscript(TranscriptParseState{}, []byte(prefix))
	if first.Malformed != nil || len(first.UsageFailures) != 1 || first.ConsumedBytes != int64(len(prefix)) {
		t.Fatalf("prefix = %+v, want progress-capable regression", first)
	}
	second := (Codex{}).ParseTranscript(first.NextState, []byte(tail))
	if second.Malformed != nil || len(second.UsageFailures) != 0 || len(second.CodexUsageSnapshots) != 1 || second.CodexUsageSnapshots[0].Delta.Input != 5 {
		t.Fatalf("restart tail = %+v, want later delta from last valid baseline", second)
	}
}

func TestCodexMessageUsesReportedModelAndIdentity(t *testing.T) {
	input := strings.Join([]string{
		`{"timestamp":"2026-09-13T08:00:00Z","ordinal":0,"type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-test"}}`,
		`{"timestamp":"2026-09-13T08:00:01Z","ordinal":1,"type":"response_item","payload":{"type":"message","id":"msg-1","role":"assistant","content":[{"type":"output_text","text":"real response"}]}}`,
	}, "\n") + "\n"
	batch := (Codex{}).ParseTranscript(TranscriptParseState{}, []byte(input))
	if len(batch.Turns) != 1 {
		t.Fatalf("turns = %+v, want one message turn", batch.Turns)
	}
	turn := batch.Turns[0]
	if turn.SourceRef != "msg-1" || turn.Model != "gpt-test" || turn.Text != "real response" {
		t.Fatalf("turn = %+v, want message identity/model/text", turn)
	}
}

func TestCodexNormalizesAllMeasuredActivityShapes(t *testing.T) {
	input := strings.Join([]string{
		`{"timestamp":"2026-09-13T08:00:00Z","ordinal":0,"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}","call_id":"call-f","id":"fc-1"}}`,
		`{"timestamp":"2026-09-13T08:00:01Z","ordinal":1,"type":"response_item","payload":{"type":"function_call_output","call_id":"call-f","output":"done"}}`,
		`{"timestamp":"2026-09-13T08:00:02Z","ordinal":2,"type":"response_item","payload":{"type":"web_search_call","id":"web-1","action":{"type":"search","query":"go"}}}`,
		`{"timestamp":"2026-09-13T08:00:03Z","ordinal":3,"type":"response_item","payload":{"type":"web_search_call_output","call_id":"web-1","output":[{"type":"input_text","text":"result"}]}}`,
		`{"timestamp":"2026-09-13T08:00:04Z","ordinal":4,"type":"response_item","payload":{"type":"tool_search_call","id":"tool-1","call_id":"call-t","arguments":{"query":"vault"}}}`,
		`{"timestamp":"2026-09-13T08:00:05Z","ordinal":5,"type":"response_item","payload":{"type":"tool_search_output","call_id":"call-t","tools":[{"name":"mcp_tool"}]}}`,
		`{"timestamp":"2026-09-13T08:00:06Z","ordinal":6,"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"AgentMessage","id":"msg-1"}}}`,
	}, "\n") + "\n"
	batch := (Codex{}).ParseTranscript(TranscriptParseState{}, []byte(input))
	if batch.Malformed != nil {
		t.Fatalf("activity batch malformed: %+v", batch.Malformed)
	}
	if len(batch.ToolCalls) != 3 || len(batch.ToolResults) != 3 || len(batch.Records) != 7 {
		t.Fatalf("calls=%d results=%d records=%d, want 3/3/7", len(batch.ToolCalls), len(batch.ToolResults), len(batch.Records))
	}
	if batch.ToolCalls[0].InputJSON != `{"cmd":"pwd"}` || batch.ToolCalls[1].CommandClass != CommandSearch || batch.ToolCalls[2].CommandClass != CommandMCP {
		t.Fatalf("calls = %+v", batch.ToolCalls)
	}
	if batch.ToolResults[0].OutputText != "done" || batch.ToolResults[1].OutputText != "result" || !strings.Contains(batch.ToolResults[2].OutputText, "mcp_tool") {
		t.Fatalf("results = %+v", batch.ToolResults)
	}
}

func TestCodexUnknownActivityIsVisibleFailure(t *testing.T) {
	input := `{"timestamp":"2026-09-13T08:00:00Z","ordinal":0,"type":"response_item","payload":{"type":"new_future_activity"}}` + "\n"
	batch := (Codex{}).ParseTranscript(TranscriptParseState{}, []byte(input))
	if batch.Malformed == nil || batch.Malformed.Reason != MalformedCodexShape {
		t.Fatalf("unknown activity = %+v, want visible Codex shape failure", batch.Malformed)
	}
	if batch.Skipped["response_item:new_future_activity"] != 0 {
		t.Fatalf("unknown activity was silently skipped: %#v", batch.Skipped)
	}
}

func TestLiveCodexCorpusInventoryRejectsUnknownActivityTypes(t *testing.T) {
	requireLive(t)
	root := os.Getenv("MATEV2_CODEX_SESSIONS_DIR")
	if root == "" {
		t.Skip("set MATEV2_CODEX_SESSIONS_DIR to inventory installed Codex rollout files")
	}
	knownTop := map[string]bool{"session_meta": true, "event_msg": true, "response_item": true, "turn_context": true, "world_state": true, "token_usage_record": true, "compacted": true}
	knownEvents := map[string]bool{"task_started": true, "task_complete": true, "token_count": true, "item_completed": true, "thread_settings_applied": true, "turn_aborted": true}
	knownResponses := map[string]bool{"message": true, "reasoning": true, "custom_tool_call": true, "custom_tool_call_output": true, "function_call": true, "function_call_output": true, "web_search_call": true, "web_search_call_output": true, "tool_search_call": true, "tool_search_output": true}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".jsonl" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := ParseCodexSessionMeta(data); err != nil {
			return nil // pre-session_meta Codex rollout format, outside this contract
		}
		parsed := (Codex{}).ParseTranscript(TranscriptParseState{}, data)
		if parsed.Malformed != nil {
			return fmt.Errorf("Codex parser rejected %s at byte %d: %s", path, parsed.Malformed.Offset, parsed.Malformed.Detail)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var record struct {
				Type    string `json:"type"`
				Payload struct {
					Type string `json:"type"`
					Role string `json:"role"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				continue
			}
			if !knownTop[record.Type] {
				return fmt.Errorf("unknown Codex top-level activity %q in %s", record.Type, path)
			}
			if record.Type == "event_msg" && !knownEvents[record.Payload.Type] {
				return fmt.Errorf("unknown Codex event_msg activity %q in %s", record.Payload.Type, path)
			}
			if record.Type == "response_item" && !knownResponses[record.Payload.Type] {
				return fmt.Errorf("unknown Codex response_item activity %q in %s", record.Payload.Type, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseCodexSessionMetaAndAdoptionHeuristic(t *testing.T) {
	meta, err := ParseCodexSessionMeta([]byte(strings.Split(codexRolloutFixture, "\n")[0] + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionID != "thread-1" || meta.Cwd != "/work/repo" || meta.CLIVersion != "0.152.1" || meta.ModelProvider != "openai" {
		t.Fatalf("meta = %+v", meta)
	}
	if _, err := ParseCodexSessionMeta([]byte(strings.Split(codexRolloutFixture, "\n")[0])); err != nil {
		t.Fatalf("single-record metadata was rejected: %v", err)
	}
	launch := time.Date(2026, 9, 13, 7, 59, 59, 0, time.UTC)
	candidates := []CodexRolloutCandidate{
		{Path: "/sessions/old.jsonl", Meta: CodexSessionMeta{SessionID: "old", Cwd: "/work/repo", Timestamp: launch.Add(-time.Nanosecond)}},
		{Path: "/sessions/one.jsonl", Meta: meta},
		{Path: "/sessions/two.jsonl", Meta: CodexSessionMeta{SessionID: "thread-2", Cwd: "/work/repo", Timestamp: launch.Add(time.Second)}},
		{Path: "/sessions/other.jsonl", Meta: CodexSessionMeta{SessionID: "thread-3", Cwd: "/other", Timestamp: launch.Add(time.Second)}},
	}
	if got := AdoptCodexRollout(candidates, "/work/repo", launch, ""); got.Status != CodexAdoptionPending || got.Reason != "locator_ambiguous" || got.Candidate.Path != "" {
		t.Fatalf("ambiguous adoption = %+v", got)
	}
	if got := AdoptCodexRollout(candidates, "/work/repo", launch, "thread-2"); got.Status != CodexAdoptionPending || got.Reason != "locator_ambiguous" {
		t.Fatalf("hint must not resolve ambiguity = %+v", got)
	}
	if got := AdoptCodexRollout(candidates[1:2], "/work/repo", launch, "thread-1"); got.Status != CodexAdoptionKnown || got.Candidate.Path != "/sessions/one.jsonl" {
		t.Fatalf("matching hint on unique candidate = %+v", got)
	}
	if got := AdoptCodexRollout(candidates[1:2], "/work/repo", launch, "forged"); got.Status != CodexAdoptionPending || got.Reason != "locator_identity_mismatch" {
		t.Fatalf("forged adoption = %+v", got)
	}
	if got := AdoptCodexRollout(candidates, "/other", launch, ""); got.Status != CodexAdoptionKnown || got.Candidate.Meta.SessionID != "thread-3" {
		t.Fatalf("other cwd adoption = %+v", got)
	}
}

func TestCodexAdoptionDoesNotUseRecencyAsATiebreaker(t *testing.T) {
	launch := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	candidates := []CodexRolloutCandidate{
		{Path: "/sessions/newer.jsonl", Meta: CodexSessionMeta{SessionID: "newer", Cwd: "/work/repo", Timestamp: launch.Add(2 * time.Second)}},
		{Path: "/sessions/older.jsonl", Meta: CodexSessionMeta{SessionID: "older", Cwd: "/work/repo", Timestamp: launch.Add(time.Second)}},
	}
	got := AdoptCodexRollout(candidates, "/work/repo", launch, "")
	if got.Status != CodexAdoptionPending || got.Reason != "locator_ambiguous" {
		t.Fatalf("recency adoption = %+v, want pending ambiguity", got)
	}
}

// TestLiveCodexRolloutCorpusMeasuresTokenCountCadence is an opt-in measurement,
// not a capability claim. Codex token_count has no turn_id of its own, so the
// corpus census compares it with task boundaries and assistant response
// messages, recording whether the observed counts agree or diverge. It also
// reports the observed compaction marker without treating that marker as proof
// of a context reset.
func TestLiveCodexRolloutCorpusMeasuresTokenCountCadence(t *testing.T) {
	requireLive(t)
	root := os.Getenv("MATEV2_CODEX_SESSIONS_DIR")
	if root == "" {
		t.Skip("set MATEV2_CODEX_SESSIONS_DIR to measure installed Codex rollout files")
	}
	var files, tokenCounts, taskStarts, taskCompletes, compacted, multiSnapshotFiles int
	var tasks, tasksWithTokenCounts, tasksEqualToAssistantMessages, tasksWithMoreTokenCounts, tasksWithMoreAssistantMessages int
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := ParseCodexSessionMeta(data); err != nil {
			return nil // pre-session_meta Codex rollout format, outside this contract
		}
		files++
		activeTokenCounts := 0
		fileHasMultipleInTask := false
		inTask := false
		taskTokenCounts, taskAssistantMessages := 0, 0
		finishTask := func() {
			if !inTask {
				return
			}
			tasks++
			if taskTokenCounts > 0 {
				tasksWithTokenCounts++
			}
			switch {
			case taskTokenCounts == taskAssistantMessages:
				tasksEqualToAssistantMessages++
			case taskTokenCounts > taskAssistantMessages:
				tasksWithMoreTokenCounts++
			default:
				tasksWithMoreAssistantMessages++
			}
			inTask = false
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var record struct {
				Type    string `json:"type"`
				Payload struct {
					Type string `json:"type"`
					Role string `json:"role"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				continue
			}
			switch {
			case record.Type == "event_msg" && record.Payload.Type == "token_count":
				tokenCounts++
				if inTask {
					taskTokenCounts++
					activeTokenCounts++
					if activeTokenCounts > 1 {
						fileHasMultipleInTask = true
					}
				}
			case record.Type == "event_msg" && record.Payload.Type == "task_started":
				finishTask()
				taskStarts++
				inTask = true
				taskTokenCounts, taskAssistantMessages = 0, 0
				activeTokenCounts = 0
			case record.Type == "event_msg" && record.Payload.Type == "task_complete":
				taskCompletes++
				finishTask()
			case record.Type == "response_item" && record.Payload.Type == "message" && record.Payload.Role == "assistant":
				if inTask {
					taskAssistantMessages++
				}
			case record.Type == "compacted":
				compacted++
			}
		}
		finishTask()
		if fileHasMultipleInTask {
			multiSnapshotFiles++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no rollout JSONL files found")
	}
	t.Logf("files=%d token_count=%d task_started=%d task_complete=%d files_with_multiple_token_count=%d compacted=%d tasks=%d tasks_with_token_count=%d tasks_token_count_equal_assistant_messages=%d tasks_token_count_more_than_assistant_messages=%d tasks_assistant_messages_more_than_token_count=%d", files, tokenCounts, taskStarts, taskCompletes, multiSnapshotFiles, compacted, tasks, tasksWithTokenCounts, tasksEqualToAssistantMessages, tasksWithMoreTokenCounts, tasksWithMoreAssistantMessages)
	t.Log("token_count cadence was measured against assistant response-message counts within task boundaries; count agreement does not prove emission cadence, so per-message attribution remains unproven")
	t.Log("compacted records are observed but context_reset remains unproven and is not derived")
}
