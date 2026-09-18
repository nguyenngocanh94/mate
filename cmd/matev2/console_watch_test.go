package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/watch"
)

// spawnedHandle is the live Herdr handle of a crew the fake adapter just
// spawned. Only the session and the agent name identify a pane to the fake,
// which is the same pair spawn.CrewHandle resolves out of the meta.
func spawnedHandle(res spawn.CrewResult) runtime.AgentHandle {
	return runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: res.Session},
		Name:    res.Agent,
		RawID:   res.Crew,
		Kind:    res.Harness,
	}
}

// The console's observer wiring (mvp.md task 18): internal/watch over the
// real spawn.CrewHandle, and its readings put into the snapshot the Console
// draws. Nothing here stubs internal/watch - the point is that a crew
// spawned through the fake Herdr really is observed, and that the row the
// Console gets carries what was observed.

func TestConsoleWatcherObservesASpawnedCrew(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	rt := deps.Runtime.(*runtime.Fake)
	rt.SetReadOutput(spawnedHandle(res), codexEmptyScreen)

	watcher := watch.New(w, watch.Deps{
		Runtime: deps.Runtime,
		Handle:  consoleCrewHandle(w, deps),
	})
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	h, ok := watcher.Health("shop", "k3")
	if !ok {
		t.Fatal("the observer recorded no health for the spawned crew")
	}
	if !h.AgentPresent || h.Composer != send.StateEmpty {
		t.Fatalf("health = %+v, want a present agent with an empty composer", h)
	}

	// And the snapshot the Console draws carries it.
	snap, err := query.Load(context.Background(), w)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if got := snap.Projects[0].Crews[0].Health; got.State != query.Absent {
		t.Fatalf("health straight out of query.Load = %+v, want Absent: query reads files only", got)
	}
	snap = withCrewHealth(snap, watcher.Snapshot())
	crew := snap.Projects[0].Crews[0]
	if !crew.Health.IsKnown() {
		t.Fatalf("crew health = %+v, want the observation filled in", crew.Health)
	}
	if !crew.Health.Value.AgentPresent || crew.Health.Value.Composer != query.ComposerEmpty {
		t.Fatalf("crew health = %+v, want a present agent with an empty composer", crew.Health.Value)
	}
}

func TestConsoleWatcherOpensRuntimeLostForAKilledAgent(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	rt := deps.Runtime.(*runtime.Fake)
	rt.SetReadOutput(spawnedHandle(res), codexEmptyScreen)

	watcher := watch.New(w, watch.Deps{Runtime: deps.Runtime, Handle: consoleCrewHandle(w, deps)})
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	rt.DropAgent(spawnedHandle(res))
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	entries, _, err := w.ReadIncidents("shop", 0)
	if err != nil {
		t.Fatalf("ReadIncidents: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != "runtime_lost" || !entries[0].Open() {
		t.Fatalf("incidents = %+v, want one open runtime_lost", entries)
	}

	// The box the Console draws reads the same file, so the crew's row is
	// `blocked` and the item is in the inbox (mvp.md section 4b).
	box := query.LoadBox(w, "shop")
	if !box.IsKnown() {
		t.Fatalf("box = %+v, want it readable", box)
	}
	if box.Value.ToResolve() != 1 {
		t.Fatalf("inbox = %+v, want the incident in it", box.Value.Inbox)
	}
	if item := box.Value.Inbox[0]; item.Kind != query.BoxIncident || item.Verb != "runtime_lost" || item.Crew != "k3" {
		t.Fatalf("inbox item = %+v, want the k3 runtime_lost incident", item)
	}
	if !strings.Contains(box.Value.Inbox[0].Resolve, "runtime_lost") {
		t.Fatalf("resolve line = %q, want it to name the incident kind", box.Value.Inbox[0].Resolve)
	}
}

func TestWithCrewHealthLeavesUnobservedCrewsAlone(t *testing.T) {
	snap := query.Snapshot{Projects: []query.ProjectNode{{
		ProjectID: "shop",
		Crews: []query.CrewNode{
			{CrewID: "k3", Health: query.AbsentField[query.CrewHealth]("no observer has looked at this crew yet")},
			{CrewID: "k9", Health: query.AbsentField[query.CrewHealth]("no observer has looked at this crew yet")},
		},
	}}}
	observed := map[watch.CrewRef]watch.Health{
		{Project: "shop", Crew: "k3"}: {AgentPresent: true, Composer: send.StateBusy, QuietFor: 3 * time.Second},
		// A crew of another project with the same id must not leak across.
		{Project: "blog", Crew: "k9"}: {AgentPresent: false, Composer: send.StateUnknown},
	}

	out := withCrewHealth(snap, observed)
	k3 := out.Projects[0].Crews[0].Health
	if !k3.IsKnown() || k3.Value.Composer != query.ComposerBusy || k3.Value.QuietFor != 3*time.Second {
		t.Fatalf("k3 health = %+v, want the observation", k3)
	}
	if k9 := out.Projects[0].Crews[1].Health; k9.State != query.Absent {
		t.Fatalf("k9 health = %+v, want it left Absent", k9)
	}
}

func TestComposerDTOCarriesEveryMeasuredState(t *testing.T) {
	for state, want := range map[send.ComposerState]query.CrewComposer{
		send.StateEmpty:                     query.ComposerEmpty,
		send.StatePending:                   query.ComposerPending,
		send.StateBusy:                      query.ComposerBusy,
		send.StateUnknown:                   query.ComposerUnknown,
		send.ComposerState("something new"): query.ComposerUnknown,
	} {
		if got := composerDTO(state); got != want {
			t.Fatalf("composerDTO(%q) = %q, want %q", state, got, want)
		}
	}
}

// TestConsoleWatcherOpensItsOwnWorkspaceHandle: the observer polls on its own
// goroutine while query.Load re-reads workspace.yaml on the UI's, and a
// *store.Workspace caches that file. Sharing one handle would be a data race
// on the cache, so the wiring opens a second one over the same directory.
func TestConsoleWatcherOpensItsOwnWorkspaceHandle(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	watcher, err := consoleWatcher(w.Root(), deps)
	if err != nil {
		t.Fatalf("consoleWatcher: %v", err)
	}
	if watcher == nil {
		t.Fatal("consoleWatcher returned no watcher")
	}
	// It sees the same workspace: a crew spawned through the console's
	// handle is observed through the watcher's.
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	deps.Runtime.(*runtime.Fake).SetReadOutput(spawnedHandle(res), codexEmptyScreen)
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if _, ok := watcher.Health("shop", "k3"); !ok {
		t.Fatal("the watcher's own workspace handle does not see the crew")
	}

	if _, err := consoleWatcher(t.TempDir(), deps); err == nil {
		t.Fatal("consoleWatcher accepted a directory that is not a workspace")
	}
}
