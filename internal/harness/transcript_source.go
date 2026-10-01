package harness

import (
	"time"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

// TranscriptSource is a harness's transcript as the timeline reads it
// (docs/plans/harness-registry-2026-09-30.md, section 3.2): where the file
// is, how to read it a little more each time, and how the usage it records
// becomes turns of one shape whatever the harness. Everything the core
// stores comes out of it normalised; what a harness needs to carry from one
// read to the next rides in the batch as state only the harness reads back.
//
// A harness without one is not observed: the timeline records that its
// transcript cannot be read and never guesses a token (plan section 3.7).
type TranscriptSource interface {
	// Locate finds an agent's transcript when nothing recorded its path.
	// It returns the location, or "" and the reason it could not: one of
	// LocateNoSession, LocateNotFound, or a reason of the harness's own.
	Locate(req TranscriptLocateRequest) (TranscriptLocation, string)
	// Read brings a transcript up to date. It reads the file whole, or,
	// when the request says the file has only grown since Prior, may carry
	// on from where Prior stopped. Either way the batch it returns covers
	// the whole file, so the core never has to stitch one together.
	Read(req TranscriptReadRequest) (TranscriptBatch, error)
	// Telemetry reads the observations beyond the token ledger from the
	// stored telemetry cursor on, and says where the cursor goes next.
	Telemetry(req TelemetryRequest) (TelemetryUpdate, error)
}

// The reasons Locate gives that every harness can meet. They land in an
// `ingest.unresolved` payload (docs/timeline.md).
const (
	// LocateNoSession: the harness names its transcript by a session id,
	// and the agent has none recorded yet.
	LocateNoSession = "no_session_id"
	// LocateNotFound: the rule named a file that is not there.
	LocateNotFound = "transcript_not_found"
)

// TranscriptLocateRequest is what the core knows about an agent whose
// transcript path nothing recorded.
type TranscriptLocateRequest struct {
	// Cwd is the directory the agent runs in.
	Cwd string
	// SessionID is the harness session id the agent's meta records, if any.
	SessionID string
	// LaunchedAt is when the agent was launched; zero when unknown.
	LaunchedAt time.Time
	// RuntimeSession asks the runtime for the session id it records for the
	// agent (Herdr's agent_session), "" when it does not say. It is nil when
	// there is no runtime record to ask. A harness that never uses it is
	// never charged the call.
	RuntimeSession func() string
	// Root is where the harness keeps its transcripts, overriding its own
	// default; tests point it at a fixture. Empty means the default.
	Root string
}

// TranscriptLocation is a found transcript.
type TranscriptLocation struct {
	Path string
	// SessionID is the harness session the file belongs to, when the rule
	// that found it learned one; empty keeps the one the request carried.
	SessionID string
	// Rule names the rule that found it, for a reader deciding how far to
	// trust the file (docs/timeline.md, "locator").
	Rule string
}

// TranscriptReadRequest is one read of a transcript file.
type TranscriptReadRequest struct {
	Path string
	// Prior is what the previous Read of this path returned, or the zero
	// batch. Grown says the file has only been appended to since: its
	// leading bytes are unchanged and it is larger. A source may then carry
	// on from Prior instead of reading the whole file again.
	Prior TranscriptBatch
	Grown bool
	// AtRest says no writer can remain: the file is an immutable snapshot
	// taken after the agent was confirmed stopped (see
	// TranscriptParser.ParseTranscriptFinal for the chain that earns it).
	// A source then emits what it would otherwise withhold because a later
	// write could still extend it.
	AtRest bool
}

// UsageTurn is one priced model call, the unit every harness's usage is
// normalised to: what one call cost, when it ran, and the tool calls it
// issued. A harness that reports usage per call (Claude) and one that
// reports a running total (Codex) both arrive here as the same deltas, so
// the timeline records turns down one path without asking which harness
// wrote them.
type UsageTurn struct {
	// SourceRef is the harness's own identity for the call.
	SourceRef string
	// Offset is the byte offset of the record that priced it.
	Offset int64
	// StartedAt and EndedAt bound the call and the tool work it caused.
	StartedAt time.Time
	EndedAt   time.Time
	// Outcome is end_turn, tool_use, or the harness's own stop reason.
	Outcome        string
	Model          string
	HarnessTurnRef string
	// Usage is this call's cost as a delta. Input is billed input only:
	// cache reads are never counted in it, for any harness, so a sum across
	// the four buckets is the call's whole cost with no overlap.
	Usage TokenUsage
	// ContextTokens is the prompt the call carried: what the context held
	// when it returned.
	ContextTokens int64
	// Calls are the tool calls the call issued, in transcript order.
	Calls []TranscriptToolCall
	// Sample is the usage record as the harness wrote it, which
	// usage_sample keeps in its raw shape: a per-call delta, or a running
	// total when Cumulative is set.
	Sample UsageSample
}

// UsageSample is one usage record in the shape the harness wrote it.
type UsageSample struct {
	// At is when the harness wrote the record.
	At         time.Time
	Cumulative bool
	Usage      TokenUsage
}

// TranscriptCompaction is the moment a harness threw the conversation away
// and replaced it with a summary: the one event that explains a context
// size falling instead of rising.
type TranscriptCompaction struct {
	Offset     int64
	OccurredAt time.Time
	// Trigger names the marker that showed it, namespaced by harness
	// ("claude.compact_boundary", "codex.compacted").
	Trigger string
}

// TelemetryRequest is one telemetry read: the stored cursor, and the
// transcript as Read last returned it.
type TelemetryRequest struct {
	Path string
	// Size is the file's size now.
	Size int64
	// Offset and State are the stored cursor. Both are zero for a first
	// read, and after the core found the file replaced or truncated.
	Offset int64
	State  TelemetryState
	// Changed says the file's size moved since the cursor was stored, or
	// that no cursor was stored at all.
	Changed bool
	Batch   TranscriptBatch
}

// TelemetryUpdate is what a telemetry read found and where the cursor goes.
type TelemetryUpdate struct {
	Facts []telemetry.Fact
	// Offset and State are the cursor to store with these facts.
	Offset int64
	State  TelemetryState
	// Error names what the read could not parse; empty when nothing.
	Error string
	// Gaps are what this harness's telemetry cannot observe at all. They
	// are carried on the capability fact so a reader knows an absent fact
	// is unobservable rather than absent.
	Gaps []string
}
