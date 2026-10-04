package grok

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

// TranscriptGrok is the format of a grok session's updates.jsonl, as
// transcript_cursor and the fact tables record it.
const TranscriptGrok harness.TranscriptFormat = "grok_usage"

// A grok session appends ACP JSONL to updates.jsonl and never rewrites it.
// usage.json in the same directory is a snapshot grok rewrites, so it is
// not the ledger. The usage record is one sessionUpdate of "turn_completed",
// written when the turn ends, and it is the whole turn: modelCalls counts
// the model calls inside it and the file does not price them separately.
//
// inputTokens includes cachedReadTokens. totalTokens is inputTokens plus
// outputTokens, so the cache is a subset of the input and is not added
// again. reasoningTokens is a part of outputTokens. cacheCreationTokens was
// 0 on the measured turn (grok 1.0.46, 2026-10-04); a non-zero value is
// refused, because which bucket it belongs to was not measured.
//
// Tool calls are separate updates and were not in the measured turn, so
// telemetry says they are not read rather than inventing them. Hook lines
// name the operator's own hooks; they are counted and not copied.

const (
	updateTurnCompleted = "turn_completed"
	updateUserMessage   = "user_message_chunk"
	updateAgentMessage  = "agent_message_chunk"
	updateAgentThought  = "agent_thought_chunk"
)

// grokLine is one updates.jsonl record.
type grokLine struct {
	Timestamp int64 `json:"timestamp"`
	Params    struct {
		Update struct {
			SessionUpdate string      `json:"sessionUpdate"`
			PromptID      string      `json:"prompt_id"`
			StopReason    string      `json:"stop_reason"`
			ElapsedMS     int64       `json:"elapsed_ms"`
			Content       grokContent `json:"content"`
			Usage         *grokUsage  `json:"usage"`
		} `json:"update"`
		Meta grokMeta `json:"_meta"`
	} `json:"params"`
}

type grokContent struct {
	Text string `json:"text"`
}

type grokMeta struct {
	EventID          string `json:"eventId"`
	AgentTimestampMs int64  `json:"agentTimestampMs"`
	PromptID         string `json:"promptId"`
}

// grokUsage is the turn_completed usage object. The nested modelUsage of
// the one model must repeat the same counters.
type grokUsage struct {
	InputTokens         int64                     `json:"inputTokens"`
	OutputTokens        int64                     `json:"outputTokens"`
	TotalTokens         int64                     `json:"totalTokens"`
	CachedReadTokens    int64                     `json:"cachedReadTokens"`
	CacheCreationTokens int64                     `json:"cacheCreationTokens"`
	ReasoningTokens     int64                     `json:"reasoningTokens"`
	ModelUsage          map[string]grokModelUsage `json:"modelUsage"`
}

type grokModelUsage struct {
	InputTokens         int64 `json:"inputTokens"`
	OutputTokens        int64 `json:"outputTokens"`
	TotalTokens         int64 `json:"totalTokens"`
	CachedReadTokens    int64 `json:"cachedReadTokens"`
	CacheCreationTokens int64 `json:"cacheCreationTokens"`
	ReasoningTokens     int64 `json:"reasoningTokens"`
}

// parseUpdates reads every complete line. It stops at the first line it
// cannot read, and leaves a trailing line with no newline unconsumed.
func parseUpdates(data []byte) harness.TranscriptBatch {
	b := harness.TranscriptBatch{
		Source:     TranscriptGrok,
		TotalBytes: int64(len(data)),
		Skipped:    map[string]int{},
	}
	text := map[string]string{}
	var offset int64
	for ordinal := 0; ; ordinal++ {
		nl := bytes.IndexByte(data[offset:], '\n')
		if nl < 0 {
			break
		}
		line := data[offset : offset+int64(nl)]
		end := offset + int64(nl) + 1
		if len(bytes.TrimSpace(line)) == 0 {
			offset = end
			continue
		}
		var rec grokLine
		if err := json.Unmarshal(line, &rec); err != nil {
			b.Malformed = &harness.TranscriptMalformed{
				Offset: offset, EndOffset: end, Ordinal: ordinal,
				Reason: harness.MalformedInvalidJSON, Detail: err.Error(),
			}
			break
		}
		kind := rec.Params.Update.SessionUpdate
		switch kind {
		case updateTurnCompleted:
			turn, mal := turnFrom(rec, text[rec.Params.Update.PromptID], offset, ordinal, line)
			if mal != nil {
				mal.Offset, mal.EndOffset, mal.Ordinal = offset, end, ordinal
				b.Malformed = mal
				break
			}
			delete(text, turn.HarnessTurnRef)
			b.Records = append(b.Records, lineRecord(harness.TranscriptKindUsage, turn.SourceRef, offset, ordinal, turn.OccurredAt, line))
			b.Turns = append(b.Turns, turn)
			b.UsageTurns = append(b.UsageTurns, usageFrom(turn, rec.Params.Update.ElapsedMS))
		case updateUserMessage:
			at := lineTime(rec)
			b.Records = append(b.Records, lineRecord(harness.TranscriptKindUser, rec.Params.Meta.EventID, offset, ordinal, at, line))
		case updateAgentMessage:
			at := lineTime(rec)
			id := rec.Params.Meta.PromptID
			if rec.Params.Update.Content.Text != "" && id != "" {
				text[id] += rec.Params.Update.Content.Text
			}
			b.Records = append(b.Records, lineRecord(harness.TranscriptKindAssistant, rec.Params.Meta.EventID, offset, ordinal, at, line))
		case updateAgentThought:
			at := lineTime(rec)
			b.Records = append(b.Records, lineRecord(harness.TranscriptKindContextEvent, rec.Params.Meta.EventID, offset, ordinal, at, line))
		default:
			if kind == "" {
				kind = "unrecognised"
			}
			b.Skipped[kind]++
		}
		if b.Malformed != nil {
			break
		}
		offset = end
	}
	b.ConsumedBytes = offset
	b.PendingBytes = b.TotalBytes - offset
	return b
}

func lineRecord(kind harness.TranscriptKind, ref string, offset int64, ordinal int, at time.Time, line []byte) harness.TranscriptRecord {
	return harness.TranscriptRecord{SourceRef: ref, Kind: kind, Offset: offset, Ordinal: ordinal, OccurredAt: at, RawJSON: string(line)}
}

func lineTime(rec grokLine) time.Time {
	if rec.Params.Meta.AgentTimestampMs > 0 {
		return time.UnixMilli(rec.Params.Meta.AgentTimestampMs).UTC()
	}
	if rec.Timestamp > 0 {
		return time.Unix(rec.Timestamp, 0).UTC()
	}
	return time.Time{}
}

// turnFrom is the one usage turn a turn_completed record prices, or why the
// record cannot be trusted. The caller fills the line's position.
func turnFrom(rec grokLine, text string, offset int64, ordinal int, _ []byte) (harness.TranscriptTurn, *harness.TranscriptMalformed) {
	fail := func(reason, detail string) (harness.TranscriptTurn, *harness.TranscriptMalformed) {
		return harness.TranscriptTurn{}, &harness.TranscriptMalformed{Reason: reason, Detail: detail}
	}
	u := rec.Params.Update
	if u.PromptID == "" {
		return fail(harness.MalformedMissingRecordID, "turn_completed has no prompt_id")
	}
	if rec.Params.Meta.AgentTimestampMs <= 0 {
		return fail(harness.MalformedMissingTimestamp, "turn_completed has no agentTimestampMs")
	}
	if u.Usage == nil {
		return fail(harness.MalformedUsageSnapshot, "turn_completed has no usage")
	}
	if u.StopReason == "" {
		return fail(harness.MalformedUsageSnapshot, "turn_completed has no stop_reason")
	}
	if u.ElapsedMS < 0 {
		return fail(harness.MalformedUsageSnapshot, "turn_completed elapsed_ms is negative")
	}
	usage, model, err := normaliseUsage(*u.Usage)
	if err != nil {
		return fail(harness.MalformedUsageSnapshot, err.Error())
	}
	ended := time.UnixMilli(rec.Params.Meta.AgentTimestampMs).UTC()
	return harness.TranscriptTurn{
		SourceRef: u.PromptID, Offset: offset, Ordinal: ordinal, OccurredAt: ended,
		Model: model, Text: text, StopReason: u.StopReason, Usage: usage, HarnessTurnRef: u.PromptID,
	}, nil
}

// normaliseUsage splits grok's turn total into the four buckets every
// harness reports. Input excludes the cache. A shape that was not measured
// is an error, not a guess.
func normaliseUsage(u grokUsage) (harness.TokenUsage, string, error) {
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.TotalTokens < 0 || u.CachedReadTokens < 0 || u.CacheCreationTokens < 0 || u.ReasoningTokens < 0 {
		return harness.TokenUsage{}, "", fmt.Errorf("a usage counter is negative")
	}
	if u.InputTokens < u.CachedReadTokens {
		return harness.TokenUsage{}, "", fmt.Errorf("cachedReadTokens %d exceeds inputTokens %d", u.CachedReadTokens, u.InputTokens)
	}
	if u.ReasoningTokens > u.OutputTokens {
		return harness.TokenUsage{}, "", fmt.Errorf("reasoningTokens %d exceeds outputTokens %d", u.ReasoningTokens, u.OutputTokens)
	}
	if u.InputTokens+u.OutputTokens != u.TotalTokens {
		return harness.TokenUsage{}, "", fmt.Errorf("totalTokens %d is not inputTokens+outputTokens %d", u.TotalTokens, u.InputTokens+u.OutputTokens)
	}
	if u.CacheCreationTokens != 0 {
		return harness.TokenUsage{}, "", fmt.Errorf("cacheCreationTokens %d is outside the measured shape, which is 0", u.CacheCreationTokens)
	}
	if len(u.ModelUsage) != 1 {
		return harness.TokenUsage{}, "", fmt.Errorf("modelUsage has %d models; the measured turn names one", len(u.ModelUsage))
	}
	var model string
	var nested grokModelUsage
	for name, usage := range u.ModelUsage {
		model, nested = name, usage
	}
	if nested.InputTokens != u.InputTokens || nested.OutputTokens != u.OutputTokens || nested.TotalTokens != u.TotalTokens ||
		nested.CachedReadTokens != u.CachedReadTokens || nested.CacheCreationTokens != u.CacheCreationTokens || nested.ReasoningTokens != u.ReasoningTokens {
		return harness.TokenUsage{}, "", fmt.Errorf("modelUsage[%s] does not match the turn total", model)
	}
	return harness.TokenUsage{
		Input:      u.InputTokens - u.CachedReadTokens,
		Output:     u.OutputTokens,
		CacheRead:  u.CachedReadTokens,
		CacheWrite: u.CacheCreationTokens,
		Reasoning:  u.ReasoningTokens,
	}, model, nil
}

func usageFrom(turn harness.TranscriptTurn, elapsedMS int64) harness.UsageTurn {
	ended := turn.OccurredAt
	return harness.UsageTurn{
		SourceRef: turn.SourceRef, Offset: turn.Offset,
		StartedAt: ended.Add(-time.Duration(elapsedMS) * time.Millisecond), EndedAt: ended,
		Outcome: turn.StopReason, Model: turn.Model, HarnessTurnRef: turn.HarnessTurnRef,
		Usage: turn.Usage, ContextTokens: turn.Usage.ContextTokens(),
		Sample: harness.UsageSample{At: ended, Usage: turn.Usage},
	}
}

// sessionTurnEnded reports whether the session records a turn_completed
// after after.
func sessionTurnEnded(data []byte, after time.Time) bool {
	for _, t := range parseUpdates(data).Turns {
		if t.OccurredAt.After(after) {
			return true
		}
	}
	return false
}

// grokTranscripts is grok's TranscriptSource: updates.jsonl inside the
// session directory a launch named, read whole on every change.
type grokTranscripts struct {
	// home is the profile's Home; empty resolves sessions as grok does.
	home string
}

// grokRuleSessionDir is the rule a grok session is located by: the session
// id mate gave the launch, as the one directory of that name under the
// sessions root.
const grokRuleSessionDir = "grok.session_dir"

// Locate implements TranscriptSource.
func (s grokTranscripts) Locate(req harness.TranscriptLocateRequest) (harness.TranscriptLocation, string) {
	if req.SessionID == "" {
		return harness.TranscriptLocation{}, harness.LocateNoSession
	}
	root := req.Root
	if root == "" {
		var err error
		if root, err = sessionsRoot(s.home); err != nil {
			return harness.TranscriptLocation{}, harness.LocateNotFound
		}
	}
	path, ok := findUpdates(root, req.SessionID)
	if !ok {
		return harness.TranscriptLocation{}, harness.LocateNotFound
	}
	return harness.TranscriptLocation{Path: path, Rule: grokRuleSessionDir}, ""
}

// Read implements TranscriptSource.
func (grokTranscripts) Read(req harness.TranscriptReadRequest) (harness.TranscriptBatch, error) {
	data, err := os.ReadFile(req.Path)
	if err != nil {
		return harness.TranscriptBatch{}, err
	}
	return parseUpdates(data), nil
}

// Telemetry implements TranscriptSource. Each turn_completed is one
// response fact for the whole turn. Tool events are a gap, and so is a
// split of the turn into the modelCalls it counted: the file does not
// price those calls on their own.
func (grokTranscripts) Telemetry(req harness.TelemetryRequest) (harness.TelemetryUpdate, error) {
	up := harness.TelemetryUpdate{Offset: req.Offset, State: req.State, Gaps: []string{
		"tool_events_not_read", "model_calls_are_turn_totals",
	}}
	if req.Offset == req.Batch.ConsumedBytes && !req.Changed {
		return up, nil
	}
	for _, t := range req.Batch.Turns {
		off := t.Offset
		up.Facts = append(up.Facts, telemetry.Fact{
			Version: telemetry.Version, ID: t.SourceRef, Kind: "response", SourceRef: t.SourceRef, SourceOffset: off,
			OccurredAt: t.OccurredAt, MeasurementKind: "normalized", HarnessTurnRef: t.HarnessTurnRef, ResponseID: t.SourceRef,
			Model: t.Model, Text: t.Text, InputTokens: t.Usage.Input, CacheReadTokens: t.Usage.CacheRead,
			CacheWriteTokens: t.Usage.CacheWrite, OutputTokens: t.Usage.Output, ReasoningTokens: t.Usage.Reasoning,
			ContextTokens: t.Usage.ContextTokens(), LedgerRefOffset: &off,
		})
	}
	up.Offset = req.Batch.ConsumedBytes
	return up, nil
}

// grokTurnEnd is grok's TurnEndEvidence: no Stop hook mate reads, but
// turn_completed is written when the turn ends.
type grokTurnEnd struct{}

// LogsAnswers implements TurnEndEvidence.
func (grokTurnEnd) LogsAnswers() bool { return false }

// EndsInTranscript implements TurnEndEvidence.
func (grokTurnEnd) EndsInTranscript() bool { return true }

// TranscriptTurnEnded implements TurnEndEvidence.
func (grokTurnEnd) TranscriptTurnEnded(session []byte, after time.Time) bool {
	return sessionTurnEnded(session, after)
}
