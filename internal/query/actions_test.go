package query

import (
	"strings"
	"testing"
)

func TestDeriveActionsPublishesReasonsFromRecordedState(t *testing.T) {
	s := Snapshot{Projects: []ProjectNode{{
		ProjectID: "project", Mate: MateNode{
			Designated: KnownField(MateIdentity{MateID: "mate", Status: MateRunning}),
			Binding:    KnownField(BindingValue{Status: BindingActive}),
		},
		Crews: []CrewNode{
			{CrewID: "failed", Status: CrewFailed},
			{CrewID: "running", Status: CrewWorking, Binding: KnownField(BindingValue{Status: BindingActive})},
		},
	}}}
	deriveActions(&s)
	if len(s.Actions) != 1 || s.Actions[0].Action != "onboard" || !s.Actions[0].Available {
		t.Fatalf("workspace actions = %+v", s.Actions)
	}
	if got := actionByName(s.Projects[0].Mate.Actions, "stop"); !got.Available {
		t.Fatalf("mate stop = %+v, want available", got)
	}
	failed := s.Projects[0].Crews[0]
	if actionByName(failed.Actions, "stop").Available {
		t.Fatal("failed crew stop must not be available without a binding")
	}
}

// TestRepairUnknownBindingIsNeverReportedAsNoStaleBinding is B6's
// regression: an Unknown Binding read is a failed read, not proof the
// binding is absent-or-not-stale. Reporting "no stale binding is recorded"
// for it asserts a fact the read never established.
func TestRepairUnknownBindingIsNeverReportedAsNoStaleBinding(t *testing.T) {
	s := Snapshot{Projects: []ProjectNode{{
		Crews: []CrewNode{
			{CrewID: "crew", Status: CrewWorking, Binding: UnknownField[BindingValue]("binding lookup timed out (2s)")},
		},
	}}}
	deriveActions(&s)
	repair := actionByName(s.Projects[0].Crews[0].Actions, "repair")
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

func TestProjectActionsExposeDesignatedMateLifecycle(t *testing.T) {
	for _, tc := range []struct {
		status MateStatus
		start  bool
		resume bool
	}{
		{status: MateCreated, start: true},
		{status: MateStopped, resume: true},
		{status: MateRunning},
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
