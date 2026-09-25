package console

import tea "github.com/charmbracelet/bubbletea"

// Keys (design I, "7 Keys and mouse"). A sheet has the keyboard before any
// pane; below it, the focused pane does.

// onKey routes a key to what has the keyboard. q always quits - except in
// a text field, where it is a character, and in a diff, where it closes
// the pager - and Ctrl+C always quits. While an action runs every other
// key waits.
func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	m.notice = ""
	if key == "ctrl+c" || (key == "q" && !m.actionInputMode && !m.diff.open) {
		if m.actionBusy {
			return m.onBusyQuit(), tea.Quit
		}
		m.quitting = true
		return m, tea.Quit
	}
	if m.plan().tooSmall {
		// Nothing else is drawn, so nothing else responds: a key that moved
		// a selection nobody can see would change where the reader lands
		// when the split grows back.
		return m, nil
	}
	if m.keysOpen {
		// `?` shows the key list until the next key, which it swallows.
		m.keysOpen = false
		return m.relayout(), nil
	}
	switch {
	case m.diff.open:
		return m.onDiffKey(key).relayout(), nil
	case m.actionBusy:
		return m, nil
	case m.actionInputMode:
		return m.onActionInputKey(msg)
	case m.harnessPick:
		return m.onHarnessKey(msg)
	case m.confirm != nil:
		return m.onConfirmKey(key)
	case m.actions:
		return m.onMenuKey(key)
	}
	switch m.phase {
	case phaseLoading:
		return m, nil
	case phaseFailed:
		if key == "r" {
			m.msg = infoMsg("Retrying snapshot read " + m.g.Ellipsis)
			return m.startLoad()
		}
		return m, nil
	}
	if m.emptyWorkspace() {
		switch key {
		case "n", "enter":
			return m.beginNewProject(), nil
		case "r":
			m.msg = footerMsg{}
			return m.startLoad()
		}
		return m, nil
	}
	switch key {
	case "?":
		m.keysOpen = true
		return m.relayout(), nil
	case "tab":
		return m.onTab(), nil
	case "shift+tab":
		return m.onTabBack(), nil
	case "r":
		if m.staged.err != "" {
			return m.retryStage()
		}
		m.msg = footerMsg{}
		return m.startLoad()
	}
	switch m.focus {
	case paneBox:
		return m.onBoxKey(key)
	case paneDetail:
		return m.onDetailKey(key)
	}
	return m.onListKey(key)
}

// onListKey is a key while the list has focus.
func (m Model) onListKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "a":
		return m.beginActions(), nil
	case "n":
		return m.beginNewProject(), nil
	case "s":
		return m.beginMateStart()
	case "m":
		return m.beginModeToggle(m.modeTarget())
	case "y":
		if r, ok := m.selectedRow(); ok {
			if v, ok := m.rowCopyValue(r); ok {
				return m.copyText(v)
			}
		}
		return m, nil
	case "o":
		return m.beginBoxActions(), nil
	case "esc", "backspace":
		return m.onBack(), nil
	case "up", "k":
		return m.moveSelection(-1), nil
	case "down", "j":
		return m.moveSelection(1), nil
	case "pgup":
		return m.moveSelection(-m.page()), nil
	case "pgdown":
		return m.moveSelection(m.page()), nil
	case "home", "g":
		return m.moveSelection(-len(m.currentRows())), nil
	case "end", "G":
		return m.moveSelection(len(m.currentRows())), nil
	case "enter":
		return m.onEnter()
	}
	return m, nil
}

// modeTarget is the Project `m` flips: the one open, or the one selected
// on the workspace level.
func (m Model) modeTarget() string {
	if m.cur().kind == frameProject {
		return m.currentProject().ProjectID
	}
	if r, ok := m.selectedRow(); ok && r.kind == rowProject {
		return r.id
	}
	return ""
}

// onDetailKey is a key while detail has focus: ↑↓ walk the fields, y
// copies the one under the cursor, Enter shows the object, Esc goes back.
func (m Model) onDetailKey(key string) (tea.Model, tea.Cmd) {
	n := len(m.detailFields(m.w))
	switch key {
	case "up", "k":
		m.detailSel = clampInt(m.detailSel-1, 0, max0(n-1))
		return m, nil
	case "down", "j":
		m.detailSel = clampInt(m.detailSel+1, 0, max0(n-1))
		return m, nil
	case "y":
		if v, ok := m.detailCopy(); ok {
			return m.copyText(v)
		}
		return m, nil
	case "a":
		return m.beginActions(), nil
	case "esc", "backspace":
		return m.setFocus(paneList), nil
	case "enter":
		return m.onEnter()
	}
	return m, nil
}

// page is how far PgUp/PgDn move: half the list's own rows.
func (m Model) page() int {
	if sl, ok := m.plan().slot(slotList); ok {
		return maxInt(1, (sl.h-1)/2)
	}
	return 1
}

// onTab cycles focus through the panes that are drawn: list, detail, box.
// Below 20 rows there is no stack, so Tab swaps detail in for the list.
func (m Model) onTab() Model {
	m.msg = footerMsg{}
	order := m.focusOrder()
	next := order[0]
	for i, f := range order {
		if f == m.focus {
			next = order[(i+1)%len(order)]
			break
		}
	}
	return m.setFocus(next)
}

// onTabBack is shift+Tab: the same cycle, backwards.
func (m Model) onTabBack() Model {
	m.msg = footerMsg{}
	order := m.focusOrder()
	prev := order[len(order)-1]
	for i, f := range order {
		if f == m.focus {
			prev = order[(i+len(order)-1)%len(order)]
			break
		}
	}
	return m.setFocus(prev)
}

// focusOrder is the panes Tab reaches, in order.
func (m Model) focusOrder() []pane {
	out := []pane{paneList}
	if m.hasDetail() {
		out = append(out, paneDetail)
	}
	if len(m.boxItems()) > 0 {
		out = append(out, paneBox)
	}
	return out
}

func (m Model) setFocus(f pane) Model {
	m.focus = f
	m.detail = f == paneDetail && m.h < stackRows
	m.detailSel = 0
	return m.relayout()
}

// onBack is Esc on the list: up one level. At the root it does nothing.
func (m Model) onBack() Model {
	m.msg = footerMsg{}
	m.detailSel = 0
	m.boxSel = -1
	return m.pop().relayout()
}
