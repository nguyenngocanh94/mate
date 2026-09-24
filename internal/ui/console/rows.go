package console

import (
	"github.com/nguyenngocanh94/mate/internal/query"
)

// rowKind names what a rendered line refers to, so Enter and the cursor can
// stay generic across levels.
type rowKind int

const (
	rowProject rowKind = iota
	rowMate
	rowCrew
	// rowCompletedGroup is the collapsed/expanded "Completed (N)" row that
	// holds a Project's finished Crews. It is presentation only: the
	// records stay in the snapshot.
	rowCompletedGroup
)

// row is one selectable row of the current frame. idx indexes the slice
// rowKind names within the model's current selection (snap.Projects or the
// current Project's Crews) - list.go's column builders use it to look the
// underlying node back up; rowMate carries no idx, since a Project has at
// most one designated Mate.
//
// id is the row's identity - the Project or Crew id, or the reserved
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
	rows := make([]row, 0, 1+len(p.Crews)+1)
	rows = append(rows, row{kind: rowMate, id: mateRowID(p.Mate)})
	var finished []row
	for i, c := range p.Crews {
		r := row{kind: rowCrew, idx: i, id: c.CrewID}
		if c.Closed {
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

func completedGroupID(parentID string) string { return "completed:" + parentID }
