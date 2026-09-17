package query

import (
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/application"
	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/persistence"
)

// TestAttentionIsDerivedFromTheLatestAttempt pins the definition every
// count in the UI depends on: a Task's attention is its latest attempt's,
// and the Project's count is how many of its Tasks have one. The definition
// lives here, not in the Console, so `mate` output and the Console cannot
// disagree about which rows need a person.
func TestAttentionIsDerivedFromTheLatestAttempt(t *testing.T) {
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

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	proj, task, crew := findCrew(t, snap)
	if !crew.Attention.IsKnown() || crew.Attention.Value.Kind != AttentionRepair {
		t.Fatalf("crew attention = %+v, want %q", crew.Attention, AttentionRepair)
	}
	if !strings.Contains(crew.Attention.Value.Why, "herdr call timed out") {
		t.Fatalf("crew attention why = %q, want the recorded reason", crew.Attention.Value.Why)
	}
	if !task.Attention.IsKnown() || task.Attention.Value.Kind != AttentionRepair {
		t.Fatalf("task attention = %+v, want the latest attempt's", task.Attention)
	}
	if !proj.Attention.IsKnown() || proj.Attention.Value.TasksNeedingAttention != 1 {
		t.Fatalf("project attention = %+v, want one task needing attention", proj.Attention)
	}
}

// TestAttentionAbsentWhenNothingNeedsIt is the other half of the count: a
// healthy Project must report Absent with a reason, not a zero-valued
// Known attention that a renderer would print as an empty amber word.
func TestAttentionAbsentWhenNothingNeedsIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "all fine", RepoID: f.repoID,
	}); err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	proj, task, crew := findCrew(t, snap)
	for name, f := range map[string]FieldState{
		"crew": crew.Attention.State, "task": task.Attention.State, "project": proj.Attention.State,
	} {
		if f != Absent {
			t.Fatalf("%s attention state = %q, want %q", name, f, Absent)
		}
	}
	if proj.Attention.Reason == "" || task.Attention.Reason == "" || crew.Attention.Reason == "" {
		t.Fatal("an Absent attention must say why nothing needs attention")
	}
}

// TestTaskAttentionAgreesWithErrorRefWhenBlockedWithNoAttempts guards
// against taskAttention and taskErrorRef disagreeing about the same Task: a
// Task recorded failed or blocked is an error state to taskErrorRef
// regardless of attempt count, so taskAttention must not call it healthy
// just because it never got as far as an attempt. No current writer is
// known to produce a failed/blocked Task with zero attempts - Task status
// transitions to those states are driven by crew_report.go, which requires
// a Crew to exist - so this seeds the row directly rather than claiming the
// combination is reachable through the application layer today.
func TestTaskAttentionAgreesWithErrorRefWhenBlockedWithNoAttempts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	workspaceID := mustWorkspaceID(t, f)
	taskID := "tsk_blocked_no_attempt"
	err := f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.InsertTask(persistence.TaskRecord{
			TaskID: taskID, WorkspaceID: workspaceID, ProjectID: f.project.ProjectID,
			RepoID: f.repoID, Title: "blocked before any attempt", Status: domain.TaskBlocked,
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, workspaceID, f.project.ProjectID)
	})
	if err != nil {
		t.Fatalf("seed blocked task: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	var task *TaskNode
	for i := range snap.Projects[0].Tasks {
		if snap.Projects[0].Tasks[i].TaskID == taskID {
			task = &snap.Projects[0].Tasks[i]
		}
	}
	if task == nil {
		t.Fatalf("blocked task missing from tree: %+v", snap.Projects[0].Tasks)
	}
	if len(task.Crews) != 0 {
		t.Fatalf("precondition failed: task has attempts: %+v", task.Crews)
	}
	if task.Error.State != Known {
		t.Fatalf("task error state = %+v, want Known: blocked is an error state to taskErrorRef", task.Error)
	}
	if !task.Attention.IsKnown() || task.Attention.Value.Kind != AttentionBlocked {
		t.Fatalf("task attention = %+v, want Known/%q to agree with taskErrorRef", task.Attention, AttentionBlocked)
	}
}

// TestTaskAttentionAgreesWithErrorRefWhenFailedWithNoAttempts is the
// symmetric half of TestTaskAttentionAgreesWithErrorRefWhenBlockedWithNoAttempts:
// taskErrorRef treats domain.TaskFailed as an error state exactly like
// domain.TaskBlocked, and taskAttention's guard must agree for both, not
// just the one branch the other test happens to seed.
func TestTaskAttentionAgreesWithErrorRefWhenFailedWithNoAttempts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	workspaceID := mustWorkspaceID(t, f)
	taskID := "tsk_failed_no_attempt"
	err := f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.InsertTask(persistence.TaskRecord{
			TaskID: taskID, WorkspaceID: workspaceID, ProjectID: f.project.ProjectID,
			RepoID: f.repoID, Title: "failed before any attempt", Status: domain.TaskFailed,
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, workspaceID, f.project.ProjectID)
	})
	if err != nil {
		t.Fatalf("seed failed task: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	var task *TaskNode
	for i := range snap.Projects[0].Tasks {
		if snap.Projects[0].Tasks[i].TaskID == taskID {
			task = &snap.Projects[0].Tasks[i]
		}
	}
	if task == nil {
		t.Fatalf("failed task missing from tree: %+v", snap.Projects[0].Tasks)
	}
	if len(task.Crews) != 0 {
		t.Fatalf("precondition failed: task has attempts: %+v", task.Crews)
	}
	if task.Error.State != Known {
		t.Fatalf("task error state = %+v, want Known: failed is an error state to taskErrorRef", task.Error)
	}
	if !task.Attention.IsKnown() || task.Attention.Value.Kind != AttentionFailed {
		t.Fatalf("task attention = %+v, want Known/%q to agree with taskErrorRef", task.Attention, AttentionFailed)
	}
}

// TestAttentionNoMateOnAProjectWithoutOne is the design's "! no mate" word:
// a Project with no designated Mate cannot start an attempt for any Task,
// which is a Project-level problem, not a Task-level one.
func TestAttentionNoMateOnAProjectWithoutOne(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	created, err := application.CreateProject(ctx, f.deps, userCaller(), "second")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	var second *ProjectNode
	for i := range snap.Projects {
		if snap.Projects[i].ProjectID == created.Project.ProjectID {
			second = &snap.Projects[i]
		}
	}
	if second == nil {
		t.Fatalf("second project missing: %+v", snap.Projects)
	}
	if !second.Attention.IsKnown() || second.Attention.Value.Kind != AttentionNoMate {
		t.Fatalf("project attention = %+v, want %q", second.Attention, AttentionNoMate)
	}
	if second.Attention.Value.TasksNeedingAttention != 0 {
		t.Fatalf("project attention count = %d, want 0 with no tasks", second.Attention.Value.TasksNeedingAttention)
	}
}

// TestAttentionStaleBindingUnderAHealthyStatus is the case a status column
// alone cannot show: nothing about the attempt's own status is wrong, but
// its binding is recorded stale, so attach is refused (ADR 0027).
func TestAttentionStaleBindingUnderAHealthyStatus(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "stale binding", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	seedBinding(t, f, reserved.Project.ProjectID, reserved.Crew.CrewID, "agent-stale", func(tx persistence.Tx, b persistence.BindingRecord) error {
		return tx.MarkBindingStale(b.BindingID, b.Version)
	})

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, task, crew := findCrew(t, snap)
	if crew.Status != domain.CrewReserved {
		t.Fatalf("crew status = %s, want a status that is not itself an error (precondition)", crew.Status)
	}
	if !crew.Attention.IsKnown() || crew.Attention.Value.Kind != AttentionStaleBinding {
		t.Fatalf("crew attention = %+v, want %q", crew.Attention, AttentionStaleBinding)
	}
	if !task.Attention.IsKnown() || task.Attention.Value.Kind != AttentionStaleBinding {
		t.Fatalf("task attention = %+v, want the stale binding carried up", task.Attention)
	}
}

// TestProjectAttentionReflectsAnUnknownMate pins F1: a Mate recorded
// unknown must make the Project's own attention Known, not Absent "its
// Mate is recorded healthy". This is the shape a failed `mate stop` leaves
// - RecordMateUnknown (internal/orchestration/stop.go:99, :110, :119) marks
// the Mate unknown without staling its binding, so the binding stays
// active-looking - and mateErrorRef already treats a Mate recorded unknown
// as an error state with a reason; attention must not disagree with it.
func TestProjectAttentionReflectsAnUnknownMate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	mate, err := application.DesignatedMate(ctx, f.store, f.project.ProjectID)
	if err != nil {
		t.Fatalf("DesignatedMate: %v", err)
	}
	// A Task with no attempts yet, so taskAttention's own no-attempt branch
	// is exercised by the same Mate shape.
	draft, err := application.CreateTask(ctx, f.deps, userCaller(), application.CreateTaskRequest{
		Title: "waiting on the mate", ProjectID: f.project.ProjectID,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	err = f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.ReserveBinding(persistence.BindingRecord{
			BindingID: "bnd_mate", ProjectID: f.project.ProjectID, AgentID: mate.MateID,
			Role: domain.RoleMate, HerdrSession: "sess_1", HerdrAgent: "agent-mate",
		}); err != nil {
			return err
		}
		held, err := tx.HeldBindingByAgent(mate.MateID)
		if err != nil {
			return err
		}
		if err := tx.ActivateBinding(held.BindingID, held.Version, persistence.HerdrHandles{
			Workspace: "hws_1", Tab: "tab_1", Pane: "pane_1",
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, mustWorkspaceID(t, f), f.project.ProjectID)
	})
	if err != nil {
		t.Fatalf("seed active mate binding: %v", err)
	}
	if _, err := application.RecordMateUnknown(ctx, f.deps, userCaller(), "", mate, "agent still live after stop"); err != nil {
		t.Fatalf("RecordMateUnknown: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if len(snap.Projects) != 1 {
		t.Fatalf("projects = %d, want 1: %+v", len(snap.Projects), snap.Projects)
	}
	proj := snap.Projects[0]
	if !proj.Mate.Designated.IsKnown() || proj.Mate.Designated.Value.Status != domain.MateUnknown {
		t.Fatalf("mate designation = %+v, want Known(unknown) (precondition)", proj.Mate.Designated)
	}
	if !proj.Mate.Error.IsKnown() {
		t.Fatalf("mate error = %+v, want Known (precondition: mateErrorRef treats unknown as an error state)", proj.Mate.Error)
	}
	if !proj.Attention.IsKnown() || proj.Attention.Value.Kind != AttentionMateUnknown {
		t.Fatalf("project attention = %+v, want Known(%q) for a Mate recorded unknown", proj.Attention, AttentionMateUnknown)
	}
	if !strings.Contains(proj.Attention.Value.Why, "agent still live after stop") {
		t.Fatalf("project attention why = %q, want the recorded reason", proj.Attention.Value.Why)
	}
	var task TaskNode
	found := false
	for _, tn := range proj.Tasks {
		if tn.TaskID == draft.Task.TaskID {
			task, found = tn, true
		}
	}
	if !found {
		t.Fatalf("draft task missing from project: %+v", proj.Tasks)
	}
	if !task.Attention.IsKnown() || task.Attention.Value.Kind != AttentionMateUnknown {
		t.Fatalf("task attention = %+v, want Known(%q) - the same Mate-unknown gap in taskAttention's no-attempt branch", task.Attention, AttentionMateUnknown)
	}
}

// TestAttentionUnreadableWhenAWorktreeReadFails pins the crewAttention
// branch at attention.go:95-96, the one case in that function no other test
// reaches: with the binding read healthy (so the binding cases above it in
// the switch fall through), a failed GetWorktree must still make the
// attempt's own attention Known/AttentionUnreadable, not Absent - a
// worktree nobody could read is exactly the kind of fact a person must look
// at, just like the binding case right above it.
func TestAttentionUnreadableWhenAWorktreeReadFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "worktree read fails", RepoID: f.repoID,
	}); err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	deps := f.deps
	deps.Store = &failingReadsStore{StateStore: f.store, failGetWorktree: true}

	snap, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, task, crew := findCrew(t, snap)
	if crew.Binding.State == Unknown {
		t.Fatalf("crew binding = %+v, want a healthy read (precondition: only the worktree read fails)", crew.Binding)
	}
	if !crew.Attention.IsKnown() || crew.Attention.Value.Kind != AttentionUnreadable {
		t.Fatalf("crew attention = %+v, want %q", crew.Attention, AttentionUnreadable)
	}
	if !strings.Contains(crew.Attention.Value.Why, errInjectedReadFailure.Error()) {
		t.Fatalf("crew attention why = %q, want the read failure's reason", crew.Attention.Value.Why)
	}
	if !task.Attention.IsKnown() || task.Attention.Value.Kind != AttentionUnreadable {
		t.Fatalf("task attention = %+v, want the unreadable worktree carried up", task.Attention)
	}
}

// TestAttentionUnreadableWhenABindingReadFails is why a failed read is
// itself attention-worthy: the fact "someone must look at this" was
// established even though the value behind it was not. Reporting Absent
// here would hide a row whose health is genuinely unknown.
func TestAttentionUnreadableWhenABindingReadFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "binding read fails", RepoID: f.repoID,
	}); err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	deps := f.deps
	deps.Store = &failingReadsStore{StateStore: f.store, failListBindings: true}

	snap, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	_, task, crew := findCrew(t, snap)
	if !crew.Attention.IsKnown() || crew.Attention.Value.Kind != AttentionUnreadable {
		t.Fatalf("crew attention = %+v, want %q", crew.Attention, AttentionUnreadable)
	}
	if !strings.Contains(crew.Attention.Value.Why, errInjectedReadFailure.Error()) {
		t.Fatalf("crew attention why = %q, want the read failure's reason", crew.Attention.Value.Why)
	}
	if !task.Attention.IsKnown() || task.Attention.Value.Kind != AttentionUnreadable {
		t.Fatalf("task attention = %+v", task.Attention)
	}
}
