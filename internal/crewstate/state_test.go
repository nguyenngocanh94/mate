package crewstate_test

import (
	"testing"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
)

// TestDeclare is the resolution order of mvp.md section 4b, one case per
// rule: meta first, then an open incident, then the last status verb, then
// spawned - plus the one backward-compatibility rule (stopped_at with no
// state=).
func TestDeclare(t *testing.T) {
	tests := []struct {
		name string
		in   crewstate.Declaration
		want crewstate.State
	}{
		{
			name: "no meta and no status line is spawned",
			in:   crewstate.Declaration{},
			want: crewstate.StateSpawned,
		},
		{
			name: "meta state=spawned and nothing else is spawned",
			in:   crewstate.Declaration{Meta: map[string]string{"state": "spawned"}},
			want: crewstate.StateSpawned,
		},
		{
			name: "meta state=finished wins over everything",
			in: crewstate.Declaration{
				Meta:         map[string]string{"state": "finished"},
				OpenIncident: true,
				LastVerb:     crewstate.VerbNeedsDecision,
			},
			want: crewstate.StateFinished,
		},
		{
			name: "meta state=failed wins over everything",
			in: crewstate.Declaration{
				Meta:         map[string]string{"state": "failed", "failed_reason": "startup screen not recognised"},
				OpenIncident: true,
				LastVerb:     crewstate.VerbWorking,
			},
			want: crewstate.StateFailed,
		},
		{
			name: "stopped_at without state= reads as finished",
			in:   crewstate.Declaration{Meta: map[string]string{"stopped_at": "2026-09-18T10:00:00Z"}},
			want: crewstate.StateFinished,
		},
		{
			name: "state= beats stopped_at when both are present",
			in: crewstate.Declaration{Meta: map[string]string{
				"state": "failed", "stopped_at": "2026-09-18T10:00:00Z",
			}},
			want: crewstate.StateFailed,
		},
		{
			name: "an open incident outranks the crew's own last verb",
			in: crewstate.Declaration{
				Meta:         map[string]string{"state": "spawned"},
				OpenIncident: true,
				LastVerb:     crewstate.VerbWorking,
			},
			want: crewstate.StateBlocked,
		},
		{
			name: "an open incident on a crew that never wrote a line is blocked",
			in:   crewstate.Declaration{OpenIncident: true},
			want: crewstate.StateBlocked,
		},
		{
			name: "last verb working",
			in:   crewstate.Declaration{LastVerb: crewstate.VerbWorking},
			want: crewstate.StateWorking,
		},
		{
			name: "last verb needs-decision",
			in:   crewstate.Declaration{LastVerb: crewstate.VerbNeedsDecision},
			want: crewstate.StateNeedsDecision,
		},
		{
			name: "last verb wait-mate",
			in:   crewstate.Declaration{LastVerb: crewstate.VerbWaitMate},
			want: crewstate.StateWaitMate,
		},
		{
			name: "wait-mate is not closed; only crew stop closes a crew",
			in: crewstate.Declaration{
				Meta:     map[string]string{"state": "spawned"},
				LastVerb: crewstate.VerbWaitMate,
			},
			want: crewstate.StateWaitMate,
		},
		{
			name: "a verb this package does not know falls through to spawned",
			in:   crewstate.Declaration{LastVerb: crewstate.StatusVerb("parked")},
			want: crewstate.StateSpawned,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := crewstate.Declare(tc.in); got != tc.want {
				t.Fatalf("Declare(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStateClosed(t *testing.T) {
	closed := []crewstate.State{crewstate.StateFinished, crewstate.StateFailed}
	open := []crewstate.State{
		crewstate.StateSpawned, crewstate.StateWorking,
		crewstate.StateNeedsDecision, crewstate.StateWaitMate, crewstate.StateBlocked,
	}
	for _, s := range closed {
		if !s.Closed() {
			t.Errorf("%q must be closed: it is a terminal state", s)
		}
	}
	for _, s := range open {
		if s.Closed() {
			t.Errorf("%q must not be closed: the crew is still work in flight", s)
		}
	}
}

// TestObserve: health is an observation, never a state. Each rule gets a
// case, in the order Observe applies them.
func TestObserve(t *testing.T) {
	tests := []struct {
		name string
		in   crewstate.Observation
		want crewstate.Health
	}{
		{
			name: "no agent recorded",
			in:   crewstate.Observation{},
			want: crewstate.Health{Kind: crewstate.HealthNoAgent, Detail: "no agent is recorded for this crew"},
		},
		{
			name: "agent recorded but herdr does not have it",
			in:   crewstate.Observation{AgentRecorded: true},
			want: crewstate.Health{Kind: crewstate.HealthAgentGone, Detail: "the recorded agent is not in herdr"},
		},
		{
			name: "busy composer carries its evidence",
			in: crewstate.Observation{
				AgentRecorded: true, AgentFound: true,
				Composer: crewstate.ComposerBusy, Evidence: "✻ Pollinating…",
			},
			want: crewstate.Health{Kind: crewstate.HealthBusy, Detail: "✻ Pollinating…"},
		},
		{
			name: "empty composer is idle",
			in: crewstate.Observation{
				AgentRecorded: true, AgentFound: true, Composer: crewstate.ComposerEmpty,
			},
			want: crewstate.Health{Kind: crewstate.HealthIdle, Detail: "composer empty"},
		},
		{
			name: "pending composer keeps the half-typed line",
			in: crewstate.Observation{
				AgentRecorded: true, AgentFound: true,
				Composer: crewstate.ComposerPending, Evidence: "› half typed",
			},
			want: crewstate.Health{Kind: crewstate.HealthPending, Detail: "› half typed"},
		},
		{
			name: "unrecognised screen with evidence",
			in: crewstate.Observation{
				AgentRecorded: true, AgentFound: true,
				Composer: crewstate.ComposerUnknown, Evidence: "harness directory-trust dialog",
			},
			want: crewstate.Health{Kind: crewstate.HealthUnrecognised, Detail: "harness directory-trust dialog"},
		},
		{
			name: "unrecognised screen with no evidence still says something",
			in: crewstate.Observation{
				AgentRecorded: true, AgentFound: true, Composer: crewstate.ComposerUnknown,
			},
			want: crewstate.Health{Kind: crewstate.HealthUnrecognised, Detail: "no composer recognised on screen"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := crewstate.Observe(tc.in); got != tc.want {
				t.Fatalf("Observe(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// TestDecideKeepsTheTwoColumnsIndependent is the rule of section 4b that a
// health reading never corrects a state: a crew that reported wait-mate and
// then started typing again still reads wait-mate, and the busy pane shows
// up in the other column instead of overwriting it.
func TestDecideKeepsTheTwoColumnsIndependent(t *testing.T) {
	got := crewstate.Decide(crewstate.Input{
		Declaration: crewstate.Declaration{LastVerb: crewstate.VerbWaitMate},
		Observation: crewstate.Observation{
			AgentRecorded: true, AgentFound: true,
			Composer: crewstate.ComposerBusy, Evidence: "✻ Pollinating…",
		},
	})
	want := "state: wait-mate · health: busy (✻ Pollinating…)"
	if got.Line() != want {
		t.Fatalf("Line() = %q, want %q", got.Line(), want)
	}
}

func TestResultLine(t *testing.T) {
	r := crewstate.Result{
		State:  crewstate.StateNeedsDecision,
		Health: crewstate.Health{Kind: crewstate.HealthIdle, Detail: "composer empty"},
	}
	if want := "state: needs-decision · health: idle (composer empty)"; r.Line() != want {
		t.Fatalf("Line() = %q, want %q", r.Line(), want)
	}

	noDetail := crewstate.Result{State: crewstate.StateFinished, Health: crewstate.Health{Kind: crewstate.HealthNoAgent}}
	if want := "state: finished · health: no-agent"; noDetail.Line() != want {
		t.Fatalf("Line() = %q, want %q", noDetail.Line(), want)
	}
}
