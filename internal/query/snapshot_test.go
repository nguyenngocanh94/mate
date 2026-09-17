package query

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/application"
	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/fsboundary"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/persistence"
)

// TestSnapshotAsOfIsTheReadsOwnClock pins AsOf to the injected clock rather
// than to any stored column: the question it answers is "how stale is what
// I am looking at", which no row in the database records.
//
// A clock frozen at one value throughout the whole read cannot tell this
// test whether AsOf was stamped before LoadSnapshot even started reading or
// after it finished - both would equal the same frozen testNow. So the
// fixture's *FakeClock is advanced from inside the read itself, by a
// wrapped Store whose ListRepos (called once per Project, before AsOf is
// stamped) bumps the clock forward; only a read that takes Clock.Now()
// after that call can see the advanced time.
func TestSnapshotAsOfIsTheReadsOwnClock(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	clock := f.deps.Clock.(*domain.FakeClock)
	wantAsOf := testNow.Add(5 * time.Minute)
	deps := f.deps
	deps.Store = &clockBumpingStore{StateStore: f.deps.Store, clock: clock, bumpTo: wantAsOf}

	snap, err := LoadSnapshot(context.Background(), deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if !snap.AsOf.Equal(wantAsOf) {
		t.Fatalf("AsOf = %s, want the clock's value as of after the read finished (%s)", snap.AsOf, wantAsOf)
	}
}

// clockBumpingStore advances a shared *FakeClock the first time ListRepos is
// called (loadProjectNode's first read for a Project, ahead of the Task and
// Crew reads and well ahead of Snapshot.AsOf being stamped), so a test can
// tell whether AsOf reflects the clock as of after the read loop or a value
// pinned before it started.
type clockBumpingStore struct {
	application.StateStore
	clock  *domain.FakeClock
	bumpTo time.Time
	bumped bool
}

func (s *clockBumpingStore) ListRepos(ctx context.Context, projectID string) ([]persistence.RepoRecord, error) {
	if !s.bumped {
		s.bumped = true
		s.clock.Set(s.bumpTo)
	}
	return s.StateStore.ListRepos(ctx, projectID)
}

// TestSnapshotWorkspaceIsTheStoresOwnLocation checks the workspace field
// reports the root the store itself resolved (ADR 0015: never a
// caller-supplied path) and the database file derived from it by the same
// constructor every other command uses.
func TestSnapshotWorkspaceIsTheStoresOwnLocation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	snap, err := LoadSnapshot(context.Background(), f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	root, err := f.store.WorkspaceRoot()
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Workspace.IsKnown() {
		t.Fatalf("workspace = %+v, want Known", snap.Workspace)
	}
	if snap.Workspace.Value.Root != root.String() {
		t.Fatalf("root = %q, want %q", snap.Workspace.Value.Root, root.String())
	}
	if !strings.HasSuffix(snap.Workspace.Value.DatabasePath, "/.matev2/matev2.db") {
		t.Fatalf("database path = %q", snap.Workspace.Value.DatabasePath)
	}
	if snap.Workspace.Value.Name == "" {
		t.Fatal("workspace name must fall back to the root's base name")
	}
}

// failingRootStore breaks WorkspaceRoot alone. It is the same
// embed-and-override shape as failingReadsStore, kept separate because the
// root is not a row read and has its own single call site.
type failingRootStore struct {
	application.StateStore
}

func (s failingRootStore) WorkspaceRoot() (fsboundary.Path, error) {
	return fsboundary.Path{}, errInjectedReadFailure
}

// TestSnapshotWorkspaceUnknownWhenRootResolutionFails is the Unknown branch
// of the workspace field: a root that cannot be resolved must not render as
// an empty path, which would read as "the workspace is at /".
func TestSnapshotWorkspaceUnknownWhenRootResolutionFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	deps := f.deps
	deps.Store = failingRootStore{StateStore: f.store}

	snap, err := LoadSnapshot(context.Background(), deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if snap.Workspace.State != Unknown {
		t.Fatalf("workspace = %+v, want Unknown", snap.Workspace)
	}
	if snap.Workspace.Reason == "" {
		t.Fatal("an Unknown workspace must carry the failure reason")
	}
	if !hasWarning(snap, "workspace root", snap.WorkspaceID) {
		t.Fatalf("warnings = %+v, want a workspace root warning", snap.Warnings)
	}
}

// TestSnapshotWarningsNameFieldRowAndReason is the footer's contract: the
// "N field(s) unknown: <field> of <row> (<reason>)" line must be writable
// from the warning alone, without the UI inventing any of the three parts.
func TestSnapshotWarningsNameFieldRowAndReason(t *testing.T) {
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

	snap, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	var found *FieldWarning
	for i := range snap.Warnings {
		if snap.Warnings[i].Field == "worktree" {
			found = &snap.Warnings[i]
		}
	}
	if found == nil {
		t.Fatalf("warnings = %+v, want one for the worktree", snap.Warnings)
	}
	if found.Row.Kind != RowCrew || found.Row.ID != reserved.Crew.CrewID {
		t.Fatalf("warning row = %+v, want the crew that failed to read", found.Row)
	}
	if !strings.Contains(found.Reason, errInjectedReadFailure.Error()) {
		t.Fatalf("warning reason = %q, want the store's own message", found.Reason)
	}
	if found.Row.Label == "" {
		t.Fatal("a warning row must carry a label the footer can print")
	}
}

// TestSnapshotWarningsCoverEveryUnknownFieldInTreeOrder is the counting
// contract: the footer says "N fields unknown", so every Unknown field in
// the tree has to be in the list exactly once, and in an order that does
// not reshuffle between two reads of unchanged state.
func TestSnapshotWarningsCoverEveryUnknownFieldInTreeOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "everything fails", RepoID: f.repoID,
	}); err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	deps := f.deps
	deps.Store = &failingReadsStore{
		StateStore: f.store, failListMates: true, failGetWorktree: true,
		failListBindings: true, failListEvents: true, failListRepos: true,
	}

	first, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	unknown := countUnknownFields(t, first)
	if len(first.Warnings) != unknown {
		t.Fatalf("warnings = %d for %d Unknown fields:\n%+v", len(first.Warnings), unknown, first.Warnings)
	}
	second, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot (second): %v", err)
	}
	if len(second.Warnings) != len(first.Warnings) {
		t.Fatalf("warning count moved between reads: %d then %d", len(first.Warnings), len(second.Warnings))
	}
	for i := range first.Warnings {
		if first.Warnings[i] != second.Warnings[i] {
			t.Fatalf("warning %d differs between two reads of unchanged state:\n%+v\n%+v", i, first.Warnings[i], second.Warnings[i])
		}
	}
}

// countUnknownFields walks the tree the way a renderer would and counts the
// fields in the Unknown state, independently of the warning list, so the
// two can be compared.
func countUnknownFields(t *testing.T, snap Snapshot) int {
	t.Helper()
	n := 0
	count := func(states ...FieldState) {
		for _, s := range states {
			if s == Unknown {
				n++
			}
		}
	}
	count(snap.Workspace.State)
	for _, p := range snap.Projects {
		count(p.Repos.State, p.Attention.State,
			p.Mate.Designated.State, p.Mate.AgentName.State, p.Mate.Binding.State,
			p.Mate.LastEvent.State, p.Mate.Error.State)
		for _, task := range p.Tasks {
			count(task.Repo.State, task.LastEvent.State, task.Error.State, task.Attention.State)
			for _, c := range task.Crews {
				count(c.Repo.State, c.Worktree.State, c.AgentName.State, c.Binding.State,
					c.LastEvent.State, c.Error.State, c.RetryOf.State, c.Attention.State)
			}
		}
	}
	return n
}

func hasWarning(snap Snapshot, field, rowID string) bool {
	for _, w := range snap.Warnings {
		if w.Field == field && (rowID == "" || w.Row.ID == rowID) {
			return true
		}
	}
	return false
}

// TestReadFailureReasonPrefersTheCodedMessage keeps the reason a user reads
// identical to the phrasing the rest of `mate` reports for that failure,
// and folds it onto one line because the footer is one terminal line.
func TestReadFailureReasonPrefersTheCodedMessage(t *testing.T) {
	t.Parallel()
	coded := observability.WrapError(observability.CodeRuntimeUnavailable, "sqlite: database is locked", errors.New("SQLITE_BUSY"))
	if got := readFailureReason(coded); got != "sqlite: database is locked" {
		t.Fatalf("reason = %q, want the coded message", got)
	}
	if got := readFailureReason(errors.New("line one\nline two")); got != "line one line two" {
		t.Fatalf("reason = %q, want the newline folded", got)
	}
	if got := readFailureReason(nil); got != "" {
		t.Fatalf("reason for nil = %q, want empty", got)
	}
}

// TestFieldZeroValueIsNotKnown is the reason FieldState is a string and not
// the design notes' uint8 iota: with an integer enum the zero value would
// be Known, so a field nobody filled in would claim its empty value was
// read successfully.
func TestFieldZeroValueIsNotKnown(t *testing.T) {
	t.Parallel()
	var f Field[string]
	if f.IsKnown() {
		t.Fatal("the zero Field must not report itself Known")
	}
	if f.State == Known || f.State == Absent || f.State == Unknown {
		t.Fatalf("the zero FieldState must be none of the three, got %q", f.State)
	}
}

// TestMateErrorReasonComesFromTheMateUnknownEvent covers the one MateStatus
// that is an error state. A Mate's "last event" is its Project's (events
// have no mate_id), so the reason has to be looked up by event type or it
// is lost the moment any other event lands after it.
func TestMateErrorReasonComesFromTheMateUnknownEvent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	mate, err := application.DesignatedMate(ctx, f.store, f.project.ProjectID)
	if err != nil {
		t.Fatalf("DesignatedMate: %v", err)
	}
	if _, err := application.RecordMateUnknown(ctx, f.deps, userCaller(), "", mate, "herdr stop could not be confirmed"); err != nil {
		t.Fatalf("RecordMateUnknown: %v", err)
	}
	// Any later event pushes mate.unknown out of "last event" position.
	if _, err := application.CreateTask(ctx, f.deps, userCaller(), application.CreateTaskRequest{
		Title: "something else", RepoID: f.repoID,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	m := snap.Projects[0].Mate
	if !m.Designated.IsKnown() || m.Designated.Value.Status != domain.MateUnknown {
		t.Fatalf("mate = %+v, want a Known unknown-status Mate", m.Designated)
	}
	if !m.Error.IsKnown() || m.Error.Value != "herdr stop could not be confirmed" {
		t.Fatalf("mate error = %+v, want the recorded reason", m.Error)
	}
	if m.LastEvent.IsKnown() && m.LastEvent.Value.EventType == observability.EventMateUnknown {
		t.Fatal("precondition failed: mate.unknown is still the project's last event")
	}
}

// TestMateErrorUnknownWhenReasonReadFails is the Unknown branch of a Mate's
// error reason: the status was read, the reason's own read was not, and
// "no reason recorded" would assert a negative nothing established.
func TestMateErrorUnknownWhenReasonReadFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	mate, err := application.DesignatedMate(ctx, f.store, f.project.ProjectID)
	if err != nil {
		t.Fatalf("DesignatedMate: %v", err)
	}
	if _, err := application.RecordMateUnknown(ctx, f.deps, userCaller(), "", mate, "herdr stop could not be confirmed"); err != nil {
		t.Fatalf("RecordMateUnknown: %v", err)
	}
	deps := f.deps
	deps.Store = &failingReadsStore{StateStore: f.store, failListEvents: true}

	snap, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if got := snap.Projects[0].Mate.Error.State; got != Unknown {
		t.Fatalf("mate error state = %q, want %q", got, Unknown)
	}
}

// TestBindingSurfacesRuntimeHandlesAndBoundSince is what the inspector's
// Runtime and Bound since lines read: the session, tab and pane the binding
// names, and which of reserved_at/activated_at BoundSince is.
//
// Reserve and activate happen in two separate writes with the clock
// advanced between them, rather than in the one transaction seedBinding
// uses, so reserved_at and activated_at are provably different instants:
// with a single shared "now" the two would be identical, and a bug that
// read reserved_at instead of activated_at would render the same timestamp
// and pass regardless.
func TestBindingSurfacesRuntimeHandlesAndBoundSince(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	clock := f.deps.Clock.(*domain.FakeClock)
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "activate me", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	bindingID := "bnd_" + reserved.Crew.CrewID
	err = f.store.Write(ctx, clock.Now(), func(tx persistence.Tx) error {
		if err := tx.ReserveBinding(persistence.BindingRecord{
			BindingID: bindingID, ProjectID: reserved.Project.ProjectID, AgentID: reserved.Crew.CrewID,
			Role: domain.RoleCrew, CrewID: reserved.Crew.CrewID,
			HerdrSession: "sess_1", HerdrAgent: "agent-x",
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, mustWorkspaceID(t, f), reserved.Project.ProjectID)
	})
	if err != nil {
		t.Fatalf("seed reserved binding: %v", err)
	}
	clock.Advance(5 * time.Minute)
	wantActivatedAt := clock.Now()
	err = f.store.Write(ctx, clock.Now(), func(tx persistence.Tx) error {
		stored, err := tx.HeldBindingByAgent(reserved.Crew.CrewID)
		if err != nil {
			return err
		}
		if err := tx.ActivateBinding(stored.BindingID, stored.Version, persistence.HerdrHandles{
			Workspace: "hw_1", Tab: "tab_1", Pane: "pane_1",
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, mustWorkspaceID(t, f), reserved.Project.ProjectID)
	})
	if err != nil {
		t.Fatalf("activate binding: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, _, crew := findCrew(t, snap)
	b := crew.Binding
	if !b.IsKnown() || b.Value.Status != BindingActive {
		t.Fatalf("binding = %+v, want a Known active binding", b)
	}
	if b.Value.Session != "sess_1" || b.Value.Workspace != "hw_1" || b.Value.Tab != "tab_1" || b.Value.Pane != "pane_1" {
		t.Fatalf("binding runtime handles = %+v", b.Value)
	}
	if b.Value.BoundSinceKind != BoundSinceActivated || !b.Value.BoundSince.Equal(wantActivatedAt) {
		t.Fatalf("bound since = %+v, want the activation time %s", b.Value, wantActivatedAt)
	}
	// The one caveat this codebase must never drop: recorded active is not
	// proof the agent is alive (ADR 0019's health observer does not exist).
	if !strings.Contains(b.Reason, "does not prove the agent is alive") {
		t.Fatalf("binding note = %q, want the liveness caveat", b.Reason)
	}
}

// TestAgentNameOutlivesAReleasedBinding is why AgentName is its own field:
// releasing a binding frees the name and the slot but keeps the row for
// audit, so the name an attempt ran under is still Known while the binding
// itself is legitimately Absent.
func TestAgentNameOutlivesAReleasedBinding(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "release me", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	seedBinding(t, f, reserved.Project.ProjectID, reserved.Crew.CrewID, "agent-gone", func(tx persistence.Tx, b persistence.BindingRecord) error {
		return tx.ReleaseBinding(b.BindingID, b.Version)
	})

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, _, crew := findCrew(t, snap)
	if !crew.AgentName.IsKnown() || crew.AgentName.Value != "agent-gone" {
		t.Fatalf("agent name = %+v, want the released row's name", crew.AgentName)
	}
	if crew.Binding.State != Absent {
		t.Fatalf("binding = %+v, want Absent once every row is released", crew.Binding)
	}
	if crew.Binding.Reason == "" {
		t.Fatal("an Absent binding must say why there is none")
	}
}

// seedBinding reserves a binding for one agent and applies mutate to it, so
// a test can exercise the activated, stale and released shapes without
// standing up the whole spawn saga.
func seedBinding(t *testing.T, f fixture, projectID, agentID, herdrAgent string, mutate func(persistence.Tx, persistence.BindingRecord) error) {
	t.Helper()
	ctx := context.Background()
	bindingID := "bnd_" + agentID
	err := f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		rec := persistence.BindingRecord{
			BindingID: bindingID, ProjectID: projectID, AgentID: agentID,
			Role: domain.RoleCrew, CrewID: agentID,
			HerdrSession: "sess_1", HerdrAgent: herdrAgent,
		}
		if err := tx.ReserveBinding(rec); err != nil {
			return err
		}
		stored, err := tx.HeldBindingByAgent(agentID)
		if err != nil {
			return err
		}
		if mutate != nil {
			if err := mutate(tx, stored); err != nil {
				return err
			}
		}
		return appendFixtureEvent(t, f.deps, tx, mustWorkspaceID(t, f), projectID)
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}
}
