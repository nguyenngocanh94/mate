package console

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

type TasksFunc func(context.Context, string) error
type tasksDoneMsg struct {
	project string
	err     error
}

// keyProject is the project a project key names: the box item's when the
// box has focus, otherwise the one open or selected (modeTarget).
func (m Model) keyProject() string {
	if m.focus == paneBox {
		items := m.boxItems()
		if sel := m.boxSelection(items); sel >= 0 {
			return items[sel].project
		}
	}
	return m.modeTarget()
}

func (m Model) beginTasks() (Model, tea.Cmd) {
	project := m.keyProject()
	if project == "" {
		m.msg = errMsg("Select a project to open Beads Viewer")
		return m, nil
	}
	if m.tasks == nil {
		m.msg = errMsg("No host tab; run mate tasks " + project + " in a terminal")
		return m, nil
	}
	m.msg = infoMsg("Opening tasks · " + project + m.g.Ellipsis)
	fn, ctx := m.tasks, m.baseCtx()
	return m, func() tea.Msg { return tasksDoneMsg{project, fn(ctx, project)} }
}
