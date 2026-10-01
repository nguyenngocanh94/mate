package pi

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

// TranscriptPi is the format of a pi session file, as transcript_cursor and
// the fact tables record it.
const TranscriptPi harness.TranscriptFormat = "pi_session"

// A pi session (pi 0.99.1, session version 3) is JSONL, one entry per line,
// each with an id and the id of its parent: a tree, appended to and never
// rewritten. The first line is the `session` header; `model_change` and
// `thinking_level_change` record what the session runs on; `message` holds
// one message whole - user, assistant, toolResult or system - written once
// it is complete. An assistant message is one model call and carries that
// call's usage, so nothing has to be withheld until a later line completes
// it, and a final read is an ordinary one.
//
// Usage is per call, like Claude's: `input` excludes `cacheRead` (measured,
// the second call of session 3f0c5a8e: input 260, cacheRead 1920, output 4,
// totalTokens 2184), and `reasoning` is a part of `output` (pi-ai's Usage
// type; the call of session c954e7e3 reasons 104 of its 106).

// piRecord is one session entry, every type's fields in one shape.
type piRecord struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	// model_change
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
	// thinking_level_change
	ThinkingLevel string `json:"thinkingLevel"`
	// message
	Message *piMessage `json:"message"`
}

type piMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Provider   string          `json:"provider"`
	Model      string          `json:"model"`
	Usage      *piUsage        `json:"usage"`
	StopReason string          `json:"stopReason"`
	// Timestamp is when the call was made, in Unix milliseconds; the
	// entry's own timestamp is when its message was complete.
	Timestamp  int64  `json:"timestamp"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	IsError    bool   `json:"isError"`
}

type piUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Reasoning  int64 `json:"reasoning"`
}

// piBlock is one content block of an assistant message.
type piBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// stopToolUse is the stop reason of a call that ran tools: the turn goes on
// with their results. Every other one pi writes ends the turn (pi-ai's
// StopReason: stop, length, error, aborted).
const stopToolUse = "toolUse"

// parsedSession is a session read whole.
type parsedSession struct {
	batch harness.TranscriptBatch
	// changes are the model and thinking-level entries, in order, each with
	// what the session ran on from it.
	changes []runtimeChange
}

// runtimeChange is the model and thinking level a session runs on from one
// entry on.
type runtimeChange struct {
	ref        string
	kind       string
	offset     int64
	occurredAt time.Time
	model      string
	level      string
}

// parseSession reads every complete line of a session. It stops at the
// first line it cannot read, and leaves a trailing line with no newline
// unconsumed: pi is still writing it.
func parseSession(data []byte) parsedSession {
	var out parsedSession
	b := &out.batch
	b.Source = TranscriptPi
	b.TotalBytes = int64(len(data))
	b.Skipped = map[string]int{}
	model, level, turnRef := "", "", ""
	var offset int64
	for ordinal := 0; ; ordinal++ {
		nl := bytes.IndexByte(data[offset:], '\n')
		if nl < 0 {
			break
		}
		line := data[offset : offset+int64(nl)]
		end := offset + int64(nl) + 1
		malformed := func(reason, detail string) {
			b.Malformed = &harness.TranscriptMalformed{Offset: offset, EndOffset: end, Ordinal: ordinal, Reason: reason, Detail: detail}
		}
		if len(bytes.TrimSpace(line)) == 0 {
			offset = end
			continue
		}
		var r piRecord
		if err := json.Unmarshal(line, &r); err != nil {
			malformed(harness.MalformedInvalidJSON, err.Error())
			break
		}
		if r.Type == "" {
			malformed(harness.MalformedMissingType, "entry has no type")
			break
		}
		at, err := time.Parse(time.RFC3339Nano, r.Timestamp)
		if err != nil {
			malformed(harness.MalformedMissingTimestamp, "entry "+r.Type+" has no readable timestamp")
			break
		}
		if r.ID == "" {
			malformed(harness.MalformedMissingRecordID, "entry "+r.Type+" has no id")
			break
		}
		rec := harness.TranscriptRecord{SourceRef: r.ID, Offset: offset, Ordinal: ordinal, OccurredAt: at, RawJSON: string(line)}
		switch r.Type {
		case "session", "custom_message":
			rec.Kind = harness.TranscriptKindContextEvent
		case "model_change", "thinking_level_change":
			rec.Kind = harness.TranscriptKindContextEvent
			if r.Type == "model_change" {
				model = modelName(r.Provider, r.ModelID)
			} else {
				level = r.ThinkingLevel
			}
			out.changes = append(out.changes, runtimeChange{ref: r.ID, kind: r.Type, offset: offset, occurredAt: at, model: model, level: level})
		case "compaction":
			rec.Kind = harness.TranscriptKindContextEvent
			b.Compactions = append(b.Compactions, harness.TranscriptCompaction{Offset: offset, OccurredAt: at, Trigger: "pi.compaction"})
		case "message":
			if r.Message == nil {
				malformed(harness.MalformedUserShape, "message entry has no message")
				break
			}
			m := r.Message
			switch m.Role {
			case "user":
				rec.Kind = harness.TranscriptKindUser
				// A prompt opens the harness turn; pi names it by nothing
				// but the entry that holds it.
				turnRef = r.ID
			case "assistant":
				if m.Usage == nil {
					malformed(harness.MalformedAssistantShape, "assistant message has no usage")
					break
				}
				var blocks []piBlock
				if err := json.Unmarshal(m.Content, &blocks); err != nil {
					malformed(harness.MalformedAssistantShape, "assistant content is not a block list: "+err.Error())
					break
				}
				rec.Kind = harness.TranscriptKindAssistant
				callModel := modelName(m.Provider, m.Model)
				if callModel == "" {
					callModel = model
				}
				var text []string
				for _, blk := range blocks {
					switch blk.Type {
					case "text":
						if blk.Text != "" {
							text = append(text, blk.Text)
						}
					case "toolCall":
						b.ToolCalls = append(b.ToolCalls, harness.TranscriptToolCall{
							SourceRef: blk.ID, TurnSourceRef: r.ID, Offset: offset, Ordinal: ordinal,
							ToolName: blk.Name, CommandClass: commandClass(blk.Name),
							InputJSON: harness.CompactJSON(blk.Arguments, "{}"), StartedAt: at,
						})
					}
				}
				b.Turns = append(b.Turns, harness.TranscriptTurn{
					SourceRef: r.ID, Offset: offset, Ordinal: ordinal, OccurredAt: at,
					Model: callModel, Text: strings.Join(text, "\n"), StopReason: m.StopReason,
					Usage: harness.TokenUsage{
						Input: m.Usage.Input, Output: m.Usage.Output,
						CacheRead: m.Usage.CacheRead, CacheWrite: m.Usage.CacheWrite, Reasoning: m.Usage.Reasoning,
					},
					HarnessTurnRef: turnRef,
				})
			case "toolResult":
				rec.Kind = harness.TranscriptKindToolResult
				output := harness.MessageText(m.Content)
				b.ToolResults = append(b.ToolResults, harness.TranscriptToolResult{
					SourceRef: r.ID, CallID: m.ToolCallID, Offset: offset, Ordinal: ordinal,
					OutputText: output, OutputBytes: int64(len(output)), IsError: m.IsError, CompletedAt: at,
				})
			case "system":
				rec.Kind = harness.TranscriptKindSystem
			default:
				// pi's own message kinds (a `!` bash run, an extension's
				// message) are context, not a call.
				rec.Kind = harness.TranscriptKindContextEvent
			}
		default:
			// label, session_info, custom, usage, context_edit,
			// branch_summary: pi's bookkeeping, or an entry newer than
			// this reader. Counted, not copied.
			b.Skipped[r.Type]++
			offset = end
			continue
		}
		if b.Malformed != nil {
			break
		}
		b.Records = append(b.Records, rec)
		offset = end
	}
	b.ConsumedBytes = offset
	b.PendingBytes = b.TotalBytes - b.ConsumedBytes
	b.NextState = harness.TranscriptParseState{HarnessTurnRef: turnRef}
	return out
}

// modelName is a call's model as pi's --model names it, provider/id.
func modelName(provider, id string) string {
	switch {
	case id == "":
		return ""
	case provider == "":
		return id
	}
	return provider + "/" + id
}

// commandClass maps pi's built-in tools; anything else, an extension's
// tool among them, is other.
func commandClass(tool string) harness.CommandClass {
	switch tool {
	case "bash":
		return harness.CommandShell
	case "read":
		return harness.CommandRead
	case "edit", "write":
		return harness.CommandEdit
	case "grep", "find", "ls":
		return harness.CommandSearch
	}
	return harness.CommandOther
}

// sessionTurnEnded reports whether the session records a turn that ended
// after after: an assistant message, written when its call completed, that
// stopped for anything but running tools.
func sessionTurnEnded(data []byte, after time.Time) bool {
	for _, t := range parseSession(data).batch.Turns {
		if t.StopReason != stopToolUse && t.OccurredAt.After(after) {
			return true
		}
	}
	return false
}

// piTranscripts is pi's TranscriptSource: the session file a launch named
// (--session-dir, --session-id), read whole on every change.
type piTranscripts struct {
	// root is the profile's SessionsDir; empty resolves it as pi does
	// (sessionsRoot).
	root string
}

// piRuleSessionDir is the rule a pi session is located by: the session id
// mate gave the launch, and pi's naming rule, `*_<id>.jsonl` under the
// sessions root.
const piRuleSessionDir = "pi.session_dir"

// Locate implements TranscriptSource. pi names its session by the id mate
// gave it at launch; the file appears once the first prompt is sent.
func (s piTranscripts) Locate(req harness.TranscriptLocateRequest) (harness.TranscriptLocation, string) {
	if req.SessionID == "" {
		return harness.TranscriptLocation{}, harness.LocateNoSession
	}
	root := req.Root
	if root == "" {
		var err error
		if root, err = sessionsRoot(s.root); err != nil {
			return harness.TranscriptLocation{}, harness.LocateNotFound
		}
	}
	path, ok := findSession(root, req.SessionID)
	if !ok {
		return harness.TranscriptLocation{}, harness.LocateNotFound
	}
	return harness.TranscriptLocation{Path: path, Rule: piRuleSessionDir}, ""
}

// Read implements TranscriptSource. A session is read whole: every message
// in it is already complete, so a read at rest is no different.
func (s piTranscripts) Read(req harness.TranscriptReadRequest) (harness.TranscriptBatch, error) {
	data, err := os.ReadFile(req.Path)
	if err != nil {
		return harness.TranscriptBatch{}, err
	}
	b := parseSession(data).batch
	normalise(&b)
	return b, nil
}

// normalise makes one usage turn per assistant message, which is one model
// call. pi reports usage per call with input excluding the cache, so a
// turn's usage is the message's own. A call ends when its message is
// written, or when the last tool it ran returns.
func normalise(b *harness.TranscriptBatch) {
	results := map[string]harness.TranscriptToolResult{}
	for _, res := range b.ToolResults {
		if _, seen := results[res.CallID]; !seen {
			results[res.CallID] = res
		}
	}
	calls := map[string][]harness.TranscriptToolCall{}
	for _, c := range b.ToolCalls {
		calls[c.TurnSourceRef] = append(calls[c.TurnSourceRef], c)
	}
	b.UsageTurns = nil
	for _, t := range b.Turns {
		ended := t.OccurredAt
		for _, c := range calls[t.SourceRef] {
			if res, ok := results[c.SourceRef]; ok && res.CompletedAt.After(ended) {
				ended = res.CompletedAt
			}
		}
		b.UsageTurns = append(b.UsageTurns, harness.UsageTurn{
			SourceRef: t.SourceRef, Offset: t.Offset,
			StartedAt: t.OccurredAt, EndedAt: ended,
			Outcome: t.StopReason, Model: t.Model, HarnessTurnRef: t.HarnessTurnRef,
			Usage: t.Usage, ContextTokens: t.Usage.ContextTokens(), Calls: calls[t.SourceRef],
			Sample: harness.UsageSample{At: t.OccurredAt, Usage: t.Usage},
		})
	}
}

// Telemetry implements TranscriptSource: the facts the session's ledger
// verified, and one context fact for each model and thinking-level entry,
// carrying what the session runs on from it. pi clamps the requested
// --thinking to what the model supports without saying so (measured:
// deepseek-flash takes medium as high, xhigh as max), so the level a Crew
// really thinks at is the one this reads back, and the dashboard reports it
// as the runtime's effort. The facts are emitted again whenever the session
// grew, as Claude's are.
func (s piTranscripts) Telemetry(req harness.TelemetryRequest) (harness.TelemetryUpdate, error) {
	up := harness.TelemetryUpdate{Offset: req.Offset, State: req.State, Gaps: []string{
		"native_execution_unavailable", "native_process_unavailable", "native_timing_unavailable",
	}}
	if req.Offset == req.Batch.ConsumedBytes && !req.Changed {
		return up, nil
	}
	data, err := os.ReadFile(req.Path)
	if err != nil {
		return up, err
	}
	parsed := parseSession(data)
	for _, c := range parsed.changes {
		up.Facts = append(up.Facts, telemetry.Fact{
			Version: telemetry.Version, ID: c.ref, Kind: "context", SourceRef: c.ref, SourceOffset: c.offset,
			OccurredAt: c.occurredAt, MeasurementKind: "native", Phase: c.kind, Model: c.model, Effort: c.level,
		})
	}
	up.Facts = append(up.Facts, ledgerFacts(req.Batch)...)
	up.Offset = req.Batch.ConsumedBytes
	return up, nil
}

// ledgerFacts are the responses, tool calls, tool results and prompts the
// batch already verified, as facts.
func ledgerFacts(b harness.TranscriptBatch) []telemetry.Fact {
	var out []telemetry.Fact
	for _, t := range b.Turns {
		off := t.Offset
		out = append(out, telemetry.Fact{
			Version: telemetry.Version, ID: t.SourceRef, Kind: "response", SourceRef: t.SourceRef, SourceOffset: off,
			OccurredAt: t.OccurredAt, MeasurementKind: "normalized", HarnessTurnRef: t.HarnessTurnRef, ResponseID: t.SourceRef,
			Model: t.Model, Text: t.Text, InputTokens: t.Usage.Input, CacheReadTokens: t.Usage.CacheRead,
			CacheWriteTokens: t.Usage.CacheWrite, OutputTokens: t.Usage.Output, ReasoningTokens: t.Usage.Reasoning,
			ContextTokens: t.Usage.ContextTokens(), LedgerRefOffset: &off,
		})
	}
	calls := map[string]harness.TranscriptToolCall{}
	for _, c := range b.ToolCalls {
		calls[c.SourceRef] = c
		at := c.StartedAt
		out = append(out, telemetry.Fact{
			Version: telemetry.Version, ID: c.SourceRef, Kind: "tool_call", SourceRef: c.SourceRef, SourceOffset: c.Offset,
			OccurredAt: at, MeasurementKind: "normalized", ResponseID: c.TurnSourceRef, WrapperRef: c.SourceRef,
			Tool: c.ToolName, Command: c.InputJSON, StartedAt: &at,
		})
	}
	for _, r := range b.ToolResults {
		at := r.CompletedAt
		c := calls[r.CallID]
		f := telemetry.Fact{
			Version: telemetry.Version, ID: r.CallID, Kind: "tool_result", SourceRef: r.SourceRef, SourceOffset: r.Offset,
			OccurredAt: at, MeasurementKind: "normalized", ResponseID: c.TurnSourceRef, WrapperRef: r.CallID,
			Tool: c.ToolName, Command: c.InputJSON, CompletedAt: &at, Output: harness.OutputFact(r.OutputText, nil),
		}
		if r.IsError {
			f.Status = "failed"
		}
		out = append(out, f)
	}
	for _, r := range b.Records {
		if r.Kind != harness.TranscriptKindUser {
			continue
		}
		var e struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(r.RawJSON), &e) != nil {
			continue
		}
		if text := harness.MessageText(e.Message.Content); text != "" {
			out = append(out, telemetry.Fact{
				Version: telemetry.Version, ID: r.SourceRef, Kind: "prompt", SourceRef: r.SourceRef, SourceOffset: r.Offset,
				OccurredAt: r.OccurredAt, MeasurementKind: "normalized", HarnessTurnRef: r.SourceRef, Text: text,
			})
		}
	}
	return out
}
