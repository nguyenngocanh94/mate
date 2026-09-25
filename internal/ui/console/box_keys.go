package console

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The box keys (mvp.md section 5, task 15).
//
//	Enter  open the pane of the crew the selected item names. The box is a
//	       place to act, not to read (2026-09-19): an inbox row says who
//	       needs what, and the thing a reader does about it is go and look
//	       at that crew's own screen. An entry whose crew is `mate` - the
//	       daemon's `wedged` incident - opens the Mate's own view.
//	a      assign: hand the selected item to the Mate - one verified line
//	       into its composer, naming the crew's question, the status file to
//	       read and the `mate send` that answers the crew.
//	l      swap the two filters the header names: `[waiting]`, which is the
//	       inbox, and `[all]`, the whole merged log. `[all]` is a debugging
//	       view, off every time the Console starts - what the rail is for is
//	       the things somebody still has to act on.
//	o      the Actions menu for the pane this box belongs to: the Mate's
//	       restart and clear-composer live there rather than on the header
//	       (2026-09-19), with the menu's own confirmation in front of the
//	       one that stops a live agent.
//	j/k    move the selection.
//
// They are bare everywhere, and they are live exactly while the box has
// focus (session_focus.go): in the session view that is the box zone, on
// the project frame it is paneBox. The Ctrl+b prefix they used to sit
// behind in stream mode is gone - a prefix is a mode with no indicator, and
// a mis-typed one delivered `r` or `q` into the agent's own composer.
// sessionHintLine and keyHints name only the focused surface's keys, so a
// reader is never told about a key that would land in the harness instead.
//
// No action here acts on anything itself: each builds an actionChoice and
// goes through runAction, so the whole existing machinery - the busy flag,
// the cancellable context, the one outcome line, the re-read afterwards -
// applies unchanged, and this package still reaches state only through
// ActionFunc. Enter is the one key that runs no action at all: opening a
// crew's pane is navigation, and it takes the same path Enter on that
// crew's tree row takes (update.go's onEnter).

// ---------- the recovery actions ----------
//
// Restarting a Mate and clearing its composer are entries in the Actions
// menu of the Mate row (actions.go), which the box zone opens with `o`.
// They were rail-header labels until 2026-09-19; a header of five
// affordances was five things to read before the first row, and both of
// these are rare enough that a menu is where a reader goes looking for
// them. The restart keeps its confirmation - it stops a live agent - and it
// is now the menu's own, which is the same confirmation every dangerous
// action on the frame already gets.

func restartMateChoice(project string) actionChoice {
	return actionChoice{
		action: ActionRestartMate, enabled: true, dangerous: true,
		desc: "Restart the Mate of " + project,
		req: ActionRequest{
			Action: ActionRestartMate, Target: project, TargetKind: "project",
		},
	}
}

func clearComposerChoice(project string) actionChoice {
	return actionChoice{
		action: ActionClearComposer, enabled: true,
		desc: "Clear the Mate's composer",
		req: ActionRequest{
			Action: ActionClearComposer, Target: project, TargetKind: "project",
		},
	}
}

// toggleBoxAll flips between the header's two filters, from the `l` key;
// setBoxAll is the same move from a click on one of the labels, which names
// the filter it wants rather than asking for the other one.
//
// The filter is Console state for the run and is never persisted: a
// debugging view that survived a restart would quietly become the default
// again. The selection resets to "follow the newest" because the two lists
// have different lengths, and an index carried across them names a row the
// reader was not looking at.
func (m Model) toggleBoxAll() Model { return m.setBoxAll(!m.boxAll) }

func (m Model) setBoxAll(all bool) Model {
	m.boxAll = all
	m.boxSel = -1
	m.msg, m.boxMsg = footerMsg{}, footerMsg{}
	return m
}

// beginBoxActions is `o`: the Actions menu of the pane this box belongs to.
// It is built from the session's own target, or from the project frame's
// Mate, rather than from whichever row the tree's cursor happens to be on -
// the reader is looking at a box, and the row under the list's cursor
// behind it is not what they mean.
func (m Model) beginBoxActions() Model {
	if m.actionBusy {
		return m
	}
	r, ok := m.boxActionsRow()
	if !ok {
		return m
	}
	m.actions, m.confirm, m.actionInputMode, m.harnessPick = true, nil, false, false
	m.actionChoices = m.actionChoicesForRow(r)
	m.actionRow = r
	m.actionIndex = 0
	m.msg, m.boxMsg = footerMsg{}, footerMsg{}
	return m
}

// boxActionsRow is the row `o` acts on: the project's Mate, since the box
// sits under the Mate's own project.
func (m Model) boxActionsRow() (row, bool) {
	if m.cur().kind != frameProject {
		return row{}, false
	}
	return row{kind: rowMate, id: mateRowID(m.currentProject().Mate)}, true
}

// beginBoxAssign is `a`, and the row's own `[assign]` button: hand the
// selected item to the Mate and ask it to answer the crew. It sends the
// entry's own `resolve:` line - the word the Mate's manual uses - while the
// button a reader presses says what the press does to them: give this away.
// A message entry holds no question - the Mate either sent it or was sent
// it - and the refusal says so rather than silently doing nothing, which is
// indistinguishable from a lost keystroke. Only `[all]` can put such a row
// under the cursor; every row of the inbox is assignable.
func (m Model) beginBoxAssign(project string, b boxList, sel int) (Model, tea.Cmd) {
	e, ok := boxSelectedEntry(b, sel)
	if !ok {
		return m, nil
	}
	if !e.Resolvable() {
		m.boxMsg = errMsg("Assign refused: a " + string(e.Kind) + " entry holds no question; only a crew status or an incident does " +
			m.g.Dot + " nothing was sent")
		return m, nil
	}
	if m.actionBusy {
		return m, nil
	}
	return m.runAction(boxAssignChoice(project, e))
}

// boxAssignChoice is built here rather than in actionChoicesForRow
// because it does not act on the *selected row* of a frame: it acts on a
// box entry, which is a different selection entirely.
func boxAssignChoice(project string, e query.BoxEntry) actionChoice {
	return actionChoice{
		action: ActionResolve, enabled: true,
		desc: "Ask the Mate to resolve " + e.Crew + "'s " + e.Verb,
		req: ActionRequest{
			Action: ActionResolve, Target: project, TargetKind: "project",
			Crew: e.Crew, Input: e.Resolve, Key: e.AssignKey,
		},
	}
}

// ---------- Enter and a click: open the crew's own pane ----------

// boxEntryCrewRow is the tree row an inbox entry names: the crew that wrote
// the status line or that the observer opened an incident about, and the
// Mate for the daemon's own `wedged` incident, whose crew field is the
// literal "mate" (internal/autopilot/doc.go). ok is false when the entry
// names no crew at all - a message to the Mate's own pane, reachable only
// through `[all]` - or when the crew it names is no longer in the snapshot,
// which is what a crew closed since the last read looks like.
func (m Model) boxEntryCrewRow(e query.BoxEntry) (row, string, bool) {
	crew := e.Crew
	if crew == "" {
		return row{}, "", false
	}
	if crew == "mate" {
		return row{kind: rowMate, id: mateRowID(m.currentProject().Mate)}, crew, true
	}
	if _, ok := m.crewByID(crew); !ok {
		return row{}, crew, false
	}
	return row{kind: rowCrew, id: crew}, crew, true
}

// openBoxCrew is what Enter and a click on a row body both end in: the
// same thing Enter on that crew's tree row does (update.go's onEnter), so
// the box cannot show a pane the tree would refuse.
func (m Model) openBoxCrew(e query.BoxEntry) (Model, tea.Cmd) {
	r, crew, ok := m.boxEntryCrewRow(e)
	if !ok {
		m.msg = errMsg(boxOpenRefusal(crew, m.g))
		m.boxMsg = m.msg
		return m, nil
	}
	m.msg, m.boxMsg = footerMsg{}, footerMsg{}
	return m.stageRow(r)
}

// boxOpenRefusal is the one line an entry with nowhere to go leaves behind.
func boxOpenRefusal(crew string, g glyphSet) string {
	if crew == "" {
		return "Open refused: this entry names no crew " + g.Dot + " nothing was opened"
	}
	return "Open refused: crew " + crew + " is not in this snapshot; it was closed " +
		g.Dot + " nothing was opened"
}

// ---------- the project frame's box panel ----------
//
// The panel exists so attention is visible without entering the session
// view, which is the whole point of showing it here. Its keys are the rail's
// own, bare - the Console owns the keyboard on this frame - but they are
// live only while Tab has moved focus onto the panel: `a` already opens the
// action menu under the list and `r` already refreshes, and rebinding either
// of them under the list would be exactly the trap the `n` key's own note
// (seams.go's actionHints) refuses to lay.

// projectBoxList is the box of the project frame the reader is on: the same
// inbox the rail draws, through the same boxList, so the panel and the rail
// can never disagree about what is waiting.
func (m Model) projectBoxList() boxList {
	if m.cur().kind != frameProject {
		return boxList{all: m.boxAll}
	}
	return boxList{field: m.currentProject().Box, all: m.boxAll}
}

// projectBox adds "and is there a row to act on".
func (m Model) projectBox() (boxList, bool) {
	b := m.projectBoxList()
	return b, b.known() && len(b.rows()) > 0
}

// projectBoxSelection resolves the panel's selection the same way
// sessionRailState resolves the rail's.
func (m Model) projectBoxSelection() int {
	b := m.projectBoxList()
	if m.boxSel < 0 {
		return boxDefaultSelection(b)
	}
	if n := len(b.rows()); n > 0 {
		return clampInt(m.boxSel, 0, n-1)
	}
	return m.boxSel
}

// onProjectBoxKey is the panel's keyboard, live only while focus is on it.
func (m Model) onProjectBoxKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	project := m.currentProject().ProjectID
	b, ok := m.projectBox()
	sel := m.projectBoxSelection()
	switch msg.String() {
	case "esc", "tab":
		m.focus = paneList
		m.msg = footerMsg{}
		return m, nil
	case "l":
		return m.toggleBoxAll(), nil
	case "o":
		return m.beginBoxActions(), nil
	}
	if !ok {
		return m, nil
	}
	n := len(b.rows())
	switch msg.String() {
	case "j", "down":
		m.boxSel = clampInt(sel+1, 0, n-1)
		m.msg, m.boxMsg = footerMsg{}, footerMsg{}
	case "k", "up":
		m.boxSel = clampInt(sel-1, 0, n-1)
		m.msg, m.boxMsg = footerMsg{}, footerMsg{}
	case "enter":
		e, has := boxSelectedEntry(b, sel)
		if !has {
			return m, nil
		}
		return m.openBoxCrew(e)
	case "a":
		return m.beginBoxAssign(project, b, sel)
	}
	return m, nil
}
