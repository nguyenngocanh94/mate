package console

import (
	"context"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// TerminalSize is the display-cell geometry of the embedded terminal pane.
// It deliberately mirrors the runtime port without importing that package;
// cmd/matev2 adapts the two at the process boundary.
type TerminalSize struct {
	Cols int
	Rows int
}

// SessionChannel is the Console-owned view of a raw PTY channel. Bytes are
// never parsed or split at this boundary. The controller owns the channel's
// lifetime and never uses Close to stop the remote agent.
type SessionChannel interface {
	Read(context.Context) ([]byte, error)
	Write(context.Context, []byte) error
	Resize(context.Context, TerminalSize) error
	Close(context.Context) error
}

// SessionStreamFactory opens the raw stream for an already-resolved Console
// target. The caller adapts its runtime identity and transport types here;
// internal/ui/console does not resolve state or import runtime.
type SessionStreamFactory func(context.Context, SessionTarget, TerminalSize) (SessionChannel, error)

// SessionMetadataReader refreshes the slow side channels while stream output
// remains the hot path. Its result may contain recorded status, runtime
// observation and inbox data, but its transcript is never applied in stream
// mode.
type SessionMetadataReader func(context.Context, SessionTarget) (SessionSnapshot, error)

// SessionTargetKind says whether a session view is attached to a Mate or a
// Crew. The two render differently: a Mate gets the "Crew › Mate" rail
// (ADR 0025), a Crew gets transcript and composer only.
type SessionTargetKind string

const (
	SessionTargetMate SessionTargetKind = "mate"
	SessionTargetCrew SessionTargetKind = "crew"
)

// SessionTarget identifies the Mate or Crew a session view is attached to.
// It mirrors application.AttachResolution's identity fields but stays
// inside the Console boundary: the caller (cmd/matev2's bridge, step 4) builds
// it from an already-resolved query.Snapshot node - this package never
// resolves a target itself, and a raw Herdr pane/tab id is never a valid ID
// here (ADR 0010's attach-target rule applies to session targets too).
type SessionTarget struct {
	Kind      SessionTargetKind
	ID        string // Mate id or Crew id
	ProjectID string
	// HarnessKind is the recorded harness (query.MateIdentity.HarnessKind /
	// query.CrewNode.HarnessKind), carried on the target so a SessionReader
	// can select the right rendering profile without a second lookup. There
	// is no common parser forcing Claude and Codex into one style (ADR
	// 0025); this is the value that selects between them.
	HarnessKind query.HarnessKind
	// AgentName is the live Herdr agent name (query.MateIdentity/AgentName
	// field, query.CrewNode.AgentName) - what the session header actually
	// names, since the frame-shape contract's header shows the agent a
	// reader would attach to, not this struct's own internal ID.
	AgentName string
	// Worktree is the Crew's worktree path (query.CrewNode.Worktree), shown
	// in the session header alongside the agent name - the contract's frame
	// shape pins a header "naming harness, agent and (for a Crew)
	// worktree". Always empty for a Mate target, which has none.
	Worktree string
	// TranscriptCapacity is the maximum number of transcript lines this
	// session frame could ever display, per SessionTranscriptCapacity(Kind,
	// w, h) at the Console's current window size - recomputed by
	// session_mode.go before every entry read and every poll tick, since
	// the window can be resized while session mode is active (view.go feeds
	// RenderSessionFrame the live m.w/m.h on every draw, not a value
	// captured once). A SessionReader bounds its runtime read window to
	// this (session_bridge.go): reading more terminal lines than the
	// Console can ever draw is pure latency cost with no benefit (ADR
	// 0025's ReadAgent latency cliff sits well above what a realistic
	// terminal frame can display). Zero means the caller has not populated
	// it (tests, the fake controller) - a SessionReader must fall back to
	// its own floor rather than requesting zero lines.
	TranscriptCapacity int
}

// SessionRuntimeStatus reuses query.FieldState's three-state vocabulary
// directly rather than inventing a parallel one, per ADR 0025's
// instruction to keep Unknown/Absent/recorded-status distinct using the
// existing query.Field convention:
//
//   - Known:   the last poll observed the agent (InspectAgent/ReadAgent
//     succeeded).
//   - Absent:  InspectAgent most recently reported agent_not_found. Renders
//     as "runtime_missing" and must never be upgraded by a
//     renderer into a lifecycle value such as
//     query.MateStatusStopped, needs_repair, or a CrewStatus
//     failure - that upgrade is a separate reconcile use case,
//     never a side effect of displaying this snapshot (ADR 0025
//     "Runtime disappearance không tự mutate lifecycle").
//   - Unknown: the poll itself failed (timeout, permission, runtime
//     unavailable) or has not completed yet. Renders as "unknown".
type SessionRuntimeStatus = query.FieldState

// SessionRuntime is what the *last poll* observed about the live process -
// an axis separate from RecordedStatus on SessionSnapshot, and the two must
// never be collapsed into one value by a renderer (ADR 0025).
type SessionRuntime struct {
	Status SessionRuntimeStatus
	// Reason explains a non-Known Status, mirroring query.Field.Reason:
	// always set when Status is Absent or Unknown.
	Reason string
	// ObservedAt is when Status was last established by a real poll; the
	// zero value means no poll has ever succeeded for this target.
	ObservedAt time.Time
}

// SessionTranscriptSource says where a transcript's content came from.
// Snapshot fallback uses SessionTranscriptPolled; stream mode keeps the
// terminal buffer as the authoritative frame and uses Streamed as metadata
// describing that source.
type SessionTranscriptSource string

const (
	SessionTranscriptPolled   SessionTranscriptSource = "polled"
	SessionTranscriptStreamed SessionTranscriptSource = "streamed"
)

// SessionTranscriptStatus says whether the last observed output could be
// rendered through the target's harness profile.
type SessionTranscriptStatus string

const (
	// SessionTranscriptParsed: the harness-specific renderer (ADR 0025 step
	// 3, keyed by SessionTarget.HarnessKind) understood the raw output and
	// Entries below is populated.
	SessionTranscriptParsed SessionTranscriptStatus = "parsed"
	// SessionTranscriptUnknown: provider output did not match the
	// harness_kind's profile, or no profile exists yet for that harness.
	// Raw is still shown as plain bounded text - the renderer must not
	// guess a structure it cannot verify (ADR 0025: "nếu provider output
	// không parse được, hiển thị plain bounded text và trạng thái
	// unknown").
	SessionTranscriptUnknown SessionTranscriptStatus = "unknown"
)

// SessionTranscriptEntryKind distinguishes the kinds of unit a per-harness
// parser can produce. Step 2 shipped only Plain; step 3 (this set) adds the
// shapes the claude-code profile (session_transcript.go) actually produces -
// a bullet-marked turn, an elbow-marked tool result, and a spark-marked
// status line - plus Gap for the blank separator line between them. Plain
// remains for a profile that recognises structure it cannot further
// classify; it is not currently produced by any profile.
type SessionTranscriptEntryKind string

const (
	SessionTranscriptEntryPlain  SessionTranscriptEntryKind = "plain"
	SessionTranscriptEntryTurn   SessionTranscriptEntryKind = "turn"
	SessionTranscriptEntryResult SessionTranscriptEntryKind = "result"
	SessionTranscriptEntryStatus SessionTranscriptEntryKind = "status"
	SessionTranscriptEntryGap    SessionTranscriptEntryKind = "gap"
)

// SessionTranscriptEntry is one harness-parsed transcript unit.
type SessionTranscriptEntry struct {
	Kind SessionTranscriptEntryKind
	Text string
	// Emphasis marks a Turn that needs the reader's attention (rendered
	// amber instead of the default foreground) - e.g. a narration that a
	// crew question is still unanswered. Ignored for every other Kind.
	Emphasis bool
	// Hint is trailing dim text appended after Text on a Status line (e.g.
	// "(esc to interrupt)"). Ignored for every other Kind.
	Hint string
}

// SessionTranscript is bounded, runtime-observed terminal output - never a
// database row and never claimed to be a faithful per-token or per-tool-
// event stream (ADR 0025: "MVP không tuyên bố độ trung thực của từng token
// hoặc tool event"). It is a distinct source from the inbox rail
// (SessionInboxEntry) and the two must never be flattened into one
// "messages" list.
type SessionTranscript struct {
	Source      SessionTranscriptSource
	HarnessKind query.HarnessKind
	Status      SessionTranscriptStatus
	// Entries are the harness-parsed units, populated only when
	// Status == SessionTranscriptParsed.
	Entries []SessionTranscriptEntry
	// Raw is the bounded recent terminal snapshot (ReadAgent's return
	// value, or its streamed equivalent later); always populated,
	// including when Status == SessionTranscriptParsed, so a fallback view
	// never shows stale content.
	Raw string
	// ObservedAt is when this transcript was captured - the poll tick, not
	// SessionSnapshot.AsOf, since a poll can refresh Runtime/Inbox while a
	// transcript read fails and leaves the previous Raw/Entries in place.
	ObservedAt time.Time
}

// SessionInboxEntry is one committed inbox item, read from query/application
// - never derived from the transcript. Interaction and completion items have
// separate vocabularies; completion is not a question awaiting a reply.
type SessionInboxEntry struct {
	Kind          query.InboxEntryKind
	InteractionID string
	Status        query.InteractionStatus
	Question      string
	Reply         string
	SentAt        time.Time
	// Awaiting reports whether this entry currently awaits a reply, per
	// query.InteractionStatus.AwaitsReply - carried here rather than
	// recomputed so the rail and the status agree on the same read.
	Awaiting bool
	// Attempt and Task are the interaction's owning Crew attempt label
	// (e.g. "attempt 2") and task title, pre-resolved by query/application -
	// the rail and digest group entries by them (session-view-contract.md),
	// and this package does not itself join across Task/Crew to produce
	// them.
	Attempt string
	Task    string
	// Note is an optional caveat about how this entry was recorded (e.g.
	// "reply was recorded before the crew stopped"), authored upstream the
	// same way query.Field.Reason is - this package only displays it.
	Note             string
	CompletionID     string
	CompletionStatus query.CrewStatus
	Outcome          string
	ReportPath       string
	ReportRevision   string
	ArtifactRefs     []string
	PRURL            string
	PRHeadSHA        string
	SourceEventID    string
}

// SessionSnapshot is one poll's worth of session state: the target's
// already-resolved recorded lifecycle status, what the last runtime poll
// observed, the current transcript, and the inbox rail.
type SessionSnapshot struct {
	Target SessionTarget
	// ControllerNotice is an operator-facing transport/fallback diagnostic.
	// It is metadata about how this frame was obtained, never a lifecycle
	// status and never transcript content.
	ControllerNotice string
	// RecordedStatus is the durable lifecycle status
	// (query.MateStatus/query.CrewStatus, rendered as text by the
	// caller) as already established by query.Snapshot/application -
	// never inferred here from Runtime, Transcript, or a successful poll
	// (ADR 0025: "Recorded status: lifecycle trong SQLite, không được suy
	// ra từ màu, transcript hoặc việc một poll thành công"). Reuses
	// query.Field's Known/Absent/Unknown vocabulary.
	RecordedStatus query.Field[string]
	Runtime        SessionRuntime
	Transcript     SessionTranscript
	Inbox          []SessionInboxEntry
	// AsOf is when this SessionSnapshot was assembled, mirroring
	// query.Snapshot.AsOf.
	AsOf time.Time
}

// SessionReader is the snapshot controller port. It is called on a
// fixed-interval ticker (ADR 0025 default 300-500ms) and replaces the whole
// SessionSnapshot each time. Stream mode uses it only after a stream failure.
// The bridge in cmd/matev2/console.go is the only place this closure is built
// from internal/query, internal/application and runtime.Adapter.
type SessionReader func(context.Context, SessionTarget) (SessionSnapshot, error)

// SessionPrompt sends one composer input to the live agent
// (runtime.PromptAgent, wired in step 4). It does not return a transcript;
// the next SessionReader poll picks up whatever effect the prompt had.
type SessionPrompt func(context.Context, SessionTarget, string) error

// SessionClose releases whatever the snapshot controller holds open for a
// target once session mode is left (Esc / Ctrl+b q). Stream mode closes its
// raw channel through SessionChannel and uses this only for compatibility
// with the existing snapshot path.
type SessionClose func(context.Context, SessionTarget) error
