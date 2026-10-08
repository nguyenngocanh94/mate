package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/watch"
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
	snap, err := query.Load(context.Background(), w, consoleHarnesses(), consoleTools())
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

// TestWithRuntimeNoticeCarriesHerdrDownIntoTheSnapshot: while Herdr cannot
// be reached the observer stands a notice the Console can draw. A snapshot
// straight out of query.Load cannot have one - query reads files only - so
// without this merge the tree on screen keeps looking fresh while nothing
// behind it can be re-read.
func TestWithRuntimeNoticeCarriesHerdrDownIntoTheSnapshot(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	down := observability.NewError(observability.CodeRuntimeUnavailable,
		"herdr is not running; start or resume the Mate with s to bring it back (session mate-shop)")
	watcher := watch.New(w, watch.Deps{
		Runtime: deps.Runtime,
		Handle:  consoleCrewHandle(w, deps),
		Session: func(context.Context) error { return down },
	})
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	snap, err := query.Load(context.Background(), w, consoleHarnesses(), consoleTools())
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if snap.Runtime.Notice != "" {
		t.Fatalf("Runtime straight out of query.Load = %+v, want empty: query reads files only", snap.Runtime)
	}
	snap = withRuntimeNotice(snap, watcher)
	if snap.Runtime.Notice == "" || snap.Runtime.At.IsZero() {
		t.Fatalf("Runtime = %+v, want the observer's notice", snap.Runtime)
	}
	if !strings.Contains(snap.Runtime.Notice, "herdr is not running") || !strings.Contains(snap.Runtime.Notice, "mate-shop") {
		t.Fatalf("Runtime notice = %q, want the failure and the session it names", snap.Runtime.Notice)
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
		{Project: "shop", Crew: "k3"}: {AgentPresent: true, Composer: send.StateBusy, QuietFor: 3 * time.Second, Source: "jev"},
		// A crew of another project with the same id must not leak across.
		{Project: "blog", Crew: "k9"}: {AgentPresent: false, Composer: send.StateUnknown},
	}

	out := withCrewHealth(snap, observed)
	k3 := out.Projects[0].Crews[0].Health
	if !k3.IsKnown() || k3.Value.Composer != query.ComposerBusy || k3.Value.QuietFor != 3*time.Second || k3.Value.Source != "jev" {
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

// withTokens (mvp.md M5 task 27) reads `.mate/mate.db` read-only and
// fills CrewNode.Tokens and MateNode.Tokens from the ledger; a workspace
// with no database yet leaves both Absent, exactly the shape withCrewHealth
// already has for Health.
func TestWithTokensFillsFromTheLedger(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude"}); err != nil {
		t.Fatalf("WriteMateMeta: %v", err)
	}
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{"state": "spawned"}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}

	snap, err := query.Load(context.Background(), w, consoleHarnesses(), consoleTools())
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if got := withTokens(snap, w); got.Projects[0].Crews[0].Tokens.State != query.Absent {
		t.Fatalf("Tokens before any database exists = %+v, want Absent", got.Projects[0].Crews[0].Tokens)
	}

	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer handle.Close()

	crewActor := timeline.CrewActorID("shop", "k3")
	mateActor := timeline.MateActorID("shop")
	seedActorTurnAndPricing(t, handle, crewActor, "shop", "crew", "test-model", 1000, 200, 0, 300, 400_000)
	seedActorTurnAndPricing(t, handle, mateActor, "shop", "mate", "test-model", 500, 0, 0, 50, 400_000)
	if _, err := handle.SQL().Exec(`UPDATE pricing SET input_per_m = 2, output_per_m = 4 WHERE model = 'test-model'`); err != nil {
		t.Fatalf("price the model: %v", err)
	}
	if _, err := handle.SQL().Exec(`UPDATE turn SET started_at = ? WHERE actor_id = ?`,
		db.FormatTime(time.Now()), mateActor); err != nil {
		t.Fatalf("date the mate's turn: %v", err)
	}

	snap, err = query.Load(context.Background(), w, consoleHarnesses(), consoleTools())
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	got := withTokens(snap, w)
	crew := got.Projects[0].Crews[0]
	if !crew.Tokens.IsKnown() {
		t.Fatalf("crew Tokens = %+v, want Known", crew.Tokens)
	}
	if crew.Tokens.Value.Total != 1500 {
		t.Fatalf("crew total tokens = %d, want 1500", crew.Tokens.Value.Total)
	}
	wantCost := (1000*2.0 + 300*4.0) / 1_000_000.0
	if crew.Tokens.Value.Cost == nil {
		t.Fatalf("crew cost = nil, want ~%v", wantCost)
	}
	if d := *crew.Tokens.Value.Cost - wantCost; d > 1e-9 || d < -1e-9 {
		t.Fatalf("crew cost = %v, want ~%v", *crew.Tokens.Value.Cost, wantCost)
	}
	if crew.Tokens.Value.ContextPct == nil {
		t.Fatalf("crew ContextPct = nil, want the seeded context window's percentage")
	}

	mate := got.Projects[0].Mate
	if !mate.Tokens.IsKnown() || mate.Tokens.Value.Total != 550 {
		t.Fatalf("mate Tokens = %+v, want Known with total 550", mate.Tokens)
	}
}

// seedActorTurnAndPricing writes just enough of the schema by hand - an
// actor, one turn, and a pricing row with the context window filled in but
// no price yet - to exercise crewTokens/mateTokens without a transcript
// fixture. Tests of the SQL the ingest itself writes live in
// internal/timeline; this is the console wiring on top of it.
func seedActorTurnAndPricing(t *testing.T, handle *db.DB, actorID, project, kind, model string,
	input, cacheRead, cacheWrite, output int64, contextWindow int64) {
	t.Helper()
	now := db.FormatTime(time.Now())
	// name is a plausible actor.name for the kind: `v_task_ledger.crew`
	// (and `spawn.ListCrews`) key a crew by its short id, never by the
	// `crew:<project>:<crew>` actor id, so a test that used the actor id as
	// the name here would silently fail every lookup keyed by crew id.
	name := actorID
	if kind == "crew" {
		name = strings.TrimPrefix(actorID, "crew:"+project+":")
	}
	if _, err := handle.SQL().Exec(
		`INSERT INTO actor(id, project, kind, name) VALUES (?, ?, ?, ?)`,
		actorID, project, kind, name); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	if _, err := handle.SQL().Exec(
		`INSERT INTO session(id, actor_id, started_at) VALUES (?, ?, ?)`,
		actorID+"#s", actorID, now); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := handle.SQL().Exec(
		`INSERT INTO turn(id, actor_id, session_id, started_at, ended_at, model, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, context_tokens_after)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		actorID+"#t", actorID, actorID+"#s", now, now, model, input, cacheRead, cacheWrite, output, input+cacheRead+cacheWrite); err != nil {
		t.Fatalf("seed turn: %v", err)
	}
	if _, err := handle.SQL().Exec(
		`INSERT INTO pricing(model, context_window) VALUES (?, ?)
		 ON CONFLICT(model) DO UPDATE SET context_window = excluded.context_window`,
		model, contextWindow); err != nil {
		t.Fatalf("seed pricing: %v", err)
	}
	if kind == "crew" {
		if _, err := handle.SQL().Exec(
			`INSERT INTO task(crew_actor_id, project, spawned_at) VALUES (?, ?, ?)`,
			actorID, project, now); err != nil {
			t.Fatalf("seed task: %v", err)
		}
	}
}
