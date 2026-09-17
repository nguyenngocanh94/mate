package console

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
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
//     which rail row the pointer is over, so that row can show its buttons.

// onSessionMouse is every mouse event while the session view is open.
func (m Model) onSessionMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	ev := tea.MouseEvent(msg)
	if m.peek.open {
		// The overlay is somebody else's screen taking the whole frame; the
		// wheel scrolls it and nothing else responds, which is exactly what
		// its keyboard does (onPeekKey).
		switch ev.Button {
		case tea.MouseButtonWheelUp:
			if m.peek.top > 0 {
				m.peek.top--
			}
		case tea.MouseButtonWheelDown:
			m.peek.top++
		}
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
	project := m.sess.target.ProjectID
	switch id {
	case labelProject:
		return m.endSession()
	case labelMode:
		return m.beginModeToggle(project)
	case labelRestart:
		return m.beginRestartMate(project), nil
	case labelClear:
		return m.beginClearComposer(project)
	case labelSend:
		return m.submitBoxReply()
	case labelCancel:
		return m.cancelBoxReply(), nil
	case labelYes:
		return m.resolveBoxConfirm(true)
	case labelNo:
		return m.resolveBoxConfirm(false)
	}
	return m, nil
}

// onSessionRailMouse is the rail and the narrow frame's digest: wheel
// scrolls the selection, motion tracks which entry would show its buttons,
// a press focuses the box and selects the entry under the pointer, a press
// on one of that entry's buttons runs it, and a second press on the same
// entry opens the peek overlay.
func (m Model) onSessionRailMouse(ev tea.MouseEvent, geo sessionGeom) (tea.Model, tea.Cmd) {
	v, has := m.sessionBoxView()
	sel := m.sessionRailState().sel
	switch ev.Button {
	case tea.MouseButtonWheelUp:
		return m.scrollBoxSelection(v, has, sel, -1), nil
	case tea.MouseButtonWheelDown:
		return m.scrollBoxSelection(v, has, sel, 1), nil
	}
	index, onEntry := -1, false
	if has && geo.railBodyH > 0 {
		index, onEntry = sessionEntryAt(v, sel, geo.railBodyH, ev.Y-geo.railBodyTop)
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
	if strip, ok := sessionEntryStrip(v, sel, m.boxHover, index, 0, ev.Y, geo.railW, m.g); ok {
		for _, b := range strip {
			if b.hit(ev.X, ev.Y) {
				m.sess.boxSel = index
				return m.runBoxEntryAction(b.id, m.sess.target.ProjectID, v, index)
			}
		}
	}
	double := m.isDoubleClick(clickSurfaceRail, index)
	m.sess.boxSel, m.boxMsg = index, footerMsg{}
	if double {
		return m.beginBoxPeek(m.sess.target.ProjectID, v, index)
	}
	return m, nil
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
		sessionStreamReservedLines(m.sess.snapshot, m.sess.target.Kind, m.w), m.railWidth)
	m.sess.terminal.Resize(size.Cols, size.Rows)
	return m, sessionStreamResizeCmd(m.baseCtx(), m.sess.stream, size, m.sess.gen)
}

// scrollBoxSelection is what the wheel does over a box: move the cursor,
// which is also what moves the pane's own scroll (boxSelectionTop derives
// the offset from the selection). It does not change focus.
func (m Model) scrollBoxSelection(v query.Field[query.BoxView], has bool, sel, delta int) Model {
	if !has {
		return m
	}
	m.sess.boxSel = clampInt(sel+delta, 0, len(v.Value.Entries)-1)
	m.boxMsg = footerMsg{}
	return m
}

// runBoxEntryAction is one action strip button.
func (m Model) runBoxEntryAction(id labelID, project string, v query.Field[query.BoxView], index int) (tea.Model, tea.Cmd) {
	switch id {
	case labelForward:
		return m.beginBoxForward(project, v, index)
	case labelReply:
		return m.beginBoxReply(project, v, index), nil
	case labelPeek:
		return m.beginBoxPeek(project, v, index)
	}
	return m, nil
}

// isDoubleClick reports whether this press is the second of a pair on the
// same thing, and records it either way.
func (m *Model) isDoubleClick(surface, index int) bool {
	prev := m.lastClick
	m.lastClick = clickMemo{surface: surface, index: index, at: time.Now()}
	return prev.surface == surface && prev.index == index &&
		!prev.at.IsZero() && time.Since(prev.at) < doubleClickWindow
}

// ---------- the project frame ----------

// onFrameMouse is the mouse outside the session view. Only the box panel
// responds: it is the one region of a project frame whose rows carry
// actions of their own, and the rest of the frame is a table a reader moves
// through with the arrow keys.
func (m Model) onFrameMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	ev := tea.MouseEvent(msg)
	if m.peek.open {
		switch ev.Button {
		case tea.MouseButtonWheelUp:
			if m.peek.top > 0 {
				m.peek.top--
			}
		case tea.MouseButtonWheelDown:
			m.peek.top++
		}
		return m, nil
	}
	if m.boxReply || m.boxConfirm || m.actions || m.actionInputMode || m.harnessPick || m.confirm != nil {
		return m, nil
	}
	top, h, ok := m.boxPanelBodyRegion()
	if !ok || ev.Y < top || ev.Y >= top+h {
		return m, nil
	}
	v, has := m.projectBox()
	sel := m.projectBoxSelection()
	switch ev.Button {
	case tea.MouseButtonWheelUp:
		if has {
			m.boxSel = clampInt(sel-1, 0, len(v.Value.Entries)-1)
		}
		return m, nil
	case tea.MouseButtonWheelDown:
		if has {
			m.boxSel = clampInt(sel+1, 0, len(v.Value.Entries)-1)
		}
		return m, nil
	}
	index, onEntry := -1, false
	if has {
		index, onEntry = sessionEntryAt(v, sel, h, ev.Y-top)
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
	if strip, ok := sessionEntryStrip(v, sel, m.boxHover, index, 0, ev.Y, m.w, m.g); ok {
		for _, b := range strip {
			if b.hit(ev.X, ev.Y) {
				m.boxSel = index
				return m.runBoxEntryAction(b.id, m.currentProject().ProjectID, v, index)
			}
		}
	}
	double := m.isDoubleClick(clickSurfacePanel, index)
	m.boxSel, m.msg, m.boxMsg = index, footerMsg{}, footerMsg{}
	if double {
		return m.beginBoxPeek(m.currentProject().ProjectID, v, index)
	}
	return m, nil
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
