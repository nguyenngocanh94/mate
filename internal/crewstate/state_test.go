package crewstate_test

import (
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/crewstate"
	"github.com/nguyenngocanh94/matev2/internal/send"
)

func TestDecide(t *testing.T) {
	tests := []struct {
		name string
		in   crewstate.Input
		want crewstate.Result
	}{
		{
			name: "no meta at all",
			in:   crewstate.Input{},
			want: crewstate.Result{State: crewstate.StateUnknown, Source: crewstate.SourceNone, Detail: "no crew metadata"},
		},
		{
			name: "meta without an agent is a stopped crew",
			in: crewstate.Input{
				Meta: map[string]string{"harness": "codex", "stopped_at": "2026-09-17T10:00:00Z"},
			},
			want: crewstate.Result{State: crewstate.StateStopped, Source: crewstate.SourceMeta, Detail: "stopped_at=2026-09-17T10:00:00Z"},
		},
		{
			name: "meta without an agent and no stopped_at recorded",
			in:   crewstate.Input{Meta: map[string]string{"harness": "codex"}},
			want: crewstate.Result{State: crewstate.StateStopped, Source: crewstate.SourceMeta, Detail: "stopped_at="},
		},
		{
			name: "herdr cannot find the recorded agent",
			in: crewstate.Input{
				Meta:       map[string]string{"agent": "crew-k3"},
				AgentFound: false,
			},
			want: crewstate.Result{State: crewstate.StateUnknown, Source: crewstate.SourceMeta, Detail: "agent recorded but absent"},
		},
		{
			name: "busy pane wins over a stale needs-decision status line",
			in: crewstate.Input{
				Meta:       map[string]string{"agent": "crew-k3"},
				AgentFound: true,
				Composer:   send.Classification{State: send.StateBusy, Evidence: "✻ Pollinating…"},
				Status:     crewstate.Status{Verb: crewstate.VerbNeedsDecision, Text: "pick a db"},
			},
			want: crewstate.Result{State: crewstate.StateWorking, Source: crewstate.SourcePane, Detail: "✻ Pollinating…"},
		},
		{
			name: "last status done",
			in: crewstate.Input{
				Meta:       map[string]string{"agent": "crew-k3"},
				AgentFound: true,
				Composer:   send.Classification{State: send.StateEmpty},
				Status:     crewstate.Status{Verb: crewstate.VerbDone, Text: "ready in branch"},
			},
			want: crewstate.Result{State: crewstate.StateDone, Source: crewstate.SourceStatusLog, Detail: "ready in branch"},
		},
		{
			name: "last status failed",
			in: crewstate.Input{
				Meta:       map[string]string{"agent": "crew-k3"},
				AgentFound: true,
				Composer:   send.Classification{State: send.StateEmpty},
				Status:     crewstate.Status{Verb: crewstate.VerbFailed, Text: "tests red"},
			},
			want: crewstate.Result{State: crewstate.StateFailed, Source: crewstate.SourceStatusLog, Detail: "tests red"},
		},
		{
			name: "needs-decision with an idle pane is parked",
			in: crewstate.Input{
				Meta:       map[string]string{"agent": "crew-k3"},
				AgentFound: true,
				Composer:   send.Classification{State: send.StateEmpty},
				Status:     crewstate.Status{Verb: crewstate.VerbNeedsDecision, Text: "pick a db"},
			},
			want: crewstate.Result{State: crewstate.StateParked, Source: crewstate.SourceStatusLog, Detail: "pick a db"},
		},
		{
			name: "blocked with an idle pane is parked",
			in: crewstate.Input{
				Meta:       map[string]string{"agent": "crew-k3"},
				AgentFound: true,
				Composer:   send.Classification{State: send.StateEmpty},
				Status:     crewstate.Status{Verb: crewstate.VerbBlocked, Text: "waiting on ci"},
			},
			want: crewstate.Result{State: crewstate.StateParked, Source: crewstate.SourceStatusLog, Detail: "waiting on ci"},
		},
		{
			name: "no attention-worthy status and an idle composer is working",
			in: crewstate.Input{
				Meta:       map[string]string{"agent": "crew-k3"},
				AgentFound: true,
				Composer:   send.Classification{State: send.StateEmpty},
				Status:     crewstate.Status{Verb: crewstate.VerbWorking, Text: "reading the ticket"},
			},
			want: crewstate.Result{State: crewstate.StateWorking, Source: crewstate.SourcePane, Detail: "idle composer"},
		},
		{
			name: "no status line yet and an idle composer is working",
			in: crewstate.Input{
				Meta:       map[string]string{"agent": "crew-k3"},
				AgentFound: true,
				Composer:   send.Classification{State: send.StateEmpty},
			},
			want: crewstate.Result{State: crewstate.StateWorking, Source: crewstate.SourcePane, Detail: "idle composer"},
		},
		{
			name: "a screen with no recognised composer is unknown",
			in: crewstate.Input{
				Meta:       map[string]string{"agent": "crew-k3"},
				AgentFound: true,
				Composer:   send.Classification{State: send.StateUnknown, Evidence: "harness directory-trust dialog"},
			},
			want: crewstate.Result{State: crewstate.StateUnknown, Source: crewstate.SourcePane, Detail: "harness directory-trust dialog"},
		},
		{
			name: "a pending composer (human mid-typing) is unknown, not working",
			in: crewstate.Input{
				Meta:       map[string]string{"agent": "crew-k3"},
				AgentFound: true,
				Composer:   send.Classification{State: send.StatePending, Evidence: "› half typed", Pending: "half typed"},
			},
			want: crewstate.Result{State: crewstate.StateUnknown, Source: crewstate.SourcePane, Detail: "› half typed"},
		},
		{
			name: "unrecognised composer with no evidence at all still gets a detail",
			in: crewstate.Input{
				Meta:       map[string]string{"agent": "crew-k3"},
				AgentFound: true,
				Composer:   send.Classification{State: send.StateUnknown},
			},
			want: crewstate.Result{State: crewstate.StateUnknown, Source: crewstate.SourcePane, Detail: "no composer recognised on screen"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := crewstate.Decide(tc.in)
			if got != tc.want {
				t.Fatalf("Decide(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestResultLine(t *testing.T) {
	r := crewstate.Result{State: crewstate.StateParked, Source: crewstate.SourceStatusLog, Detail: "pick a db"}
	want := "state: parked · source: status-log · pick a db"
	if got := r.Line(); got != want {
		t.Fatalf("Line() = %q, want %q", got, want)
	}

	noDetail := crewstate.Result{State: crewstate.StateUnknown, Source: crewstate.SourceNone}
	if got := noDetail.Line(); got != "state: unknown · source: none" {
		t.Fatalf("Line() with no detail = %q", got)
	}
}
