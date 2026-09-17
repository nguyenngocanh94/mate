package query

import "fmt"

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
	// AttentionFailed: the Crew is recorded failed.
	AttentionFailed AttentionKind = "failed"
	// AttentionRebase: the Crew is recorded needs_rebase.
	AttentionRebase AttentionKind = "rebase"
	// AttentionRepair: the Crew is recorded needs_repair.
	AttentionRepair AttentionKind = "repair"
	// AttentionReview: the Crew is recorded awaiting_review and is waiting
	// on a person. awaiting_review is never success.
	AttentionReview AttentionKind = "review"
	// AttentionBlocked: the Crew is recorded blocked.
	AttentionBlocked AttentionKind = "blocked"
	// AttentionStaleBinding: the Crew's status looks fine but its runtime
	// binding is recorded stale, so matev2 could not confirm the agent
	// stopped and attach is refused.
	AttentionStaleBinding AttentionKind = "stale"
	// AttentionUnreadable: a field this row's health depends on came back
	// Unknown. A read failure is itself something a person must look at, so
	// it produces a Known attention rather than an Unknown one: the fact
	// "this needs attention" was established, even though the value behind it
	// was not.
	AttentionUnreadable AttentionKind = "unknown"
	// AttentionNoMate: the Project has no designated Mate, so no Crew can
	// be spawned for it.
	AttentionNoMate AttentionKind = "no mate"
	// AttentionMateStale: the Project's Mate binding is recorded stale.
	AttentionMateStale AttentionKind = "mate stale"
	// AttentionMateUnknown: the Project's Mate is itself recorded unknown
	// (MateUnknown) - `mate stop` calls RecordMateUnknown when Herdr's
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

// ProjectAttention is a Project's own attention plus how many of its Crews
// need attention, which is what the list's ATTENTION column and the project
// inspector's "3 · 2 need attention" line both read.
//
// Kind is set when the Project itself needs attention (no Mate, stale Mate
// binding). CrewsNeedingAttention counts Crews whose own Attention is
// Known, and is independent of Kind: a Project with a healthy Mate and two
// failed Crews has no Kind and a count of 2.
type ProjectAttention struct {
	Kind                  AttentionKind
	Why                   string
	CrewsNeedingAttention int
}

// crewAttention is the per-Crew definition every other count derives from.
// Order matters: a Crew can be both failed and stale-bound, and the worse
// fact wins the one word the column has room for.
func crewAttention(c CrewNode) Field[Attention] {
	switch c.Status {
	case CrewFailed:
		return KnownField(Attention{AttentionFailed, attemptWhy(c, "failed", c.Error)})
	case CrewNeedsRepair:
		return KnownField(Attention{AttentionRepair, attemptWhy(c, "needs repair", c.Error)})
	case CrewNeedsRebase:
		return KnownField(Attention{AttentionRebase, attemptWhy(c, "needs rebase", c.Error)})
	case CrewBlocked:
		return KnownField(Attention{AttentionBlocked, attemptWhy(c, "blocked", c.Error)})
	case CrewAwaitingReview:
		return KnownField(Attention{AttentionReview, fmt.Sprintf("crew %s is recorded awaiting_review and waits on a person", c.CrewID)})
	}
	// A status that is not itself an error can still hide one: a binding
	// nobody could read, or one recorded stale under a running attempt.
	switch {
	case c.Binding.State == Unknown:
		return KnownField(Attention{AttentionUnreadable, fmt.Sprintf("the runtime binding of crew %s could not be read: %s", c.CrewID, c.Binding.Reason)})
	case c.Binding.State == Known && c.Binding.Value.Status == BindingStale:
		return KnownField(Attention{AttentionStaleBinding, fmt.Sprintf("crew %s is recorded %s but its runtime binding is stale, so attach is refused", c.CrewID, c.Status)})
	case c.Worktree.State == Unknown:
		return KnownField(Attention{AttentionUnreadable, fmt.Sprintf("the worktree of crew %s could not be read: %s", c.CrewID, c.Worktree.Reason)})
	}
	return AbsentField[Attention](fmt.Sprintf("crew %s is recorded %s and nothing about it needs attention", c.CrewID, c.Status))
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

// attemptWhy phrases a Crew's error state, preferring the reason recorded
// on its last event and saying plainly when there is none - and when the
// read that would have supplied it failed, which is not the same as there
// being none.
func attemptWhy(c CrewNode, phrase string, reason Field[ErrorReason]) string {
	head := fmt.Sprintf("crew %s %s", c.CrewID, phrase)
	switch {
	case reason.State == Known && reason.Value != "":
		return head + ": " + string(reason.Value)
	case reason.State == Unknown:
		return head + "; the recorded reason could not be read: " + reason.Reason
	default:
		return head + "; no reason is recorded on its last event"
	}
}

// projectAttention counts the Crews needing attention and adds the
// Project's own structural problems: no Mate at all, or a Mate whose
// binding is recorded stale.
func projectAttention(p ProjectNode) Field[ProjectAttention] {
	count := 0
	for _, c := range p.Crews {
		if c.Attention.State == Known {
			count++
		}
	}
	out := ProjectAttention{CrewsNeedingAttention: count}
	switch {
	case p.Mate.Designated.State == Absent:
		out.Kind, out.Why = AttentionNoMate, "the project has no designated Mate, so no crew can be spawned"
	case p.Mate.Designated.State == Unknown:
		out.Kind, out.Why = AttentionUnreadable, "the project's Mate could not be read: "+p.Mate.Designated.Reason
	case p.Mate.Designated.Value.Status == MateUnknown:
		out.Kind, out.Why = AttentionMateUnknown, mateUnknownWhy(p.Mate, "so no crew can be spawned")
	case p.Mate.Binding.State == Unknown:
		out.Kind, out.Why = AttentionUnreadable, "the Mate's runtime binding could not be read: "+p.Mate.Binding.Reason
	case p.Mate.Binding.State == Known && p.Mate.Binding.Value.Status == BindingStale:
		out.Kind, out.Why = AttentionMateStale, "the Mate's runtime binding is recorded stale, so attach is refused"
	}
	if out.Kind == "" && count == 0 {
		return AbsentField[ProjectAttention]("no crew in this project needs attention and its Mate is recorded healthy")
	}
	return KnownField(out)
}
