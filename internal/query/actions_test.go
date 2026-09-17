package query

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/domain"
)

func TestDeriveActionsPublishesReasonsFromRecordedState(t *testing.T) {
	s := Snapshot{Projects: []ProjectNode{{
		ProjectID: "project", Mate: MateNode{
			Designated: KnownField(MateIdentity{MateID: "mate", Status: domain.MateRunning}),
			Binding:    KnownField(BindingValue{Status: BindingActive}),
		},
		Tasks: []TaskNode{{Crews: []CrewNode{
			{CrewID: "failed", Status: domain.CrewFailed},
			{CrewID: "running", Status: domain.CrewRunning, Binding: KnownField(BindingValue{Status: BindingActive})},
		}}},
	}}}
	deriveActions(&s)
	if len(s.Actions) != 1 || s.Actions[0].Action != "onboard" || !s.Actions[0].Available {
		t.Fatalf("workspace actions = %+v", s.Actions)
	}
	if got := actionByName(s.Projects[0].Mate.Actions, "stop"); !got.Available {
		t.Fatalf("mate stop = %+v, want available", got)
	}
	failed := s.Projects[0].Tasks[0].Crews[0]
	if actionByName(failed.Actions, "retry").Available || actionByName(failed.Actions, "retry").Reason != "another attempt is active" {
		t.Fatalf("failed crew retry = %+v, want a truthful active-attempt refusal", failed.Actions)
	}
	if actionByName(failed.Actions, "stop").Available {
		t.Fatal("failed crew stop must not be available without a binding")
	}
}

// TestRetryIsRefusedByEveryNonTerminalSiblingStatus is B5's regression: the
// occupancy rule persistence.ReserveCrewAttempt actually enforces is
// activeCrewSQL, `status NOT IN ('succeeded', 'failed')`
// (internal/persistence/crew.go), not just reserved|preparing|running.
// Before the fix, a needs_repair/needs_rebase/blocked sibling was invisible
// to this check, so retry was offered and the service refused it anyway
// (see the failure scenario in the PR 51 counter-review's B5).
func TestRetryIsRefusedByEveryNonTerminalSiblingStatus(t *testing.T) {
	nonTerminal := []domain.CrewStatus{
		domain.CrewReserved, domain.CrewPreparing, domain.CrewRunning,
		domain.CrewAwaitingReview, domain.CrewBlocked, domain.CrewNeedsRebase, domain.CrewNeedsRepair,
	}
	for _, siblingStatus := range nonTerminal {
		t.Run(string(siblingStatus), func(t *testing.T) {
			s := Snapshot{Projects: []ProjectNode{{
				Tasks: []TaskNode{{Crews: []CrewNode{
					{CrewID: "target", Status: domain.CrewFailed},
					{CrewID: "sibling", Status: siblingStatus},
				}}},
			}}}
			deriveActions(&s)
			target := s.Projects[0].Tasks[0].Crews[0]
			retry := actionByName(target.Actions, "retry")
			if retry.Available {
				t.Fatalf("retry with a %s sibling = %+v, want refused", siblingStatus, retry)
			}
			if retry.Reason != "another attempt is active" {
				t.Fatalf("retry reason = %q, want the truthful active-attempt refusal", retry.Reason)
			}
		})
	}
}

// TestRetryIsAvailableWhenEverySiblingIsTerminal is the regression's other
// side: succeeded/failed are exactly the two statuses activeCrewSQL
// excludes, so a sibling in either must never block retry.
func TestRetryIsAvailableWhenEverySiblingIsTerminal(t *testing.T) {
	s := Snapshot{Projects: []ProjectNode{{
		Tasks: []TaskNode{{Crews: []CrewNode{
			{CrewID: "target", Status: domain.CrewNeedsRepair},
			{CrewID: "succeeded-sibling", Status: domain.CrewSucceeded},
			{CrewID: "failed-sibling", Status: domain.CrewFailed},
		}}},
	}}}
	deriveActions(&s)
	if retry := actionByName(s.Projects[0].Tasks[0].Crews[0].Actions, "retry"); !retry.Available {
		t.Fatalf("retry with only terminal siblings = %+v, want available", retry)
	}
}

// TestRepairUnknownBindingIsNeverReportedAsNoStaleBinding is B6's
// regression: an Unknown Binding read is a failed read, not proof the
// binding is absent-or-not-stale. Reporting "no stale binding is recorded"
// for it asserts a fact the read never established.
func TestRepairUnknownBindingIsNeverReportedAsNoStaleBinding(t *testing.T) {
	s := Snapshot{Projects: []ProjectNode{{
		Tasks: []TaskNode{{Crews: []CrewNode{
			{CrewID: "crew", Status: domain.CrewNeedsRepair, Binding: UnknownField[BindingValue]("binding lookup timed out (2s)")},
		}}},
	}}}
	deriveActions(&s)
	repair := actionByName(s.Projects[0].Tasks[0].Crews[0].Actions, "repair")
	if repair.Available {
		t.Fatalf("repair with an unknown binding = %+v, want refused", repair)
	}
	if strings.Contains(repair.Reason, "no stale binding is recorded") {
		t.Fatalf("repair reason = %q, asserts a fact the failed read never established", repair.Reason)
	}
	if !strings.Contains(repair.Reason, "unknown") {
		t.Fatalf("repair reason = %q, want it to say the binding read is unknown", repair.Reason)
	}
}

func createdWorktree() Field[WorktreeValue] {
	return KnownField(WorktreeValue{Path: "/w", Branch: "crew/x", Status: WorktreeRecordedCreated})
}

func noOpenMerge() Field[OpenMergeValue] {
	return AbsentField[OpenMergeValue]("no open merge request")
}

// TestDiscardAvailabilityFollowsDiscardCrewPreconditions pins the Console
// capability on what orchestration.DiscardCrew actually refuses from
// committed state: an open merge request, and a worktree that is not
// known-created. DiscardCrew itself does not filter by Crew status, so
// running/failed/succeeded/awaiting_review are all eligible when those two
// facts allow it.
func TestDiscardAvailabilityFollowsDiscardCrewPreconditions(t *testing.T) {
	eligible := []domain.CrewStatus{
		domain.CrewReserved, domain.CrewPreparing, domain.CrewRunning,
		domain.CrewFailed, domain.CrewSucceeded, domain.CrewAwaitingReview,
		domain.CrewBlocked, domain.CrewNeedsRebase, domain.CrewNeedsRepair,
	}
	for _, status := range eligible {
		t.Run("available/"+string(status), func(t *testing.T) {
			s := Snapshot{Projects: []ProjectNode{{
				Tasks: []TaskNode{{Crews: []CrewNode{{
					CrewID: "crew", Status: status, Worktree: createdWorktree(), OpenMerge: noOpenMerge(),
				}}}},
			}}}
			deriveActions(&s)
			got := actionByName(s.Projects[0].Tasks[0].Crews[0].Actions, "discard")
			if !got.Available {
				t.Fatalf("discard for %s = %+v, want available: DiscardCrew does not filter by status", status, got)
			}
		})
	}

	s := Snapshot{Projects: []ProjectNode{{
		Tasks: []TaskNode{{Crews: []CrewNode{{
			CrewID: "crew", Status: domain.CrewFailed, Worktree: createdWorktree(),
			OpenMerge: KnownField(OpenMergeValue{RequestID: "mr_1", Status: domain.MergePendingConfirmation}),
		}}}},
	}}}
	deriveActions(&s)
	got := actionByName(s.Projects[0].Tasks[0].Crews[0].Actions, "discard")
	if got.Available {
		t.Fatalf("discard with an open merge = %+v, want refused", got)
	}
	if !strings.Contains(got.Reason, "open merge request") {
		t.Fatalf("discard reason = %q, want DiscardCrew's open-merge refusal", got.Reason)
	}
}

func TestDiscardIsRefusedWhenWorktreeIsNotKnownCreated(t *testing.T) {
	cases := []struct {
		name   string
		wt     Field[WorktreeValue]
		wantIn string
	}{
		{name: "unknown", wt: UnknownField[WorktreeValue]("worktree lookup timed out (2s)"), wantIn: "unknown"},
		{name: "absent", wt: AbsentField[WorktreeValue]("no worktree is recorded for this attempt"), wantIn: "no worktree"},
		{name: "removed", wt: KnownField(WorktreeValue{Path: "/w", Branch: "crew/x", Status: WorktreeRecordedRemoved}), wantIn: "removed"},
		{name: "creating", wt: KnownField(WorktreeValue{Path: "/w", Branch: "crew/x", Status: WorktreeRecordedCreating}), wantIn: "creating"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Snapshot{Projects: []ProjectNode{{
				Tasks: []TaskNode{{Crews: []CrewNode{{
					CrewID: "crew", Status: domain.CrewFailed, Worktree: tc.wt, OpenMerge: noOpenMerge(),
				}}}},
			}}}
			deriveActions(&s)
			got := actionByName(s.Projects[0].Tasks[0].Crews[0].Actions, "discard")
			if got.Available {
				t.Fatalf("discard with worktree %s = %+v, want refused", tc.name, got)
			}
			if !strings.Contains(got.Reason, tc.wantIn) {
				t.Fatalf("discard reason = %q, want it to name %q", got.Reason, tc.wantIn)
			}
		})
	}
}

func TestDiscardUnknownMergeReadIsNeverReportedAsNoOpenMerge(t *testing.T) {
	s := Snapshot{Projects: []ProjectNode{{
		Tasks: []TaskNode{{Crews: []CrewNode{{
			CrewID: "crew", Status: domain.CrewFailed, Worktree: createdWorktree(),
			OpenMerge: UnknownField[OpenMergeValue]("merge request lookup timed out (2s)"),
		}}}},
	}}}
	deriveActions(&s)
	got := actionByName(s.Projects[0].Tasks[0].Crews[0].Actions, "discard")
	if got.Available {
		t.Fatalf("discard with unknown merge read = %+v, want refused", got)
	}
	if strings.Contains(got.Reason, "no open merge") {
		t.Fatalf("discard reason = %q, asserts a fact the failed read never established", got.Reason)
	}
	if !strings.Contains(got.Reason, "unknown") && !strings.Contains(got.Reason, "could not be read") {
		t.Fatalf("discard reason = %q, want it to say the merge-request read is unknown", got.Reason)
	}
}

func TestProjectActionsExposeDesignatedMateLifecycle(t *testing.T) {
	for _, tc := range []struct {
		status domain.MateStatus
		start  bool
		resume bool
	}{
		{status: domain.MateCreated, start: true},
		{status: domain.MateStopped, resume: true},
		{status: domain.MateRunning},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			s := Snapshot{Projects: []ProjectNode{{Mate: MateNode{
				Designated: KnownField(MateIdentity{MateID: "mate", Status: tc.status}),
			}}}}
			deriveActions(&s)
			got := s.Projects[0].Actions
			if actionByName(got, "start").Available != tc.start || actionByName(got, "resume").Available != tc.resume {
				t.Fatalf("Project actions for %s = %+v", tc.status, got)
			}
		})
	}
}

func TestProjectWithoutMateDoesNotExposeLifecycleActions(t *testing.T) {
	s := Snapshot{Projects: []ProjectNode{{Mate: MateNode{Designated: AbsentField[MateIdentity]("no Mate")}}}}
	deriveActions(&s)
	if got := actionByName(s.Projects[0].Actions, "start"); got.Available {
		t.Fatalf("start = %+v, must not be available without a Mate", got)
	}
	if got := actionByName(s.Projects[0].Actions, "onboard"); !got.Available {
		t.Fatalf("onboard = %+v, want available without a Mate", got)
	}
}

func actionByName(actions []ActionAvailability, name string) ActionAvailability {
	for _, a := range actions {
		if a.Action == name {
			return a
		}
	}
	return ActionAvailability{Action: name, Reason: "not found"}
}
