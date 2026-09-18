package query

import (
	"errors"
	"fmt"
	"strings"
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

// Mode is a Project's communication mode (mvp.md section 5). Supervised is
// the default and means no byte is ever sent to the Mate's pane without the
// reader pressing a key; auto means the Console's digest daemon may. The
// durable form of this is the presence of `mate/.auto`; this type is only
// how the two are named on screen.
type Mode string

const (
	ModeSupervised Mode = "supervised"
	ModeAuto       Mode = "auto"
)

func (m Mode) String() string { return string(m) }

// ModeFor names the mode the `.auto` flag's presence selects.
func ModeFor(auto bool) Mode {
	if auto {
		return ModeAuto
	}
	return ModeSupervised
}

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

// CrewStatus is the Crew attempt lifecycle. awaiting_review does not become
// succeeded without a Mate or user accepting it.
type CrewStatus string

const (
	CrewReserved       CrewStatus = "reserved"
	CrewPreparing      CrewStatus = "preparing"
	CrewRunning        CrewStatus = "running"
	CrewAwaitingReview CrewStatus = "awaiting_review"
	CrewSucceeded      CrewStatus = "succeeded"
	CrewFailed         CrewStatus = "failed"
	CrewBlocked        CrewStatus = "blocked"
	CrewNeedsRebase    CrewStatus = "needs_rebase"
	CrewNeedsRepair    CrewStatus = "needs_repair"
	// CrewStopped is a crew whose meta no longer names an agent (StopCrew
	// ran) and whose status file never got a line: torn down, not
	// waiting. A crew that did write is shown by its own last verb.
	CrewStopped CrewStatus = "stopped"
)

// ParseCrewStatus rejects empty and unknown values.
func ParseCrewStatus(s string) (CrewStatus, error) {
	v := CrewStatus(strings.ToLower(strings.TrimSpace(s)))
	switch v {
	case CrewReserved, CrewPreparing, CrewRunning, CrewAwaitingReview, CrewSucceeded, CrewFailed, CrewBlocked, CrewNeedsRebase, CrewNeedsRepair:
		return v, nil
	case "":
		return "", fmt.Errorf("crew status: %w", ErrEmptyValue)
	default:
		return "", fmt.Errorf("crew status %q: %w", s, ErrInvalidValue)
	}
}

func (s CrewStatus) String() string { return string(s) }

// IsFinished reports whether the attempt's outcome is already recorded, so
// the Console can drop it out of the active list into its Completed group.
// awaiting_review is not finished: it is waiting on a person.
func (s CrewStatus) IsFinished() bool {
	return s == CrewSucceeded || s == CrewFailed
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
