package query

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/application"
	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/git"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/persistence"
	"github.com/nguyenngocanh94/matev2/internal/workspace"
)

// errInjectedReadFailure is what failingReadsStore returns for a read this
// test deliberately breaks; LoadSnapshot never inspects the error
// value, only that ListMates/GetWorktree/ListBindings/ListEvents failed, so
// any non-nil error exercises the same path a real driver failure would.
var errInjectedReadFailure = errors.New("injected read failure")

// failingReadsStore wraps a real StateStore (the embed-and-override pattern
// internal/application/ask_ownership_test.go already uses) and forces one
// named read to fail, so the Unknown branch of each per-row field
// (the Mate designation, worktree, binding, last event, error reason and
// repo list) has a test that actually reaches it. Without this, the
// FieldState contract is enforced
// only by doc comment: a mutation flipping Unknown to Absent, or the far
// worse mutation flipping it to Known (a failed read rendered as fact), can
// survive the whole suite - see TestLoadSnapshot*Unknown* below, each
// of which is written to catch exactly one such mutation.
type failingReadsStore struct {
	application.StateStore
	failListMates         bool
	failGetWorktree       bool
	failListBindings      bool
	failListEvents        bool
	failListRepos         bool
	failListMergeRequests bool
}

func (s *failingReadsStore) ListRepos(ctx context.Context, projectID string) ([]persistence.RepoRecord, error) {
	if s.failListRepos {
		return nil, errInjectedReadFailure
	}
	return s.StateStore.ListRepos(ctx, projectID)
}

func (s *failingReadsStore) ListMates(ctx context.Context, projectID string) ([]persistence.MateRecord, error) {
	if s.failListMates {
		return nil, errInjectedReadFailure
	}
	return s.StateStore.ListMates(ctx, projectID)
}

func (s *failingReadsStore) GetWorktree(ctx context.Context, crewID string) (persistence.WorktreeRecord, error) {
	if s.failGetWorktree {
		return persistence.WorktreeRecord{}, errInjectedReadFailure
	}
	return s.StateStore.GetWorktree(ctx, crewID)
}

func (s *failingReadsStore) ListBindings(ctx context.Context, f persistence.BindingFilter) ([]persistence.BindingRecord, error) {
	if s.failListBindings {
		return nil, errInjectedReadFailure
	}
	return s.StateStore.ListBindings(ctx, f)
}

func (s *failingReadsStore) ListEvents(ctx context.Context, f persistence.EventFilter) ([]observability.Event, error) {
	if s.failListEvents {
		return nil, errInjectedReadFailure
	}
	return s.StateStore.ListEvents(ctx, f)
}

func (s *failingReadsStore) ListMergeRequests(ctx context.Context, crewID string) ([]persistence.MergeRequestRecord, error) {
	if s.failListMergeRequests {
		return nil, errInjectedReadFailure
	}
	return s.StateStore.ListMergeRequests(ctx, crewID)
}

var testNow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func userCaller() application.Caller { return application.Caller{Role: domain.RoleUser} }

type fixture struct {
	deps    application.Deps
	store   *persistence.Store
	project application.ProjectSummary
	repoID  string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	layout, err := workspace.Bootstrap(dir)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	store, err := persistence.Open(context.Background(), layout.Paths.DBFile, persistence.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	deps := application.Deps{Store: store, Clock: domain.NewFakeClock(testNow), IDs: &domain.FakeIDGenerator{}}
	init, err := application.InitializeWorkspace(context.Background(), deps, layout.Root, userCaller())
	if err != nil {
		t.Fatalf("InitializeWorkspace: %v", err)
	}
	proj, err := application.CreateProject(context.Background(), deps, userCaller(), "default")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	root, err := store.WorkspaceRoot()
	if err != nil {
		t.Fatalf("WorkspaceRoot: %v", err)
	}
	repoID := "repo_1"
	const mateID = "mate_1"
	err = store.Write(context.Background(), deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.InsertRepo(persistence.RepoRecord{
			RepoID: repoID, WorkspaceID: init.Workspace.WorkspaceID, ProjectID: proj.Project.ProjectID,
			Path: root.String() + "/repos/sample", DisplayName: "sample",
			DefaultBranch: "main", ValidationStatus: persistence.RepoValidated,
		}); err != nil {
			return err
		}
		if err := tx.InsertMate(persistence.MateRecord{
			MateID: mateID, WorkspaceID: init.Workspace.WorkspaceID, ProjectID: proj.Project.ProjectID,
			HarnessKind: domain.HarnessClaude, Status: domain.MateCreated,
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, deps, tx, init.Workspace.WorkspaceID, proj.Project.ProjectID)
	})
	if err != nil {
		t.Fatalf("seed repo and mate: %v", err)
	}
	return fixture{deps: deps, store: store, project: proj.Project, repoID: repoID}
}

// appendFixtureEvent satisfies Write's audit-event-per-mutation contract
// when a test seeds state directly through persistence rather than through
// a use case that already appends its own event.
func appendFixtureEvent(t *testing.T, deps application.Deps, tx persistence.Tx, workspaceID, projectID string) error {
	t.Helper()
	ev := observability.NewEvent(observability.EventProjectCreated, deps.Clock.Now())
	id, err := deps.IDs.New("evt")
	if err != nil {
		return err
	}
	ev.EventID = id
	ev.WorkspaceID, ev.ProjectID = workspaceID, projectID
	return tx.AppendEvent(ev)
}

// findCrew locates the loaded tree's single Project/Task/Crew triple this
// package's fixtures always produce, failing loudly if the shape assumed by
// a test's seeding does not hold.
func findCrew(t *testing.T, tree Snapshot) (ProjectNode, TaskNode, CrewNode) {
	t.Helper()
	if len(tree.Projects) != 1 {
		t.Fatalf("projects = %d, want 1: %+v", len(tree.Projects), tree.Projects)
	}
	p := tree.Projects[0]
	if len(p.Tasks) != 1 {
		t.Fatalf("tasks = %d, want 1: %+v", len(p.Tasks), p.Tasks)
	}
	task := p.Tasks[0]
	if len(task.Crews) != 1 {
		t.Fatalf("crews = %d, want 1: %+v", len(task.Crews), task.Crews)
	}
	return p, task, task.Crews[0]
}

// G6-A happy path: a spawned Crew attempt renders every DTO level with real
// data from the four navigation levels, sourced entirely through
// application's use cases (ReserveCrewAttempt, ListProjects, ListTasks,
// ListCrews) - never through a fresh SQLite or Herdr call in this package.
func TestLoadSnapshotHealthyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "do the thing", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}

	tree, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}

	ws, err := f.store.GetWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tree.WorkspaceID != ws.WorkspaceID {
		t.Fatalf("workspace id = %q, want %q", tree.WorkspaceID, ws.WorkspaceID)
	}
	proj, task, crew := findCrew(t, tree)
	if proj.ProjectID != f.project.ProjectID {
		t.Fatalf("project node = %+v", proj)
	}
	if !proj.Mate.Designated.IsKnown() || proj.Mate.Designated.Value.Status != domain.MateCreated {
		t.Fatalf("mate node = %+v", proj.Mate)
	}
	if task.TaskID != reserved.Task.TaskID || task.Title != "do the thing" {
		t.Fatalf("task node = %+v", task)
	}
	// crew.reserved carries the Task's id too (application.ReserveCrewAttempt),
	// and was appended after task.created in the same transaction, so it is
	// legitimately the Task's last event, not the Task's own creation event.
	if !task.LastEvent.IsKnown() || task.LastEvent.Value.EventType != observability.EventCrewReserved {
		t.Fatalf("task last event = %+v", task.LastEvent)
	}
	if task.Error.State != Absent {
		t.Fatalf("task error = %+v, want Absent for a running task", task.Error)
	}
	if crew.CrewID != reserved.Crew.CrewID || crew.Status != domain.CrewReserved || crew.HarnessKind != reserved.Crew.HarnessKind {
		t.Fatalf("crew node = %+v", crew)
	}
	if !crew.Worktree.IsKnown() || crew.Worktree.Value.Path != reserved.Worktree.Path {
		t.Fatalf("crew worktree = %+v", crew.Worktree)
	}
	if crew.AgentName.State != Absent || crew.Binding.State != Absent {
		t.Fatalf("crew agent = %+v / binding = %+v, want Absent before any binding is recorded", crew.AgentName, crew.Binding)
	}
	if !crew.LastEvent.IsKnown() || crew.LastEvent.Value.EventType != observability.EventCrewReserved {
		t.Fatalf("crew last event = %+v", crew.LastEvent)
	}
	if crew.Error.State != Absent {
		t.Fatalf("crew error = %+v, want Absent for a healthy reserved crew", crew.Error)
	}
	if crew.OpenMerge.State != Absent {
		t.Fatalf("crew open merge = %+v, want Absent when no merge request exists", crew.OpenMerge)
	}
}

// A held runtime_binding row names the live Herdr agent for a Crew, read as
// a durable database fact (application.HeldBindingsForAgent) - not a fresh
// Herdr call.
func TestLoadSnapshotCrewAgentKnownWhenBindingHeld(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "bind me", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	err = f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.ReserveBinding(persistence.BindingRecord{
			BindingID: "bnd_1", ProjectID: reserved.Project.ProjectID, AgentID: reserved.Crew.CrewID,
			Role: domain.RoleCrew, CrewID: reserved.Crew.CrewID,
			HerdrSession: "sess_1", HerdrAgent: "agent-x",
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, mustWorkspaceID(t, f), reserved.Project.ProjectID)
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	tree, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, _, crew := findCrew(t, tree)
	if !crew.AgentName.IsKnown() || crew.AgentName.Value != "agent-x" {
		t.Fatalf("crew agent name = %+v", crew.AgentName)
	}
	if !crew.Binding.IsKnown() || crew.Binding.Value.Status != BindingReserved {
		t.Fatalf("crew binding = %+v, want a Known reserved binding", crew.Binding)
	}
	if crew.Binding.Value.Session != "sess_1" || crew.Binding.Value.Runtime != "herdr" {
		t.Fatalf("crew binding runtime handles = %+v", crew.Binding.Value)
	}
	if crew.Binding.Value.BoundSinceKind != BoundSinceReserved || crew.Binding.Value.BoundSince.IsZero() {
		t.Fatalf("crew binding bound-since = %+v, want the reservation time", crew.Binding.Value)
	}
}

// A Crew that ends up needs_repair carries an error state with the reason
// recorded on the event that drove it there - not a zero value that would
// render as "no error".
func TestLoadSnapshotCrewErrorRefKnownWithReason(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "goes wrong", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	if err := application.MarkCrewNeedsRepair(ctx, f.deps, userCaller(), "", reserved.Crew, "herdr call timed out", false); err != nil {
		t.Fatalf("MarkCrewNeedsRepair: %v", err)
	}

	tree, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, _, crew := findCrew(t, tree)
	if crew.Status != domain.CrewNeedsRepair {
		t.Fatalf("crew status = %s", crew.Status)
	}
	if !crew.Error.IsKnown() || crew.Error.Value != "herdr call timed out" {
		t.Fatalf("crew error = %+v", crew.Error)
	}
	if crew.LastEvent.Value.EventType != observability.EventCrewNeedsRepair {
		t.Fatalf("crew last event = %+v", crew.LastEvent)
	}
}

// A Project without a designated Mate (every Project but the default gets
// none automatically) is Absent, not a zero-valued "known" Mate.
func TestLoadSnapshotMateAbsentWhenProjectHasNoMate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	created, err := application.CreateProject(ctx, f.deps, userCaller(), "second")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	tree, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	var second *ProjectNode
	for i := range tree.Projects {
		if tree.Projects[i].ProjectID == created.Project.ProjectID {
			second = &tree.Projects[i]
		}
	}
	if second == nil {
		t.Fatalf("second project missing from tree: %+v", tree.Projects)
	}
	if second.Mate.Designated.State != Absent {
		t.Fatalf("mate designation = %+v, want Absent", second.Mate.Designated)
	}
	// An Absent designation must be carried into every dependent field, not
	// left at the unset zero FieldState that renders as unreadable.
	for name, state := range map[string]FieldState{
		"agent name": second.Mate.AgentName.State, "binding": second.Mate.Binding.State,
		"last event": second.Mate.LastEvent.State, "error reason": second.Mate.Error.State,
	} {
		if state != Absent {
			t.Fatalf("mate %s state = %q, want %q", name, state, Absent)
		}
	}
}

// A Crew seeded without its worktree row (never happens through the spawn
// saga, which inserts both in the same transaction, but is not structurally
// impossible - e.g. a future direct persistence write) renders Absent, not a
// path that reads as empty-but-fine.
func TestLoadSnapshotWorktreeAbsentWhenNoWorktreeRow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	taskID, crewID := "tsk_bare", "crew_bare"
	workspaceID := mustWorkspaceID(t, f)
	err := f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.InsertTask(persistence.TaskRecord{
			TaskID: taskID, WorkspaceID: workspaceID, ProjectID: f.project.ProjectID,
			RepoID: f.repoID, Title: "bare crew", Status: domain.TaskRunning,
		}); err != nil {
			return err
		}
		if _, err := tx.ReserveCrew(persistence.CrewRecord{
			CrewID: crewID, WorkspaceID: workspaceID, ProjectID: f.project.ProjectID,
			TaskID: taskID, RepoID: f.repoID, Attempt: 1, HarnessKind: domain.HarnessClaude,
		}); err != nil {
			return err
		}
		// A workspace-scoped event with no task_id/crew_id, so it does not
		// satisfy this test's "no events recorded for this crew" premise.
		return appendFixtureEvent(t, f.deps, tx, workspaceID, f.project.ProjectID)
	})
	if err != nil {
		t.Fatalf("seed bare crew: %v", err)
	}

	tree, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, _, crew := findCrew(t, tree)
	if crew.Worktree.State != Absent {
		t.Fatalf("worktree = %+v, want Absent", crew.Worktree)
	}
	if crew.AgentName.State != Absent || crew.Binding.State != Absent {
		t.Fatalf("agent = %+v / binding = %+v, want Absent", crew.AgentName, crew.Binding)
	}
	if crew.LastEvent.State != Absent {
		t.Fatalf("last event = %+v, want Absent for a crew seeded with no events", crew.LastEvent)
	}
	if crew.Error.State != Absent {
		t.Fatalf("error = %+v, want Absent for a freshly reserved crew", crew.Error)
	}
}

func mustWorkspaceID(t *testing.T, f fixture) string {
	t.Helper()
	ws, err := f.store.GetWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ws.WorkspaceID
}

// TestLoadSnapshotMateUnknownWhenListMatesFails is B3a: a failed
// ListMates must surface as Unknown, not Absent (there might well be a
// Mate; the read just could not confirm it) and not Known (that would
// assert a designation that was never actually read).
func TestLoadSnapshotMateUnknownWhenListMatesFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	deps := f.deps
	deps.Store = &failingReadsStore{StateStore: f.store, failListMates: true}

	tree, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if len(tree.Projects) == 0 {
		t.Fatal("no projects loaded")
	}
	if got := tree.Projects[0].Mate.Designated.State; got != Unknown {
		t.Fatalf("mate designation state = %q, want %q", got, Unknown)
	}
	if tree.Projects[0].Mate.Designated.Reason == "" {
		t.Fatal("an Unknown designation must carry the read failure's reason")
	}
	// The dependent fields cannot be read either, and Unknown must not decay
	// into Absent on the way down - mateWithout must carry Unknown into every
	// one of them, not just Binding: a mutation that flips LastEvent and
	// Error to Absent (the "Unknown decays to Absent" the file's own comment
	// at read.go:173-175 forbids) must fail here.
	mate := tree.Projects[0].Mate
	for name, state := range map[string]FieldState{
		"binding": mate.Binding.State, "last event": mate.LastEvent.State, "error reason": mate.Error.State,
	} {
		if state != Unknown {
			t.Fatalf("mate %s state = %q, want %q", name, state, Unknown)
		}
	}
	if mate.LastEvent.Reason == "" || mate.Error.Reason == "" {
		t.Fatal("an Unknown last event/error reason must carry the read failure's reason")
	}
}

// TestLoadSnapshotWorktreeUnknownWhenGetWorktreeFails is B3a for
// WorktreeRef: a failed GetWorktree must not collapse into Absent (no row)
// or Known (a path/branch that was never read).
func TestLoadSnapshotWorktreeUnknownWhenGetWorktreeFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "worktree read fails", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	deps := f.deps
	deps.Store = &failingReadsStore{StateStore: f.store, failGetWorktree: true}

	tree, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, _, crew := findCrew(t, tree)
	if crew.CrewID != reserved.Crew.CrewID {
		t.Fatalf("crew = %+v, want %s", crew, reserved.Crew.CrewID)
	}
	if got := crew.Worktree.State; got != Unknown {
		t.Fatalf("worktree state = %q, want %q", got, Unknown)
	}
	if crew.Worktree.Reason == "" {
		t.Fatal("an Unknown worktree must carry the read failure's reason")
	}
}

// TestLoadSnapshotAgentUnknownWhenListBindingsFails is B3a for
// AgentRef: a failed ListBindings must not collapse into Absent (no binding
// held) or Known (a name that was never read).
func TestLoadSnapshotAgentUnknownWhenListBindingsFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "binding read fails", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	deps := f.deps
	deps.Store = &failingReadsStore{StateStore: f.store, failListBindings: true}

	tree, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, _, crew := findCrew(t, tree)
	if crew.CrewID != reserved.Crew.CrewID {
		t.Fatalf("crew = %+v, want %s", crew, reserved.Crew.CrewID)
	}
	if got := crew.AgentName.State; got != Unknown {
		t.Fatalf("agent name state = %q, want %q", got, Unknown)
	}
	if got := crew.Binding.State; got != Unknown {
		t.Fatalf("binding state = %q, want %q", got, Unknown)
	}
	if crew.Binding.Reason == "" {
		t.Fatal("an Unknown binding must carry the read failure's reason")
	}
}

// TestLoadSnapshotLastEventUnknownWhenListEventsFails is B3a for
// EventRef: a failed ListEvents must not collapse into Absent (no matching
// event recorded) or Known (an event that was never actually read).
func TestLoadSnapshotLastEventUnknownWhenListEventsFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "event read fails", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	deps := f.deps
	deps.Store = &failingReadsStore{StateStore: f.store, failListEvents: true}

	tree, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, task, crew := findCrew(t, tree)
	if crew.CrewID != reserved.Crew.CrewID {
		t.Fatalf("crew = %+v, want %s", crew, reserved.Crew.CrewID)
	}
	if got := task.LastEvent.State; got != Unknown {
		t.Fatalf("task last event state = %q, want %q", got, Unknown)
	}
	if got := crew.LastEvent.State; got != Unknown {
		t.Fatalf("crew last event state = %q, want %q", got, Unknown)
	}
}

// TestLoadSnapshotErrorRefUnknownWhenLastEventReadFails is B2b: a Crew
// whose lifecycle status is itself an error state (needs_repair here) has a
// Reason that comes from a *separate* ListEvents read, so that read failing
// must surface as an Unknown error field - never as a Known one with an
// empty value, which would assert "no reason
// recorded" about a reason that was never actually read. Checked on both
// Task and Crew, since taskErrorRef and crewErrorRef each fold this
// independently.
func TestLoadSnapshotErrorRefUnknownWhenLastEventReadFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "goes wrong, unreadably", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	if err := application.MarkCrewNeedsRepair(ctx, f.deps, userCaller(), "", reserved.Crew, "herdr call timed out", false); err != nil {
		t.Fatalf("MarkCrewNeedsRepair: %v", err)
	}
	deps := f.deps
	deps.Store = &failingReadsStore{StateStore: f.store, failListEvents: true}

	tree, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, _, crew := findCrew(t, tree)
	if crew.Status != domain.CrewNeedsRepair {
		t.Fatalf("crew status = %s, want needs_repair (precondition)", crew.Status)
	}
	if got := crew.Error.State; got != Unknown {
		t.Fatalf("crew error state = %q, want %q (value=%q)", got, Unknown, crew.Error.Value)
	}
}

// TestErrorReasonDistinguishesNoEventFromAnUnreasonedEvent is F7: "no reason
// is recorded on the last matching event" asserts an event exists; when the
// event read succeeded and found none at all, that sentence is false, so
// errorReason must say a different thing for the two cases. Exercised
// directly against errorReason rather than through a seeded Task/Crew,
// since producing a genuinely eventless error-state row through the
// application layer is not straightforward (every writer that reaches an
// error status also appends an event).
func TestErrorReasonDistinguishesNoEventFromAnUnreasonedEvent(t *testing.T) {
	t.Parallel()
	noEvent := errorReason("failed", observability.Event{},
		AbsentField[EventValue]("no event is recorded for this attempt"), crewErrorReasonEvents)
	if !noEvent.IsKnown() || noEvent.Value != "" {
		t.Fatalf("no-event reason = %+v, want Known with an empty value", noEvent)
	}
	if !strings.Contains(noEvent.Reason, "no event is recorded") {
		t.Fatalf("no-event reason text = %q, want it to say no event is recorded", noEvent.Reason)
	}
	if strings.Contains(noEvent.Reason, "last matching event") {
		t.Fatalf("no-event reason text = %q must not claim a matching event exists", noEvent.Reason)
	}

	unreasoned := errorReason("failed",
		observability.Event{EventType: observability.EventCrewFailed, Payload: map[string]any{}},
		KnownField(EventValue{EventType: observability.EventCrewFailed}), crewErrorReasonEvents)
	if !unreasoned.IsKnown() || unreasoned.Value != "" {
		t.Fatalf("unreasoned-event reason = %+v, want Known with an empty value", unreasoned)
	}
	if !strings.Contains(unreasoned.Reason, "no reason is recorded on the last matching event") {
		t.Fatalf("unreasoned-event reason text = %q, want the matching-event sentence", unreasoned.Reason)
	}
	if unreasoned.Reason == noEvent.Reason {
		t.Fatalf("the no-event and event-without-reason sentences must not be identical: %q", unreasoned.Reason)
	}
}

// TestMateErrorRefIgnoresAMateUnknownEventNamingADifferentMate is F8:
// mateErrorRef filters mate.unknown events by Project only (the events
// table has no mate_id column), so it checks the payload's own mate_id
// explicitly rather than trusting the filter to have scoped it to this
// Mate. Only one Mate may be ACTIVE per Project at a time, but a Project
// can have multiple Mate rows over time (one stopped, another started
// later), so a mismatched mate_id in an event naming an earlier or
// otherwise different Mate is not unreachable by construction - this
// seeds the mismatch directly at the persistence layer as hardening
// evidence for that case.
func TestMateErrorRefIgnoresAMateUnknownEventNamingADifferentMate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	mate, err := application.DesignatedMate(ctx, f.store, f.project.ProjectID)
	if err != nil {
		t.Fatalf("DesignatedMate: %v", err)
	}
	err = f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.UpdateMateStatus(mate.MateID, mate.Version, domain.MateUnknown); err != nil {
			return err
		}
		ev := observability.NewEvent(observability.EventMateUnknown, f.deps.Clock.Now())
		id, err := f.deps.IDs.New("evt")
		if err != nil {
			return err
		}
		ev.EventID = id
		ev.WorkspaceID, ev.ProjectID = mustWorkspaceID(t, f), mate.ProjectID
		ev.Payload = map[string]any{"mate_id": "mate_someone_else", "reason": "not this mate's problem"}
		return tx.AppendEvent(ev)
	})
	if err != nil {
		t.Fatalf("seed mismatched mate.unknown event: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	got := snap.Projects[0].Mate.Error
	if !got.IsKnown() || got.Value != "" {
		t.Fatalf("mate error = %+v, want Known with an empty value, not the other Mate's reason", got)
	}
	if strings.Contains(got.Reason, "not this mate's problem") {
		t.Fatalf("mate error reason = %q leaked the mismatched event's payload", got.Reason)
	}
}

// TestMateErrorRefIgnoresAMateUnknownEventWithNoMateID is B3: the events
// table has no mate_id column, so mateErrorRef's own payload check must
// require a matching string mate_id rather than treating a missing, empty
// or malformed one as a match. Absence of identity is not proof of
// identity. Reproduces the counter-review's exact scenario: a mate.unknown
// event for the same Project, correct EventType, but no mate_id in its
// payload at all - the shape a malformed or foreign writer could produce
// even though every current writer (setMateStatus) always sets it.
func TestMateErrorRefIgnoresAMateUnknownEventWithNoMateID(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	mate, err := application.DesignatedMate(ctx, f.store, f.project.ProjectID)
	if err != nil {
		t.Fatalf("DesignatedMate: %v", err)
	}
	err = f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.UpdateMateStatus(mate.MateID, mate.Version, domain.MateUnknown); err != nil {
			return err
		}
		ev := observability.NewEvent(observability.EventMateUnknown, f.deps.Clock.Now())
		id, err := f.deps.IDs.New("evt")
		if err != nil {
			return err
		}
		ev.EventID = id
		ev.WorkspaceID, ev.ProjectID = mustWorkspaceID(t, f), mate.ProjectID
		ev.Payload = map[string]any{"reason": "not this mate's problem"}
		return tx.AppendEvent(ev)
	})
	if err != nil {
		t.Fatalf("seed mate.unknown event with no mate_id: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	got := snap.Projects[0].Mate.Error
	if !got.IsKnown() || got.Value != "" {
		t.Fatalf("mate error = %+v, want Known with an empty value, not a foreign reason", got)
	}
	if strings.Contains(got.Reason, "not this mate's problem") {
		t.Fatalf("mate error reason = %q leaked the identity-less event's payload", got.Reason)
	}
}

func TestLoadSnapshotOpenMergeIsKnownWhenARequestIsOpen(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "merge me", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	ws, err := f.store.GetWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.InsertMergeRequest(persistence.MergeRequestRecord{
			RequestID: "mr_open1", WorkspaceID: ws.WorkspaceID, ProjectID: reserved.Project.ProjectID,
			CrewID: reserved.Crew.CrewID, RepoID: f.repoID,
			SourceBranch: "crew/" + reserved.Crew.CrewID, TargetBranch: "main",
			RequestedByRole: domain.RoleUser,
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, ws.WorkspaceID, reserved.Project.ProjectID)
	})
	if err != nil {
		t.Fatalf("InsertMergeRequest: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, _, crew := findCrew(t, snap)
	if !crew.OpenMerge.IsKnown() || crew.OpenMerge.Value.RequestID != "mr_open1" {
		t.Fatalf("open merge = %+v, want Known mr_open1", crew.OpenMerge)
	}
	if crew.OpenMerge.Value.Status != domain.MergePendingConfirmation {
		t.Fatalf("open merge status = %q, want pending_confirmation", crew.OpenMerge.Value.Status)
	}
	discard := actionByName(crew.Actions, "discard")
	if discard.Available || !strings.Contains(discard.Reason, "open merge request") {
		t.Fatalf("discard = %+v, want the open-merge refusal DiscardCrew itself uses", discard)
	}
}

func TestLoadSnapshotOpenMergeUnknownWhenTheListFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "merge read fails", RepoID: f.repoID,
	}); err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	deps := f.deps
	deps.Store = &failingReadsStore{StateStore: f.store, failListMergeRequests: true}

	snap, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, _, crew := findCrew(t, snap)
	if crew.OpenMerge.State != Unknown {
		t.Fatalf("open merge = %+v, want Unknown when ListMergeRequests fails", crew.OpenMerge)
	}
	if !hasWarning(snap, "open merge", crew.CrewID) {
		t.Fatalf("warnings = %+v, want an open-merge warning on the crew", snap.Warnings)
	}
	discard := actionByName(crew.Actions, "discard")
	if discard.Available {
		t.Fatalf("discard = %+v, must not be available when the merge read failed", discard)
	}
	if strings.Contains(discard.Reason, "no open merge") {
		t.Fatalf("discard reason = %q, asserts a fact the failed read never established", discard.Reason)
	}
}

// TestLoadSnapshotCrewErrorRefKnownWithReasonFromReportedDone is B2a:
// `crew done --status ready` on a branch that does not fast-forward reaches
// needs_rebase through crew.reported_done (application.RecordCrewDone /
// precheckFastForward), not through a crew.needs_rebase event type - there
// is no such type. Before crewErrorReasonEvents named
// EventCrewReportedDone, this reason was silently dropped and the Console
// rendered "no reason recorded" for a reason that was, in fact, recorded.
// needs_repair from the same precheck (an unreadable worktree row) loses
// its reason identically, so both are checked here.
func TestLoadSnapshotCrewErrorRefKnownWithReasonFromReportedDone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "branch is behind", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	// crew done only accepts a running (or needs_rebase/needs_repair) crew;
	// ReserveCrewAttempt leaves it reserved, so this test-only status
	// rewrite (mirroring internal/application/crew_report_test.go's
	// unexported setCrewStatus) fast-forwards past the preparing step
	// that is not this test's concern.
	rec, err := f.store.GetCrew(ctx, reserved.Crew.CrewID)
	if err != nil {
		t.Fatal(err)
	}
	err = f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.UpdateCrewStatus(rec.CrewID, rec.Version, domain.CrewRunning); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, rec.WorkspaceID, rec.ProjectID)
	})
	if err != nil {
		t.Fatalf("set crew running: %v", err)
	}
	summary := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(summary, []byte("did the thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// git.NewFake()'s zero-valued FastForwardResultValue is
	// {FastForward:false, Diverged:false}, which precheckFastForward reads
	// as a clean "no" - exactly the "branch is behind" case B2a reproduces,
	// with no explicit fake configuration needed.
	crewCaller := application.Caller{Role: domain.RoleCrew, AgentID: reserved.Crew.CrewID, CrewID: reserved.Crew.CrewID}
	if _, err := application.RecordCrewDone(ctx, f.deps, git.NewFake(), crewCaller, application.CrewDoneRequest{
		CrewID: reserved.Crew.CrewID, Result: "ready", SummaryFile: summary,
	}); err != nil {
		t.Fatalf("RecordCrewDone: %v", err)
	}

	tree, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, _, crew := findCrew(t, tree)
	if crew.Status != domain.CrewNeedsRebase {
		t.Fatalf("crew status = %s, want needs_rebase (precondition)", crew.Status)
	}
	if crew.LastEvent.Value.EventType != observability.EventCrewReportedDone {
		t.Fatalf("crew last event = %+v, want crew.reported_done", crew.LastEvent)
	}
	if !crew.Error.IsKnown() || crew.Error.Value != "source branch does not fast-forward onto the target branch" {
		t.Fatalf("crew error = %+v, want the fast-forward precheck's reason", crew.Error)
	}
}
