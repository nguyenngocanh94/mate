package query

import (
	"fmt"

	"github.com/nguyenngocanh94/matev2/internal/domain"
)

// AttentionKind is why a row needs a human's attention now. It is derived
// from recorded state only - a status the database holds, or a field this
// snapshot failed to read - never from a live probe of an agent, a pane or
// a worktree on disk (ADR 0019's health observer does not exist).
//
// This vocabulary is the one definition of "needs attention" in the
// codebase. It lives here rather than in the Console so that `mate`
// command output, the Console's ATTENTION column and the Console's
// "N need attention" counts cannot drift apart, and so that adding a
// status changes one switch instead of several renderers.
type AttentionKind string

const (
	// AttentionFailed: the latest attempt is recorded failed.
	AttentionFailed AttentionKind = "failed"
	// AttentionRebase: the latest attempt is recorded needs_rebase.
	AttentionRebase AttentionKind = "rebase"
	// AttentionRepair: the latest attempt is recorded needs_repair.
	AttentionRepair AttentionKind = "repair"
	// AttentionReview: the latest attempt is recorded awaiting_review and is
	// waiting on a person. awaiting_review is never success.
	AttentionReview AttentionKind = "review"
	// AttentionBlocked: the latest attempt is recorded blocked.
	AttentionBlocked AttentionKind = "blocked"
	// AttentionStaleBinding: the attempt's status looks fine but its runtime
	// binding is recorded stale, so mate could not confirm the agent stopped
	// and attach is refused (ADR 0027).
	AttentionStaleBinding AttentionKind = "stale"
	// AttentionUnreadable: a field this row's health depends on came back
	// Unknown. A read failure is itself something a person must look at, so
	// it produces a Known attention rather than an Unknown one: the fact
	// "this needs attention" was established, even though the value behind it
	// was not.
	AttentionUnreadable AttentionKind = "unknown"
	// AttentionNoMate: the Project has no designated Mate, so no Crew can be
	// started for its Tasks.
	AttentionNoMate AttentionKind = "no mate"
	// AttentionMateStale: the Project's Mate binding is recorded stale.
	AttentionMateStale AttentionKind = "mate stale"
	// AttentionMateUnknown: the Project's Mate is itself recorded unknown
	// (domain.MateUnknown) - `mate stop` calls RecordMateUnknown when Herdr's
	// stop fails or the re-listed inventory still shows the agent, and that
	// path does not stale the binding, so the Mate can sit unknown with an
	// active-looking binding. mateErrorRef already treats this as the one
	// Mate error state; attention must agree rather than calling the
	// Project healthy.
	AttentionMateUnknown AttentionKind = "mate unknown"
)

// Attention is one row's reason for needing attention. Kind is the one-word
// label the list's ATTENTION column shows; Why is the sentence the
// inspector shows beside it.
type Attention struct {
	Kind AttentionKind
	Why  string
}

// ProjectAttention is a Project's own attention plus how many of its Tasks
// need attention, which is what the list's ATTENTION column and the project
// inspector's "3 · 2 need attention" line both read.
//
// Kind is set when the Project itself needs attention (no Mate, stale Mate
// binding). TasksNeedingAttention counts Tasks whose own Attention is
// Known, and is independent of Kind: a Project with a healthy Mate and two
// failed Tasks has no Kind and a count of 2.
type ProjectAttention struct {
	Kind                  AttentionKind
	Why                   string
	TasksNeedingAttention int
}

// crewAttention is the per-attempt definition every other count derives
// from. Order matters: a Crew can be both failed and stale-bound, and the
// worse fact wins the one word the column has room for.
func crewAttention(c CrewNode) Field[Attention] {
	switch c.Status {
	case domain.CrewFailed:
		return KnownField(Attention{AttentionFailed, attemptWhy(c, "failed", c.Error)})
	case domain.CrewNeedsRepair:
		return KnownField(Attention{AttentionRepair, attemptWhy(c, "needs repair", c.Error)})
	case domain.CrewNeedsRebase:
		return KnownField(Attention{AttentionRebase, attemptWhy(c, "needs rebase", c.Error)})
	case domain.CrewBlocked:
		return KnownField(Attention{AttentionBlocked, attemptWhy(c, "blocked", c.Error)})
	case domain.CrewAwaitingReview:
		return KnownField(Attention{AttentionReview, fmt.Sprintf("attempt %d is recorded awaiting_review and waits on a person", c.Attempt)})
	}
	// A status that is not itself an error can still hide one: a binding
	// nobody could read, or one recorded stale under a running attempt.
	switch {
	case c.Binding.State == Unknown:
		return KnownField(Attention{AttentionUnreadable, fmt.Sprintf("the runtime binding of attempt %d could not be read: %s", c.Attempt, c.Binding.Reason)})
	case c.Binding.State == Known && c.Binding.Value.Status == BindingStale:
		return KnownField(Attention{AttentionStaleBinding, fmt.Sprintf("attempt %d is recorded %s but its runtime binding is stale, so attach is refused", c.Attempt, c.Status)})
	case c.Worktree.State == Unknown:
		return KnownField(Attention{AttentionUnreadable, fmt.Sprintf("the worktree of attempt %d could not be read: %s", c.Attempt, c.Worktree.Reason)})
	}
	return AbsentField[Attention](fmt.Sprintf("attempt %d is recorded %s and nothing about it needs attention", c.Attempt, c.Status))
}

// mateUnknownWhy phrases a Mate recorded unknown, folding in the reason
// mateErrorRef already resolved for it when that read succeeded - mirroring
// attemptWhy's treatment of a Crew's error state, so the same reason is not
// authored twice in two different sentences.
func mateUnknownWhy(mate MateNode, consequence string) string {
	head := "the project's Mate is recorded unknown, " + consequence
	if mate.Error.State == Known && mate.Error.Value != "" {
		return head + ": " + string(mate.Error.Value)
	}
	return head
}

// attemptWhy phrases an attempt's error state, preferring the reason
// recorded on its last event and saying plainly when there is none - and
// when the read that would have supplied it failed, which is not the same
// as there being none.
func attemptWhy(c CrewNode, phrase string, reason Field[ErrorReason]) string {
	head := fmt.Sprintf("attempt %d %s", c.Attempt, phrase)
	switch {
	case reason.State == Known && reason.Value != "":
		return head + ": " + string(reason.Value)
	case reason.State == Unknown:
		return head + "; the recorded reason could not be read: " + reason.Reason
	default:
		return head + "; no reason is recorded on its last event"
	}
}

// taskAttention derives a Task's attention from its latest attempt, which
// is the attempt a person would act on. A Task with no attempts otherwise
// needs attention only when its Project has no Mate to start one - except
// when the Task's own recorded status is itself an error state
// (domain.TaskFailed / domain.TaskBlocked): taskErrorRef already calls that
// Task an error regardless of attempt count, and attention must agree
// rather than calling a recorded-failed or recorded-blocked Task healthy
// just because it never got as far as an attempt. Whether a writer can
// actually produce that combination today is not established here; the
// guard exists because the two functions must not disagree about the same
// Task either way.
func taskAttention(t TaskNode, mate MateNode) Field[Attention] {
	if len(t.Crews) == 0 {
		switch t.Status {
		case domain.TaskFailed:
			return KnownField(Attention{AttentionFailed, fmt.Sprintf("the task is recorded %s with no attempt ever started", t.Status)})
		case domain.TaskBlocked:
			return KnownField(Attention{AttentionBlocked, fmt.Sprintf("the task is recorded %s with no attempt ever started", t.Status)})
		}
		switch mate.Designated.State {
		case Absent:
			return KnownField(Attention{AttentionNoMate, "the project has no designated Mate, so no attempt can be started for this task"})
		case Unknown:
			return KnownField(Attention{AttentionUnreadable, "the project's Mate could not be read, so whether an attempt can be started is unknown: " + mate.Designated.Reason})
		case Known:
			if mate.Designated.Value.Status == domain.MateUnknown {
				return KnownField(Attention{AttentionMateUnknown, mateUnknownWhy(mate, "so no attempt can be started for this task")})
			}
		}
		return AbsentField[Attention](fmt.Sprintf("no attempt has been started and the task is recorded %s", t.Status))
	}
	return crewAttention(t.Crews[len(t.Crews)-1])
}

// projectAttention counts the Tasks needing attention and adds the
// Project's own structural problems: no Mate at all, or a Mate whose
// binding is recorded stale.
func projectAttention(p ProjectNode) Field[ProjectAttention] {
	count := 0
	for _, t := range p.Tasks {
		if t.Attention.State == Known {
			count++
		}
	}
	out := ProjectAttention{TasksNeedingAttention: count}
	switch {
	case p.Mate.Designated.State == Absent:
		out.Kind, out.Why = AttentionNoMate, "the project has no designated Mate, so no attempt can be started"
	case p.Mate.Designated.State == Unknown:
		out.Kind, out.Why = AttentionUnreadable, "the project's Mate could not be read: "+p.Mate.Designated.Reason
	case p.Mate.Designated.Value.Status == domain.MateUnknown:
		out.Kind, out.Why = AttentionMateUnknown, mateUnknownWhy(p.Mate, "so no attempt can be started")
	case p.Mate.Binding.State == Unknown:
		out.Kind, out.Why = AttentionUnreadable, "the Mate's runtime binding could not be read: "+p.Mate.Binding.Reason
	case p.Mate.Binding.State == Known && p.Mate.Binding.Value.Status == BindingStale:
		out.Kind, out.Why = AttentionMateStale, "the Mate's runtime binding is recorded stale, so attach is refused"
	}
	if out.Kind == "" && count == 0 {
		return AbsentField[ProjectAttention]("no task in this project needs attention and its Mate is recorded healthy")
	}
	return KnownField(out)
}
