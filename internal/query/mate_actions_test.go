package query

import (
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/domain"
)

func findAction(t *testing.T, actions []ActionAvailability, name string) ActionAvailability {
	t.Helper()
	for _, a := range actions {
		if a.Action == name {
			return a
		}
	}
	t.Fatalf("no %q entry in %+v", name, actions)
	return ActionAvailability{}
}

// TestMateActionsAuthorOnboardAndSwitchHarness. Before this, mateActions
// emitted only start/stop/resume, so the Console's create-Mate entry was the
// one action on a Project frame whose enablement was NOT authored here - the
// drift this package exists to prevent.
func TestMateActionsAuthorOnboardAndSwitchHarness(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name             string
		mate             MateNode
		onboard, switchH bool
	}{
		{
			name:    "no mate yet",
			mate:    MateNode{Designated: AbsentField[MateIdentity]("this Project has no Mate")},
			onboard: true, switchH: false,
		},
		{
			name: "created mate",
			mate: MateNode{Designated: KnownField(MateIdentity{
				MateID: "mate_1", HarnessKind: domain.HarnessClaude, Status: domain.MateCreated,
			})},
			onboard: false, switchH: true,
		},
		{
			name: "stopped mate",
			mate: MateNode{Designated: KnownField(MateIdentity{
				MateID: "mate_1", HarnessKind: domain.HarnessClaude, Status: domain.MateStopped,
			})},
			onboard: false, switchH: true,
		},
		{
			name: "running mate",
			mate: MateNode{Designated: KnownField(MateIdentity{
				MateID: "mate_1", HarnessKind: domain.HarnessClaude, Status: domain.MateRunning,
			})},
			onboard: false, switchH: true,
		},
		{
			name:    "unreadable designation",
			mate:    MateNode{Designated: UnknownField[MateIdentity]("mate lookup timed out (2s)")},
			onboard: false, switchH: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actions := mateActions(tc.mate)
			if got := findAction(t, actions, "onboard"); got.Available != tc.onboard {
				t.Fatalf("onboard = %+v, want available=%v", got, tc.onboard)
			}
			if got := findAction(t, actions, "switch_harness"); got.Available != tc.switchH {
				t.Fatalf("switch_harness = %+v, want available=%v", got, tc.switchH)
			}
		})
	}
}

// TestUnreadableMateNeverReportsAFactItDidNotRead: an Unknown designation is
// a failed read, so neither refusal may be worded as the established fact
// "this Project has no Mate" or "this Project already has one".
func TestUnreadableMateNeverReportsAFactItDidNotRead(t *testing.T) {
	t.Parallel()
	actions := mateActions(MateNode{Designated: UnknownField[MateIdentity]("mate lookup timed out (2s)")})
	for _, name := range []string{"onboard", "switch_harness"} {
		got := findAction(t, actions, name)
		if got.Available {
			t.Fatalf("%s was offered on an unreadable Mate: %+v", name, got)
		}
		if got.Reason == "" {
			t.Fatalf("%s carries no reason", name)
		}
		for _, banned := range []string{"has no Mate", "already has"} {
			if contains(got.Reason, banned) {
				t.Fatalf("%s reason %q asserts a fact the failed read never established", name, got.Reason)
			}
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) <= len(s) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
