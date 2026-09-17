package console

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

func TestCrewIsFinishedIsTheComplementOfOccupiesRepoSlot(t *testing.T) {
	statuses := []query.CrewStatus{
		query.CrewReserved, query.CrewPreparing, query.CrewRunning,
		query.CrewAwaitingReview, query.CrewSucceeded, query.CrewFailed,
		query.CrewBlocked, query.CrewNeedsRebase, query.CrewNeedsRepair,
	}
	for _, s := range statuses {
		if crewIsFinished(s) == s.OccupiesRepoSlot() {
			t.Fatalf("%s: finished=%v occupies=%v, they must not agree", s, crewIsFinished(s), s.OccupiesRepoSlot())
		}
	}
	if crewIsFinished(query.CrewAwaitingReview) {
		t.Fatal("awaiting_review must stay in the active list; the captain still has to review it")
	}
	if !crewIsFinished(query.CrewSucceeded) || !crewIsFinished(query.CrewFailed) {
		t.Fatal("succeeded and failed are the finished statuses the Completed group holds")
	}
}

func TestDefaultCrewListHidesFinishedAttemptsBehindCompletedGroup(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter"))
	rows := m.currentRows()
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want the running Crew and one Completed group", rows)
	}
	if rows[0].kind != rowCrew || rows[0].id != sampleTree().Projects[0].Tasks[0].Crews[1].CrewID {
		t.Fatalf("first row = %+v, want the running attempt", rows[0])
	}
	if rows[1].kind != rowCompletedGroup {
		t.Fatalf("second row = %+v, want the Completed group", rows[1])
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "Completed (1)") {
		t.Fatalf("default view missing Completed group:\n%s", frame)
	}
	if !strings.Contains(flattenWrap(frame), "failed") {
		t.Fatalf("collapsed Completed group must still say a hidden failed attempt needs attention:\n%s", frame)
	}
}

func TestGoldenCompletedGroupCollapsedAndExpanded(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter"))
	assertGolden(t, "attempts-completed-collapsed-120x36-unicode", renderFrame(t, m))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter"))
	assertGolden(t, "attempts-completed-expanded-120x36-unicode", renderFrame(t, m))
}

func TestEnterOnCompletedGroupRevealsFinishedCrewsWithoutDeletingThem(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter"))
	rows := m.currentRows()
	if len(rows) != 3 {
		t.Fatalf("expanded rows = %+v, want running + group + finished Crew", rows)
	}
	if rows[2].kind != rowCrew || rows[2].id != sampleTree().Projects[0].Tasks[0].Crews[0].CrewID {
		t.Fatalf("expanded finished row = %+v, want the failed attempt still in the snapshot", rows[2])
	}
}

func TestJumpToFinishedCrewExpandsCompletedGroup(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	failedID := sampleTree().Projects[0].Tasks[0].Crews[0].CrewID
	var ok bool
	m, ok = m.jumpToCrew(failedID)
	if !ok {
		t.Fatal("jumpToCrew failed to locate the finished Crew")
	}
	r, ok := m.selectedRow()
	if !ok || r.kind != rowCrew || r.id != failedID {
		t.Fatalf("selected = %+v ok=%v, want the finished Crew after jump", r, ok)
	}
	if !m.completedOpen[sampleTree().Projects[0].Tasks[0].TaskID] {
		t.Fatal("jumpToCrew must expand the Completed group so the hidden Crew is selectable")
	}
}

func TestAwaitingReviewStaysInTheActiveCrewList(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Tasks[0].Crews[0].Status = query.CrewAwaitingReview
	tree.Projects[0].Tasks[0].Crews[0].Attention = query.KnownField(query.Attention{Kind: query.AttentionReview, Why: "review"})
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter"))
	rows := m.currentRows()
	if len(rows) != 2 || rows[0].kind != rowCrew || rows[1].kind != rowCrew {
		t.Fatalf("rows = %+v, want both attempts listed because awaiting_review is not finished", rows)
	}
	for _, r := range rows {
		if r.kind == rowCompletedGroup {
			t.Fatalf("awaiting_review must not be hidden in a Completed group: %+v", rows)
		}
	}
}

func TestDiscardRequiresConfirmationAndRunsTheBridge(t *testing.T) {
	calls := 0
	var got ActionRequest
	tree := sampleTree()
	tree.Projects[0].Tasks[0].Crews = []query.CrewNode{tree.Projects[0].Tasks[0].Crews[0]}
	m := New(func(_ context.Context) (query.Snapshot, error) { return tree, nil }, nil,
		func(_ context.Context, req ActionRequest) (string, error) {
			calls++
			got = req
			return "Crew discarded; worktree removed; branch deleted", nil
		})
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("a"))
	m.actionIndex = 5
	m, cmd := send(t, m, key("enter"))
	if cmd != nil || m.confirm == nil || m.confirm.choice.action != ActionDiscard {
		t.Fatalf("discard must open confirmation: cmd=%v confirm=%+v", cmd, m.confirm)
	}
	if calls != 0 {
		t.Fatalf("runner called before confirmation: %d", calls)
	}
	m, cmd = send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("confirming discard did not queue the runner")
	}
	m, _ = send(t, m, cmd())
	if calls != 1 || got.Action != ActionDiscard || got.TargetKind != "crew" || got.Target != tree.Projects[0].Tasks[0].Crews[0].CrewID {
		t.Fatalf("runner request = %+v calls=%d", got, calls)
	}
}

func TestCompletedGroupOfSucceededCrewsDoesNotClaimUnknownAttention(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Tasks[0].Crews = []query.CrewNode{
		{
			CrewID:    "crew_succeeded",
			Attempt:   1,
			Status:    query.CrewSucceeded,
			Attention: query.AbsentField[query.Attention]("attempt succeeded and nothing about it needs attention"),
		},
		tree.Projects[0].Tasks[0].Crews[1],
	}
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down")) // Completed group
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "Completed (1)") {
		t.Fatalf("missing Completed group:\n%s", frame)
	}
	if strings.Contains(flattenWrap(frame), "unknown") {
		t.Fatalf("succeeded-only Completed group must not invent an unknown read:\n%s", frame)
	}
}

func TestFinishedTaskHidesBehindCompletedGroupOnProjectList(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Tasks[1].Status = query.TaskSucceeded
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	rows := m.currentRows()
	var sawGroup, sawReady, sawSucceeded bool
	for _, r := range rows {
		switch {
		case r.kind == rowCompletedGroup:
			sawGroup = true
		case r.kind == rowTask && r.id == tree.Projects[0].Tasks[0].TaskID:
			sawReady = true
		case r.kind == rowTask && r.id == tree.Projects[0].Tasks[1].TaskID:
			sawSucceeded = true
		}
	}
	if !sawReady || sawSucceeded || !sawGroup {
		t.Fatalf("project rows = %+v, want the running Task listed and the succeeded one in Completed", rows)
	}
}
