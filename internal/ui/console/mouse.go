package console

import tea "github.com/charmbracelet/bubbletea"

// The mouse (design I, "7 Keys and mouse"): a click on a row is Enter, a
// click on a pane's rule focuses that pane, a click on ▸ toggles the
// group and on [assign] assigns, and the wheel scrolls the pane under the
// pointer. mate draws no selection of its own: shift+drag is the
// terminal's. Every hit test reads plan() and the same scroll offsets the
// renderer used, so a click resolves against the frame that was drawn.

func (m Model) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.phase != phaseReady {
		return m, nil
	}
	p := m.plan()
	if p.tooSmall {
		return m, nil
	}
	ev := tea.MouseEvent(msg)
	wheel := ev.Button == tea.MouseButtonWheelUp || ev.Button == tea.MouseButtonWheelDown
	press := ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft
	if !wheel && !press {
		return m, nil
	}
	sl, ok := slotAt(p, ev.Y)
	if !ok {
		return m, nil
	}
	delta := 1
	if ev.Button == tea.MouseButtonWheelUp {
		delta = -1
	}
	if sl.kind == slotSheet {
		return m.onSheetMouse(p, sl, ev.Y, wheel, delta)
	}
	if m.sheetOpen() != sheetNone {
		// A sheet is modal: the panes above it are for looking at.
		return m, nil
	}
	if ev.Y == sl.top {
		if press {
			return m.focusSlot(sl.kind), nil
		}
		return m, nil
	}
	top, h := sl.body()
	line := ev.Y - top
	switch sl.kind {
	case slotList:
		if wheel {
			return m.moveSelection(delta), nil
		}
		return m.onListClick(p, h, line)
	case slotDetail:
		m.focus = paneDetail
		if wheel {
			m.detailSel = clampInt(m.detailSel+delta, 0, max0(len(m.detailFields(p.w))-1))
			return m, nil
		}
		m.detailSel = m.detailFieldAt(p, h, line)
		return m, nil
	case slotBox:
		return m.onBoxMouse(p, h, line, ev.X, wheel, delta)
	}
	return m, nil
}

func slotAt(p framePlan, y int) (slot, bool) {
	for _, s := range p.slots {
		if y >= s.top && y < s.top+s.h {
			return s, true
		}
	}
	return slot{}, false
}

func (m Model) focusSlot(k slotKind) Model {
	switch k {
	case slotDetail:
		m.focus = paneDetail
	case slotBox:
		m.focus = paneBox
	default:
		m.focus = paneList
	}
	m.msg = footerMsg{}
	return m
}

// onListClick selects the row under the pointer and does what Enter does
// on it: open a Project, toggle Completed, show a Mate or Crew.
func (m Model) onListClick(p framePlan, h, line int) (tea.Model, tea.Cmd) {
	body := m.listBody(p, h)
	if line < 0 || line >= len(body.rows) || body.rows[line] < 0 {
		return m, nil
	}
	idx := body.rows[line]
	rows := m.currentRows()
	f := m.cur()
	f.sel, f.selID = idx, rows[idx].id
	m = m.setCur(f)
	m.focus = paneList
	m.detailSel = 0
	m.msg = footerMsg{}
	return m.onEnter()
}

// detailFieldAt is the field drawn on a detail body line.
func (m Model) detailFieldAt(p framePlan, h, line int) int {
	fields := m.detailFields(p.w)
	n := 0
	for _, f := range fields {
		n += len(f.lines)
	}
	at := line + m.detailTop(p, n, h)
	for i, f := range fields {
		if at < len(f.lines) {
			return i
		}
		at -= len(f.lines)
	}
	return clampInt(m.detailSel, 0, max0(len(fields)-1))
}

func (m Model) onBoxMouse(p framePlan, h, line, x int, wheel bool, delta int) (tea.Model, tea.Cmd) {
	items := m.boxItems()
	if wheel {
		return m.selectBoxItem(items, m.boxSelection(items)+delta), nil
	}
	m.focus = paneBox
	b := m.boxLines(p, m.boxMode(p))
	at := line + m.boxTop(b, h)
	if at < 0 || at >= len(b.hits) || b.hits[at].item < 0 {
		return m, nil
	}
	hit := b.hits[at]
	m = m.selectBoxItem(items, hit.item)
	// [assign] is right-aligned on the item's title line, so its columns
	// do not move when focus reflows the panes.
	if hit.assign > 0 && x >= hit.assignX0 && x < hit.assign {
		return m.beginBoxAssign(items[hit.item])
	}
	return m.showBoxItem(items[hit.item])
}

// onSheetMouse: a click on an actions row runs it; the wheel moves the
// sheet's cursor, or scrolls a diff.
func (m Model) onSheetMouse(p framePlan, sl slot, y int, wheel bool, delta int) (tea.Model, tea.Cmd) {
	switch m.sheetOpen() {
	case sheetDiff:
		if wheel {
			return m.scrollDiff(delta), nil
		}
	case sheetActions:
		if wheel {
			m.actionIndex = clampInt(m.actionIndex+delta, 0, max0(len(m.menu)-1))
			return m, nil
		}
		top, _ := sl.body()
		if i := y - top; i >= 0 && i < len(m.menu) {
			m.actionIndex = i
			return m.runEntry(i)
		}
	case sheetHarness:
		if wheel {
			m.harnessIndex = clampInt(m.harnessIndex+delta, 0, len(harnessOrder)-1)
			return m, nil
		}
		// A click on a harness row is Enter on it, like every other row.
		top, _ := sl.body()
		if i := y - top; i >= 0 && i < len(harnessOrder) {
			m.harnessIndex = i
			return m.onHarnessKey(tea.KeyMsg{Type: tea.KeyEnter})
		}
	}
	return m, nil
}
