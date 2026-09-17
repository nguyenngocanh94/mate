package query

import (
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/application"
	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/persistence"
)

// TestRepoResolvesFromTheProjectsOwnRepoList checks the repo the inspector
// renders for a Task and for an attempt, and that it is resolved from the
// Project's one repo read rather than a read per row.
func TestRepoResolvesFromTheProjectsOwnRepoList(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "has a repo", RepoID: f.repoID,
	}); err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	proj, task, crew := findCrew(t, snap)
	if !proj.Repos.IsKnown() || len(proj.Repos.Value) != 1 {
		t.Fatalf("project repos = %+v, want the one registered repo", proj.Repos)
	}
	if !task.Repo.IsKnown() || task.Repo.Value.DisplayName != "sample" {
		t.Fatalf("task repo = %+v", task.Repo)
	}
	if !crew.Repo.IsKnown() || crew.Repo.Value.RepoID != f.repoID {
		t.Fatalf("crew repo = %+v", crew.Repo)
	}
	if crew.Repo.Value.DefaultBranch != "main" || !strings.HasSuffix(crew.Repo.Value.Path, "/repos/sample") {
		t.Fatalf("crew repo detail = %+v", crew.Repo.Value)
	}
}

// TestRepoUnknownWhenTheRepoListReadFails is the Unknown branch of every
// row's Repo: the failure must propagate from the Project's one repo read
// into each Task and attempt that resolves against it, and must not decay
// into Absent - "this task has no repo" is a different sentence from "the
// repo list could not be read".
func TestRepoUnknownWhenTheRepoListReadFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "repo read fails", RepoID: f.repoID,
	}); err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	deps := f.deps
	deps.Store = &failingReadsStore{StateStore: f.store, failListRepos: true}

	snap, err := LoadSnapshot(ctx, deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	proj, task, crew := findCrew(t, snap)
	for name, state := range map[string]FieldState{
		"project repos": proj.Repos.State, "task repo": task.Repo.State, "crew repo": crew.Repo.State,
	} {
		if state != Unknown {
			t.Fatalf("%s state = %q, want %q", name, state, Unknown)
		}
	}
	if !hasWarning(snap, "repos", proj.ProjectID) || !hasWarning(snap, "repo", crew.CrewID) {
		t.Fatalf("warnings = %+v, want the project's repo list and the crew's repo", snap.Warnings)
	}
}

// TestRepoAbsentWhenTheTaskNamesNoneOrAnUnregisteredOne separates the two
// Absent cases from a read failure: a draft Task has no repo yet, and an id
// that is not in the Project's list is a recorded inconsistency the read
// did establish.
func TestRepoAbsentWhenTheTaskNamesNoneOrAnUnregisteredOne(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := application.CreateTask(ctx, f.deps, userCaller(), application.CreateTaskRequest{
		Title: "no repo yet", ProjectID: f.project.ProjectID,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	danglingTask := "tsk_dangling"
	workspaceID := mustWorkspaceID(t, f)
	err := f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.InsertTask(persistence.TaskRecord{
			TaskID: danglingTask, WorkspaceID: workspaceID, ProjectID: f.project.ProjectID,
			Title: "names a repo nobody registered", Status: domain.TaskDraft,
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, workspaceID, f.project.ProjectID)
	})
	if err != nil {
		t.Fatalf("seed dangling task: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	for _, task := range snap.Projects[0].Tasks {
		if task.Repo.State != Absent {
			t.Fatalf("task %q repo = %+v, want Absent", task.Title, task.Repo)
		}
		if task.Repo.Reason == "" {
			t.Fatalf("task %q Absent repo carries no reason", task.Title)
		}
	}
	if hasWarning(snap, "repo", "") {
		t.Fatalf("an Absent repo must not produce a warning: %+v", snap.Warnings)
	}
}

// TestRepoAbsentWhenATaskNamesARepoRegisteredInAnotherProject pins the
// resolveRepo branch at read.go:132, which is reachable specifically
// because task.repo_id references repo(repo_id) with no project-scoped
// check (migrations/0006): a Task in one Project can legally name a repo
// registered in another. TestRepoAbsentWhenTheTaskNamesNoneOrAnUnregisteredOne
// seeds only tasks with an empty repo_id, which reaches the repoID == ""
// branch instead - this test gives the "repo X is not registered in this
// project" sentence its own reachable case.
func TestRepoAbsentWhenATaskNamesARepoRegisteredInAnotherProject(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	other, err := application.CreateProject(ctx, f.deps, userCaller(), "elsewhere")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	workspaceID := mustWorkspaceID(t, f)
	const elsewhereRepo = "repo_elsewhere"
	const crossTask = "tsk_cross_project_repo"
	err = f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.InsertRepo(persistence.RepoRecord{
			RepoID: elsewhereRepo, WorkspaceID: workspaceID, ProjectID: other.Project.ProjectID,
			Path: "/tmp/elsewhere", DisplayName: "elsewhere", DefaultBranch: "main",
			ValidationStatus: persistence.RepoValidated,
		}); err != nil {
			return err
		}
		if err := tx.InsertTask(persistence.TaskRecord{
			TaskID: crossTask, WorkspaceID: workspaceID, ProjectID: f.project.ProjectID,
			RepoID: elsewhereRepo, Title: "names a repo registered elsewhere", Status: domain.TaskDraft,
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, workspaceID, f.project.ProjectID)
	})
	if err != nil {
		t.Fatalf("seed cross-project task: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	var task *TaskNode
	for _, proj := range snap.Projects {
		if proj.ProjectID != f.project.ProjectID {
			continue
		}
		for i := range proj.Tasks {
			if proj.Tasks[i].TaskID == crossTask {
				task = &proj.Tasks[i]
			}
		}
	}
	if task == nil {
		t.Fatalf("cross-project task missing from its own project: %+v", snap.Projects)
	}
	if task.Repo.State != Absent {
		t.Fatalf("task repo = %+v, want Absent (repo read succeeded; the id just is not registered here)", task.Repo)
	}
	if !strings.Contains(task.Repo.Reason, elsewhereRepo) || !strings.Contains(task.Repo.Reason, "is not registered in this project") {
		t.Fatalf("task repo reason = %q, want it to name %q as not registered in this project", task.Repo.Reason, elsewhereRepo)
	}
}

// TestRetryLinkageNamesThePreviousAttempt covers the inspector's "Retry of
// attempt 1 · crew_…" line. Retry is not a separate command (G5-09):
// re-spawning a Task whose only prior attempt is terminal links
// automatically, so the linkage is a recorded fact and the attempt *number*
// is resolved from the Task's own attempts with no extra read.
func TestRetryLinkageNamesThePreviousAttempt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	first, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "retry me", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	if err := application.AbandonCrewAttempt(ctx, f.deps, userCaller(), "", first.Crew, "launch failed"); err != nil {
		t.Fatalf("AbandonCrewAttempt: %v", err)
	}
	second, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		TaskID: first.Task.TaskID, RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt (retry): %v", err)
	}
	if second.Crew.RetryOfCrewID != first.Crew.CrewID {
		t.Fatalf("precondition failed: retry_of = %q, want %q", second.Crew.RetryOfCrewID, first.Crew.CrewID)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	task := snap.Projects[0].Tasks[0]
	if len(task.Crews) != 2 {
		t.Fatalf("attempts = %d, want 2: %+v", len(task.Crews), task.Crews)
	}
	// ListCrews orders by (created_at, attempt), so the latest attempt - the
	// one the list's ATTEMPTS count and the task's attention read - is last.
	if task.Crews[0].RetryOf.State != Absent {
		t.Fatalf("first attempt retry-of = %+v, want Absent", task.Crews[0].RetryOf)
	}
	if task.Crews[0].RetryOf.Reason == "" {
		t.Fatal("an Absent retry-of must say why: it is a first attempt")
	}
	retry := task.Crews[1].RetryOf
	if !retry.IsKnown() || retry.Value.CrewID != first.Crew.CrewID || retry.Value.Attempt != first.Crew.Attempt {
		t.Fatalf("retry-of = %+v, want attempt %d / %s", retry, first.Crew.Attempt, first.Crew.CrewID)
	}
}

// TestRetryLinkageAbsentWhenTheAttemptIsElsewhere is the recorded-
// inconsistency branch: the linkage id was read successfully, but the
// attempt it names is not among this Task's attempts, so the whole Field
// is Absent - carrying the unresolved crew id in its Reason - rather than
// Known with Attempt silently left at its zero value.
//
// A retry_of_crew_id that names no crew at all is impossible - the column
// is a foreign key - so what this seeds is the case the schema does still
// allow: a retry pointing at another Task's attempt. application.
// ReserveCrewAttempt only ever links within one Task, so this is a shape no
// current writer produces and no constraint forbids.
func TestRetryLinkageAbsentWhenTheAttemptIsElsewhere(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	other, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "another task's attempt", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	taskID, crewID := "tsk_orphan", "crew_orphan"
	workspaceID := mustWorkspaceID(t, f)
	err = f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.InsertTask(persistence.TaskRecord{
			TaskID: taskID, WorkspaceID: workspaceID, ProjectID: f.project.ProjectID,
			RepoID: f.repoID, Title: "orphan retry", Status: domain.TaskRunning,
		}); err != nil {
			return err
		}
		if _, err := tx.ReserveCrew(persistence.CrewRecord{
			CrewID: crewID, WorkspaceID: workspaceID, ProjectID: f.project.ProjectID,
			TaskID: taskID, RepoID: f.repoID, Attempt: 2, HarnessKind: domain.HarnessClaude,
			RetryOfCrewID: other.Crew.CrewID,
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, workspaceID, f.project.ProjectID)
	})
	if err != nil {
		t.Fatalf("seed orphan retry: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	var crew *CrewNode
	for i := range snap.Projects[0].Tasks {
		task := snap.Projects[0].Tasks[i]
		if task.TaskID == taskID && len(task.Crews) == 1 {
			crew = &task.Crews[0]
		}
	}
	if crew == nil {
		t.Fatalf("the orphan task's attempt is missing: %+v", snap.Projects[0].Tasks)
	}
	if crew.RetryOf.State != Absent {
		t.Fatalf("retry-of = %+v, want Absent: the linked attempt is not among this task's attempts", crew.RetryOf)
	}
	if !strings.Contains(crew.RetryOf.Reason, other.Crew.CrewID) {
		t.Fatalf("retry-of reason = %q, want it to name the unresolved crew %s", crew.RetryOf.Reason, other.Crew.CrewID)
	}
}

// TestTaskCarriesItsBrief is the inspector's Brief line, which the task
// list does not show and so had no reader before the redesign.
func TestTaskCarriesItsBrief(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	if _, err := application.CreateTask(ctx, f.deps, userCaller(), application.CreateTaskRequest{
		Title: "titled", Brief: "the long form of what to do", RepoID: f.repoID,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	snap, err := LoadSnapshot(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if got := snap.Projects[0].Tasks[0].Brief; got != "the long form of what to do" {
		t.Fatalf("brief = %q", got)
	}
}
