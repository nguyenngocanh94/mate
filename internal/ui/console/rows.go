package console

import (
	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/query"
)

// rowKind names what a rendered line refers to, so Enter and the cursor can
// stay generic across levels.
type rowKind int

const (
	rowProject rowKind = iota
	rowMate
	rowTask
	rowCrew
	// rowCompletedGroup is the collapsed/expanded "Completed (N)" row that
	// holds finished Crews (on a Task frame) or finished Tasks (on a
	// Project frame). It is presentation only: the records stay in the
	// snapshot.
	rowCompletedGroup
)

// row is one selectable row of the current frame. idx indexes the slice
// rowKind names within the model's current selection (snap.Projects, the
// current Project's Tasks, or the current Task's Crews) - list.go's column
// builders use it to look the underlying node back up; rowMate carries no
// idx, since a Project has at most one designated Mate.
//
// id is the row's identity - the Project, Task or Crew id, or the reserved
// "mate:none" for a Project with no designated Mate. It is what a frame's
// selID stores, so a refresh that inserted or removed rows above the
// selection re-finds the same row rather than the same index
// (Model.reconcileSelection).
type row struct {
	kind rowKind
	idx  int
	id   string
}

func projectRows(projects []query.ProjectNode) []row {
	rows := make([]row, 0, len(projects))
	for i, p := range projects {
		rows = append(rows, row{kind: rowProject, idx: i, id: p.ProjectID})
	}
	return rows
}

func projectDetailRows(p query.ProjectNode, completedOpen bool) []row {
	rows := make([]row, 0, 1+len(p.Tasks)+1)
	rows = append(rows, row{kind: rowMate, id: mateRowID(p.Mate)})
	var finished []row
	for i, t := range p.Tasks {
		r := row{kind: rowTask, idx: i, id: t.TaskID}
		if taskIsFinished(t.Status) {
			finished = append(finished, r)
			continue
		}
		rows = append(rows, r)
	}
	if len(finished) == 0 {
		return rows
	}
	rows = append(rows, row{kind: rowCompletedGroup, idx: -1, id: completedGroupID(p.ProjectID)})
	if completedOpen {
		rows = append(rows, finished...)
	}
	return rows
}

// mateRowID keeps the Mate row selectable even when there is no Mate: the
// row still exists (it says so), so it still needs an identity a frame's
// selID can hold across a refresh.
func mateRowID(mate query.MateNode) string {
	if mate.Designated.IsKnown() && mate.Designated.Value.MateID != "" {
		return mate.Designated.Value.MateID
	}
	return "mate:none"
}

func crewRows(t query.TaskNode, completedOpen bool) []row {
	rows := make([]row, 0, len(t.Crews)+1)
	var finished []row
	for i, c := range t.Crews {
		r := row{kind: rowCrew, idx: i, id: c.CrewID}
		if crewIsFinished(c.Status) {
			finished = append(finished, r)
			continue
		}
		rows = append(rows, r)
	}
	if len(finished) == 0 {
		return rows
	}
	rows = append(rows, row{kind: rowCompletedGroup, idx: -1, id: completedGroupID(t.TaskID)})
	if completedOpen {
		rows = append(rows, finished...)
	}
	return rows
}

func completedGroupID(parentID string) string { return "completed:" + parentID }

// crewIsFinished is the Console's presentation predicate: OccupiesRepoSlot's
// complement (succeeded/failed today) drops out of the active list into the
// Completed group. awaiting_review occupies the slot, so it stays visible.
func crewIsFinished(s domain.CrewStatus) bool {
	return !s.OccupiesRepoSlot()
}

// taskIsFinished hides Tasks whose outcome is already recorded. awaiting_review
// stays in the active list for the same reason a Crew does.
func taskIsFinished(s domain.TaskStatus) bool {
	return s == domain.TaskSucceeded || s == domain.TaskFailed || s == domain.TaskCancelled
}
