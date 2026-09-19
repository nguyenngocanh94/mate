package query

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/crewstate"
)

// The lifecycle enums the read model and the Console render. They lived in
// v1's internal/domain, a package matev2 does not carry across (mvp.md §8),
// and they are the only part of it these DTOs used, so they are inlined
// here with the types that give them meaning. The string values are v1's
// unchanged, which is what keeps the golden fixtures honest.
//
// Nothing here reads state. Parsing and classification only: a backend
// fills a Snapshot, this package says what its values mean.

var (
	// ErrEmptyValue is returned when a parsed enum is missing.
	ErrEmptyValue = errors.New("empty value")
	// ErrInvalidValue is returned when a parsed enum is not a known member.
	ErrInvalidValue = errors.New("invalid value")
)

// HarnessKind is a supported agent harness. matev2 launches the two v1
// probed: Claude Code and Codex CLI. It mirrors harness.Kind's values
// without this package importing internal/harness, for the same reason
// WorktreeStatus mirrors the store's own spelling: the Console must stay
// free of every package that talks to a real process.
type HarnessKind string

const (
	HarnessClaude HarnessKind = "claude"
	HarnessCodex  HarnessKind = "codex"
)

// ParseHarnessKind rejects empty and unknown kinds.
func ParseHarnessKind(s string) (HarnessKind, error) {
	switch HarnessKind(strings.ToLower(strings.TrimSpace(s))) {
	case HarnessClaude:
		return HarnessClaude, nil
	case HarnessCodex:
		return HarnessCodex, nil
	case "":
		return "", fmt.Errorf("harness kind: %w", ErrEmptyValue)
	default:
		return "", fmt.Errorf("harness kind %q: %w", s, ErrInvalidValue)
	}
}

func (k HarnessKind) String() string { return string(k) }

// Mode is a Project's communication mode (mvp.md section 5). Manual is
// the default and means no byte is ever sent to the Mate's pane without the
// reader pressing a key; auto means the Console's digest daemon may. The
// durable form of this is the presence of `mate/.auto`; this type is only
// how the two are named on screen.
type Mode string

const (
	ModeManual Mode = "manual"
	ModeAuto   Mode = "auto"
)

func (m Mode) String() string { return string(m) }

// ModeFor names the mode the `.auto` flag's presence selects.
func ModeFor(auto bool) Mode {
	if auto {
		return ModeAuto
	}
	return ModeManual
}

// AutoDaemon is what the Console's auto-mode daemon (mvp.md section 5, task
// 19) has done for one Project. It is not a Field: every value in it is an
// in-process observation with a meaningful zero - "this console has sent
// nothing yet" is Sends == 0, not an Unknown - and nothing on disk records
// it, so there is no read that could fail and no reason to carry one.
//
// query.Load cannot fill it: the daemon lives in the console process beside
// the observer, and cmd/matev2 merges its snapshot into the tree the same
// way it merges the observer's health readings.
type AutoDaemon struct {
	// Sends is how many digests this console has delivered for the Project.
	Sends int
	// LastSentAt is when the last one was delivered, zero before the first.
	LastSentAt time.Time
	// Notice is why the daemon's last tick did not deliver, empty when it
	// did or when it had nothing to say. One line per tick, not per item:
	// a tick sends one digest and so has one outcome.
	Notice string
	// NoticeAt is when that notice was recorded.
	NoticeAt time.Time
}

// Sent reports whether the daemon has delivered anything for the Project,
// which is what puts its indicator in the MODE cell.
func (d AutoDaemon) Sent() bool { return d.Sends > 0 }

// MateStatus is the Mate lifecycle. Herdr's blocked/idle/done are runtime
// observations, not Mate statuses (mvp.md §2 decision 8).
type MateStatus string

const (
	MateCreated  MateStatus = "created"
	MateStarting MateStatus = "starting"
	MateRunning  MateStatus = "running"
	MateStopping MateStatus = "stopping"
	MateStopped  MateStatus = "stopped"
	MateUnknown  MateStatus = "unknown"
)

// ParseMateStatus rejects empty and unknown values.
func ParseMateStatus(s string) (MateStatus, error) {
	v := MateStatus(strings.ToLower(strings.TrimSpace(s)))
	switch v {
	case MateCreated, MateStarting, MateRunning, MateStopping, MateStopped, MateUnknown:
		return v, nil
	case "":
		return "", fmt.Errorf("mate status: %w", ErrEmptyValue)
	default:
		return "", fmt.Errorf("mate status %q: %w", s, ErrInvalidValue)
	}
}

func (s MateStatus) String() string { return string(s) }

// OccupiesActiveSlot reports whether this status counts as the one active
// Mate of a Project.
func (s MateStatus) OccupiesActiveSlot() bool {
	switch s {
	case MateStarting, MateRunning, MateStopping, MateUnknown:
		return true
	default:
		return false
	}
}

// CrewStatus is a Crew's displayed state: the seven-state vocabulary of
// mvp.md section 4b, with one owner each. The values are crewstate's own,
// spelled as constants of this type so the Console never has to import
// crewstate and the two vocabularies cannot drift - a change to a spelling
// in crewstate is a compile error here, not a silent rename.
//
// The v1 lifecycle (`reserved`, `preparing`, `running`, `awaiting_review`,
// `succeeded`, `needs_rebase`, `needs_repair`, `stopped`) is gone: those
// states had no owner in matev2, nothing wrote them, and a status nobody
// sets is a status the Console can only render as a lie.
type CrewStatus string

const (
	// CrewSpawned: the app spawned the crew and it has written nothing yet.
	CrewSpawned = CrewStatus(crewstate.StateSpawned)
	// CrewWorking, CrewNeedsDecision and CrewWaitMate are the three verbs a
	// crew writes to its own `.status`.
	CrewWorking       = CrewStatus(crewstate.StateWorking)
	CrewNeedsDecision = CrewStatus(crewstate.StateNeedsDecision)
	CrewWaitMate      = CrewStatus(crewstate.StateWaitMate)
	// CrewBlocked is the observer's: an open incident in `incidents.log`
	// says the crew cannot report for itself any more.
	CrewBlocked = CrewStatus(crewstate.StateBlocked)
	// CrewFinished and CrewFailed are terminal, and only `crew stop` (or a
	// failed spawn) writes them.
	CrewFinished = CrewStatus(crewstate.StateFinished)
	CrewFailed   = CrewStatus(crewstate.StateFailed)
)

// ParseCrewStatus rejects empty and unknown values.
func ParseCrewStatus(s string) (CrewStatus, error) {
	v := CrewStatus(strings.ToLower(strings.TrimSpace(s)))
	switch v {
	case CrewSpawned, CrewWorking, CrewNeedsDecision, CrewWaitMate, CrewBlocked, CrewFinished, CrewFailed:
		return v, nil
	case "":
		return "", fmt.Errorf("crew status: %w", ErrEmptyValue)
	default:
		return "", fmt.Errorf("crew status %q: %w", s, ErrInvalidValue)
	}
}

func (s CrewStatus) String() string { return string(s) }

// Closed reports whether the task is over: `finished` or `failed`, the two
// states `matev2 crew stop` writes. A closed Crew leaves ProjectNode.Crews
// and is counted in ProjectNode.ClosedCrews instead. `wait-mate` is not
// closed - it is the crew reporting, and closing is somebody else's
// decision (mvp.md section 4b).
func (s CrewStatus) Closed() bool { return crewstate.State(s).Closed() }

// CrewStateOf resolves one Crew's displayed state in the fixed order of
// mvp.md section 4b, over the same pure table `matev2 state` uses. Every
// reader of a Crew's state in this codebase goes through here or through
// crewstate.Declare directly; there is no second ordering anywhere.
//
// meta is `crews/<id>.meta`, openIncident is whether box.OpenIncidents
// returned anything for the crew, and lastVerb is the verb of its last
// recognised `.status` line (box.LastVerb).
func CrewStateOf(meta map[string]string, openIncident bool, lastVerb string) CrewStatus {
	return CrewStatus(crewstate.Declare(crewstate.Declaration{
		Meta:         meta,
		OpenIncident: openIncident,
		LastVerb:     crewstate.StatusVerb(lastVerb),
	}))
}

// MergeRequestStatus is the merge request lifecycle. A successful merge
// whose cleanup failed is cleanup_pending, never failed.
type MergeRequestStatus string

const (
	MergePendingConfirmation MergeRequestStatus = "pending_confirmation"
	MergeExecuting           MergeRequestStatus = "executing"
	MergeMerged              MergeRequestStatus = "merged"
	MergeCleaned             MergeRequestStatus = "cleaned"
	MergeNeedsRebase         MergeRequestStatus = "needs_rebase"
	MergeFailed              MergeRequestStatus = "failed"
	MergeCleanupPending      MergeRequestStatus = "cleanup_pending"
)

// ParseMergeRequestStatus rejects empty and unknown values.
func ParseMergeRequestStatus(s string) (MergeRequestStatus, error) {
	v := MergeRequestStatus(strings.ToLower(strings.TrimSpace(s)))
	switch v {
	case MergePendingConfirmation, MergeExecuting, MergeMerged, MergeCleaned, MergeNeedsRebase, MergeFailed, MergeCleanupPending:
		return v, nil
	case "":
		return "", fmt.Errorf("merge request status: %w", ErrEmptyValue)
	default:
		return "", fmt.Errorf("merge request status %q: %w", s, ErrInvalidValue)
	}
}

func (s MergeRequestStatus) String() string { return string(s) }

// IsOpen reports whether the request still blocks another request for the
// same Crew.
func (s MergeRequestStatus) IsOpen() bool {
	return s == MergePendingConfirmation || s == MergeExecuting
}

// InboxEntryKind keeps completion handoffs separate from Crew-to-Mate
// interactions. A completion is never an interaction awaiting a reply.
type InboxEntryKind string

const (
	InboxEntryInteraction InboxEntryKind = "interaction"
	InboxEntryCompletion  InboxEntryKind = "completion"
)

// InteractionStatus is the Crew-to-Mate interaction lifecycle the session
// view's inbox rail renders. mvp.md §4 replaces the v1 interaction record
// with an append-only `.status` line, so nothing writes these values today;
// the vocabulary is kept because the rail that renders them is kept.
type InteractionStatus string

const (
	InteractionQueued    InteractionStatus = "queued"
	InteractionDelivered InteractionStatus = "delivered"
	InteractionAnswered  InteractionStatus = "answered"
	InteractionBlocked   InteractionStatus = "blocked"
	InteractionExpired   InteractionStatus = "expired"
	InteractionStopped   InteractionStatus = "stopped"
)

// ParseInteractionStatus rejects empty and unknown values.
func ParseInteractionStatus(s string) (InteractionStatus, error) {
	v := InteractionStatus(strings.ToLower(strings.TrimSpace(s)))
	switch v {
	case InteractionQueued, InteractionDelivered, InteractionAnswered, InteractionBlocked, InteractionExpired, InteractionStopped:
		return v, nil
	case "":
		return "", fmt.Errorf("interaction status: %w", ErrEmptyValue)
	default:
		return "", fmt.Errorf("interaction status %q: %w", s, ErrInvalidValue)
	}
}

func (s InteractionStatus) String() string { return string(s) }

// AwaitsReply reports whether the interaction is still waiting on an
// answer.
func (s InteractionStatus) AwaitsReply() bool {
	return s == InteractionQueued || s == InteractionDelivered
}

// Terminal reports whether no further transition is possible.
func (s InteractionStatus) Terminal() bool {
	return s == InteractionAnswered || s == InteractionExpired || s == InteractionStopped
}
