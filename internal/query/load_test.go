package query

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

func newWorkspace(t *testing.T, projects ...string) *store.Workspace {
	t.Helper()
	root := t.TempDir()
	ws, err := store.Init(root)
	if err != nil {
		t.Fatalf("init workspace: %v", err)
	}
	for _, name := range projects {
		if err := os.MkdirAll(filepath.Join(root, name, ".git"), 0o755); err != nil {
			t.Fatalf("create repo %s: %v", name, err)
		}
		if err := ws.AddProject(name, store.ProjectConfig{Repo: name}); err != nil {
			t.Fatalf("add project %s: %v", name, err)
		}
	}
	return ws
}

func TestLoadListsRegisteredProjectsWithTheirRepo(t *testing.T) {
	ws := newWorkspace(t, "shop", "blog")
	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(snap.Projects) != 2 {
		t.Fatalf("projects = %+v, want two", snap.Projects)
	}
	if snap.Workspace.State != Known || snap.Workspace.Value.Root != ws.Root() {
		t.Fatalf("workspace field = %+v, want the store's own resolved root", snap.Workspace)
	}
	if snap.AsOf.IsZero() {
		t.Fatal("AsOf is zero; a snapshot must say how old it is")
	}
	p := snap.Projects[0]
	if p.ProjectID != "shop" || p.Name != "shop" {
		t.Fatalf("first project = %+v, want shop", p)
	}
	if p.Repos.State != Known || len(p.Repos.Value) != 1 {
		t.Fatalf("repos = %+v, want the one registered repo", p.Repos)
	}
	if got, want := p.Repos.Value[0].Path, filepath.Join(ws.Root(), "shop"); got != want {
		t.Fatalf("repo path = %q, want %q", got, want)
	}
	if got := p.Repos.Value[0].DefaultBranch; got != store.DefaultBranch {
		t.Fatalf("default branch = %q, want %q", got, store.DefaultBranch)
	}
}

// TestLoadReportsNoMateAsAbsentNotUnknown: a project whose mate.meta does
// not exist has no Mate, which the read established. That is Absent - the
// state the Console renders as "none assigned" - and never Unknown, which
// would claim the read failed.
func TestLoadReportsNoMateAsAbsentNotUnknown(t *testing.T) {
	ws := newWorkspace(t, "shop")
	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	mate := snap.Projects[0].Mate
	if mate.Designated.State != Absent {
		t.Fatalf("designation = %+v, want Absent for a project with no mate.meta", mate.Designated)
	}
	if mate.Designated.Reason == "" {
		t.Fatal("an Absent designation must carry the reason there is no Mate")
	}
	if len(snap.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none: nothing failed to read", snap.Warnings)
	}
	if a := snap.Projects[0].Attention; a.State != Known || a.Value.Kind != AttentionNoMate {
		t.Fatalf("project attention = %+v, want the no-mate kind", a)
	}
}

// TestLoadReadsMateMetaAndCrewStatus is the shape mvp.md section 3
// describes: a Mate row exists because mate.meta exists, its recorded pane
// is what makes it running, and a Crew's status is the last line of its
// own .status file.
func TestLoadReadsMateMetaAndCrewStatus(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteMateMeta("shop", map[string]string{
		"harness": "claude", "session": "fm-x", "pane": "p-1", "agent": "mate-shop",
	}); err != nil {
		t.Fatalf("write mate.meta: %v", err)
	}
	if err := ws.WriteCrewMeta("shop", "k3", map[string]string{
		"task": "wire the webhook", "harness": "codex",
		"worktree": "/w/shop-k3", "branch": "matev2/k3",
	}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	for _, line := range []string{"working: reading the brief", "needs-decision: which adapter?"} {
		if err := ws.AppendStatus("shop", "k3", line); err != nil {
			t.Fatalf("append status: %v", err)
		}
	}

	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	if p.Mate.Designated.State != Known {
		t.Fatalf("designation = %+v, want Known once mate.meta exists", p.Mate.Designated)
	}
	if got := p.Mate.Designated.Value.Status; got != MateRunning {
		t.Fatalf("mate status = %q, want running for a recorded pane", got)
	}
	if got := p.Mate.Designated.Value.HarnessKind; got != HarnessClaude {
		t.Fatalf("mate harness = %q, want claude", got)
	}
	if p.Mate.Binding.State != Known || p.Mate.Binding.Reason == "" {
		t.Fatalf("binding = %+v, want Known with the caveat that it proves nothing about the agent", p.Mate.Binding)
	}

	if len(p.Crews) != 1 {
		t.Fatalf("crews = %+v, want the one crew meta", p.Crews)
	}
	c := p.Crews[0]
	if c.CrewID != "k3" || c.Task != "wire the webhook" || c.HarnessKind != HarnessCodex {
		t.Fatalf("crew = %+v, want k3 from its meta", c)
	}
	if got := c.Status; got != CrewStatus("needs-decision") {
		t.Fatalf("crew status = %q, want the last status line's state word", got)
	}
	if c.Worktree.State != Known || c.Worktree.Value.Branch != "matev2/k3" {
		t.Fatalf("worktree = %+v, want the recorded worktree and branch", c.Worktree)
	}
}

// TestLoadGivesACrewThatWroteNothingTheSpawnedState: the meta is what
// records the crew, so a crew with no status line yet is `spawned` - the
// last rule of mvp.md section 4b's resolution order, and not blank.
func TestLoadGivesACrewThatWroteNothingTheSpawnedState(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k9", map[string]string{"task": "scout", "state": "spawned"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := snap.Projects[0].Crews[0].Status; got != CrewSpawned {
		t.Fatalf("status = %q, want %q", got, CrewSpawned)
	}
}

// TestLoadResolvesEachCrewStateInTheOrderOfSection4b walks the whole table
// on real files: the meta first, then an open incident, then the crew's own
// last verb, then `spawned`. The incident case is the one the observer
// (task 18) will fill in for real; until then box.Load reads no
// incidents.log, so this drives it through the box view the same way Load
// does and asserts the rule, not the producer.
func TestLoadResolvesEachCrewStateInTheOrderOfSection4b(t *testing.T) {
	ws := newWorkspace(t, "shop")
	write := func(crew string, meta map[string]string, status ...string) {
		t.Helper()
		if err := ws.WriteCrewMeta("shop", crew, meta); err != nil {
			t.Fatalf("write crew meta %s: %v", crew, err)
		}
		for _, line := range status {
			if err := ws.AppendStatus("shop", crew, line); err != nil {
				t.Fatalf("append status %s: %v", crew, err)
			}
		}
	}
	// A crew's own last verb, including the legacy spellings.
	write("k1", map[string]string{"state": "spawned"}, "working: reading the ticket")
	write("k2", map[string]string{"state": "spawned"}, "working: a", "needs-decision: A or B?")
	write("k3", map[string]string{"state": "spawned"}, "wait-mate: ready in branch matev2/k3")
	write("k4", map[string]string{"state": "spawned"}, "done: ready in branch matev2/k4")
	// Nothing written at all.
	write("k5", map[string]string{"state": "spawned"})
	// The meta's terminal state outranks whatever the crew last said.
	write("k6", map[string]string{"state": "finished", "stopped_at": "2026-09-18T10:00:00Z"}, "working: mid-turn when it was closed")
	write("k7", map[string]string{"state": "failed", "failed_reason": "startup screen not recognised"})
	// Backward compatibility: stopped_at with no state= is finished.
	write("k8", map[string]string{"stopped_at": "2026-09-17T10:00:00Z"})

	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	got := map[string]CrewStatus{}
	for _, c := range p.Crews {
		got[c.CrewID] = c.Status
	}
	want := map[string]CrewStatus{
		"k1": CrewWorking,
		"k2": CrewNeedsDecision,
		"k3": CrewWaitMate,
		"k4": CrewWaitMate,
		"k5": CrewSpawned,
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("crew %s = %q, want %q", id, got[id], w)
		}
	}
	// k6, k7 and k8 are closed and therefore not rows at all.
	for _, id := range []string{"k6", "k7", "k8"} {
		if _, ok := got[id]; ok {
			t.Errorf("crew %s is closed and must not be a row", id)
		}
	}
	if p.ClosedCrews != 3 {
		t.Errorf("ClosedCrews = %d, want 3", p.ClosedCrews)
	}
}

// TestLoadReadsBlockedFromTheObserversOpenIncidents drives the `blocked`
// rule end to end over real files: the observer (internal/watch) appends to
// `incidents.log`, box.Load merges it, and Load resolves the state from
// there. Nothing is hand-built; the rule under test is the resolution.
//
// k1 has an open incident and is blocked even though it last said
// `working:`; k2's incident was resolved, so it is back to its own verb;
// k3 has none.
func TestLoadReadsBlockedFromTheObserversOpenIncidents(t *testing.T) {
	ws := newWorkspace(t, "shop")
	for _, id := range []string{"k1", "k2", "k3"} {
		if err := ws.WriteCrewMeta("shop", id, map[string]string{"state": "spawned"}); err != nil {
			t.Fatalf("write crew meta %s: %v", id, err)
		}
		if err := ws.AppendStatus("shop", id, "working: reading the ticket"); err != nil {
			t.Fatalf("append status %s: %v", id, err)
		}
	}
	for _, inc := range []store.IncidentEntry{
		{Time: time.Now().Add(-time.Hour), Crew: "k1", Kind: "stale", State: store.IncidentOpen, Text: "no status for 20m"},
		{Time: time.Now().Add(-time.Hour), Crew: "k2", Kind: "runtime_lost", State: store.IncidentOpen, Text: "the agent left herdr"},
		{Time: time.Now(), Crew: "k2", Kind: "runtime_lost", State: store.IncidentResolved, Text: "the agent is back"},
	} {
		if err := ws.AppendIncident("shop", inc); err != nil {
			t.Fatalf("AppendIncident: %v", err)
		}
	}

	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	if p.ClosedCrews != 0 {
		t.Fatalf("ClosedCrews = %d, want 0: an incident never closes a crew", p.ClosedCrews)
	}
	got := map[string]CrewStatus{}
	attention := map[string]Field[Attention]{}
	for _, c := range p.Crews {
		got[c.CrewID] = c.Status
		attention[c.CrewID] = c.Attention
	}
	want := map[string]CrewStatus{"k1": CrewBlocked, "k2": CrewWorking, "k3": CrewWorking}
	for id, wantState := range want {
		if got[id] != wantState {
			t.Errorf("crew %s = %q, want %q", id, got[id], wantState)
		}
	}
	// And `blocked` is attention, with a sentence naming the observer.
	if a := attention["k1"]; a.State != Known || a.Value.Kind != AttentionBlocked {
		t.Fatalf("k1 attention = %+v, want a Known blocked attention", a)
	}
	if a := attention["k3"]; a.State == Known {
		t.Fatalf("k3 attention = %+v, want none: it is simply working", a)
	}
}

// Load reads only `.matev2/`'s flat files; token usage lives in the derived
// `.matev2/matev2.db` (mvp.md M5 task 27), so both the Mate and a Crew must
// come back with Tokens Absent, exactly the way Health does - the Console's
// wiring fills it in afterwards, never this package.
func TestLoadLeavesTokensAbsent(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteMateMeta("shop", map[string]string{"harness": "claude"}); err != nil {
		t.Fatalf("write mate meta: %v", err)
	}
	if err := ws.WriteCrewMeta("shop", "k3", map[string]string{"state": "spawned"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}

	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	if p.Mate.Tokens.State != Absent {
		t.Fatalf("Mate.Tokens.State = %v, want Absent", p.Mate.Tokens.State)
	}
	if len(p.Crews) != 1 || p.Crews[0].Tokens.State != Absent {
		t.Fatalf("Crew.Tokens.State = %+v, want Absent", p.Crews)
	}
}

// TestLoadDoesNotBlockOnABudgetIncident: decision 2026-09-20 (mvp.md section
// 4b) - a `budget` incident is real and stays in the box, but it must not
// displace a crew's own declared state the way `stale`/`runtime_lost` do.
func TestLoadDoesNotBlockOnABudgetIncident(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k9", map[string]string{"state": "spawned"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	if err := ws.AppendStatus("shop", "k9", "working: burning through the task"); err != nil {
		t.Fatalf("append status: %v", err)
	}
	if err := ws.AppendIncident("shop", store.IncidentEntry{
		Time: time.Now(), Crew: "k9", Kind: "budget", State: store.IncidentOpen,
		Text: "620,000 tokens of 500,000 tokens",
	}); err != nil {
		t.Fatalf("AppendIncident: %v", err)
	}

	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var got CrewStatus
	for _, c := range snap.Projects[0].Crews {
		if c.CrewID == "k9" {
			got = c.Status
		}
	}
	if got != CrewWorking {
		t.Fatalf("k9 status = %q with only a budget incident open, want %q (never blocked)", got, CrewWorking)
	}
}

// TestCrewStateOfBlocksOnAnOpenIncident: `blocked` is the observer's state
// and the only way in is an unresolved incident, which outranks the crew's
// own last verb but never a terminal meta state (mvp.md section 4b).
func TestCrewStateOfBlocksOnAnOpenIncident(t *testing.T) {
	spawned := map[string]string{"state": "spawned"}
	if got := CrewStateOf(spawned, true, "working"); got != CrewBlocked {
		t.Errorf("open incident over working = %q, want %q", got, CrewBlocked)
	}
	if got := CrewStateOf(spawned, false, "working"); got != CrewWorking {
		t.Errorf("no incident = %q, want %q", got, CrewWorking)
	}
	closed := map[string]string{"state": "finished", "stopped_at": "2026-09-18T10:00:00Z"}
	if got := CrewStateOf(closed, true, "working"); got != CrewFinished {
		t.Errorf("open incident over a closed crew = %q, want %q", got, CrewFinished)
	}
}

// TestLoadPicksUpAProjectRegisteredAfterOpen: the Console's 'r' is meant to
// show a project another process just added, so every load re-reads
// workspace.yaml rather than answering from the config cached at Open.
func TestLoadPicksUpAProjectRegisteredAfterOpen(t *testing.T) {
	ws := newWorkspace(t, "shop")
	other, err := store.Open(ws.Root())
	if err != nil {
		t.Fatalf("open workspace again: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ws.Root(), "blog", ".git"), 0o755); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := other.AddProject("blog", store.ProjectConfig{Repo: "blog"}); err != nil {
		t.Fatalf("add project: %v", err)
	}
	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(snap.Projects) != 2 {
		t.Fatalf("projects = %+v, want the newly registered one too", snap.Projects)
	}
}

// TestLoadHidesClosedCrewsAndCountsThem: closing is the decision that ends
// a task and it is `matev2 crew stop`'s `state=finished|failed`, not the
// crew's own report (2026-09-18). A crew that said `wait-mate` is still a
// row; a closed one is not, and ClosedCrews says how many were dropped.
func TestLoadHidesClosedCrewsAndCountsThem(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k1", map[string]string{
		"task": "ship", "agent": "crew-k1", "pane": "w1:p2", "state": "spawned"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	if err := ws.AppendStatus("shop", "k1", "wait-mate: ready in branch matev2/k1"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := ws.WriteCrewMeta("shop", "k9", map[string]string{
		"task": "scout", "state": "finished", "stopped_at": "2026-09-18T10:18:34Z", "teardown": "clean"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	if len(p.Crews) != 1 || p.Crews[0].CrewID != "k1" {
		t.Fatalf("crews = %+v, want only the open k1", p.Crews)
	}
	if p.Crews[0].Status != CrewWaitMate || p.Crews[0].Closed {
		t.Fatalf("k1 = status %q closed %v, want wait-mate and open", p.Crews[0].Status, p.Crews[0].Closed)
	}
	if p.ClosedCrews != 1 {
		t.Fatalf("closed = %d, want 1", p.ClosedCrews)
	}
}

// TestLoadFillsUpdatedFromTheBox: the UPDATED column is the newest box
// entry for the row - a crew's last status line or message, the Mate's
// last typed or sent line - and Absent with a reason when there is none
// (2026-09-19: the column was blank for every row).
func TestLoadFillsUpdatedFromTheBox(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k1", map[string]string{"task": "ship", "agent": "crew-k1", "pane": "w1:p2", "state": "spawned"}); err != nil {
		t.Fatal(err)
	}
	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := snap.Projects[0].Crews[0].LastEvent; got.State != Absent {
		t.Fatalf("a silent crew's last event = %+v, want Absent", got)
	}
	if err := ws.AppendStatus("shop", "k1", "working: reading the brief"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 19, 3, 30, 0, 0, time.UTC)
	if err := ws.AppendSent("shop", store.SentEntry{Time: at, Source: "user", Target: "mate", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if snap, err = Load(context.Background(), ws); err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	if got := p.Crews[0].LastEvent; got.State != Known || got.Value.EventType != "status" || got.Value.OccurredAt.IsZero() {
		t.Fatalf("crew last event = %+v, want the status line's time", got)
	}
	if got := p.Mate.LastEvent; got.State != Known || !got.Value.OccurredAt.Equal(at) {
		t.Fatalf("mate last event = %+v, want the line typed at %s", got, at)
	}
}
