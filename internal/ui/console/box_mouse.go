package console

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// ---------- clickable labels ----------

// labelID names one clickable label, so a hit test returns an identity
// rather than a rectangle the caller has to interpret.
type labelID int

const (
	labelNone labelID = iota
	// labelAssign is the box entry's own button.
	labelAssign
)

// labelSpec is a label's identity and the exact text drawn for it. The two
// travel together so the renderer and the hit test measure the same string.
type labelSpec struct {
	id   labelID
	text string
}

// placedLabel is a labelSpec with the frame coordinates it was drawn at.
type placedLabel struct {
	labelSpec
	x, y int
}

func (l placedLabel) hit(x, y int) bool {
	return y == l.y && x >= l.x && x < l.x+cells(l.text)
}

// boxStripButtons is the trailing action strip an attention entry grows
// when it is selected or hovered. There is one button, and it is the one
// thing an inbox row does that is not "go and look": hand the item to the
// Mate (2026-09-19). Everything else a reader wants with a waiting crew is
// in that crew's own pane, and the rest of the row opens it - so the strip
// never competes with the row it sits on.
func boxStripButtons(g glyphSet) []labelSpec {
	return []labelSpec{{labelAssign, "[assign]"}}
}

// boxStripWidth is how many cells the strip occupies, including the single
// spaces between its buttons and the one that separates it from the entry's
// own text.
func boxStripWidth(g glyphSet) int {
	total := 1
	for i, b := range boxStripButtons(g) {
		if i > 0 {
			total++
		}
		total += cells(b.text)
	}
	return total
}

const (
	// boxEntryLead is the marker, attention mark and separating space every
	// entry line starts with (box.go's boxEntryLine).
	boxEntryLead = 3
	// boxStripMinText is how much of an entry's own words must survive for
	// the strip to be worth drawing over them.
	boxStripMinText = 10
)

// boxStripFits reports whether an entry line w cells wide can carry the
// strip and still show a readable amount of the entry's own words. The
// renderer and the hit test both ask this, so a strip that was not drawn is
// never clickable and one that was drawn always is.
func boxStripFits(w int, g glyphSet) bool {
	return w-boxEntryLead-boxStripWidth(g) >= boxStripMinText
}

// boxStripPlacement returns the strip's buttons at their frame
// coordinates, given the entry line's left edge and width. ok is false when
// the line is too narrow, which is also exactly when the renderer draws no
// strip.
func boxStripPlacement(x0, y, w int, g glyphSet) ([]placedLabel, bool) {
	if !boxStripFits(w, g) {
		return nil, false
	}
	at := x0 + w - boxStripWidth(g) + 1
	var out []placedLabel
	for i, b := range boxStripButtons(g) {
		if i > 0 {
			at++
		}
		out = append(out, placedLabel{labelSpec: b, x: at, y: y})
		at += cells(b.text)
	}
	return out, true
}

// boxEntryAt maps a body row of a box pane back to the entry drawn on
// it. It mirrors boxBodyLines' own windowing - the same plan, the same
// scroll offset, the same top padding - rather than storing what was drawn,
// so a click resolves against the frame the reader is actually looking at.
func boxEntryAt(b boxList, sel, h, row, w int) (int, bool) {
	if h <= 0 || row < 0 || row >= h || !b.known() || len(b.rows()) == 0 {
		return 0, false
	}
	plan := boxPlan(b, sel, w)
	start, end := window(len(plan), boxPlanTop(plan, sel, h), h)
	pad := h - (end - start)
	if row < pad {
		return 0, false
	}
	i := start + (row - pad)
	if i >= end {
		return 0, false
	}
	return plan[i].index, true
}

// boxEntryStrip is the action strip of the entry at one index, when
// that entry has one: only an attention entry that is selected or hovered
// grows its button, which is the same condition boxEntryLine draws it
// under.
func boxEntryStrip(b boxList, sel, hover, index, x0, row, w int, g glyphSet) ([]placedLabel, bool) {
	e, ok := boxSelectedEntry(b, index)
	if !ok || !boxEntryHasStrip(e, index == sel, index == hover) {
		return nil, false
	}
	return boxStripPlacement(x0, row, w, g)
}

// boxEntryHasStrip is the one predicate the renderer and the hit test share
// for "does this entry show its button": an entry that needs a decision,
// is blocked, finished or failed, or is an incident - which is exactly what
// internal/query already decided when it set Attention - and only while the
// reader's cursor or pointer is on it.
//
// An entry already handed to the Mate (mvp.md task 30) shows no button: a
// second press would only be told "already assigned", and the row's own
// words - "assigned, queued" or "assigned HH:MM" - need the columns the
// strip would take from them at the rail's width.
func boxEntryHasStrip(e query.BoxEntry, selected, hovered bool) bool {
	return e.Attention && e.Assigned.State == "" && (selected || hovered)
}

// runBoxEntryAction is the entry's one strip button.
func (m Model) runBoxEntryAction(id labelID, project string, b boxList, index int) (tea.Model, tea.Cmd) {
	if id == labelAssign {
		return m.beginBoxAssign(project, b, index)
	}
	return m, nil
}

// onFrameMouse is the mouse outside the session view. A press on a list
// Mate/Crew row is Enter (docs/mvp.md M10). The box panel still owns
// presses on its own rows.
func (m Model) onFrameMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	ev := tea.MouseEvent(msg)
	// The diff overlay takes the wheel over the whole frame while it is
	// open (diff.go): it is a full-region reader, and a wheel that scrolled
	// the box panel underneath it would move something nobody can see.
	if m.diff.open {
		switch ev.Button {
		case tea.MouseButtonWheelUp:
			return m.scrollDiff(-1, layout(m.w, m.h)), nil
		case tea.MouseButtonWheelDown:
			return m.scrollDiff(1, layout(m.w, m.h)), nil
		}
		return m, nil
	}
	if m.actions || m.actionInputMode || m.harnessPick || m.confirm != nil {
		return m, nil
	}
	if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
		if idx, ok := m.listRowIndexAt(ev.X, ev.Y); ok {
			rows := m.currentRows()
			f := m.cur()
			f.sel = idx
			f.selID = rows[idx].id
			m = m.setCur(f)
			m.focus = paneList
			m.inspTop = 0
			m.msg = footerMsg{}
			r := rows[idx]
			if r.kind == rowMate || r.kind == rowCrew {
				return m.onEnter()
			}
			return m, nil
		}
	}
	top, h, ok := m.boxPanelBodyRegion()
	if !ok || ev.Y < top || ev.Y >= top+h {
		return m, nil
	}
	b, has := m.projectBox()
	sel := m.projectBoxSelection()
	switch ev.Button {
	case tea.MouseButtonWheelUp:
		if has {
			m.boxSel = clampInt(sel-1, 0, len(b.rows())-1)
		}
		return m, nil
	case tea.MouseButtonWheelDown:
		if has {
			m.boxSel = clampInt(sel+1, 0, len(b.rows())-1)
		}
		return m, nil
	}
	index, onEntry := -1, false
	if has {
		index, onEntry = boxEntryAt(b, sel, h, ev.Y-top, m.w)
	}
	if ev.Action == tea.MouseActionMotion {
		m.boxHover = -1
		if onEntry && m.focus == paneBox {
			m.boxHover = index
		}
		return m, nil
	}
	if ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft {
		return m, nil
	}
	m.focus = paneBox
	if !onEntry {
		return m, nil
	}
	if strip, ok := boxEntryStrip(b, sel, m.boxHover, index, 0, ev.Y, m.w, m.g); ok {
		for _, button := range strip {
			if button.hit(ev.X, ev.Y) {
				m.boxSel = index
				return m.runBoxEntryAction(button.id, m.currentProject().ProjectID, b, index)
			}
		}
	}
	m.boxSel, m.msg, m.boxMsg = index, footerMsg{}, footerMsg{}
	e, has := boxSelectedEntry(b, index)
	if !has {
		return m, nil
	}
	return m.openBoxCrew(e)
}

// boxPanelBodyRegion is the frame rows the project frame's box panel draws
// its entries on: the panel sits on the bottom edge of the main region,
// under its own rule and title line (boxPanelLines). It is derived from the
// same boxRegion predicate the renderer and listLayout use, so the rows a
// click resolves against are the rows the panel was given.
func (m Model) boxPanelBodyRegion() (top, height int, ok bool) {
	if m.cur().kind != frameProject || m.phase != phaseReady {
		return 0, 0, false
	}
	l := layout(m.w, m.h)
	boxH, panel := m.boxRegion(l)
	if !panel || boxH <= 2 {
		return 0, 0, false
	}
	const panelChrome = 2 // the full-width rule and the title line
	return frameBodyTop + l.Body - boxH + panelChrome, boxH - panelChrome, true
}
