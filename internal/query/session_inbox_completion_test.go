package query

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/application"
	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/git"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/persistence"
)

func TestLoadMateInboxIncludesCompletionBesideInteractionWhenMateStopped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{Title: "deliver result", RepoID: f.repoID})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.UpdateCrewStatus(reserved.Crew.CrewID, reserved.Crew.Version, domain.CrewRunning); err != nil {
			return err
		}
		return tx.AppendEvent(observability.Event{EventID: "evt_query_crew_running", EventType: "crew.running", SchemaVersion: 1, OccurredAt: f.deps.Clock.Now(), WorkspaceID: reserved.Crew.WorkspaceID, ProjectID: reserved.Crew.ProjectID, CrewID: reserved.Crew.CrewID})
	}); err != nil {
		t.Fatal(err)
	}
	asked, err := application.AskMate(ctx, f.deps, crewCaller(reserved.Crew.CrewID, reserved.Task.TaskID), application.AskMateRequest{
		ProjectID: f.project.ProjectID, TaskID: reserved.Task.TaskID, CrewID: reserved.Crew.CrewID,
		Question: "is the report useful?", Timeout: 1, NoWait: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(summary, []byte("blocked by provider\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := application.RecordCrewDone(ctx, f.deps, git.NewFake(), crewCaller(reserved.Crew.CrewID, reserved.Task.TaskID), application.CrewDoneRequest{CrewID: reserved.Crew.CrewID, Result: "blocked", SummaryFile: summary}); err != nil {
		t.Fatal(err)
	}
	mate, err := f.store.GetMate(ctx, "mate_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.UpdateMateStatus(mate.MateID, mate.Version, domain.MateStopped); err != nil {
			return err
		}
		return tx.AppendEvent(observability.Event{EventID: "evt_query_mate_stopped", EventType: "mate.stopped", SchemaVersion: 1, OccurredAt: f.deps.Clock.Now(), WorkspaceID: mate.WorkspaceID, ProjectID: mate.ProjectID, AgentID: mate.MateID})
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := LoadMateInbox(ctx, f.deps, "mate_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want interaction and completion", entries)
	}
	if entries[0].Kind != InboxEntryCompletion || entries[0].CompletionID == "" || entries[0].Outcome != "blocked" || entries[0].CompletionStatus != domain.CrewBlocked {
		t.Fatalf("first entry = %+v, want blocked completion", entries[0])
	}
	if entries[1].Kind != InboxEntryInteraction || entries[1].InteractionID != asked.InteractionID {
		t.Fatalf("second entry = %+v, want interaction", entries[1])
	}
}
