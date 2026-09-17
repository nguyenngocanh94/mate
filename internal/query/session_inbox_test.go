package query

import (
	"context"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/application"
	"github.com/nguyenngocanh94/matev2/internal/domain"
)

// crewCaller builds the Crew caller AskMate requires, scoped to one attempt.
func crewCaller(crewID, taskID string) application.Caller {
	return application.Caller{Role: domain.RoleCrew, AgentID: crewID, CrewID: crewID, TaskID: taskID}
}

// TestLoadMateInboxHealthyPath is the ADR 0025 step 5 happy path: a Crew's
// AskMate question, read back through LoadMateInbox exactly the way the
// Console session view's inbox rail needs it - newest first, Awaiting true
// while the question is unanswered, Attempt/Task resolved from the real
// Crew/Task rows, and no Note fabricated.
func TestLoadMateInboxHealthyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "fix the flaky test", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	caller := crewCaller(reserved.Crew.CrewID, reserved.Task.TaskID)
	asked, err := application.AskMate(ctx, f.deps, caller, application.AskMateRequest{
		ProjectID: f.project.ProjectID, TaskID: reserved.Task.TaskID, CrewID: reserved.Crew.CrewID,
		Question: "Should I rebase onto main?", Timeout: 1, NoWait: true,
	})
	if err != nil {
		t.Fatalf("AskMate: %v", err)
	}

	entries, err := LoadMateInbox(ctx, f.deps, "mate_1")
	if err != nil {
		t.Fatalf("LoadMateInbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want 1", entries)
	}
	e := entries[0]
	if e.InteractionID != asked.InteractionID {
		t.Fatalf("interaction id = %q, want %q", e.InteractionID, asked.InteractionID)
	}
	if e.Status != domain.InteractionQueued || !e.Awaiting {
		t.Fatalf("status = %q awaiting = %v, want queued/awaiting", e.Status, e.Awaiting)
	}
	if e.Question != "Should I rebase onto main?" {
		t.Fatalf("question = %q", e.Question)
	}
	if e.Reply != "" {
		t.Fatalf("reply = %q, want empty before ReplyToCrew", e.Reply)
	}
	if e.Attempt != "attempt 1" {
		t.Fatalf("attempt = %q, want %q", e.Attempt, "attempt 1")
	}
	if e.Task != "fix the flaky test" {
		t.Fatalf("task = %q, want the Task's own title", e.Task)
	}
	if e.Note != "" {
		t.Fatalf("note = %q, want empty for an ordinary queued question", e.Note)
	}
}

// TestLoadMateInboxAnsweredNotAwaiting: once ReplyToCrew has recorded an
// answer - through the application use case, never re-derived here - the
// entry must no longer report Awaiting, and must carry the reply text.
func TestLoadMateInboxAnsweredNotAwaiting(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "ship the feature", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	caller := crewCaller(reserved.Crew.CrewID, reserved.Task.TaskID)
	asked, err := application.AskMate(ctx, f.deps, caller, application.AskMateRequest{
		ProjectID: f.project.ProjectID, TaskID: reserved.Task.TaskID, CrewID: reserved.Crew.CrewID,
		Question: "Ready to merge?", Timeout: 1, NoWait: true,
	})
	if err != nil {
		t.Fatalf("AskMate: %v", err)
	}
	mateCaller := application.Caller{Role: domain.RoleMate, AgentID: "mate_1"}
	if _, err := application.ReplyToCrew(ctx, f.deps, mateCaller, application.ReplyToCrewRequest{
		InteractionID: asked.InteractionID, Message: "yes, go ahead",
	}); err != nil {
		t.Fatalf("ReplyToCrew: %v", err)
	}

	entries, err := LoadMateInbox(ctx, f.deps, "mate_1")
	if err != nil {
		t.Fatalf("LoadMateInbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want 1", entries)
	}
	e := entries[0]
	if e.Status != domain.InteractionAnswered || e.Awaiting {
		t.Fatalf("status = %q awaiting = %v, want answered/not-awaiting", e.Status, e.Awaiting)
	}
	if e.Reply != "yes, go ahead" {
		t.Fatalf("reply = %q", e.Reply)
	}
}

// TestLoadMateInboxNewestFirstAndScoped: LoadMateInbox must return entries
// newest first (session_render.go's rail/digest both drop from the tail
// assuming that order) and must never include a question addressed to a
// different Mate.
func TestLoadMateInboxNewestFirstAndScoped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "first task", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	caller := crewCaller(reserved.Crew.CrewID, reserved.Task.TaskID)
	first, err := application.AskMate(ctx, f.deps, caller, application.AskMateRequest{
		ProjectID: f.project.ProjectID, TaskID: reserved.Task.TaskID, CrewID: reserved.Crew.CrewID,
		Question: "first question", Timeout: 1, NoWait: true,
	})
	if err != nil {
		t.Fatalf("AskMate (first): %v", err)
	}
	f.deps.Clock.(*domain.FakeClock).Advance(time.Minute)
	second, err := application.AskMate(ctx, f.deps, caller, application.AskMateRequest{
		ProjectID: f.project.ProjectID, TaskID: reserved.Task.TaskID, CrewID: reserved.Crew.CrewID,
		Question: "second question", Timeout: 1, NoWait: true,
	})
	if err != nil {
		t.Fatalf("AskMate (second): %v", err)
	}

	entries, err := LoadMateInbox(ctx, f.deps, "mate_1")
	if err != nil {
		t.Fatalf("LoadMateInbox: %v", err)
	}
	if len(entries) != 2 || entries[0].InteractionID != second.InteractionID || entries[1].InteractionID != first.InteractionID {
		t.Fatalf("entries = %+v, want [second, first] newest first", entries)
	}

	if entries, err := LoadMateInbox(ctx, f.deps, "mate_no_such_id"); err != nil || len(entries) != 0 {
		t.Fatalf("a Mate with no addressed interactions must get an empty rail, not another Mate's: entries=%+v err=%v", entries, err)
	}
}
