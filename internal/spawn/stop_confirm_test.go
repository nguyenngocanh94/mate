package spawn_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// These tests pin the two false-positive branches of a confirmed stop: an
// empty `agent list` must not read as absence while `agent get` still finds
// the agent, nor while `agent get` fails. A confirmed StopMate is the
// precondition context refresh relies on before it freezes the Mate's
// transcript for harness.TranscriptParser.ParseTranscriptFinal, which flushes
// the last message group on the assumption that no writer remains.

// errHerdrDown is a transport failure: it is not agent_not_found, so it says
// nothing about whether the agent exists.
var errHerdrDown = errors.New("herdr: connection refused")

// inspectFailsAfter passes the first n InspectAgent calls through to the fake
// and fails every later one with errHerdrDown. It lets a stop see the agent,
// really stop it - so the inventory is empty - and then lose `agent get` at
// the confirmation, which a global Fake.InspectErr cannot express.
type inspectFailsAfter struct {
	*runtime.Fake
	n int
}

func (r *inspectFailsAfter) InspectAgent(ctx context.Context, handle runtime.AgentHandle) (runtime.ObservedAgent, error) {
	if r.n <= 0 {
		return runtime.ObservedAgent{}, errHerdrDown
	}
	r.n--
	return r.Fake.InspectAgent(ctx, handle)
}

func listKey(handle runtime.AgentHandle) string {
	return handle.Session.Name + "/" + handle.Name
}

// listOmitsLiveAgent hides a live agent from `agent list` and makes every
// stop report success without removing it: `agent get` still finds it.
func listOmitsLiveAgent(rt *runtime.Fake, handle runtime.AgentHandle) {
	rt.ListOmit = map[string]bool{listKey(handle): true}
	rt.StopLeavesAgent = true
}

func assertEmptyList(t *testing.T, rt *runtime.Fake, handle runtime.AgentHandle) {
	t.Helper()
	listed, err := rt.ListAgents(context.Background(), handle.Session)
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	for _, obs := range listed {
		if obs.Handle.Name == handle.Name {
			t.Fatalf("the inventory still lists %q; the scenario needs it absent", handle.Name)
		}
	}
}

func TestStopMateRefusesAnEmptyListWhileInspectFindsTheAgent(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	started, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	handle, _, err := spawn.MateHandle(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("MateHandle: %v", err)
	}
	listOmitsLiveAgent(rt, handle)

	_, err = spawn.StopMate(context.Background(), w, deps, "shop")
	if err == nil {
		t.Fatal("an empty inventory while `agent get` still finds the agent must not confirm a stop")
	}
	if !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("error = %v, want it to say the stop is not confirmed", err)
	}
	assertEmptyList(t, rt, handle)
	if _, err := rt.InspectAgent(context.Background(), handle); err != nil {
		t.Fatalf("the scenario needs `agent get` to still find the agent: %v", err)
	}
	assertMateNotReleased(t, w, rt, started)
}

func TestStopMateRefusesAnEmptyListWhileInspectFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		// passes is how many InspectAgent calls succeed before Herdr is
		// lost: 0 fails the liveness check that precedes the stop, 1 lets
		// the stop happen and fails its confirmation.
		passes int
	}{
		{name: "before the stop", passes: 0},
		{name: "at the confirmation", passes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorkspace(t, "shop")
			fake := runtime.NewFake()
			deps := fakeDeps(t, fake)
			started, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
			if err != nil {
				t.Fatalf("StartMate: %v", err)
			}
			handle, _, err := spawn.MateHandle(context.Background(), w, deps, "shop")
			if err != nil {
				t.Fatalf("MateHandle: %v", err)
			}
			if tc.passes == 0 {
				// The agent is already out of the inventory when the
				// stop begins.
				fake.ListOmit = map[string]bool{listKey(handle): true}
			}
			deps.Runtime = &inspectFailsAfter{Fake: fake, n: tc.passes}

			_, err = spawn.StopMate(context.Background(), w, deps, "shop")
			if err == nil {
				t.Fatal("an empty inventory while `agent get` fails must not confirm a stop")
			}
			if !errors.Is(err, errHerdrDown) {
				t.Fatalf("error = %v, want it to carry the inspect failure", err)
			}
			assertEmptyList(t, fake, handle)
			assertMateNotReleased(t, w, fake, started)
		})
	}
}

// assertMateNotReleased checks that a refused stop left the Mate's record and
// tab as they were: nothing downstream may treat it as stopped.
func assertMateNotReleased(t *testing.T, w *store.Workspace, rt *runtime.Fake, started spawn.StartResult) {
	t.Helper()
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatalf("ReadMateMeta: %v", err)
	}
	if meta[spawn.MetaAgent] != started.Agent || meta[spawn.MetaPane] != started.Pane {
		t.Fatalf("meta = %v, want the agent and pane kept after an unconfirmed stop", meta)
	}
	if meta[spawn.MetaStoppedAt] != "" {
		t.Fatalf("meta stopped_at = %q after an unconfirmed stop", meta[spawn.MetaStoppedAt])
	}
	if slices.Contains(rt.Calls, "RemoveTab") {
		t.Fatalf("calls %v: an unconfirmed stop must not close the tab", rt.Calls)
	}
}

func TestStopCrewRefusesAnEmptyListWhileInspectFindsTheAgent(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	handle, _, err := spawn.CrewHandle(context.Background(), w, deps, "shop", "k3")
	if err != nil {
		t.Fatalf("CrewHandle: %v", err)
	}
	listOmitsLiveAgent(rt, handle)

	_, err = spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false)
	if err == nil {
		t.Fatal("an empty inventory while `agent get` still finds the agent must not confirm a stop")
	}
	if !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("error = %v, want it to say the stop is not confirmed", err)
	}
	assertEmptyList(t, rt, handle)
	assertCrewNotTornDown(t, w, rt, res)
}

func TestStopCrewRefusesAnEmptyListWhileInspectFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		passes int
	}{
		{name: "before the stop", passes: 0},
		{name: "at the confirmation", passes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := crewWorkspace(t, "shop")
			fake := runtime.NewFake()
			deps := fakeDeps(t, fake)
			res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
				Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
			})
			if err != nil {
				t.Fatalf("SpawnCrew: %v", err)
			}
			handle, _, err := spawn.CrewHandle(context.Background(), w, deps, "shop", "k3")
			if err != nil {
				t.Fatalf("CrewHandle: %v", err)
			}
			if tc.passes == 0 {
				fake.ListOmit = map[string]bool{listKey(handle): true}
			}
			deps.Runtime = &inspectFailsAfter{Fake: fake, n: tc.passes}

			_, err = spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false)
			if err == nil {
				t.Fatal("an empty inventory while `agent get` fails must not confirm a stop")
			}
			if !errors.Is(err, errHerdrDown) {
				t.Fatalf("error = %v, want it to carry the inspect failure", err)
			}
			assertEmptyList(t, fake, handle)
			assertCrewNotTornDown(t, w, fake, res)
		})
	}
}

// assertCrewNotTornDown checks that a refused stop left the crew's record,
// tab and worktree in place.
func assertCrewNotTornDown(t *testing.T, w *store.Workspace, rt *runtime.Fake, res spawn.CrewResult) {
	t.Helper()
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	if meta[spawn.MetaAgent] != res.Agent || meta[spawn.MetaPane] != res.Pane {
		t.Fatalf("meta = %v, want the agent and pane kept after an unconfirmed stop", meta)
	}
	if meta[spawn.MetaTeardown] != "" || meta[spawn.MetaStoppedAt] != "" {
		t.Fatalf("meta = %v, want no teardown recorded after an unconfirmed stop", meta)
	}
	if slices.Contains(rt.Calls, "RemoveTab") {
		t.Fatalf("calls %v: an unconfirmed stop must not close the tab", rt.Calls)
	}
	if _, err := os.Stat(res.Worktree); err != nil {
		t.Fatalf("the worktree must survive an unconfirmed stop: %v", err)
	}
}
