package console

import (
	tea "github.com/charmbracelet/bubbletea"
)

// beginStage asks the host to show the agent in the sibling pane and stays
// on the tree. It never opens a PTY in this process (docs/mvp.md M10).
func (m Model) beginStage(target SessionTarget) (Model, tea.Cmd) {
	label := sessionLabel(target)
	m.msg = infoMsg("Opening " + label + " in the next pane" + m.g.Ellipsis)
	fn := m.stage
	ctx := m.baseCtx()
	return m, func() tea.Msg {
		err := fn(ctx, target)
		return stageDoneMsg{err: err, label: label}
	}
}

type stageDoneMsg struct {
	err   error
	label string
}

func (m Model) onStageDone(msg stageDoneMsg) Model {
	if msg.err != nil {
		m.msg = errMsg(msg.err.Error())
		return m
	}
	m.msg = okMsg("Showing " + msg.label + " in the next pane")
	return m
}
