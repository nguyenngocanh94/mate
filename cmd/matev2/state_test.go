package main

import (
	"context"
	"errors"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/crewstate"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
)

// TestStateOfCrewGathersTheFourInputs exercises stateOfCrew - the CLI's
// gathering half of docs/mvp.md task 13 - end to end over a fake runtime.
// internal/crewstate/state_test.go already covers Decide's full branch
// table in isolation; these check that the CLI hands it the right inputs.
func TestStateOfCrewGathersTheFourInputs(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}

	t.Run("busy pane overrides a stale needs-decision status line", func(t *testing.T) {
		rt.SetReadOutput(handle, codexBusyScreen)
		if err := w.AppendStatus("shop", "k3", "needs-decision: pick a db"); err != nil {
			t.Fatal(err)
		}
		got, err := stateOfCrew(context.Background(), w, deps, "shop", "k3")
		if err != nil {
			t.Fatalf("stateOfCrew: %v", err)
		}
		if got.State != crewstate.StateWorking || got.Source != crewstate.SourcePane {
			t.Fatalf("got = %+v, want working · pane", got)
		}
	})

	t.Run("idle pane with a needs-decision status is parked", func(t *testing.T) {
		rt.SetReadOutput(handle, codexEmptyScreen)
		got, err := stateOfCrew(context.Background(), w, deps, "shop", "k3")
		if err != nil {
			t.Fatalf("stateOfCrew: %v", err)
		}
		want := crewstate.Result{State: crewstate.StateParked, Source: crewstate.SourceStatusLog, Detail: "pick a db"}
		if got != want {
			t.Fatalf("got = %+v, want %+v", got, want)
		}
		if got.Line() != "state: parked · source: status-log · pick a db" {
			t.Fatalf("Line() = %q", got.Line())
		}
	})

	t.Run("done supersedes the parked status once the crew reports it", func(t *testing.T) {
		rt.SetReadOutput(handle, codexEmptyScreen)
		if err := w.AppendStatus("shop", "k3", "done: chose A"); err != nil {
			t.Fatal(err)
		}
		got, err := stateOfCrew(context.Background(), w, deps, "shop", "k3")
		if err != nil {
			t.Fatalf("stateOfCrew: %v", err)
		}
		want := crewstate.Result{State: crewstate.StateDone, Source: crewstate.SourceStatusLog, Detail: "chose A"}
		if got != want {
			t.Fatalf("got = %+v, want %+v", got, want)
		}
	})

	t.Run("a stopped crew reports stopped from meta alone", func(t *testing.T) {
		if _, err := stopFakeCrew(t, w, deps, "shop", "k3"); err != nil {
			t.Fatalf("StopCrew: %v", err)
		}
		got, err := stateOfCrew(context.Background(), w, deps, "shop", "k3")
		if err != nil {
			t.Fatalf("stateOfCrew: %v", err)
		}
		if got.State != crewstate.StateStopped || got.Source != crewstate.SourceMeta {
			t.Fatalf("got = %+v, want stopped · meta", got)
		}
	})
}

// TestStateOfCrewWithNoMetaAtAll is the "no crew has ever been recorded"
// branch: an empty meta file, never a live agent.
func TestStateOfCrewWithNoMetaAtAll(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)

	got, err := stateOfCrew(context.Background(), w, deps, "shop", "ghost")
	if err != nil {
		t.Fatalf("stateOfCrew: %v", err)
	}
	if got.State != crewstate.StateUnknown || got.Source != crewstate.SourceNone {
		t.Fatalf("got = %+v, want unknown · none", got)
	}
}

func TestCrewStateUsageErrors(t *testing.T) {
	var ue *usageError
	if err := cmdState(nil, nil, discard{}); !errors.As(err, &ue) {
		t.Fatalf("cmdState with no args: err = %v, want *usageError", err)
	}
	if err := cmdState([]string{"shop"}, nil, discard{}); !errors.As(err, &ue) {
		t.Fatalf("cmdState with 1 arg: err = %v, want *usageError", err)
	}
}

// discard is a minimal io.Writer that keeps these tests from needing
// bytes.Buffer just to satisfy the stderr parameter.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
