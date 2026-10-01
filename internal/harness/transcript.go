package harness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

// TranscriptParser is the parse half of a harness's TranscriptSource: it
// turns one format's bytes into normalized facts. It is not part of the
// Profile; only the harness's own TranscriptSource calls it, because a
// harness may be launchable while its transcript is unreadable (ADR 0016).
//
// The two methods differ only in what they do with a message whose records may
// still be appended to; see ParseTranscript and ParseTranscriptFinal.
type TranscriptParser interface {
	// ParseTranscript parses an incremental byte range (the whole file, or the
	// tail from transcript_cursor.byte_offset). It reports only facts it can
	// never have to amend: the trailing message group, whose harness records a
	// later write may still extend, is withheld and named by OpenSourceRef, and
	// ConsumedBytes stops at that group's first record so a caller that resumes
	// from there re-reads it whole.
	ParseTranscript(state TranscriptParseState, data []byte) TranscriptBatch
	// ParseTranscriptFinal parses a range the caller has established is at
	// rest: a session's last sync, run only after the harness runtime has been
	// positively stopped, so that no writer can remain. It emits the trailing
	// message group instead of withholding it; without it the last turn of a
	// finished session would never be ingested.
	//
	// "Positively stopped" means observed stopped, not asked to stop. The one
	// caller is Claude's TranscriptSource.Read, for a read whose AtRest the
	// timeline (internal/timeline/telemetry.go) sets only when
	// Located.Finalized is set. That flag is earned by a chain, each link of
	// which must hold:
	//
	//   1. context refresh (cmd/mate/context_refresh.go) calls StopMate, and
	//      StopMate reports Confirmed only through confirmGone
	//      (internal/spawn/stop.go): InspectAgent must return agent_not_found
	//      AND ListAgents must omit the name. Live or unknown on either side
	//      fails the stop. When Herdr's session list does not report the
	//      Mate's session running - missing from the list, or listed but not
	//      running - there is no agent to ask: StopMate clears the stale
	//      record and returns AlreadyGone with Confirmed false. When the list
	//      cannot be read at all, StopMate fails.
	//   2. only when StopMate returns nil with Confirmed set does refresh call
	//      store.FreezeMateSession, which copies the transcript to an immutable
	//      snapshot under the Mate's sessions/ directory and marks the archive
	//      finalized=true. An unconfirmed stop leaves the archive refresh wrote
	//      before stopping: on the live transcript and not finalized. The
	//      snapshot, not the sync's timing, is what keeps a writer out: no
	//      harness ever appends to the copy, so there is no "after
	//      confirmation, before release" window to sequence the sync in.
	//   3. the timeline sets Located.Finalized only for an archive marked
	//      finalized whose located path is that snapshot (LocatorMeta).
	//
	// The two false-positive branches of confirmGone - ListAgents empty while
	// InspectAgent still finds the agent, and ListAgents empty while
	// InspectAgent fails - are pinned for StopMate and StopCrew by
	// internal/spawn/stop_confirm_test.go. That an inferred absence never
	// reads as Confirmed is pinned by internal/spawn/stop_unconfirmed_test.go,
	// and that refresh then does not finalize by
	// cmd/mate/context_refresh_finalize_test.go.
	//
	// Everything outside that chain still uses ParseTranscript: a live Mate,
	// a Mate whose stop was not confirmed, every Crew, and every Codex range. `crew done` cannot qualify - it makes
	// no runtime call and the Crew agent itself is the caller, so the harness
	// is running the very tool call that invoked it. For those sessions the
	// last message group is never flushed. It is visibly pending -
	// OpenSourceRef names it and the cursor still points at it - not silently
	// lost.
	//
	// At rest is a precondition, and the parser cannot check it. A JSONL range
	// that ends on a complete newline-terminated record is indistinguishable,
	// from its bytes alone, from one whose writer has stopped mid-message, so
	// the parser does not claim to prove at rest. It withholds only what the
	// bytes do show is unfinished - a trailing partial line, or a range an
	// unclassifiable line stopped early - which is necessary but not
	// sufficient. Called on a live session it can therefore emit a truncated
	// turn, and a truncated turn is unamendable once stored (agent_turn is
	// unique on (binding_id, source, source_ref) and never updated).
	// Establishing the precondition is the caller's job, and enforcing it
	// belongs to the reconcile/trigger boundary in the sibling tasks, not to
	// the parser (ADR 0016).
	//
	// A parser whose format cannot split one message across records may
	// implement it as ParseTranscript: the precondition is then vacuous.
	ParseTranscriptFinal(state TranscriptParseState, data []byte) TranscriptBatch
}

// TranscriptParseState is the parser state a caller stores beside
// transcript_cursor.byte_offset and hands back on the next sync. It exists
// because a harness turn - the attribution unit, from a prompt to the end of
// the work it caused - is opened by one record and spans every record after
// it, while an incremental parse sees only the tail from the cursor.
//
// The alternative, having the parser emit each record's raw parent references
// and letting reconcile rebuild the graph, was rejected: `parentUuid` and
// `promptId` are Claude's own shapes, and resolving them outside the adapter
// would put format knowledge in reconcile, which ADR 0016 keeps out. It would
// also arrive too late - `agent_turn` is immutable and unique on
// (binding_id, source, source_ref), so a turn's harness turn must be known at
// its first insert and can never be backfilled.
//
// The state is a value, and the pairing is exact: NextState is the state as of
// ConsumedBytes, not as of the end of the range, so a caller commits cursor
// and state together and a resume behaves identically to never having stopped.
//
// A zero state is the honest starting point for a caller with none (a first
// sync, or a stored state it could not read): the records before the first
// opener in the range then carry no harness turn at all. Unknown stays
// unknown - an association is never synthesised from position or time.
type TranscriptParseState struct {
	// HarnessTurnRef is the id of the harness turn open at the cursor, or
	// empty when none is known - which includes "a prompt opened a turn and
	// did not name it", not only "nothing has opened one yet". Empty is a
	// fact, and a caller stores it as NULL rather than attaching the work to
	// whatever turn was open before.
	//
	// For Claude it is the promptId of the currently open prompt. Three
	// consequences of that, each measured and each easy to get wrong:
	// a prompt carrying a different id opens that turn; a prompt carrying no
	// readable id CLEARS this to empty (it must never fall back to the last
	// id seen - the work that follows belongs to a prompt the transcript
	// cannot name, and agent_turn is immutable, so charging it to the
	// previous turn could never be undone); and a tool_result record never
	// changes it, even though a tool_result is a user record that may carry a
	// promptId of its own - it reports the outcome of work already inside the
	// open turn, so it neither opens one nor ends one.
	HarnessTurnRef string
	// LastCumulativeJSON is the Codex cursor's previous cumulative usage
	// snapshot. It is kept beside HarnessTurnRef so an incremental Codex tail
	// can turn its next token_count into a delta without making the parser
	// depend on persistence. Claude leaves this empty.
	LastCumulativeJSON string
	// CodexModel is the model named by the latest Codex turn_context record.
	// Claude leaves this empty. It is carried so a response in a later
	// incremental range can retain the model actually reported by Codex.
	CodexModel string
}

// TranscriptFormat names a harness transcript format. These are the exact
// strings transcript_cursor.source and every fact table's source column
// accept (migration 0007 CHECK constraints).
type TranscriptFormat string

// TranscriptKind is the storage classification of one raw transcript record,
// matching agent_transcript_record.kind. Not every harness produces every
// kind: the Claude parser emits user, assistant, system and tool_result and
// leaves usage/context_event for a format that marks them explicitly.
type TranscriptKind string

const (
	TranscriptKindUser         TranscriptKind = "user"
	TranscriptKindAssistant    TranscriptKind = "assistant"
	TranscriptKindSystem       TranscriptKind = "system"
	TranscriptKindToolResult   TranscriptKind = "tool_result"
	TranscriptKindUsage        TranscriptKind = "usage"
	TranscriptKindContextEvent TranscriptKind = "context_event"
)

// TokenUsage is one turn's per-call token accounting. It mirrors the four
// counters harnesses report and is deliberately not a cumulative total:
// Claude reports usage per API call, so a turn's numbers are already deltas
// and a caller must never add them to a running total again (ADR 0016,
// "Deltas are canonical").
type TokenUsage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Reasoning  int64
}

// ContextTokens is the input side carried into the next call: fresh input plus
// both cache buckets. It mirrors agent_turn.context_tokens.
func (u TokenUsage) ContextTokens() int64 { return u.Input + u.CacheRead + u.CacheWrite }

// CommandClass groups tool calls for the crew_tool_cost view. The values match
// agent_tool_call.command_class. A mapper maps an unrecognised tool name to
// CommandOther rather than dropping the call.
type CommandClass string

const (
	CommandShell  CommandClass = "shell"
	CommandRead   CommandClass = "read"
	CommandEdit   CommandClass = "edit"
	CommandSearch CommandClass = "search"
	CommandMCP    CommandClass = "mcp"
	CommandAgent  CommandClass = "agent"
	CommandOther  CommandClass = "other"
)

// TranscriptRecord is one raw record copied verbatim from the transcript. The
// adapter alone knows the format-specific JSON; everything from here out is
// normalized. SourceRef is the harness's own record identity (Claude's
// per-record uuid), not a mate id.
type TranscriptRecord struct {
	SourceRef string
	Kind      TranscriptKind
	// Offset is the byte offset of this record's line start within the
	// parsed run. A caller advances transcript_cursor.byte_offset to the
	// batch's ConsumedBytes, so Offset is what makes partial-progress resumes
	// idempotent.
	Offset int64
	// Ordinal is the 0-based line index of this record within the parsed run.
	// Both are absolute file positions only when the run began at byte 0.
	Ordinal    int
	OccurredAt time.Time
	RawJSON    string
}

// TranscriptTurn is a derived per-call fact: one assistant API response, its
// model, its full assistant text and its token usage. SourceRef is the
// harness's message identity (Claude's message.id), which is stable across the
// several JSONL records one response can span.
//
// A turn's Usage is counted once per response, and it is the response's final
// figure, not a running one. It is an error to emit one turn per raw assistant
// record: a harness writes one record per content block and restates the
// message's usage on each, so per-record turns multiply the cost. It is
// equally an error to add those restatements together, or to keep the first:
// they are overlapping snapshots of one response, so the last is the completed
// cost and anything else under- or over-counts.
//
// A turn is emitted only once its message is complete, and it is emitted whole:
// agent_turn rows are unique on (binding_id, source, source_ref) and never
// updated, so a turn parsed from a partially written message could never be
// completed later. ParseTranscript withholds the trailing group that may still
// grow; ParseTranscriptFinal flushes it, which is sound only under its
// precondition that no writer can remain (see the port docs).
type TranscriptTurn struct {
	SourceRef  string
	Offset     int64
	Ordinal    int
	OccurredAt time.Time
	Model      string
	Text       string
	StopReason string
	Usage      TokenUsage
	// HarnessTurnRef names the harness turn this response belongs to: the
	// unit from one prompt to the end of the work it caused, which spans
	// several of these per-response turns. It is the harness's own id
	// (Claude's promptId), never a mate id, and it is empty when the range
	// carried no opener and the caller supplied no state - a caller stores
	// that as NULL and must not invent an association for it.
	HarnessTurnRef string
	// SubagentID is set only for a response written by a harness subagent
	// (Claude's sidechain, whose records carry isSidechain and agentId), and
	// is empty for the main stream. It matters because a subagent's usage is
	// recorded only in its own stream: a parent transcript does not carry it,
	// so a reader that ignored it would lose the most expensive turns
	// entirely.
	SubagentID string
	// ContextReset is always false from the Claude parser: how a Claude
	// transcript marks a context reset is not yet identified (ADR 0016,
	// "Open questions"). It is never guessed from token counts.
	ContextReset bool
	// DurationMS is nil from the Claude parser. The only duration-bearing
	// record (system/turn_duration) does not reliably identify its turn, so
	// the parser reports unknown rather than a fabricated number.
	DurationMS *int64
}

// TranscriptToolCall is a derived tool invocation. SourceRef is the harness's
// tool-use id; TurnSourceRef is the SourceRef of the turn that issued it, which
// may be empty when the format does not carry the association.
type TranscriptToolCall struct {
	SourceRef     string
	TurnSourceRef string
	Offset        int64
	Ordinal       int
	ToolName      string
	CommandClass  CommandClass
	InputJSON     string
	StartedAt     time.Time
}

// TranscriptToolResult is a derived tool outcome. It is a separate append-only
// fact from the call, keyed by CallID; SourceRef is the harness's result
// record/block identity. A call without a result is valid in-flight activity.
type TranscriptToolResult struct {
	SourceRef   string
	CallID      string
	Offset      int64
	Ordinal     int
	OutputText  string
	OutputBytes int64
	IsError     bool
	CompletedAt time.Time
}

// TranscriptUsageFailure is a progress-capable usage error. Unlike
// TranscriptMalformed, it does not strand the cursor: the complete source
// record was understood, but it could not contribute a trustworthy delta.
// Codex uses it for a cumulative counter regression. The reconciliation layer
// may surface the failure while still ingesting later records.
type TranscriptUsageFailure struct {
	Offset  int64
	Ordinal int
	Reason  string
	Detail  string
}

// TranscriptMalformed describes the first complete line a parser could not
// classify. The parser stops there: the caller commits the prefix and
// quarantines the exact line span without advancing through it (ADR 0016,
// "call and result are separate facts" / the fragment gate). Reason is a
// stable parse-level code; a caller maps it to transcript_malformed and never
// branches on Detail. Recovery is an explicit user decision: retry leaves the
// line available for re-ingest, while skip records an acknowledged gap.
type TranscriptMalformed struct {
	// Offset is the first byte of the complete malformed source line.
	Offset int64
	// EndOffset is the byte immediately after that line's newline. It is zero
	// only for parser-state failures that have no source line.
	EndOffset int64
	Ordinal   int
	Reason    string
	Detail    string
}

// Stable parse-level reason codes for TranscriptMalformed.Reason. They name
// the shape that failed, not user-facing text.
const (
	MalformedInvalidJSON       = "invalid_json"
	MalformedMissingType       = "missing_type"
	MalformedMissingTimestamp  = "missing_timestamp"
	MalformedMissingRecordID   = "missing_record_id"
	MalformedAssistantShape    = "assistant_shape"
	MalformedUserShape         = "user_shape"
	MalformedToolCallShape     = "tool_call_shape"
	MalformedToolResultShape   = "tool_result_shape"
	MalformedUsageSnapshot     = "usage_snapshot"
	MalformedTurnIdentitySplit = "turn_identity_split"
	MalformedCumulativeUsage   = "cumulative_usage_regressed"
	MalformedCodexShape        = "codex_record_shape"
)

// TranscriptBatch is the normalized result of parsing one byte range. It is a
// value, not a stream, so a caller can inspect every fact before deciding what
// to commit (ADR 0016 G5-11 step 4 keeps parsing outside the write).
type TranscriptBatch struct {
	Source      TranscriptFormat
	Records     []TranscriptRecord
	Turns       []TranscriptTurn
	ToolCalls   []TranscriptToolCall
	ToolResults []TranscriptToolResult
	// UsageTurns are the priced model calls, normalised: the same unit and
	// the same meaning of every counter for every harness. TranscriptSource
	// fills them; a bare parse leaves them empty.
	UsageTurns []UsageTurn
	// UnpricedCalls are tool calls no usage turn covers yet: real work the
	// transcript has not priced. The core records them as actions with no
	// turn rather than dropping them.
	UnpricedCalls []TranscriptToolCall
	// Compactions are the context compactions the transcript records.
	Compactions []TranscriptCompaction
	// UsageFailures are complete, recognized usage records that could not
	// produce a trustworthy delta. They are progress-capable and do not set
	// Malformed or stop the parse.
	UsageFailures []TranscriptUsageFailure
	// ConsumedBytes is the offset a caller may advance
	// transcript_cursor.byte_offset to, relative to the start of data. Every
	// fact above has an Offset below it, so a caller commits exactly this
	// batch from exactly this cursor. It stops at the first malformed line
	// (never advancing through it), and it stops at the first record of a
	// withheld trailing message group, so a resume never starts inside one.
	// A trailing unterminated fragment is not consumed.
	ConsumedBytes int64
	// TotalBytes is the length of the bytes handed to the parser.
	TotalBytes int64
	// PendingBytes is TotalBytes - ConsumedBytes: a trailing fragment with no
	// terminating newline (an active-file boundary), everything from a
	// malformed line on, or the bytes of a withheld trailing message group.
	PendingBytes int64
	// OpenSourceRef names the trailing message group the parser withheld (a
	// harness message id; the SourceRef of the turn a final parse would emit),
	// or is empty when nothing was withheld. It is what lets a caller report
	// partial progress - "a message is still being written" - instead of
	// success or failure. A final parse sets it only when the bytes show the
	// range is unfinished; an idle writer it cannot see does not.
	OpenSourceRef string
	// OpenOffset is the byte offset of that group's first record; it equals
	// ConsumedBytes whenever OpenSourceRef is set. OpenRecords counts the raw
	// records withheld with it. Both are zero when nothing was withheld.
	OpenOffset  int64
	OpenRecords int
	// Malformed is nil unless a complete line failed to classify.
	Malformed *TranscriptMalformed
	// NextState is the parser state as of ConsumedBytes - not as of the end
	// of the range - so a caller persists it in the same write as the cursor
	// and the next sync resumes exactly where this one stopped. Facts the
	// parser withheld or stopped before are therefore not reflected in it.
	NextState TranscriptParseState
	// Skipped counts, by top-level type, every complete record the parser did
	// not copy. It contains known harness housekeeping and unknown future
	// types alike; it is diagnostic only and is not persisted. A caller can
	// surface a non-zero count for an unrecognised type without trusting it.
	Skipped map[string]int
	// HarnessState is what the harness that parsed the batch carries to its
	// next read of the same file, such as Codex's running usage totals. It
	// is opaque: the core keeps it with the batch and never reads it.
	HarnessState any
}

// MessageText is a message's text as a transcript record carries it: a JSON
// string, or the text of a list of content blocks joined by newlines, or
// else the compacted JSON itself.
func MessageText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] == '"' {
		var text string
		if json.Unmarshal(trimmed, &text) == nil {
			return text
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
		return strings.Join(parts, "\n")
	}
	return CompactJSON(trimmed, string(trimmed))
}

// CompactJSON is raw with insignificant whitespace removed, the trimmed raw
// text when it is not valid JSON, or fallback when it is empty.
func CompactJSON(raw json.RawMessage, fallback string) string {
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

// HashText is the hex SHA-256 of s, as telemetry records a text it does not
// keep.
func HashText(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// OutputFact is the telemetry fact for one tool output: its size, hash and a
// bounded preview, and whether the harness said it truncated it.
func OutputFact(s string, n *int64) *telemetry.Output {
	preview := s
	if len(preview) > 1200 {
		preview = preview[:1200]
	}
	o := &telemetry.Output{Bytes: int64(len(s)), SHA256: HashText(s), NewBytes: n, Preview: preview}
	if strings.Contains(s, "Warning: truncated output") || strings.Contains(s, "tokens truncated") || strings.Contains(s, "Output truncated") {
		yes := true
		o.Truncated = &yes
	}
	return o
}
