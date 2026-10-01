package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

// claudeTranscripts is Claude Code's TranscriptSource: its session JSONL
// under the config directory's projects/, read whole on every change,
// with usage already per call.
type claudeTranscripts struct {
	// configDir is the profile's CLAUDE_CONFIG_DIR; empty resolves it as
	// transcript discovery always has (EffectiveClaudeConfigDir).
	configDir string
}

// claudeRuleProjects is the rule a Claude transcript is located by:
// `session_id=` plus Claude's own naming rule,
// `<config>/projects/<slug of cwd>/<session-id>.jsonl`.
const claudeRuleProjects = "claude.projects"

// Locate implements TranscriptSource. Claude names its transcript by the
// session id mate gave it at launch, so the id and the cwd are the path.
func (s claudeTranscripts) Locate(req harness.TranscriptLocateRequest) (harness.TranscriptLocation, string) {
	if req.SessionID == "" {
		return harness.TranscriptLocation{}, harness.LocateNoSession
	}
	root := req.Root
	if root == "" {
		configDir, err := EffectiveClaudeConfigDir(s.configDir)
		if err != nil {
			return harness.TranscriptLocation{}, harness.LocateNotFound
		}
		root = filepath.Join(configDir, "projects")
	}
	path := ClaudeTranscriptPath(root, req.Cwd, req.SessionID)
	if fi, err := os.Stat(path); path == "" || err != nil || fi.IsDir() {
		return harness.TranscriptLocation{}, harness.LocateNotFound
	}
	return harness.TranscriptLocation{Path: path, Rule: claudeRuleProjects}, ""
}

// Read implements TranscriptSource. A Claude transcript is re-read whole on
// every change rather than tailed: ParseTranscript withholds the trailing
// message group because a later write may still extend it, so a tail would
// have to carry the parser's own state across reads and a resume that got
// it wrong would charge one turn's tokens to another, permanently. A file
// at rest is parsed final, which flushes that group.
func (s claudeTranscripts) Read(req harness.TranscriptReadRequest) (harness.TranscriptBatch, error) {
	data, err := os.ReadFile(req.Path)
	if err != nil {
		return harness.TranscriptBatch{}, err
	}
	var b harness.TranscriptBatch
	if req.AtRest {
		b = (Claude{}).ParseTranscriptFinal(harness.TranscriptParseState{}, data)
	} else {
		b = (Claude{}).ParseTranscript(harness.TranscriptParseState{}, data)
	}
	normaliseClaude(&b)
	return b, nil
}

// normaliseClaude makes one usage turn per assistant message group, which
// is one API response: the unit Claude restates usage on, and the unit
// ParseTranscript already keys by message id. Claude reports usage per
// call and its input_tokens excludes both cache buckets, so a turn's usage
// is the response's own and needs no arithmetic.
//
// ContextTokens is the input side of that response - fresh input plus both
// cache buckets - because that is what was carried into the call and
// therefore what the context held when it returned.
func normaliseClaude(b *harness.TranscriptBatch) {
	results := firstResults(*b)
	callsByTurn := map[string][]harness.TranscriptToolCall{}
	for _, call := range b.ToolCalls {
		callsByTurn[call.TurnSourceRef] = append(callsByTurn[call.TurnSourceRef], call)
	}
	b.UsageTurns, b.UnpricedCalls, b.Compactions = nil, nil, nil
	for _, t := range b.Turns {
		calls := callsByTurn[t.SourceRef]
		ended := t.OccurredAt
		for _, call := range calls {
			if res, ok := results[call.SourceRef]; ok && res.CompletedAt.After(ended) {
				ended = res.CompletedAt
			} else if call.StartedAt.After(ended) {
				ended = call.StartedAt
			}
		}
		b.UsageTurns = append(b.UsageTurns, harness.UsageTurn{
			SourceRef: t.SourceRef, Offset: t.Offset,
			StartedAt: t.OccurredAt, EndedAt: ended,
			Outcome: t.StopReason, Model: t.Model, HarnessTurnRef: t.HarnessTurnRef,
			Usage: t.Usage, ContextTokens: t.Usage.ContextTokens(), Calls: calls,
			Sample: harness.UsageSample{At: t.OccurredAt, Usage: t.Usage},
		})
	}

	// Claude marks a compaction on the summary record itself
	// (`isCompactSummary`) or with a `compact_boundary` system record.
	for _, rec := range b.Records {
		if !strings.Contains(rec.RawJSON, `"compact_boundary"`) && !strings.Contains(rec.RawJSON, `"isCompactSummary"`) {
			continue
		}
		var probe struct {
			Type             string `json:"type"`
			Subtype          string `json:"subtype"`
			IsCompactSummary bool   `json:"isCompactSummary"`
		}
		if json.Unmarshal([]byte(rec.RawJSON), &probe) != nil {
			continue
		}
		trigger := ""
		switch {
		case probe.IsCompactSummary:
			trigger = "claude.compact_summary"
		case probe.Type == "system" && probe.Subtype == "compact_boundary":
			trigger = "claude.compact_boundary"
		}
		if trigger != "" {
			b.Compactions = append(b.Compactions, harness.TranscriptCompaction{Offset: rec.Offset, OccurredAt: rec.OccurredAt, Trigger: trigger})
		}
	}
}

// firstResults is each tool call's result, the first one the batch carries.
func firstResults(b harness.TranscriptBatch) map[string]harness.TranscriptToolResult {
	out := make(map[string]harness.TranscriptToolResult, len(b.ToolResults))
	for _, res := range b.ToolResults {
		if _, seen := out[res.CallID]; !seen {
			out[res.CallID] = res
		}
	}
	return out
}

// Telemetry implements TranscriptSource. Claude's transcript carries no
// native execution, process or timing record, so its telemetry is the
// normalised facts its ledger already verified, emitted again whenever the
// ledger moved.
func (s claudeTranscripts) Telemetry(req harness.TelemetryRequest) (harness.TelemetryUpdate, error) {
	up := harness.TelemetryUpdate{Offset: req.Offset, State: req.State, Gaps: []string{
		"child_usage_unavailable", "native_execution_unavailable", "native_process_unavailable", "native_timing_unavailable",
	}}
	if req.Offset != req.Batch.ConsumedBytes || req.Changed {
		up.Facts = NormalizedTelemetry(req.Batch)
		up.Offset = req.Batch.ConsumedBytes
	}
	return up, nil
}

// NormalizedTelemetry supplies Claude and older harnesses with the observations
// already verified by their transcript adapter. No native process/timing claims
// are made for this fallback.
func NormalizedTelemetry(tb harness.TranscriptBatch) []telemetry.Fact {
	var out []telemetry.Fact
	for _, t := range tb.Turns {
		off := t.Offset
		f := telemetry.Fact{Version: telemetry.Version, ID: t.SourceRef, Kind: "response", SourceRef: t.SourceRef, SourceOffset: off, OccurredAt: t.OccurredAt, MeasurementKind: "normalized", HarnessTurnRef: t.HarnessTurnRef, ResponseID: t.SourceRef, Model: t.Model, Text: t.Text, InputTokens: t.Usage.Input, CacheReadTokens: t.Usage.CacheRead, CacheWriteTokens: t.Usage.CacheWrite, OutputTokens: t.Usage.Output, ReasoningTokens: t.Usage.Reasoning, ContextTokens: t.Usage.ContextTokens(), LedgerRefOffset: &off}
		out = append(out, f)
	}
	calls := map[string]harness.TranscriptToolCall{}
	for _, c := range tb.ToolCalls {
		calls[c.SourceRef] = c
		at := c.StartedAt
		out = append(out, telemetry.Fact{Version: telemetry.Version, ID: c.SourceRef, Kind: "tool_call", SourceRef: c.SourceRef, SourceOffset: c.Offset, OccurredAt: at, MeasurementKind: "normalized", ResponseID: c.TurnSourceRef, WrapperRef: c.SourceRef, Tool: c.ToolName, Command: c.InputJSON, StartedAt: &at})
	}
	for _, r := range tb.ToolResults {
		at := r.CompletedAt
		c := calls[r.CallID]
		f := telemetry.Fact{Version: telemetry.Version, ID: r.CallID, Kind: "tool_result", SourceRef: r.SourceRef, SourceOffset: r.Offset, OccurredAt: at, MeasurementKind: "normalized", ResponseID: c.TurnSourceRef, WrapperRef: r.CallID, Tool: c.ToolName, Command: c.InputJSON, CompletedAt: &at, Output: harness.OutputFact(r.OutputText, nil)}
		if r.IsError {
			f.Status = "failed"
		}
		out = append(out, f)
	}
	for _, r := range tb.Records {
		if r.Kind != harness.TranscriptKindUser {
			continue
		}
		var p struct {
			Type     string `json:"type"`
			PromptID string `json:"promptId"`
			Message  struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(r.RawJSON), &p) != nil {
			continue
		}
		if bytes.Contains(p.Message.Content, []byte(`"tool_result"`)) {
			continue
		}
		text := harness.MessageText(p.Message.Content)
		if text != "" {
			out = append(out, telemetry.Fact{Version: telemetry.Version, ID: r.SourceRef, Kind: "prompt", SourceRef: r.SourceRef, SourceOffset: r.Offset, OccurredAt: r.OccurredAt, MeasurementKind: "normalized", HarnessTurnRef: p.PromptID, Text: text})
		}
	}
	return out
}
