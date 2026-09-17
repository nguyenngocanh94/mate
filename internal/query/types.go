package query

import (
	"time"
)

// Snapshot is the Console's navigation tree in one read: Workspace ->
// Project -> {Mate, Task -> Crew}, plus every field that came back Unknown
// and the moment the read finished. It is deliberately one aggregate read
// composed here rather than one read per level: Console redraws on every
// keystroke, Phase 1 workspace volume is small, and an aggregate read keeps
// that redraw from becoming an N+1 read storm.
//
// Its name is the design's ("recorded snapshot"), and it is the honest one:
// nothing in here is a live observation. Every status is durable database
// state, and AsOf is how old the whole picture is.
type Snapshot struct {
	WorkspaceID string
	// Actions are the workspace-level actions the snapshot authorizes. Each
	// entry includes unavailable actions and their reason so a UI never has
	// to infer capability from a zero value or a live probe.
	Actions []ActionAvailability
	// Workspace is the open workspace's own location, from the store's
	// resolved root (StateStore.WorkspaceRoot) rather than a caller-supplied
	// path - Unknown when that resolution failed.
	Workspace Field[WorkspaceValue]
	// AsOf is when this read finished, taken from the injected clock after
	// the last field was loaded. It is not a database column: no row records
	// "when did someone last look", and a stored timestamp would answer a
	// different question than "how stale is what I am looking at".
	AsOf     time.Time
	Projects []ProjectNode
	// Warnings lists every Unknown field in the tree, in tree order. Empty
	// means every field either read successfully or is legitimately absent -
	// it does not mean the snapshot is fresh.
	Warnings []FieldWarning
}

// WorkspaceValue is where the open workspace lives. Name is the root
// directory's base name: there is no workspace name column and no
// `workspace` table (ADR 0005), so the directory is the only name a UI can
// truthfully show.
type WorkspaceValue struct {
	Name         string
	Root         string
	DatabasePath string
}

// ProjectNode is one registered Project plus its designated Mate, its
// registered repos and its Crews.
//
// v1 hung Crews off a Task and a Task off the Project, so a Crew was one
// numbered attempt at a Task. matev2 has no Task: mvp.md's model is
// Project -> {Mate, Crew}, where a Crew is spawned for one job, runs once
// and is torn down (mvp.md sections 1 and 3). The attempts level went with
// it.
type ProjectNode struct {
	ProjectID string
	Actions   []ActionAvailability
	Name      string
	Mate      MateNode
	// Repos are the Project's registered repos, read once per Project. A
	// Crew's own Repo field is resolved against this list rather than
	// through a per-row read, so a Project with many Crews still costs one
	// repo read.
	Repos Field[[]RepoValue]
	// Crews are the Project's Crews, oldest first.
	Crews     []CrewNode
	Attention Field[ProjectAttention]
}

// MateNode is the Project's designated Mate. Every field is set on every
// path: when the designation itself is Absent or Unknown the dependent
// fields carry that forward with the same reason, so no field is ever left
// at the unset zero FieldState.
type MateNode struct {
	Actions []ActionAvailability
	// Designated is the Project's designated Mate (application.
	// DesignatedMate): Absent when the Project has no Mate yet, Unknown when
	// the designation could not be resolved (the ListMates read failed, or
	// more than one default row - needs_repair), Known otherwise.
	Designated Field[MateIdentity]
	// AgentName is the Herdr agent name recorded for this Mate, taken from
	// its most recent runtime_binding row - including a released one, which
	// is why it can be Known while Binding is Absent: stopping an agent
	// releases the binding but the name it ran under stays on the audit row.
	AgentName Field[string]
	Binding   Field[BindingValue]
	LastEvent Field[EventValue]
	Error     Field[ErrorReason]
}

// MateIdentity is the designated Mate row itself. Status is the durable
// Mate lifecycle (MateStatus); it is not a live Herdr liveness check
// - see CrewNode.
type MateIdentity struct {
	MateID      string
	HarnessKind HarnessKind
	Status      MateStatus
	IsDefault   bool
}

// CrewNode is one Crew. Status is what the backend recorded, never a live
// Herdr observation: mvp.md section 2 decision 8 keeps Herdr's
// screen-scraped idle/blocked/done out of any conclusion about whether work
// is finished. The store-backed loader fills it from the last line of
// crews/<id>.status, which is why CrewStatus is a string type and an
// unrecognised word renders as itself rather than as a blank cell.
type CrewNode struct {
	CrewID    string
	Actions   []ActionAvailability
	ProjectID string
	RepoID    string
	// Task is the one-line job this Crew was spawned for (crews/<id>.meta's
	// `task=`). It is the Crew row's title.
	Task        string
	HarnessKind HarnessKind
	Status      CrewStatus
	CreatedAt   time.Time
	Repo        Field[RepoValue]
	Worktree    Field[WorktreeValue]
	// AgentName is the Herdr agent name recorded for this Crew, from its most
	// recent runtime_binding row (released rows included) - see
	// MateNode.AgentName.
	AgentName Field[string]
	Binding   Field[BindingValue]
	LastEvent Field[EventValue]
	Error     Field[ErrorReason]
	Attention Field[Attention]
}

// RepoValue is one registered repo as the inspector renders it.
type RepoValue struct {
	RepoID        string
	DisplayName   string
	Path          string
	DefaultBranch string
}

// WorktreeValue is the Crew's worktree row (StateStore.GetWorktree). Path,
// Branch and Status share one Field because they share one read: there is
// no state of the database in which the path is readable and the branch is
// not.
type WorktreeValue struct {
	Path   string
	Branch string
	Status WorktreeStatus
}

// WorktreeStatus mirrors persistence.WorktreeStatus as a query-package type
// so a UI can compare against named constants without importing
// internal/persistence (internal/ui/console/doc.go's import boundary). The
// string values are identical and loadWorktree is the one place the
// conversion happens.
//
// WorktreeRecorded* is the recorded outcome of the last worktree operation,
// never a live check: WorktreeRecordedUnknown means mate could not prove
// what happened (ADR 0012 - cleanup must not act on it), not that this
// snapshot failed to read the row. That failure is Field.State == Unknown,
// which is a different thing and must render differently.
type WorktreeStatus string

const (
	WorktreeRecordedCreating WorktreeStatus = "creating"
	WorktreeRecordedCreated  WorktreeStatus = "created"
	WorktreeRecordedRemoved  WorktreeStatus = "removed"
	WorktreeRecordedUnknown  WorktreeStatus = "unknown"
)

// BindingStatus mirrors persistence.BindingStatus's held values (reserved,
// active, stale) as a query-package type, for the same reason
// WorktreeStatus does. `released` has no constant here: a released binding
// is not a held one, so it never reaches a Known BindingValue - it only
// supplies AgentName.
type BindingStatus string

const (
	BindingReserved BindingStatus = "reserved"
	BindingActive   BindingStatus = "active"
	BindingStale    BindingStatus = "stale"
)

// BindingValue is the held runtime_binding row for a Mate or Crew - a
// durable database fact, not a fresh Herdr call. Status carries the
// binding's own recorded status: per ADR 0027 a stale binding means mate
// could not confirm the agent actually stopped, and
// application.ResolveAttachTarget refuses to attach to one, so rendering
// only the agent name would make a stale binding indistinguishable from a
// live one - exactly the falsehood this field exists to prevent. An active
// one does not prove the agent is alive either; that caveat travels in the
// Field's Reason so every caller repeats it.
type BindingValue struct {
	Status    BindingStatus
	AgentName string
	// Runtime is which runtime owns the session below. It is "herdr" because
	// the schema records Herdr handles specifically (the columns are
	// herdr_session/herdr_workspace/herdr_tab/herdr_pane) - a schema fact,
	// not a probe of what is running.
	Runtime string
	Session string
	// Workspace is the Herdr workspace id the binding recorded. It is the
	// handle that goes stale first (ADR 0011: a force pane-close of the last
	// pane closes the workspace), so it is shown as recorded, never as the
	// place to look now.
	Workspace string
	Tab       string
	Pane      string
	// BoundSince is activated_at when the row has ever been activated,
	// reserved_at otherwise (bindingField) - and a stale binding can be
	// either: MarkBindingStale permits both a reserved and an active source
	// row (internal/persistence/binding.go), so a binding staled before it
	// was ever activated has BoundSince == reserved_at, not an activation
	// time. Whichever source it is, BoundSince is never when the binding
	// went stale: persistence separately tracks updated_at for that
	// transition, but nothing here reads it, so BoundSince is always "since
	// when has this been bound" (reservation or activation, per
	// BoundSinceKind), never "since when has it been stale".
	BoundSince time.Time
	// BoundSinceKind says which of the two BoundSince is, so a UI never
	// labels a reservation time as the moment an agent started.
	BoundSinceKind BoundSinceKind
}

// BoundSinceKind names which timestamp BindingValue.BoundSince came from.
type BoundSinceKind string

const (
	BoundSinceActivated BoundSinceKind = "activated"
	BoundSinceReserved  BoundSinceKind = "reserved"
)

// ErrorReason is why a row whose recorded status is itself an error state
// got there, as recorded on the last matching event. The three states of a
// Field[ErrorReason] are each a different sentence and none may be folded
// into another:
//
//   - Absent: the recorded status is not an error state. This is not a read
//     failure and not "no reason found".
//   - Known with a non-empty value: the status is an error state and the
//     last matching event named the reason.
//   - Known with an empty value: the status is an error state and the event
//     read succeeded, but no reason is available - either no matching event
//     carried one, or no matching event exists at all. Field.Reason says
//     which, in its own sentence (see errorReason): "no reason is recorded
//     on the last matching event" and "no event is recorded that could
//     carry a reason" are different facts, both established by a read.
//   - Unknown: the status is an error state but the ListEvents read that
//     would have supplied the reason failed. Unlike the status - already
//     read successfully to reach this point - the reason comes from a
//     separate read that can fail independently, so it must not be reported
//     as "no reason recorded": that would assert a negative the read never
//     established.
type ErrorReason string

// EventValue is the most recently recorded event for a Mate, Crew or Task
// (StateStore.ListEvents, ordered by recorded_at then rowid). There is
// deliberately no free-text "detail": event payloads have no common detail
// key, so any such field would be invented rather than read.
type EventValue struct {
	EventID       string
	EventType     string
	OccurredAt    time.Time
	CorrelationID string
}
