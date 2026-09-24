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
	Name string
	Root string
}

// ProjectNode is one registered Project plus its designated Mate, its
// registered repos and its Crews.
//
// v1 hung Crews off a Task and a Task off the Project, so a Crew was one
// numbered attempt at a Task. mate has no Task: mvp.md's model is
// Project -> {Mate, Crew}, where a Crew is spawned for one job, runs once
// and is torn down (mvp.md sections 1 and 3). The attempts level went with
// it.
type ProjectNode struct {
	ProjectID string
	Actions   []ActionAvailability
	Name      string
	// Mode is the Project's communication mode (mvp.md section 5): the
	// presence of `mate/.auto`. It is a Project-level fact rather than a
	// Mate-level one because the flag exists whether or not a Mate has ever
	// been started, and the daemon that acts on it (mvp.md task 19) is the
	// Console's, not the Mate's.
	Mode Mode
	// Daemon is what the auto-mode daemon has done for this Project in this
	// console's lifetime (mvp.md task 19). It is filled by cmd/mate after
	// Load, not by Load: the daemon is a live thing in the console process,
	// the way the observer's crew health is.
	Daemon AutoDaemon
	Mate   MateNode
	// Repos are the Project's registered repos, read once per Project. A
	// Crew's own Repo field is resolved against this list rather than
	// through a per-row read, so a Project with many Crews still costs one
	// repo read.
	Repos Field[[]RepoValue]
	// Crews are the Project's open Crews, oldest first. A Crew is open
	// until the Mate or the captain closes it with `mate crew stop`
	// (mvp.md section 4): a `done:` line is the Crew's report, not the end
	// of its task - a scout ends when the captain accepts the report, a
	// ship ends when its branch is merged - so a Crew that said `done`
	// stays listed until somebody closes it.
	Crews []CrewNode
	// ClosedCrews is how many of the Project's recorded Crews have been
	// closed (`stopped_at` in their meta). Their records stay under
	// `crews/`; they are not rows.
	ClosedCrews int
	Attention   Field[ProjectAttention]
	// Box is the Project's message box (mvp.md section 4): crew status
	// lines, sent.log and observer incidents merged in time order. It is
	// read per Project rather than per Crew because that is what it is - a
	// project-wide log, not a per-row field - and because the Console's
	// rail and the project frame's box panel both draw the whole thing.
	Box Field[BoxView]
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
	// Tokens is the Mate's token usage today, read from `.mate/mate.db`
	// (mvp.md M5 task 27) - see CrewNode.Tokens for why this package leaves
	// it Absent and who fills it in.
	Tokens Field[TokenValue]
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
	// Closed is `stopped_at` in the Crew's meta: the Mate or the captain
	// ran `mate crew stop`. Load drops closed Crews from ProjectNode.Crews
	// and counts them in ClosedCrews; the field is here for readers that
	// load a single Crew.
	Closed    bool
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
	// Health is the observer's live observation of this Crew (mvp.md
	// section 4b: "Bên cạnh trạng thái luôn có một cột sức khỏe do quan
	// sát, không phải trạng thái"). It is the one field in this package
	// that is not recorded state, and it is deliberately not filled by
	// Load: this package only reads `.mate/`, and an observation comes
	// from Herdr. The Console's wiring (cmd/mate) asks internal/watch for
	// its latest snapshot and fills this in after Load returns, so a Crew
	// nobody is watching keeps the Absent field Load left - never a zero
	// value that would render as "the agent is gone".
	//
	// It is a display column and never a state: whatever it says, the
	// Crew's Status comes from the meta, the incidents and the status file,
	// in that order.
	Health Field[CrewHealth]
	// Tokens is this Crew's whole-task token usage, read from
	// `.mate/mate.db`'s `v_task_ledger` (mvp.md M5 task 27). Like
	// Health it is not this package's to fill: this package only reads
	// `.mate/`'s flat files, and the ledger lives in the derived
	// database. The Console's wiring (cmd/mate, alongside
	// withCrewHealth) opens the database read-only and fills this in after
	// Load returns; a one-shot CLI read or a console that has not opened
	// the database yet leaves it at the Absent Load gave it.
	Tokens Field[TokenValue]
}

// TokenValue is one actor's token usage as the ledger reports it: a total
// across all four buckets, a cost that is nil until every turn's model has
// a real price (mvp.md M5: "cost is NULL until then, because a missing
// price is not a price of zero" - see docs/timeline.md), and a context
// percentage that is nil until the actor has a turn whose model carries a
// known context window in pricing.yaml.
type TokenValue struct {
	Total int64
	// Cost is nil, not zero, until pricing.yaml prices every model this
	// actor's turns used.
	Cost *float64
	// ContextPct is nil, not zero, until the actor's most recent turn's
	// model has a known context_window.
	ContextPct *float64
}

// CrewHealth is one observation of a Crew's pane and of Herdr's inventory,
// made by the observer (internal/watch) rather than read from a file.
//
// Composer is a string type here for the reason CrewStatus is: internal/ui/
// console may not import internal/send (its boundary test), so the measured
// composer states travel as their own words and an unrecognised one renders
// as itself.
type CrewHealth struct {
	// AgentPresent is whether Herdr still lists the agent the Crew's meta
	// records. False is a positive answer from Herdr - the agent is gone -
	// not a failed read: a lookup the observer could not complete leaves
	// the whole Field Absent instead.
	AgentPresent bool
	// Composer is what the pane was showing: empty, pending, busy, unknown.
	Composer CrewComposer
	// QuietFor is how long the pane's contents and the Crew's status file
	// have both been unchanged, as of ObservedAt.
	QuietFor time.Duration
	// ComposerFor is how long the composer has read its current state; the
	// duration the NOTE column shows for a busy pane, since a working
	// harness redraws its spinner and is never quiet.
	ComposerFor time.Duration
	// ObservedAt is when the observation was made. It is not the snapshot's
	// AsOf: the observer polls on its own interval, so this can be older.
	ObservedAt time.Time
}

// CrewComposer is the composer state of a Crew's pane, as internal/send
// classified it.
type CrewComposer string

const (
	ComposerEmpty   CrewComposer = "empty"
	ComposerPending CrewComposer = "pending"
	ComposerBusy    CrewComposer = "busy"
	ComposerUnknown CrewComposer = "unknown"
)

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
