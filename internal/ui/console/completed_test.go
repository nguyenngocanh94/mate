package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

func TestCrewIsFinishedHoldsOnlyTheRecordedOutcomes(t *testing.T) {
	active := []query.CrewStatus{
		query.CrewReserved, query.CrewPreparing, query.CrewRunning,
		query.CrewAwaitingReview, query.CrewBlocked,
		query.CrewNeedsRebase, query.CrewNeedsRepair,
	}
	for _, s := range active {
		if s.IsFinished() {
			t.Fatalf("%s must stay in the active list", s)
		}
	}
	if query.CrewAwaitingReview.IsFinished() {
		t.Fatal("awaiting_review must stay in the active list; the captain still has to review it")
	}
	if !query.CrewSucceeded.IsFinished() || !query.CrewFailed.IsFinished() {
		t.Fatal("succeeded and failed are the finished statuses the Completed group holds")
	}
}

// intoProjectLevel opens sampleTree's first Project. Its rows are the Mate
// row, the one active Crew, then the Completed group holding the failed one.
func intoProjectLevel(t *testing.T) Model {
	t.Helper()
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, key("enter"))
	return m
}

func TestDefaultCrewListHidesFinishedCrewsBehindCompletedGroup(t *testing.T) {
	m := intoProjectLevel(t)
	rows := m.currentRows()
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want the Mate row, the running Crew and one Completed group", rows)
	}
	if rows[1].kind != rowCrew || rows[1].id != sampleTree().Projects[0].Crews[1].CrewID {
		t.Fatalf("second row = %+v, want the running Crew", rows[1])
	}
	if rows[2].kind != rowCompletedGroup {
		t.Fatalf("third row = %+v, want the Completed group", rows[2])
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "Completed (1)") {
		t.Fatalf("default view missing Completed group:\n%s", frame)
	}
	if !strings.Contains(flattenWrap(frame), "failed") {
		t.Fatalf("collapsed Completed group must still say a hidden failed crew needs attention:\n%s", frame)
	}
}

func TestGoldenCompletedGroupCollapsedAndExpanded(t *testing.T) {
	m := intoProjectLevel(t)
	m, _ = send(t, m, key("down")) // the running Crew
	m, _ = send(t, m, key("down")) // the Completed group
	assertGolden(t, "project-completed-collapsed-120x36-unicode", renderFrame(t, m))
	m, _ = send(t, m, key("enter"))
	assertGolden(t, "project-completed-expanded-120x36-unicode", renderFrame(t, m))
}

func TestEnterOnCompletedGroupRevealsFinishedCrewsWithoutDeletingThem(t *testing.T) {
	m := intoProjectLevel(t)
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter"))
	rows := m.currentRows()
	if len(rows) != 4 {
		t.Fatalf("expanded rows = %+v, want mate + running + group + finished Crew", rows)
	}
	if rows[3].kind != rowCrew || rows[3].id != sampleTree().Projects[0].Crews[0].CrewID {
		t.Fatalf("expanded finished row = %+v, want the failed crew still in the snapshot", rows[3])
	}
}

func TestJumpToFinishedCrewExpandsCompletedGroup(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	failedID := sampleTree().Projects[0].Crews[0].CrewID
	var ok bool
	m, ok = m.jumpToCrew(failedID)
	if !ok {
		t.Fatal("jumpToCrew failed to locate the finished Crew")
	}
	r, ok := m.selectedRow()
	if !ok || r.kind != rowCrew || r.id != failedID {
		t.Fatalf("selected = %+v ok=%v, want the finished Crew after jump", r, ok)
	}
	if !m.completedOpen[sampleTree().Projects[0].ProjectID] {
		t.Fatal("jumpToCrew must expand the Completed group so the hidden Crew is selectable")
	}
}

func TestAwaitingReviewStaysInTheActiveCrewList(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Crews[0].Status = query.CrewAwaitingReview
	tree.Projects[0].Crews[0].Attention = query.KnownField(query.Attention{Kind: query.AttentionReview, Why: "review"})
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	rows := m.currentRows()
	if len(rows) != 3 || rows[1].kind != rowCrew || rows[2].kind != rowCrew {
		t.Fatalf("rows = %+v, want both crews listed because awaiting_review is not finished", rows)
	}
	for _, r := range rows {
		if r.kind == rowCompletedGroup {
			t.Fatalf("awaiting_review must not be hidden in a Completed group: %+v", rows)
		}
	}
}

func TestCompletedGroupOfSucceededCrewsDoesNotClaimUnknownAttention(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Crews = []query.CrewNode{
		{
			CrewID:    "crew_succeeded",
			Status:    query.CrewSucceeded,
			Attention: query.AbsentField[query.Attention]("the crew succeeded and nothing about it needs attention"),
		},
		tree.Projects[0].Crews[1],
	}
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down")) // Completed group
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "Completed (1)") {
		t.Fatalf("missing Completed group:\n%s", frame)
	}
	if strings.Contains(flattenWrap(frame), "unknown") {
		t.Fatalf("succeeded-only Completed group must not invent an unknown read:\n%s", frame)
	}
}
