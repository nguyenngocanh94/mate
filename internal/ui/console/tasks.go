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

func (m Model) beginTasks() (Model, tea.Cmd) {
	project := m.modeTarget()
	if m.focus == paneBox {
		items := m.boxItems()
		if sel := m.boxSelection(items); sel >= 0 {
			project = items[sel].project
		}
	}
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
