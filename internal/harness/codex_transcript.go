package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Codex's rollout is newline-delimited JSON. This file is the only place that
// knows its record shapes; callers receive the normalized transcript types.
// Unlike Claude, Codex's token_count usage is a session-wide cumulative
// snapshot. Each snapshot becomes a CodexUsageSnapshot, kept in the batch's
// opaque HarnessState; it is deliberately not a TranscriptTurn because a
// token snapshot is not an assistant response. The usage turns the core
// stores are derived from them (normaliseCodex).

// CodexSessionMeta is the identity header of a rollout file.
type CodexSessionMeta struct {
	SessionID     string
	Cwd           string
	CLIVersion    string
	ModelProvider string
	Timestamp     time.Time
}

// CodexRolloutCandidate is a rollout whose session_meta has already been
// validated by the adapter. Meta.Timestamp is the top-level session_meta
// timestamp used by the adoption heuristic.
type CodexRolloutCandidate struct {
	Path string
	Meta CodexSessionMeta
}

// CodexCumulativeUsage is the complete six-counter total reported by a Codex
// token_count record. The Total counter is retained even though TokenUsage
// intentionally exposes only the five billable dimensions.
type CodexCumulativeUsage struct {
	Input      int64
	CacheRead  int64
	CacheWrite int64
	Output     int64
	Reasoning  int64
	Total      int64
}

// CodexUsageSnapshot is usage bounded by a Codex harness turn. Baseline is
// the previous cumulative total (zero with HasBaseline=false on the first
// snapshot); Delta is the non-negative difference from it. Only Codex's
// own TranscriptSource reads them: it turns them into UsageTurns.
type CodexUsageSnapshot struct {
	SourceRef      string
	Offset         int64
	Ordinal        int
	OccurredAt     time.Time
	HarnessTurnRef string
	// Model is the model effective at this checkpoint, rather than the
	// model at the end of the parsed range. Pricing is effective at the
	// checkpoint's occurred_at, so losing this boundary corrupts history.
	Model       string
	HasBaseline bool
	Baseline    CodexCumulativeUsage
	Cumulative  CodexCumulativeUsage
	Delta       TokenUsage
}

type CodexAdoptionStatus string

const (
	CodexAdoptionKnown   CodexAdoptionStatus = "known"
	CodexAdoptionPending CodexAdoptionStatus = "pending"
)

// CodexRolloutAdoption is the result of the pre-notify Codex session
// heuristic. Ambiguous and pending results intentionally carry no candidate.
type CodexRolloutAdoption struct {
	Status    CodexAdoptionStatus
	Reason    string
	Candidate CodexRolloutCandidate
}

// ParseCodexSessionMeta validates and decodes the first session_meta record.
// A caller may use it while scanning a sessions directory; parsing a tail does
// not require the header because ParseTranscript is also used after adoption.
func ParseCodexSessionMeta(data []byte) (CodexSessionMeta, error) {
	line, ok := firstJSONLine(data)
	if !ok {
		return CodexSessionMeta{}, fmt.Errorf("codex rollout has no complete session_meta record")
	}
	var rec codexEnvelope
	if err := json.Unmarshal(line, &rec); err != nil {
		return CodexSessionMeta{}, fmt.Errorf("decode Codex session_meta: %w", err)
	}
	if rec.Type != "session_meta" {
		return CodexSessionMeta{}, fmt.Errorf("Codex rollout starts with %q, want session_meta", rec.Type)
	}
	meta, err := decodeCodexSessionMeta(rec)
	if err != nil {
		return CodexSessionMeta{}, err
	}
	return meta, nil
}

// AdoptCodexRollout applies the deliberately conservative pre-notify
// adoption rule. It accepts only a canonical cwd match and a session_meta
// timestamp at or after launch. A thread hint is the provider session id
// observed in a Codex notification; it validates a unique candidate but never
// narrows an ambiguous set. Recency is never used as a tiebreaker, because two
// launches in the same worktree can be genuinely ambiguous.
func AdoptCodexRollout(candidates []CodexRolloutCandidate, agentCwd string, launchedAt time.Time, hintedSessionID string) CodexRolloutAdoption {
	matched := make([]CodexRolloutCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Meta.Cwd != agentCwd || candidate.Meta.Timestamp.Before(launchedAt) {
			continue
		}
		matched = append(matched, candidate)
	}

	switch len(matched) {
	case 0:
		reason := "locator_pending"
		if hintedSessionID != "" {
			reason = "locator_identity_mismatch"
		}
		return CodexRolloutAdoption{Status: CodexAdoptionPending, Reason: reason}
	case 1:
		if hintedSessionID != "" && matched[0].Meta.SessionID != hintedSessionID {
			return CodexRolloutAdoption{Status: CodexAdoptionPending, Reason: "locator_identity_mismatch"}
		}
		return CodexRolloutAdoption{Status: CodexAdoptionKnown, Candidate: matched[0]}
	default:
		return CodexRolloutAdoption{Status: CodexAdoptionPending, Reason: "locator_ambiguous"}
	}
}

func (Codex) ParseTranscript(state TranscriptParseState, data []byte) TranscriptBatch {
	return parseCodexRange(state, data)
}

// Codex rollout records are individually complete JSON objects. There is no
// Claude-style multi-record message group to defer, so final parsing is
// ordinary parsing. The caller-owned at-rest precondition remains part of the
// shared interface contract, but is vacuous for this format's record boundary.
func (Codex) ParseTranscriptFinal(state TranscriptParseState, data []byte) TranscriptBatch {
	return parseCodexRange(state, data)
}

type codexParse struct {
	batch         TranscriptBatch
	snapshots     []CodexUsageSnapshot
	turnRef       string
	model         string
	lastCumul     codexCumulative
	hasCumulative bool
	turnChanges   []codexStateChange
	lineEnd       int64
}

type codexStateChange struct {
	offset        int64
	turnRef       string
	model         string
	lastCumul     codexCumulative
	hasCumulative bool
}

func parseCodexRange(state TranscriptParseState, data []byte) TranscriptBatch {
	p := &codexParse{batch: TranscriptBatch{
		Source:     TranscriptCodex,
		TotalBytes: int64(len(data)),
		Skipped:    map[string]int{},
	}, turnRef: state.HarnessTurnRef, model: state.CodexModel, hasCumulative: state.LastCumulativeJSON != ""}
	if state.LastCumulativeJSON != "" {
		var err error
		p.lastCumul, err = decodeCumulativeJSON(state.LastCumulativeJSON)
		if err != nil {
			p.malformed(0, 0, MalformedCumulativeUsage, "stored cumulative cursor is invalid: "+err.Error())
			p.batch.PendingBytes = p.batch.TotalBytes
			return p.batch
		}
	}

	var offset int64
	lineOrdinal := 0
	for offset < int64(len(data)) {
		nl := bytes.IndexByte(data[offset:], '\n')
		if nl < 0 {
			break
		}
		lineOffset := offset
		line := bytes.TrimRight(data[offset:offset+int64(nl)], "\r")
		offset += int64(nl) + 1
		p.lineEnd = offset
		currentLineOrdinal := lineOrdinal
		lineOrdinal++
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var envelope codexEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			p.malformed(lineOffset, currentLineOrdinal, MalformedInvalidJSON, err.Error())
			break
		}
		if envelope.Type == "" {
			p.malformed(lineOffset, currentLineOrdinal, MalformedMissingType, "record has no type")
			break
		}
		if envelope.Ordinal == nil {
			p.malformed(lineOffset, currentLineOrdinal, MalformedMissingRecordID, "record has no ordinal")
			break
		}
		if *envelope.Ordinal < 0 || int64(int(*envelope.Ordinal)) != *envelope.Ordinal {
			p.malformed(lineOffset, currentLineOrdinal, MalformedCodexShape, "record ordinal is out of range")
			break
		}
		if strings.TrimSpace(envelope.Timestamp) == "" {
			p.malformed(lineOffset, currentLineOrdinal, MalformedMissingTimestamp, "record has no timestamp")
			break
		}
		ts, err := time.Parse(time.RFC3339Nano, envelope.Timestamp)
		if err != nil {
			p.malformed(lineOffset, currentLineOrdinal, MalformedMissingTimestamp, err.Error())
			break
		}
		ordinal := int(*envelope.Ordinal)
		raw := string(line)
		if err := p.add(envelope, raw, lineOffset, ordinal, ts); err != nil {
			p.malformed(lineOffset, currentLineOrdinal, err.reason, err.detail)
			break
		}
	}

	if p.batch.Malformed != nil {
		p.batch.ConsumedBytes = p.batch.Malformed.Offset
	} else {
		p.batch.ConsumedBytes = offset
	}
	p.batch.PendingBytes = p.batch.TotalBytes - p.batch.ConsumedBytes
	p.batch.NextState = p.stateAt(p.batch.ConsumedBytes)
	p.batch.HarnessState = codexTranscriptState{snapshots: p.snapshots}
	return p.batch
}

type codexParseError struct {
	reason string
	detail string
}

func (p *codexParse) add(rec codexEnvelope, raw string, offset int64, ordinal int, ts time.Time) *codexParseError {
	sourceRef := strconv.Itoa(ordinal)
	appendRecord := func(kind TranscriptKind) {
		p.batch.Records = append(p.batch.Records, TranscriptRecord{
			SourceRef: sourceRef, Kind: kind, Offset: offset, Ordinal: ordinal,
			OccurredAt: ts, RawJSON: raw,
		})
	}

	switch rec.Type {
	case "session_meta":
		if _, err := decodeCodexSessionMeta(rec); err != nil {
			return &codexParseError{MalformedCodexShape, err.Error()}
		}
		appendRecord(TranscriptKindSystem)
	case "turn_context":
		var payload codexTurnContextPayload
		if err := json.Unmarshal(rec.Payload, &payload); err != nil || payload.Model == "" {
			return &codexParseError{MalformedCodexShape, "turn_context has no model"}
		}
		p.model = payload.Model
		p.turnChanges = append(p.turnChanges, codexStateChange{offset: offset, turnRef: p.turnRef, model: p.model, lastCumul: p.lastCumul, hasCumulative: p.hasCumulative})
		appendRecord(TranscriptKindContextEvent)
	case "event_msg":
		var payload codexEventPayload
		if err := json.Unmarshal(rec.Payload, &payload); err != nil || payload.Type == "" {
			return &codexParseError{MalformedCodexShape, "event_msg payload has no type"}
		}
		switch payload.Type {
		case "task_started":
			if payload.TurnID == "" {
				return &codexParseError{MalformedCodexShape, "task_started has no turn_id"}
			}
			p.turnRef = payload.TurnID
			p.turnChanges = append(p.turnChanges, codexStateChange{offset: offset, turnRef: p.turnRef, model: p.model, lastCumul: p.lastCumul, hasCumulative: p.hasCumulative})
			appendRecord(TranscriptKindContextEvent)
		case "task_complete":
			if payload.TurnID == "" {
				return &codexParseError{MalformedCodexShape, "task_complete has no turn_id"}
			}
			p.turnRef = ""
			p.turnChanges = append(p.turnChanges, codexStateChange{offset: offset, turnRef: p.turnRef, model: p.model, lastCumul: p.lastCumul, hasCumulative: p.hasCumulative})
			appendRecord(TranscriptKindContextEvent)
		case "item_started", "item_completed":
			if payload.TurnID == "" {
				return &codexParseError{MalformedCodexShape, "item_completed has no turn_id"}
			}
			appendRecord(TranscriptKindContextEvent)
		case "token_count":
			usage, cumulative, hasSnapshot, err := decodeTokenCount(payload.Info, p.lastCumul)
			appendRecord(TranscriptKindUsage)
			if err != nil {
				p.batch.UsageFailures = append(p.batch.UsageFailures, TranscriptUsageFailure{
					Offset: offset, Ordinal: ordinal, Reason: MalformedCumulativeUsage, Detail: err.Error(),
				})
				return nil
			}
			if !hasSnapshot {
				return nil
			}
			p.snapshots = append(p.snapshots, CodexUsageSnapshot{
				SourceRef: sourceRef, Offset: offset, Ordinal: ordinal, OccurredAt: ts,
				Model:          p.model,
				HarnessTurnRef: p.turnRef, HasBaseline: p.hasCumulative,
				Baseline: codexCumulativePublic(p.lastCumul), Cumulative: codexCumulativePublic(cumulative), Delta: usage,
			})
			p.lastCumul = cumulative
			p.hasCumulative = true
			p.turnChanges = append(p.turnChanges, codexStateChange{offset: offset, turnRef: p.turnRef, model: p.model, lastCumul: p.lastCumul, hasCumulative: p.hasCumulative})
		default:
			if codexEventHousekeeping[payload.Type] {
				appendRecord(TranscriptKindContextEvent)
				return nil
			}
			return &codexParseError{MalformedCodexShape, "unrecognized event_msg type " + payload.Type}
		}
	case "response_item":
		var payload codexResponsePayload
		if err := json.Unmarshal(rec.Payload, &payload); err != nil || payload.Type == "" {
			return &codexParseError{MalformedCodexShape, "response_item payload has no type"}
		}
		switch payload.Type {
		case "message":
			if payload.Role == "" {
				return &codexParseError{MalformedCodexShape, "message has no role"}
			}
			kind, ok := codexMessageKind[payload.Role]
			if !ok {
				return &codexParseError{MalformedCodexShape, "message has unrecognized role " + payload.Role}
			}
			appendRecord(kind)
			if payload.Role == "assistant" {
				messageRef := payload.ID
				if messageRef == "" {
					messageRef = sourceRef
				}
				p.batch.Turns = append(p.batch.Turns, TranscriptTurn{
					SourceRef: messageRef, Offset: offset, Ordinal: ordinal, OccurredAt: ts,
					Model: p.model, Text: codexMessageText(payload.Content), HarnessTurnRef: p.turnRef,
				})
			}
		case "custom_tool_call", "function_call", "web_search_call", "tool_search_call":
			call, err := p.codexToolCall(payload, sourceRef, offset, ordinal, ts)
			if err != nil {
				return err
			}
			appendRecord(TranscriptKindAssistant)
			p.batch.ToolCalls = append(p.batch.ToolCalls, call)
		case "custom_tool_call_output", "function_call_output", "web_search_call_output", "tool_search_output":
			result, err := p.codexToolResult(payload, sourceRef, offset, ordinal, ts)
			if err != nil {
				return err
			}
			appendRecord(TranscriptKindToolResult)
			p.batch.ToolResults = append(p.batch.ToolResults, result)
		case "reasoning":
			appendRecord(TranscriptKindAssistant)
		default:
			return &codexParseError{MalformedCodexShape, "unrecognized response_item type " + payload.Type}
		}
	case "compacted":
		appendRecord(TranscriptKindContextEvent)
	case "token_usage_record":
		appendRecord(TranscriptKindUsage)
	case "world_state":
		p.batch.Skipped[rec.Type]++
	default:
		return &codexParseError{MalformedCodexShape, "unrecognized top-level type " + rec.Type}
	}
	return nil
}

func (p *codexParse) codexToolCall(payload codexResponsePayload, sourceRef string, offset int64, ordinal int, ts time.Time) (TranscriptToolCall, *codexParseError) {
	callID := payload.CallID
	if callID == "" {
		callID = payload.ID
	}
	if callID == "" {
		// Some web_search_call records carry neither field. The source
		// ordinal is the only identity the rollout provides; preserve the
		// activity under it rather than dropping a real call.
		callID = sourceRef
	}
	name := payload.Name
	input := payload.Input
	switch payload.Type {
	case "function_call":
		if name == "" {
			return TranscriptToolCall{}, &codexParseError{MalformedToolCallShape, "function_call has no name"}
		}
		input = payload.Arguments
	case "web_search_call":
		name, input = "web_search", payload.Action
	case "tool_search_call":
		name, input = "tool_search", payload.Arguments
	}
	if name == "" {
		return TranscriptToolCall{}, &codexParseError{MalformedToolCallShape, payload.Type + " has no name"}
	}
	inputJSON, err := codexJSONValue(input)
	if payload.Type == "function_call" {
		inputJSON, err = codexFunctionArguments(input)
	}
	if err != nil {
		return TranscriptToolCall{}, &codexParseError{MalformedToolCallShape, err.Error()}
	}
	return TranscriptToolCall{
		SourceRef: callID, TurnSourceRef: sourceRef, Offset: offset, Ordinal: ordinal,
		ToolName: name, CommandClass: ClassifyCodexTool(name), InputJSON: inputJSON, StartedAt: ts,
	}, nil
}

func (p *codexParse) codexToolResult(payload codexResponsePayload, sourceRef string, offset int64, ordinal int, ts time.Time) (TranscriptToolResult, *codexParseError) {
	if payload.CallID == "" {
		return TranscriptToolResult{}, &codexParseError{MalformedToolResultShape, payload.Type + " has no call_id"}
	}
	output := payload.Output
	if len(bytes.TrimSpace(output)) == 0 {
		switch payload.Type {
		case "web_search_call_output":
			output = payload.Action
		case "tool_search_output":
			output = payload.Tools
		}
	}
	text := codexOutputText(output)
	return TranscriptToolResult{
		SourceRef: sourceRef, CallID: payload.CallID, Offset: offset, Ordinal: ordinal,
		OutputText: text, OutputBytes: int64(len(text)), IsError: payload.IsError != nil && *payload.IsError,
		CompletedAt: ts,
	}, nil
}

func (p *codexParse) malformed(offset int64, ordinal int, reason, detail string) {
	p.batch.Malformed = &TranscriptMalformed{Offset: offset, EndOffset: p.lineEnd, Ordinal: ordinal, Reason: reason, Detail: detail}
}

func (p *codexParse) stateAt(offset int64) TranscriptParseState {
	turnRef := p.turnRef
	model := p.model
	last := p.lastCumul
	hasCumulative := p.hasCumulative
	for _, change := range p.turnChanges {
		if change.offset >= offset {
			break
		}
		turnRef, model, last, hasCumulative = change.turnRef, change.model, change.lastCumul, change.hasCumulative
	}
	lastJSON := ""
	if hasCumulative {
		lastJSON = last.JSON()
	}
	return TranscriptParseState{HarnessTurnRef: turnRef, LastCumulativeJSON: lastJSON, CodexModel: model}
}

// CodexTurnCompletedAfter reports whether a Codex rollout records a finished
// turn (`event_msg` `task_complete`) stamped after after. It is how a caller
// that typed a line into a Codex agent knows the turn on it ended: Codex has
// no Stop hook, and its composer reads empty between tool calls, so an empty
// composer is not the end of a turn (measured 2026-09-24, task 38: a stow
// judged over by the composer was cut off mid-turn by the restart). A turn
// Codex aborted is not a finished one. Lines that do not parse are skipped.
func CodexTurnCompletedAfter(rollout []byte, after time.Time) bool {
	for _, line := range bytes.Split(rollout, []byte("\n")) {
		if !bytes.Contains(line, []byte(`"task_complete"`)) {
			continue
		}
		var rec codexEnvelope
		if json.Unmarshal(line, &rec) != nil || rec.Type != "event_msg" {
			continue
		}
		var payload codexEventPayload
		if json.Unmarshal(rec.Payload, &payload) != nil || payload.Type != "task_complete" {
			continue
		}
		if ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil && ts.After(after) {
			return true
		}
	}
	return false
}

type codexEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Ordinal   *int64          `json:"ordinal"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexTurnContextPayload struct {
	Model string `json:"model"`
}

type codexSessionPayload struct {
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	CLIVersion    string `json:"cli_version"`
	ModelProvider string `json:"model_provider"`
}

func decodeCodexSessionMeta(rec codexEnvelope) (CodexSessionMeta, error) {
	var payload codexSessionPayload
	if err := json.Unmarshal(rec.Payload, &payload); err != nil {
		return CodexSessionMeta{}, fmt.Errorf("session_meta payload is not an object: %w", err)
	}
	if payload.SessionID == "" || payload.Cwd == "" || payload.CLIVersion == "" || payload.ModelProvider == "" {
		return CodexSessionMeta{}, fmt.Errorf("session_meta is missing session_id, cwd, cli_version, or model_provider")
	}
	if !strings.HasPrefix(payload.Cwd, "/") {
		return CodexSessionMeta{}, fmt.Errorf("session_meta cwd %q is not absolute", payload.Cwd)
	}
	ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
	if err != nil {
		return CodexSessionMeta{}, fmt.Errorf("session_meta timestamp %q: %w", rec.Timestamp, err)
	}
	return CodexSessionMeta{SessionID: payload.SessionID, Cwd: payload.Cwd, CLIVersion: payload.CLIVersion, ModelProvider: payload.ModelProvider, Timestamp: ts}, nil
}

type codexEventPayload struct {
	Type   string          `json:"type"`
	TurnID string          `json:"turn_id"`
	Info   json.RawMessage `json:"info"`
}

type codexResponsePayload struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Arguments json.RawMessage `json:"arguments"`
	Action    json.RawMessage `json:"action"`
	Output    json.RawMessage `json:"output"`
	Tools     json.RawMessage `json:"tools"`
	IsError   *bool           `json:"is_error"`
}

var codexEventHousekeeping = map[string]bool{
	"thread_settings_applied": true,
	"turn_aborted":            true,
}

var codexMessageKind = map[string]TranscriptKind{
	"assistant": TranscriptKindAssistant,
	"developer": TranscriptKindSystem,
	"user":      TranscriptKindUser,
}

type codexCumulative struct {
	Input      int64 `json:"input_tokens"`
	CacheRead  int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
	Total      int64 `json:"total_tokens"`
}

func decodeTokenCount(raw json.RawMessage, previous codexCumulative) (TokenUsage, codexCumulative, bool, error) {
	var info struct {
		Total json.RawMessage `json:"total_token_usage"`
	}
	if err := json.Unmarshal(raw, &info); err != nil || len(bytes.TrimSpace(info.Total)) == 0 || string(bytes.TrimSpace(info.Total)) == "null" {
		if err != nil {
			return TokenUsage{}, codexCumulative{}, false, fmt.Errorf("token_count info is not an object: %w", err)
		}
		return TokenUsage{}, previous, false, nil
	}
	current, err := decodeCumulativeJSON(string(info.Total))
	if err != nil {
		return TokenUsage{}, codexCumulative{}, true, err
	}
	for name, values := range map[string][2]int64{
		"input_tokens":             {current.Input, previous.Input},
		"cached_input_tokens":      {current.CacheRead, previous.CacheRead},
		"cache_write_input_tokens": {current.CacheWrite, previous.CacheWrite},
		"output_tokens":            {current.Output, previous.Output},
		"reasoning_output_tokens":  {current.Reasoning, previous.Reasoning},
		"total_tokens":             {current.Total, previous.Total},
	} {
		if values[0] < 0 || values[0] < values[1] {
			return TokenUsage{}, codexCumulative{}, true, fmt.Errorf("%s regressed from %d to %d", name, values[1], values[0])
		}
	}
	return TokenUsage{
		Input: current.Input - previous.Input, CacheRead: current.CacheRead - previous.CacheRead,
		CacheWrite: current.CacheWrite - previous.CacheWrite, Output: current.Output - previous.Output,
		Reasoning: current.Reasoning - previous.Reasoning,
	}, current, true, nil
}

func decodeCumulativeJSON(raw string) (codexCumulative, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return codexCumulative{}, fmt.Errorf("cumulative usage is not JSON: %w", err)
	}
	var out codexCumulative
	fields := []struct {
		name string
		to   *int64
	}{
		{"input_tokens", &out.Input}, {"cached_input_tokens", &out.CacheRead},
		{"cache_write_input_tokens", &out.CacheWrite}, {"output_tokens", &out.Output},
		{"reasoning_output_tokens", &out.Reasoning}, {"total_tokens", &out.Total},
	}
	for _, field := range fields {
		rawValue, ok := values[field.name]
		if !ok {
			return codexCumulative{}, fmt.Errorf("cumulative usage has no %s", field.name)
		}
		if err := json.Unmarshal(rawValue, field.to); err != nil {
			return codexCumulative{}, fmt.Errorf("cumulative usage %s: %w", field.name, err)
		}
	}
	return out, nil
}

func (c codexCumulative) JSON() string {
	b, _ := json.Marshal(c)
	return string(b)
}

func codexCumulativePublic(c codexCumulative) CodexCumulativeUsage {
	return CodexCumulativeUsage{
		Input: c.Input, CacheRead: c.CacheRead, CacheWrite: c.CacheWrite,
		Output: c.Output, Reasoning: c.Reasoning, Total: c.Total,
	}
}

func codexFunctionArguments(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return "", fmt.Errorf("function arguments string: %w", err)
		}
		if json.Valid([]byte(encoded)) {
			return compactJSON([]byte(encoded), encoded), nil
		}
	}
	return codexJSONValue(raw)
}

func codexJSONValue(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "null", nil
	}
	trimmed := bytes.TrimSpace(raw)
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", fmt.Errorf("tool input string: %w", err)
		}
		b, err := json.Marshal(s)
		return string(b), err
	}
	return compactJSON(trimmed, "null"), nil
}

func codexOutputText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			return s
		}
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if trimmed[0] == '[' && json.Unmarshal(trimmed, &blocks) == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return compactJSON(trimmed, string(trimmed))
}

var codexToolClasses = map[string]CommandClass{
	"exec": CommandShell, "exec_command": CommandShell, "shell": CommandShell, "command_execution": CommandShell,
	"read": CommandRead, "Read": CommandRead, "read_file": CommandRead, "cat": CommandRead,
	"apply_patch": CommandEdit, "edit": CommandEdit, "write_file": CommandEdit,
	"grep": CommandSearch, "rg": CommandSearch, "glob": CommandSearch, "find": CommandSearch,
	"web_search":  CommandSearch,
	"tool_search": CommandMCP,
	"spawn_agent": CommandAgent, "agent": CommandAgent,
}

func ClassifyCodexTool(name string) CommandClass {
	if strings.HasPrefix(name, "mcp__") {
		return CommandMCP
	}
	if class, ok := codexToolClasses[name]; ok {
		return class
	}
	return CommandOther
}

func firstJSONLine(data []byte) ([]byte, bool) {
	for len(data) > 0 {
		nl := bytes.IndexByte(data, '\n')
		if nl < 0 {
			line := bytes.TrimSpace(data)
			return line, len(line) > 0
		}
		line := bytes.TrimSpace(data[:nl])
		data = data[nl+1:]
		if len(line) > 0 {
			return line, true
		}
	}
	return nil, false
}

const (
	TranscriptCodex TranscriptFormat = "codex_rollout"
)
