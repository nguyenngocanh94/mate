package console

import (
	tea "github.com/charmbracelet/bubbletea"
)

// The Console's mouse. Every event is resolved against sessionGeometry
// (session_focus.go) - the same geometry the renderer drew from - so a
// click can only ever act on what is under the pointer.
//
// Three rules hold everywhere here:
//
//   - A click inside a zone focuses that zone. Focus is never changed by a
//     wheel or a bare motion, because a reader scrolling to read something
//     has not decided to type into it.
//   - Nothing reaches the PTY unless the pointer is inside the terminal
//     zone, and then the coordinates are translated to that pane's own
//     origin first: the agent's program is drawing at (0,0) of its own
//     screen, not of the Console's frame.
//   - Bare motion (no button held) is never forwarded to the PTY. Mouse
//     reporting is on for the whole Console run, so bare motion arrives
//     constantly; forwarding it would be a PTY write per pointer cell for
//     no interaction anybody asked for. It is used for one thing only:
//     which rail row the pointer is over, so that row can show its button.
//
// One press is the whole vocabulary of a box row (2026-09-19): on
// `[assign]` it hands the item to the Mate, anywhere else on the row it
// opens that crew's own pane. There is no double click left in the
// Console - a gesture that means something different from two single ones
// is one a reader has to be told about.

// onSessionMouse is every mouse event while the session view is open.
func (m Model) onSessionMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	ev := tea.MouseEvent(msg)
	if m.actions || m.confirm != nil {
		// The Actions menu takes the whole frame in the session view the way
		// it takes the whole body on the project frame (view.go); nothing
		// behind it answers the pointer.
		return m, nil
	}
	geo := m.sessionGeom()
	if m.draggingSplit {
		if ev.Action == tea.MouseActionRelease {
			m.draggingSplit = false
			return m, nil
		}
		return m.dragRailTo(ev.X)
	}
	if geo.inSplitter(ev.X, ev.Y) {
		if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
			m.draggingSplit = true
		}
		return m, nil
	}
	if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
		if id, ok := geo.labelAt(ev.X, ev.Y); ok {
			return m.onSessionLabel(id)
		}
	}
	if geo.inRail(ev.X, ev.Y) || geo.inDigest(ev.X, ev.Y) {
		return m.onSessionRailMouse(ev, geo)
	}
	if geo.inPane(ev.X, ev.Y) {
		return m.onSessionPaneMouse(ev, geo)
	}
	return m, nil
}

// sessionGeom lays the frame out as it is on screen right now.
func (m Model) sessionGeom() sessionGeom {
	return sessionGeometry(m.sess.snapshot, m.sessionRailState(), m.sess.terminal != nil, m.w, m.h, m.g)
}

// onSessionLabel runs one clickable label. Each one is the mouse's way to
// the same thing a key already does, never a second implementation of it.
func (m Model) onSessionLabel(id labelID) (tea.Model, tea.Cmd) {
	switch id {
	case labelAll:
		return m.setBoxAll(true), nil
	case labelWaiting:
		return m.setBoxAll(false), nil
	}
	return m, nil
}

// onSessionRailMouse is the rail and the narrow frame's digest: wheel
// scrolls the selection, motion tracks which entry would show its button, a
// press on `[assign]` hands that entry to the Mate, and a press anywhere
// else on a row focuses the box, selects that entry and opens the pane of
// the crew it names.
func (m Model) onSessionRailMouse(ev tea.MouseEvent, geo sessionGeom) (tea.Model, tea.Cmd) {
	b, has := m.sessionBoxList()
	sel := m.sessionRailState().sel
	switch ev.Button {
	case tea.MouseButtonWheelUp:
		return m.scrollBoxSelection(b, has, sel, -1), nil
	case tea.MouseButtonWheelDown:
		return m.scrollBoxSelection(b, has, sel, 1), nil
	}
	index, onEntry := -1, false
	if has && geo.railBodyH > 0 {
		index, onEntry = sessionEntryAt(b, sel, geo.railBodyH, ev.Y-geo.railBodyTop, geo.railW)
	}
	if ev.Action == tea.MouseActionMotion {
		m.boxHover = -1
		if onEntry {
			m.boxHover = index
		}
		return m, nil
	}
	if ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft {
		return m, nil
	}
	m.sess.zone = zoneBox
	if !onEntry {
		return m, nil
	}
	if strip, ok := sessionEntryStrip(b, sel, m.boxHover, index, 0, ev.Y, geo.railW, m.g); ok {
		for _, button := range strip {
			if button.hit(ev.X, ev.Y) {
				m.sess.boxSel = index
				return m.runBoxEntryAction(button.id, m.sess.target.ProjectID, b, index)
			}
		}
	}
	m.sess.boxSel, m.boxMsg = index, footerMsg{}
	return m.openBoxEntryFromSession(b, index)
}

// onSessionPaneMouse focuses the terminal and hands the event to the PTY in
// that pane's own coordinates. An event over the pane's chrome - the notice
// or runtime banner above the agent's screen - still moves focus but is not
// forwarded: there is no cell of the agent's screen under it to name.
func (m Model) onSessionPaneMouse(ev tea.MouseEvent, geo sessionGeom) (tea.Model, tea.Cmd) {
	if ev.Action == tea.MouseActionMotion && ev.Button == tea.MouseButtonNone {
		m.boxHover = -1
		return m, nil
	}
	if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
		m.sess.zone = zoneTerminal
		m.boxHover = -1
	}
	if m.sess.stream == nil {
		return m, nil
	}
	local := ev
	local.X, local.Y = ev.X-geo.paneX, ev.Y-geo.paneTop
	if local.X < 0 || local.Y < 0 || local.Y >= geo.paneH {
		return m, nil
	}
	if data, ok := encodeMouseMsg(tea.MouseMsg(local)); ok {
		m.sess.stream.enqueueWrite(data)
	}
	return m, nil
}

// dragRailTo moves the splitter to the column the pointer is on and resizes
// the agent's PTY to match. The width is clamped to the same bounds
// resolveRailWidth applies, and the PTY is told immediately rather than on
// release: a terminal that redraws only when the drag ends shows the reader
// a half-empty pane for as long as they hold the button.
func (m Model) dragRailTo(x int) (tea.Model, tea.Cmd) {
	want := clampInt(x, railMinWidth, railMaxWidth)
	if resolveRailWidth(m.sess.target.Kind, m.w, want) == resolveRailWidth(m.sess.target.Kind, m.w, m.railWidth) {
		return m, nil
	}
	m.railWidth = want
	if m.sess.stream == nil || m.sess.phase != sessionActive {
		return m, nil
	}
	size := streamTerminalSize(m.sess.target.Kind, m.w, m.h,
		sessionStreamReservedLines(m.sess.snapshot, m.sess.target.Kind, m.w, m.boxAll), m.railWidth)
	m.sess.terminal.Resize(size.Cols, size.Rows)
	return m, sessionStreamResizeCmd(m.baseCtx(), m.sess.stream, size, m.sess.gen)
}

// scrollBoxSelection is what the wheel does over a box: move the cursor,
// which is also what moves the pane's own scroll (boxSelectionTop derives
// the offset from the selection). It does not change focus.
func (m Model) scrollBoxSelection(b boxList, has bool, sel, delta int) Model {
	if !has {
		return m
	}
	m.sess.boxSel = clampInt(sel+delta, 0, len(b.rows())-1)
	m.boxMsg = footerMsg{}
	return m
}

// runBoxEntryAction is the entry's one strip button.
func (m Model) runBoxEntryAction(id labelID, project string, b boxList, index int) (tea.Model, tea.Cmd) {
	if id == labelAssign {
		return m.beginBoxAssign(project, b, index)
	}
	return m, nil
}

// ---------- the project frame ----------

// onFrameMouse is the mouse outside the session view. Only the box panel
// responds: it is the one region of a project frame whose rows carry
// actions of their own, and the rest of the frame is a table a reader moves
// through with the arrow keys.
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
		index, onEntry = sessionEntryAt(b, sel, h, ev.Y-top, m.w)
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
	if strip, ok := sessionEntryStrip(b, sel, m.boxHover, index, 0, ev.Y, m.w, m.g); ok {
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
