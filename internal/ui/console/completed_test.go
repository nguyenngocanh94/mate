package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The Completed group holds crews whose task is over - finished and failed,
// the two states `crew stop` writes (mvp.md section 4b) - behind one row.
func TestOnlyTheTwoTerminalStatesAreClosed(t *testing.T) {
	for _, s := range []query.CrewStatus{
		query.CrewSpawned, query.CrewWorking,
		query.CrewNeedsDecision, query.CrewWaitMate, query.CrewBlocked,
	} {
		if s.Closed() {
			t.Fatalf("%s must stay in the active list", s)
		}
	}
	if !query.CrewFinished.Closed() || !query.CrewFailed.Closed() {
		t.Fatal("finished and failed are the terminal states the Completed group holds")
	}
}

func mdlIntoProject(t *testing.T) Model {
	t.Helper()
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, key("enter"))
	return m
}

// The collapsed group keeps its failure outside: "! 1 failed".
func TestDefaultCrewListHidesFinishedCrewsBehindCompletedGroup(t *testing.T) {
	m := mdlIntoProject(t)
	rows := m.currentRows()
	if len(rows) != 3 || rows[1].kind != rowCrew || rows[1].id != sampleTree().Projects[0].Crews[1].CrewID || rows[2].kind != rowCompletedGroup {
		t.Fatalf("rows = %+v, want the Mate, the running Crew and one Completed group", rows)
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "▸ Completed (1)") || !strings.Contains(frame, "! 1 failed") {
		t.Fatalf("collapsed group must say it holds a failed crew:\n%s", frame)
	}
}

func TestEnterOnCompletedGroupRevealsFinishedCrewsWithoutDeletingThem(t *testing.T) {
	m := mdlIntoProject(t)
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter"))
	rows := m.currentRows()
	if len(rows) != 4 || rows[3].kind != rowCrew || rows[3].id != sampleTree().Projects[0].Crews[0].CrewID {
		t.Fatalf("expanded rows = %+v, want the failed crew still listed", rows)
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "▾ Completed (1)") {
		t.Fatalf("the open group is not marked open:\n%s", frame)
	}
	m, _ = send(t, m, key("enter"))
	if len(m.currentRows()) != 3 {
		t.Fatalf("Enter again must collapse the group, rows = %+v", m.currentRows())
	}
}

// A click on the group's row toggles it, the same as Enter.
func TestClickOnCompletedGroupTogglesIt(t *testing.T) {
	m := mdlIntoProject(t)
	lines := strings.Split(renderFrame(t, m), "\n")
	y := -1
	for i, l := range lines {
		if strings.Contains(l, "Completed (1)") {
			y = i
		}
	}
	if y < 0 {
		t.Fatal("precondition: the group row is not drawn")
	}
	m, _ = send(t, m, tea.MouseMsg{X: 4, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !m.completedOpen[m.cur().id] {
		t.Fatal("a click on the Completed row did not open it")
	}
}

func TestJumpToFinishedCrewExpandsCompletedGroup(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	failedID := sampleTree().Projects[0].Crews[0].CrewID
	m, ok := m.jumpToCrew(failedID)
	if !ok {
		t.Fatal("jumpToCrew failed to locate the finished Crew")
	}
	if r, ok := m.selectedRow(); !ok || r.kind != rowCrew || r.id != failedID {
		t.Fatalf("selected = %+v ok=%v, want the finished Crew", r, ok)
	}
	if !m.completedOpen[sampleTree().Projects[0].ProjectID] {
		t.Fatal("jumpToCrew must expand the Completed group so the hidden Crew is selectable")
	}
}

// A crew that has handed its work back is still in flight until somebody
// runs `crew stop` (2026-09-18).
func TestWaitMateStaysInTheActiveCrewList(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Crews[0].Status = query.CrewWaitMate
	tree.Projects[0].Crews[0].Closed = false
	tree.Projects[0].Crews[0].Attention = query.AbsentField[query.Attention]("the crew reported and waits on the Mate")
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	for _, r := range m.currentRows() {
		if r.kind == rowCompletedGroup {
			t.Fatalf("wait-mate must not be hidden in a Completed group: %+v", m.currentRows())
		}
	}
	if !strings.Contains(renderFrame(t, m), "wait-mate") {
		t.Fatalf("the wait-mate crew's status is not drawn:\n%s", renderFrame(t, m))
	}
}

// A group of finished crews does not invent a failure or an unknown read.
func TestCompletedGroupOfFinishedCrewsDoesNotClaimAProblem(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Crews = []query.CrewNode{
		{
			CrewID: "crew_finished", Status: query.CrewFinished, Closed: true,
			Attention: query.AbsentField[query.Attention]("the crew finished and nothing about it needs attention"),
		},
		tree.Projects[0].Crews[1],
	}
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down"))
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "Completed (1)") {
		t.Fatalf("missing Completed group:\n%s", frame)
	}
	var group string
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "Completed (1)") {
			group = l
		}
	}
	if strings.Contains(group, "!") || strings.Contains(group, "failed") || strings.Contains(group, "unknown") {
		t.Fatalf("a finished-only group claims a problem: %q", group)
	}
}
