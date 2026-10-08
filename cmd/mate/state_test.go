package main

import (
	"context"
	"errors"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// TestStateOfCrewGathersTheInputs exercises stateOfCrew - the CLI's
// gathering half of docs/mvp.md task 13, in the vocabulary of section 4b -
// end to end over a fake runtime. internal/crewstate/state_test.go covers
// Declare and Observe in isolation; these check that the CLI hands them the
// right inputs, and in particular that the two columns stay independent.
func TestStateOfCrewGathersTheInputs(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}

	t.Run("a freshly spawned crew that has written nothing is spawned", func(t *testing.T) {
		rt.SetReadOutput(handle, codexEmptyScreen)
		got, err := stateOfCrew(context.Background(), w, deps, "shop", "k3")
		if err != nil {
			t.Fatalf("stateOfCrew: %v", err)
		}
		want := crewstate.Result{
			State:  crewstate.StateSpawned,
			Health: crewstate.Health{Kind: crewstate.HealthIdle, Detail: "composer empty", Source: "fixture"},
		}
		if got != want {
			t.Fatalf("got = %+v, want %+v", got, want)
		}
	})

	t.Run("a busy pane is health, never a state", func(t *testing.T) {
		rt.SetReadOutput(handle, codexBusyScreen)
		if err := w.AppendStatus("shop", "k3", "needs-decision: pick a db"); err != nil {
			t.Fatal(err)
		}
		got, err := stateOfCrew(context.Background(), w, deps, "shop", "k3")
		if err != nil {
			t.Fatalf("stateOfCrew: %v", err)
		}
		// The crew asked and stopped its turn; that it is typing again does
		// not answer the question, so the state stays needs-decision and
		// the pane shows up in the other column (mvp.md section 4b).
		if got.State != crewstate.StateNeedsDecision {
			t.Fatalf("state = %q, want needs-decision: a pane reading never overrides the record", got.State)
		}
		if got.Health.Kind != crewstate.HealthBusy {
			t.Fatalf("health = %+v, want busy", got.Health)
		}
	})

	t.Run("an idle pane on an unanswered question", func(t *testing.T) {
		rt.SetReadOutput(handle, codexEmptyScreen)
		got, err := stateOfCrew(context.Background(), w, deps, "shop", "k3")
		if err != nil {
			t.Fatalf("stateOfCrew: %v", err)
		}
		if want := "state: needs-decision · health: idle (composer empty) · via fixture"; got.Line() != want {
			t.Fatalf("Line() = %q, want %q", got.Line(), want)
		}
	})

	t.Run("the legacy done verb reads as wait-mate", func(t *testing.T) {
		rt.SetReadOutput(handle, codexEmptyScreen)
		if err := w.AppendStatus("shop", "k3", "done: chose A"); err != nil {
			t.Fatal(err)
		}
		got, err := stateOfCrew(context.Background(), w, deps, "shop", "k3")
		if err != nil {
			t.Fatalf("stateOfCrew: %v", err)
		}
		if got.State != crewstate.StateWaitMate {
			t.Fatalf("state = %q, want wait-mate", got.State)
		}
	})

	t.Run("a closed crew reports its terminal state and a no-agent health", func(t *testing.T) {
		// stopFakeCrew passes --discard, which is the caller deciding the
		// work will not land: `failed`, not `finished` (mvp.md section 4b).
		if _, err := stopFakeCrew(t, w, deps, "shop", "k3"); err != nil {
			t.Fatalf("StopCrew: %v", err)
		}
		got, err := stateOfCrew(context.Background(), w, deps, "shop", "k3")
		if err != nil {
			t.Fatalf("stateOfCrew: %v", err)
		}
		if got.State != crewstate.StateFailed {
			t.Fatalf("state = %q, want failed", got.State)
		}
		if got.Health.Kind != crewstate.HealthNoAgent {
			t.Fatalf("health = %+v, want no-agent", got.Health)
		}
	})
}

// TestStateOfCrewWithNoMetaAtAll: "there is no such crew" is not a state.
// `unknown` left the vocabulary with mvp.md section 4b, so the command
// refuses rather than inventing a seventh answer.
func TestStateOfCrewWithNoMetaAtAll(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)

	_, err := stateOfCrew(context.Background(), w, deps, "shop", "ghost")
	if err == nil {
		t.Fatal("stateOfCrew on a crew that was never recorded must fail")
	}
	var oerr *observability.Error
	if !errors.As(err, &oerr) || oerr.Code != observability.CodeNotFound {
		t.Fatalf("err = %v, want a not-found refusal", err)
	}
}

// tokensSuffix (mvp.md M5 task 27): empty with no database at all, and
// " · tokens: … · ctx: …%" once the ledger has a row for the crew.
func TestTokensSuffix(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{"state": "spawned"}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	if got := tokensSuffix(w, "shop", "k3"); got != "" {
		t.Fatalf("tokensSuffix with no database = %q, want empty", got)
	}

	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer handle.Close()
	actorID := timeline.CrewActorID("shop", "k3")
	seedActorTurnAndPricing(t, handle, actorID, "shop", "crew", "test-model", 90_000, 6_000, 0, 300, 100_000)

	if got := tokensSuffix(w, "shop", "k3"); got != " · tokens: 96.3k · ctx: 96%" {
		t.Fatalf("tokensSuffix = %q, want tokens and ctx", got)
	}

	// A crew nothing has been recorded for yet has no row in the ledger.
	if got := tokensSuffix(w, "shop", "k9"); got != "" {
		t.Fatalf("tokensSuffix for an unrecorded crew = %q, want empty", got)
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
