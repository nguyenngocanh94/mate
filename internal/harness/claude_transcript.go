package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Claude's session transcript is newline-delimited JSON at
// ~/.claude/projects/<slug>/<session-id>.jsonl (one record per line). This file
// is the only place in mate that knows those shapes; everything it returns is
// the normalized vocabulary in transcript.go.
//
// Four measured shape facts drive the parser and are not obvious from the file
// format:
//
//   - One assistant API response is written as several JSONL records, one per
//     content block, sharing a message.id. A turn is therefore one message.id,
//     not one record; emitting a turn per record would multiply the token cost.
//   - Each of those records restates the message's usage, and the restatement
//     is a running snapshot, not always a repeat. In the measured corpus every
//     main-stream group repeats identical numbers, but 342 subagent groups grow
//     - output and thinking rising while input and both cache buckets stay
//     fixed - so the last record is the completed cost. Keeping the first
//     record's numbers undercounted that corpus by 412,965 output and 252,113
//     thinking tokens, all of it subagent work.
//   - Attribution needs a unit larger than one response: the harness turn, from
//     a prompt to the end of the work it caused. Only user records carry its id
//     (promptId), so an incremental parse must be handed the turn open at its
//     cursor; see TranscriptParseState.
//   - Non-agent records dominate by volume (attachment and other housekeeping
//     records were roughly ten times the agent activity in the measured
//     corpus). They are skipped, and their types are counted in
//     TranscriptBatch.Skipped rather than parsed or turned into facts.

// ParseTranscript parses an incremental byte range of a Claude session JSONL
// file into normalized facts. It is pure over the bytes it is given: Offset and
// Ordinal are positions within data (a record's byte offset and its 0-based
// line index). A caller handing it the whole file therefore gets absolute file
// positions, while one that seeks to transcript_cursor.byte_offset and passes
// only the tail must rebase both against its own base. Record identity is
// stable wherever the run starts: Records carry the record uuid, turns are
// keyed by message id, and a tool result is keyed by the harness tool id.
//
// Incremental means a message may be split across two calls. Claude writes one
// assistant API response as several records - one per content block, each with
// the same message.id - and appends them over a short window, so a range that
// ends in the middle of one would otherwise yield a partial turn that could
// never be completed (agent_turn is unique on (binding_id, source, source_ref)
// and an update trigger aborts). The parser therefore withholds the trailing
// message group whole: no record, turn, tool call or tool result at or after
// its first record is returned, ConsumedBytes stops at that first record, and
// OpenSourceRef names the message. A caller that resumes from ConsumedBytes
// re-reads the group and sees it once, complete, with usage counted once. This
// is sound because Claude message ids do not recur: once a later id appears,
// the earlier one is closed (measured over 330 sessions: 40,096 assistant
// records, no id reappearing after a newer one, and every session's last line
// complete). A format version that broke that property would collide on the
// fact tables' unique indexes - loudly - rather than double-count silently.
//
// A caller whose range is at rest - a session's final sync, after the agent has
// been observed stopped - would use ParseTranscriptFinal instead, which flushes
// that group. The timeline uses this only for immutable snapshots created after context
// refresh confirmed a runtime stop. Live transcripts still use the deferral.
//
// A trailing fragment with no newline is an active-file boundary: it is not
// consumed and does not fail. The first complete line that cannot be classified
// stops the parse; ConsumedBytes stays before it and Malformed names it.
// Everything before the stop is still returned. A malformed line remains
// unconsumed. If it can be parsed well enough to prove it is outside the open
// assistant message group, the preceding group is emitted; otherwise the group
// is withheld too. This fail-closed rule prevents a malformed record from
// freezing an incomplete usage snapshot into history.
func (Claude) ParseTranscript(state TranscriptParseState, data []byte) TranscriptBatch {
	return claudeParseRange(state, data, false)
}

// ParseTranscriptFinal parses a range the caller has established is at rest,
// emitting the trailing message group ParseTranscript would withhold. It is the
// same parse with the deferral switched off, so every other guarantee -
// malformed handling, the active-file boundary, usage counted once per message -
// is identical.
//
// The at-rest precondition is the caller's, and nothing here can verify it: a
// range ending on a complete newline-terminated record is byte-identical to one
// whose writer has stopped mid-message, because the last record of a message is
// not marked. A live call can therefore emit a truncated turn - the blocks
// written so far - which is unamendable once stored (agent_turn is unique on
// (binding_id, source, source_ref) and never updated; the tables' uniqueness
// then turns a later sync into a failure rather than a repair). What the bytes
// do show is still withheld, exactly as ParseTranscript withholds it: a range
// ending in a partial line, or one a malformed line stopped early, keeps the
// group back and names it in OpenSourceRef. That is necessary, not sufficient.
//
// Use it only after the harness runtime has been positively stopped and
// the bytes copied to an immutable snapshot. Context refresh supplies this
// precondition through StopMate, FreezeMateSession and Located.Finalized;
// a live transcript, even one whose composer looks idle, does not.
//
// TestClaudeParseTranscriptFinalCannotProveAtRestFromBytes is the executable
// statement of this precondition.
func (Claude) ParseTranscriptFinal(state TranscriptParseState, data []byte) TranscriptBatch {
	return claudeParseRange(state, data, true)
}

func claudeParseRange(state TranscriptParseState, data []byte, final bool) TranscriptBatch {
	p := &claudeParse{
		turnRef: state.HarnessTurnRef,
		seedRef: state.HarnessTurnRef,
		batch: TranscriptBatch{
			Source:     TranscriptClaude,
			TotalBytes: int64(len(data)),
			Skipped:    map[string]int{},
		},
		turnIdx: map[string]int{},
	}
	var offset int64
	ordinal := 0
	stop := int64(-1)
	fragment := false
	for offset < int64(len(data)) {
		nl := bytes.IndexByte(data[offset:], '\n')
		if nl < 0 {
			fragment = true // trailing unterminated fragment
			break
		}
		line := data[offset : offset+int64(nl)]
		lineOffset := offset
		offset += int64(nl) + 1
		p.lineEnd = offset
		lineOrdinal := ordinal
		ordinal++

		trimmed := bytes.TrimRight(line, "\r")
		if len(bytes.TrimSpace(trimmed)) == 0 {
			continue
		}
		raw := string(trimmed)

		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(trimmed, &head); err != nil {
			p.malformed(lineOffset, lineOrdinal, MalformedInvalidJSON, err.Error())
			stop = lineOffset
			break
		}
		if head.Type == "" {
			p.malformed(lineOffset, lineOrdinal, MalformedMissingType, "record has no type")
			stop = lineOffset
			break
		}

		var rec claudeRecord
		switch head.Type {
		case "assistant", "user", "system":
			if err := json.Unmarshal(trimmed, &rec); err != nil {
				p.malformed(lineOffset, lineOrdinal, MalformedInvalidJSON, err.Error())
				stop = lineOffset
				break
			}
			switch head.Type {
			case "assistant":
				var messageHead struct {
					ID string `json:"id"`
				}
				sameMessage := p.lastMsgID != "" && json.Unmarshal(rec.Message, &messageHead) == nil && messageHead.ID == p.lastMsgID
				p.addAssistant(rec, raw, lineOffset, lineOrdinal)
				if p.batch.Malformed != nil && !sameMessage && messageHead.ID != "" {
					p.malformedOutsideOpenGroup = true
				}
			case "user":
				p.addUser(rec, raw, lineOffset, lineOrdinal)
				if p.batch.Malformed != nil {
					p.malformedOutsideOpenGroup = true
				}
			case "system":
				p.addSystem(rec, raw, lineOffset, lineOrdinal)
				if p.batch.Malformed != nil {
					p.malformedOutsideOpenGroup = true
				}
			}
		default:
			// Housekeeping and unknown future types alike: the parser cannot
			// classify it into the kind vocabulary, so it is not copied. The
			// count lets a caller notice an unrecognised type without trusting
			// it. Known harness housekeeping observed in 330 sessions of CLI
			// 2.1.269 (ADR 0016 names the first group; the rest were censused
			// here): attachment, file-history-snapshot, file-history-delta,
			// ai-title, atis-latch, mode, permission-mode, last-prompt,
			// queue-operation, pr-link, cost-state, agent-name, custom-title.
			p.batch.Skipped[head.Type]++
		}
		if p.batch.Malformed != nil {
			stop = p.batch.Malformed.Offset
			break
		}
	}
	p.batch.Turns = p.turns
	if stop >= 0 {
		p.batch.ConsumedBytes = stop
	} else {
		p.batch.ConsumedBytes = offset
	}
	// A group is withheld unless a later message id closed it (ParseTranscript,
	// which never emits a group that may still grow) or the caller has
	// established the range is at rest (ParseTranscriptFinal, whose caller owns
	// a precondition the bytes cannot show). Within that, the range's own
	// visible end still counts: a partial trailing line is evidence a writer
	// was active, so even a final parse withholds there.
	if fragment || (!final && p.batch.Malformed == nil) || (final && p.batch.Malformed != nil) || (p.batch.Malformed != nil && !p.malformedOutsideOpenGroup) {
		p.withholdOpenMessage()
	}
	p.batch.NextState = p.stateAt(p.batch.ConsumedBytes)
	p.batch.PendingBytes = p.batch.TotalBytes - p.batch.ConsumedBytes
	return p.batch
}

type claudeParse struct {
	batch   TranscriptBatch
	turns   []TranscriptTurn
	turnIdx map[string]int
	// lastMsgID is the message id of the last assistant record seen. Its group
	// is the only one a later write can extend.
	lastMsgID string
	// turnRef is the harness turn open at the record being parsed, seeded
	// from the caller's state and updated by every user record that carries a
	// promptId. turnRefAt records each change with the offset of the record
	// that made it, so the state can be reported as of ConsumedBytes rather
	// than as of the end of the range.
	turnRef                   string
	seedRef                   string
	turnRefAt                 []turnRefChange
	lineEnd                   int64
	malformedOutsideOpenGroup bool
}

type turnRefChange struct {
	offset int64
	ref    string
}

// stateAt returns the harness turn open immediately before offset: the last
// change made by a record that lies strictly before it. A change at exactly
// offset belongs to a record the caller has not consumed, so it does not count.
func (p *claudeParse) stateAt(offset int64) TranscriptParseState {
	ref := p.seedRef
	for _, c := range p.turnRefAt {
		if c.offset >= offset {
			break
		}
		ref = c.ref
	}
	return TranscriptParseState{HarnessTurnRef: ref}
}

// withholdOpenMessage drops the trailing message group and moves
// ConsumedBytes back to its first record. The group's first record offset is
// the offset of the turn that grouping created for it, so no separate index is
// needed. Every fact the parser produced for the group is at or after that
// offset - including a tool result the group's own tool calls produced - so
// filtering by offset drops exactly the group and nothing else.
func (p *claudeParse) withholdOpenMessage() {
	if p.lastMsgID == "" {
		return
	}
	i, ok := p.turnIdx[p.lastMsgID]
	if !ok {
		return
	}
	cut := p.turns[i].Offset
	if cut >= p.batch.ConsumedBytes {
		// Unreachable today: every record of the group is parsed, so the
		// group starts before the cursor (which a stop or the end of the
		// range put past it). Kept because a cursor that moved forward here
		// would skip bytes and silently lose the group.
		return
	}
	before := len(p.batch.Records)
	p.batch.Records = filterBefore(p.batch.Records, cut, func(r TranscriptRecord) int64 { return r.Offset })
	p.batch.Turns = filterBefore(p.batch.Turns, cut, func(t TranscriptTurn) int64 { return t.Offset })
	p.batch.ToolCalls = filterBefore(p.batch.ToolCalls, cut, func(c TranscriptToolCall) int64 { return c.Offset })
	p.batch.ToolResults = filterBefore(p.batch.ToolResults, cut, func(r TranscriptToolResult) int64 { return r.Offset })
	p.batch.OpenSourceRef = p.lastMsgID
	p.batch.OpenOffset = cut
	p.batch.OpenRecords = before - len(p.batch.Records)
	p.batch.ConsumedBytes = cut
}

// filterBefore keeps the facts whose offset is strictly before cut, in order.
func filterBefore[T any](facts []T, cut int64, at func(T) int64) []T {
	kept := facts[:0]
	for _, fact := range facts {
		if at(fact) < cut {
			kept = append(kept, fact)
		}
	}
	return kept
}

func (p *claudeParse) malformed(offset int64, ordinal int, reason, detail string) {
	p.batch.Malformed = &TranscriptMalformed{Offset: offset, EndOffset: p.lineEnd, Ordinal: ordinal, Reason: reason, Detail: detail}
}

// claudeRecord is the subset of a copied record's top-level shape the parser
// needs. message is kept raw because its content is polymorphic.
type claudeRecord struct {
	Type      string          `json:"type"`
	UUID      string          `json:"uuid"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
	// PromptID opens and names a harness turn. Every count in this file comes
	// from one census of the local corpus, taken 2026-09-13 over 334
	// transcripts and 192,322 records; it is a living tree, so a later census
	// will not reproduce them exactly. PromptID appears on user records only -
	// 25,587 of 25,820 - and on no assistant record at all (0 of 41,479),
	// which is why an incremental parse cannot derive the turn from its own
	// tail and needs carried state.
	PromptID string `json:"promptId"`
	// ParentUUID is the record's predecessor in Claude's own record chain. It
	// is decoded so the shape is accounted for and not silently ignored, but
	// the parser deliberately does not build the graph from it: a subagent
	// stream's first record has parentUuid null (19 of 19 measured), so the
	// chain does not reach across streams and is not what joins a child to
	// its parent - a shared PromptID is (19 of 19).
	ParentUUID *string `json:"parentUuid"`
	// IsSidechain marks a record written by a subagent, and AgentID names
	// which. Measured: both appear on every record of a subagents/agent-*.jsonl
	// stream (2,540 of 2,540) and on no record of a main stream (0 of
	// 189,782), and each of the 19 such files carries exactly one agentId.
	IsSidechain bool   `json:"isSidechain"`
	AgentID     string `json:"agentId"`
}

// subagentID is the subagent this record belongs to, or empty for the main
// stream. A record that says it is a sidechain without naming its agent is
// unknown, not guessable, so it is reported as a shape failure rather than
// attributed to the main stream.
func (r claudeRecord) subagentID() (string, bool) {
	if !r.IsSidechain {
		return "", true
	}
	if r.AgentID == "" {
		return "", false
	}
	return r.AgentID, true
}

type claudeMessage struct {
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	StopReason string          `json:"stop_reason"`
	Usage      *claudeUsage    `json:"usage"`
	Content    json.RawMessage `json:"content"`
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	OutputTokensDetails      *struct {
		ThinkingTokens int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

func (u claudeUsage) tokenUsage() TokenUsage {
	t := TokenUsage{
		Input:      u.InputTokens,
		Output:     u.OutputTokens,
		CacheRead:  u.CacheReadInputTokens,
		CacheWrite: u.CacheCreationInputTokens,
	}
	if u.OutputTokensDetails != nil {
		t.Reasoning = u.OutputTokensDetails.ThinkingTokens
	}
	return t
}

// usageSnapshotAdvance checks that a later record of one assistant message
// carries a usage snapshot that can be a continuation of the earlier one, and
// names the invariant that failed if it cannot. The measured shape is that
// input and both cache buckets are fixed for a message while output and
// thinking only grow (corpus: 342 growing groups, every one of them
// output-monotonic, thinking-monotonic and last-is-max; no group anywhere
// varied input or either cache bucket).
//
// This is the assumption "the last snapshot is the completed cost" rests on,
// so a record that breaks it means the parser's model of the format is wrong
// and its arithmetic can no longer be trusted. It is refused as a malformed
// line rather than absorbed, in the same fail-closed way as an unclassifiable
// shape: a wrong number reported confidently is worse than a stalled cursor.
func usageSnapshotAdvance(prev, next TokenUsage) string {
	switch {
	case next.Input != prev.Input:
		return fmt.Sprintf("input_tokens changed within one message: %d then %d", prev.Input, next.Input)
	case next.CacheRead != prev.CacheRead:
		return fmt.Sprintf("cache_read_input_tokens changed within one message: %d then %d", prev.CacheRead, next.CacheRead)
	case next.CacheWrite != prev.CacheWrite:
		return fmt.Sprintf("cache_creation_input_tokens changed within one message: %d then %d", prev.CacheWrite, next.CacheWrite)
	case next.Output < prev.Output:
		return fmt.Sprintf("output_tokens went backwards within one message: %d then %d", prev.Output, next.Output)
	case next.Reasoning < prev.Reasoning:
		return fmt.Sprintf("thinking_tokens went backwards within one message: %d then %d", prev.Reasoning, next.Reasoning)
	}
	return ""
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   *bool           `json:"is_error"`
}

func (p *claudeParse) addAssistant(rec claudeRecord, raw string, offset int64, ordinal int) {
	if rec.UUID == "" {
		p.malformed(offset, ordinal, MalformedMissingRecordID, "assistant record has no uuid")
		return
	}
	ts, err := parseTranscriptTime(rec.Timestamp)
	if err != nil {
		p.malformed(offset, ordinal, MalformedMissingTimestamp, err.Error())
		return
	}
	var msg claudeMessage
	if err := json.Unmarshal(rec.Message, &msg); err != nil {
		p.malformed(offset, ordinal, MalformedAssistantShape, "message is not an object")
		return
	}
	if msg.ID == "" {
		p.malformed(offset, ordinal, MalformedAssistantShape, "assistant message has no id")
		return
	}
	// Claude always writes usage for an assistant response. Absent usage would
	// force a turn with invented zeros, so it is refused rather than defaulted.
	if msg.Usage == nil {
		p.malformed(offset, ordinal, MalformedAssistantShape, "assistant message has no usage")
		return
	}
	blocks, err := decodeClaudeContent(msg.Content)
	if err != nil {
		p.malformed(offset, ordinal, MalformedAssistantShape, err.Error())
		return
	}
	// Validate every contribution before applying any of it, so a line that
	// fails midway never leaves a half-attached turn or a raw record for a line
	// the caller will re-read.
	for _, blk := range blocks {
		if blk.Type == "tool_use" && (blk.ID == "" || blk.Name == "") {
			p.malformed(offset, ordinal, MalformedToolCallShape, "tool_use block has no id or name")
			return
		}
	}
	usage := msg.Usage.tokenUsage()
	sub, ok := rec.subagentID()
	if !ok {
		p.malformed(offset, ordinal, MalformedAssistantShape, "record is marked isSidechain but names no agentId")
		return
	}
	if i, ok := p.turnIdx[msg.ID]; ok {
		if why := usageSnapshotAdvance(p.turns[i].Usage, usage); why != "" {
			p.malformed(offset, ordinal, MalformedUsageSnapshot, why)
			return
		}
		// One response belongs to one harness turn and one stream. Measured
		// over 23,013 message groups, none spans two of either and none is
		// interleaved with another message's records, so a group that does is
		// a shape this parser cannot attribute - and attributing usage to the
		// wrong turn is exactly the error this field exists to prevent.
		if p.turns[i].HarnessTurnRef != p.turnRef {
			p.malformed(offset, ordinal, MalformedTurnIdentitySplit,
				fmt.Sprintf("message %s spans two harness turns: %q then %q", msg.ID, p.turns[i].HarnessTurnRef, p.turnRef))
			return
		}
		if p.turns[i].SubagentID != sub {
			p.malformed(offset, ordinal, MalformedTurnIdentitySplit,
				fmt.Sprintf("message %s spans two subagents: %q then %q", msg.ID, p.turns[i].SubagentID, sub))
			return
		}
	}

	p.batch.Records = append(p.batch.Records, TranscriptRecord{
		SourceRef: rec.UUID, Kind: TranscriptKindAssistant, Offset: offset, Ordinal: ordinal,
		OccurredAt: ts, RawJSON: raw,
	})

	i, ok := p.turnIdx[msg.ID]
	p.lastMsgID = msg.ID
	if !ok {
		i = len(p.turns)
		p.turnIdx[msg.ID] = i
		p.turns = append(p.turns, TranscriptTurn{
			SourceRef: msg.ID, Offset: offset, Ordinal: ordinal, OccurredAt: ts,
			Model: msg.Model, StopReason: msg.StopReason, Usage: usage,
			HarnessTurnRef: p.turnRef, SubagentID: sub,
		})
	} else {
		// A later record of the same response carries the message metadata
		// again and a fresh usage snapshot. The snapshot is replaced, never
		// added: the records of one message report overlapping running totals
		// of the same response, so the last one is the completed cost and a
		// sum would multiply it.
		p.turns[i].Usage = usage
		if p.turns[i].Model == "" {
			p.turns[i].Model = msg.Model
		}
		if p.turns[i].StopReason == "" {
			p.turns[i].StopReason = msg.StopReason
		}
	}

	for _, blk := range blocks {
		switch blk.Type {
		case "text":
			p.turns[i].Text = joinTranscriptText(p.turns[i].Text, blk.Text)
		case "tool_use":
			p.batch.ToolCalls = append(p.batch.ToolCalls, TranscriptToolCall{
				SourceRef:     blk.ID,
				TurnSourceRef: msg.ID,
				Offset:        offset,
				Ordinal:       ordinal,
				ToolName:      blk.Name,
				CommandClass:  ClassifyClaudeTool(blk.Name),
				InputJSON:     compactJSON(blk.Input, "{}"),
				StartedAt:     ts,
			})
		}
	}
}

func (p *claudeParse) addUser(rec claudeRecord, raw string, offset int64, ordinal int) {
	if rec.UUID == "" {
		p.malformed(offset, ordinal, MalformedMissingRecordID, "user record has no uuid")
		return
	}
	ts, err := parseTranscriptTime(rec.Timestamp)
	if err != nil {
		p.malformed(offset, ordinal, MalformedMissingTimestamp, err.Error())
		return
	}
	var msg claudeMessage
	if err := json.Unmarshal(rec.Message, &msg); err != nil {
		p.malformed(offset, ordinal, MalformedUserShape, "user message is not an object")
		return
	}
	blocks, err := decodeClaudeContent(msg.Content)
	if err != nil {
		p.malformed(offset, ordinal, MalformedUserShape, err.Error())
		return
	}

	// Validate before applying, as for an assistant line: a tool_result without
	// an id cannot be attached to a call, so the line is refused whole.
	results := make([]claudeBlock, 0, len(blocks))
	for _, blk := range blocks {
		if blk.Type != "tool_result" {
			continue
		}
		if blk.ToolUseID == "" {
			p.malformed(offset, ordinal, MalformedToolResultShape, "tool_result block has no tool_use_id")
			return
		}
		results = append(results, blk)
	}
	if _, ok := rec.subagentID(); !ok {
		p.malformed(offset, ordinal, MalformedUserShape, "record is marked isSidechain but names no agentId")
		return
	}
	kind := TranscriptKindUser
	if len(results) > 0 {
		kind = TranscriptKindToolResult
	}
	// Only a prompt opens or ends a harness turn. A user record is a prompt
	// unless it carries tool_result blocks: a tool result reports the outcome
	// of work already inside the open turn, so it neither opens one nor ends
	// one. That distinction is the whole rule, and both halves of it are
	// measured:
	//
	//   - A prompt is not only a plain-string one. Restricting openers to
	//     string content (ADR 0016's first draft) misses the 28 turns opened
	//     by a block-array prompt - one carrying an image or an attachment -
	//     which misattributes 81 assistant records to the previous turn and
	//     leaves 196 with no turn at all, against 6 under this rule.
	//   - A tool_result never introduces an id of its own (0 of 23,123), and
	//     188 of them carry no id at all, so letting one open a turn would
	//     invent an association and letting one clear the ref would end a turn
	//     that is still running. Where it does carry an id it repeats the open
	//     turn's, which is corroboration:
	//     TestLiveClaudeTranscriptCorpusHarnessTurnRuleHoldsOnRealSessions checks
	//     the open turn against exactly that, reporting 157 disagreements
	//     under the narrow rule and 0 under this one.
	//
	// A prompt's id then decides what happens. A different id opens that turn;
	// the same id changes nothing, which is why the rule is a change of id and
	// not "every prompt opens a turn" (84 consecutive openers in the corpus
	// repeat the id they already had). A prompt with no readable id CLEARS the
	// ref to unknown - it must never inherit, because the work that follows
	// belongs to a new prompt the transcript cannot name, and charging it to
	// the previous turn would charge one Task's cost to another with no way
	// back: agent_turn is immutable. Unknown is a fact here; a guess is not.
	if len(results) == 0 && rec.PromptID != p.turnRef {
		p.turnRef = rec.PromptID
		p.turnRefAt = append(p.turnRefAt, turnRefChange{offset: offset, ref: rec.PromptID})
	}
	p.batch.Records = append(p.batch.Records, TranscriptRecord{
		SourceRef: rec.UUID, Kind: kind, Offset: offset, Ordinal: ordinal,
		OccurredAt: ts, RawJSON: raw,
	})

	for k, blk := range results {
		out := toolResultText(blk.Content)
		p.batch.ToolResults = append(p.batch.ToolResults, TranscriptToolResult{
			SourceRef:   toolResultRef(rec.UUID, k, len(results)),
			CallID:      blk.ToolUseID,
			Offset:      offset,
			Ordinal:     ordinal,
			OutputText:  out,
			OutputBytes: int64(len(out)),
			// Claude omits is_error on a non-error result (measured over the
			// corpus: 3588 absent, 18515 explicit false, 451 true), so absent
			// means false and an error is never inferred from the content.
			IsError:     blk.IsError != nil && *blk.IsError,
			CompletedAt: ts,
		})
	}
}

func (p *claudeParse) addSystem(rec claudeRecord, raw string, offset int64, ordinal int) {
	if rec.UUID == "" {
		p.malformed(offset, ordinal, MalformedMissingRecordID, "system record has no uuid")
		return
	}
	ts, err := parseTranscriptTime(rec.Timestamp)
	if err != nil {
		p.malformed(offset, ordinal, MalformedMissingTimestamp, err.Error())
		return
	}
	p.batch.Records = append(p.batch.Records, TranscriptRecord{
		SourceRef: rec.UUID, Kind: TranscriptKindSystem, Offset: offset, Ordinal: ordinal,
		OccurredAt: ts, RawJSON: raw,
	})
}

// decodeClaudeContent accepts the two shapes message.content takes: a bare
// string, or an array of content blocks. null and an empty value yield no
// blocks. Anything else is a shape the parser does not understand.
func decodeClaudeContent(raw json.RawMessage) ([]claudeBlock, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return nil, nil
	}
	switch t[0] {
	case '"':
		var s string
		if err := json.Unmarshal(t, &s); err != nil {
			return nil, fmt.Errorf("content string did not decode: %w", err)
		}
		return []claudeBlock{{Type: "text", Text: s}}, nil
	case '[':
		var blocks []claudeBlock
		if err := json.Unmarshal(t, &blocks); err != nil {
			return nil, fmt.Errorf("content block array did not decode: %w", err)
		}
		return blocks, nil
	default:
		return nil, fmt.Errorf("content is neither a string nor an array")
	}
}

// toolResultText renders a tool_result content value as full text. A string is
// used verbatim. An array of text blocks is joined, preserving every byte of
// text. Any other shape (images, mixed blocks) is kept as its compact JSON, so
// content is never silently dropped.
func toolResultText(content json.RawMessage) string {
	t := bytes.TrimSpace(content)
	if len(t) == 0 || string(t) == "null" {
		return ""
	}
	if t[0] == '"' {
		var s string
		if err := json.Unmarshal(t, &s); err == nil {
			return s
		}
	}
	if t[0] == '[' {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(t, &blocks); err == nil {
			parts := make([]string, 0, len(blocks))
			allText := true
			for _, b := range blocks {
				if b.Type != "text" {
					allText = false
					break
				}
				parts = append(parts, b.Text)
			}
			if allText {
				return strings.Join(parts, "\n")
			}
		}
	}
	return compactJSON(t, string(t))
}

// toolResultRef names one tool_result block within a user record. Claude's
// blocks carry no id, so the record's own uuid is the identity when it holds a
// single result; a record with several results disambiguates by index.
func toolResultRef(uuid string, index, count int) string {
	if count == 1 {
		return uuid
	}
	return fmt.Sprintf("%s#%d", uuid, index)
}

func joinTranscriptText(existing, add string) string {
	if existing == "" {
		return add
	}
	if add == "" {
		return existing
	}
	return existing + "\n" + add
}

func compactJSON(raw json.RawMessage, fallback string) string {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return fallback
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, t); err != nil {
		return string(t)
	}
	return buf.String()
}

func parseTranscriptTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, fmt.Errorf("record has no timestamp")
	}
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q: %w", value, err)
	}
	return ts, nil
}

// claudeToolClasses maps Claude's built-in tool names to a command class. Any
// name not listed (and any non-mcp-prefixed name) is CommandOther, never
// dropped.
var claudeToolClasses = map[string]CommandClass{
	"Bash":       CommandShell,
	"BashOutput": CommandShell,
	"KillShell":  CommandShell,
	"KillBash":   CommandShell,

	"Read":         CommandRead,
	"NotebookRead": CommandRead,

	"Edit":         CommandEdit,
	"MultiEdit":    CommandEdit,
	"Write":        CommandEdit,
	"NotebookEdit": CommandEdit,

	"Grep": CommandSearch,
	"Glob": CommandSearch,

	"Task":         CommandAgent,
	"Agent":        CommandAgent,
	"ListAgents":   CommandAgent,
	"SendMessage":  CommandAgent,
	"SendFeedback": CommandAgent,
}

// ClassifyClaudeTool maps a Claude tool name to a CommandClass. MCP tools use
// the mcp__<server>__<tool> convention; everything unrecognised is other.
func ClassifyClaudeTool(name string) CommandClass {
	if strings.HasPrefix(name, "mcp__") {
		return CommandMCP
	}
	if c, ok := claudeToolClasses[name]; ok {
		return c
	}
	return CommandOther
}

// ClaudeProjectSlug encodes an agent cwd the way Claude Code names its
// ~/.claude/projects/<slug> directory: every UTF-16 code unit that is not
// [A-Za-z0-9-] becomes '-'. UTF-16 code units, not runes, is measured
// behaviour: an astral character becomes two dashes while a BMP character
// becomes one.
//
// The slug is of the filesystem-resolved cwd Claude actually runs in, not of
// $PWD and not of an unresolved spelling: launched in a directory whose given
// path contains a symlinked component with PWD pointing elsewhere, Claude Code
// 2.1.269 wrote its transcript under the slug of the resolved path.
// TestLiveClaudeProjectSlugResolution (opt in with MATE_CLAUDE_LIVE=1) creates
// that fixture - a physical path holding a dot, a dash, a space and an astral
// character, reached once directly and once through a symlink, with a
// divergent PWD - and fails if the observed directory is not this function's
// result for the resolved path. Resolution itself stays the caller's job
// through the harness_transcript fsboundary; this function only transforms a
// string.
//
// The encoding is collision-resistant, not injective: '.' and '-' both map to
// '-' ("a.b" and "a-b" share a slug). It is a search optimization only -
// adoption must still validate the file's owner and provider session id (ADR
// 0016, "Unproven": correctness does not depend on the slug when the validated
// Stop path/session id identifies the file).
//
// A slug longer than claudeSlugMax is cut to that length and suffixed with
// '-' and a hash of the whole cwd, so a deep path still fits in one directory
// name (read from claude-code 2.1.286's bundle, where the same rule names its
// cache directories; TestLiveClaudeProjectSlugResolution measures it on a
// real CLI). Without the cut a long cwd names a directory no filesystem can
// hold.
func ClaudeProjectSlug(cwd string) string {
	units := utf16.Encode([]rune(cwd))
	var b strings.Builder
	b.Grow(len(units))
	for _, u := range units {
		if (u >= 'a' && u <= 'z') || (u >= 'A' && u <= 'Z') || (u >= '0' && u <= '9') || u == '-' {
			b.WriteRune(rune(u))
			continue
		}
		b.WriteByte('-')
	}
	slug := b.String()
	if len(slug) <= claudeSlugMax {
		return slug
	}
	// The slug is ASCII, one byte per UTF-16 code unit, so a byte cut is
	// the CLI's code-unit cut.
	return slug[:claudeSlugMax] + "-" + strconv.FormatInt(claudeSlugHash(units), 36)
}

// claudeSlugMax is the longest slug Claude Code uses whole.
const claudeSlugMax = 200

// claudeSlugHash is the CLI's hash of a long cwd: Java's String.hashCode
// over the UTF-16 code units, in 32-bit two's complement, then made
// non-negative the way JavaScript's Math.abs does (so -2^31 stays 2^31).
func claudeSlugHash(units []uint16) int64 {
	var h int32
	for _, u := range units {
		h = h*31 + int32(u)
	}
	v := int64(h)
	if v < 0 {
		v = -v
	}
	return v
}

// ClaudeTranscriptFileName is the basename of a session's transcript:
// <provider session id>.jsonl.
func ClaudeTranscriptFileName(providerSessionID string) string {
	return providerSessionID + ".jsonl"
}

// ClaudeTranscriptPath computes the candidate transcript path for a Claude
// session. It is pure: it neither reads nor resolves the path. A caller must
// still validate the result as a regular file beneath the persisted search root
// through the harness_transcript fsboundary before adopting it.
func ClaudeTranscriptPath(searchRoot, cwd, providerSessionID string) string {
	return filepath.Join(searchRoot, ClaudeProjectSlug(cwd), ClaudeTranscriptFileName(providerSessionID))
}
