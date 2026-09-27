package console

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The actions sheet (design F). `a` opens it for the selection. The order
// never changes between objects of a kind, and an action that cannot run
// stays in its place with · in the key column and the reason on the right,
// so muscle memory holds and "why not?" is answered in the row. Each entry
// also has its own key, so `a` then `x` skips the arrows.

// entryKind is what an entry does when it runs.
type entryKind int

const (
	entryAction     entryKind = iota // ActionFunc, through choice
	entryShow                        // stage the row in the next pane
	entryOpen                        // open the Project, or toggle Completed
	entryCopy                        // y: copy the object's id
	entryNewProject                  // n: the new-project sheet
	entryStartMate                   // s: start, resume or create the Mate
	entryMode                        // m: flip the communication mode
)

// menuEntry is one row of the actions sheet.
type menuEntry struct {
	key     string
	label   string
	kind    entryKind
	choice  actionChoice
	enabled bool
	// reason is why a disabled entry cannot run, in a few words.
	reason string
	// about is the block under the list: what the highlighted entry does.
	about string
}

// confirms reports whether running the entry asks first (design G).
func (e menuEntry) confirms() bool { return e.kind == entryAction && e.choice.dangerous }

// menuFor builds the sheet for a row. ok is false when the frame has no
// row and the workspace offers only a new Project.
func (m Model) menuFor(r row, ok bool) []menuEntry {
	if !ok {
		if m.cur().kind == frameWorkspace {
			return []menuEntry{m.newProjectEntry()}
		}
		return nil
	}
	switch r.kind {
	case rowMate:
		return m.withNoticeEntry(r, []menuEntry{
			m.showEntry(r),
			m.startMateEntry(r),
			m.choiceEntry("x", "Stop mate…", m.stopChoice(r)),
			m.restartEntry(),
			m.modeEntry(m.currentProject().ProjectID),
			m.clearComposerEntry(),
			m.copyEntry(r),
		})
	case rowCrew:
		return m.withNoticeEntry(r, []menuEntry{
			m.showEntry(r),
			m.choiceEntry("x", "Stop crew…", m.stopChoice(r)),
			m.choiceEntry("p", "Repair binding…", m.repairChoice(r)),
			m.mergeEntry(r),
			m.choiceEntry("d", "Diff", m.diffChoice(r)),
			m.copyEntry(r),
		})
	case rowProject:
		return []menuEntry{
			{key: "enter", label: "Open project", kind: entryOpen, enabled: true,
				about: "Opens the Project's Mate and crews in this list."},
			m.startMateEntry(r),
			m.modeEntry(r.id),
			m.newProjectEntry(),
		}
	case rowCompletedGroup:
		label := "Show completed"
		if m.completedOpen[m.cur().id] {
			label = "Hide completed"
		}
		return []menuEntry{{key: "enter", label: label, kind: entryOpen, enabled: true,
			about: "Finished crews stay in the snapshot; this shows or hides them in the list."}}
	}
	return nil
}

// choiceEntry wraps one of actions.go's choices, keeping its availability
// and the query layer's override (actionChoicesForRow's rule).
func (m Model) choiceEntry(key, label string, c actionChoice) menuEntry {
	r, _ := m.selectedRow()
	if m.actionRow.id != "" {
		r = m.actionRow
	}
	if available, reason, found := m.actionCapability(r, c); found {
		c.enabled = available
		if !available {
			c.desc = "unavailable · " + reason
		}
	}
	e := menuEntry{key: key, label: label, kind: entryAction, choice: c, enabled: c.enabled, about: c.desc}
	if !c.enabled {
		e.reason = shortReason(c.desc)
	}
	return e
}

// shortReason is a disabled choice's reason without its "unavailable ·"
// head: the right-hand column has room for a few words, and the block
// under the list carries the whole sentence.
func shortReason(desc string) string {
	s := strings.TrimPrefix(desc, "unavailable · ")
	s = strings.TrimPrefix(s, "Mate is ")
	if i := strings.Index(s, ";"); i > 0 {
		s = s[:i]
	}
	return s
}

func (m Model) showEntry(r row) menuEntry {
	e := menuEntry{key: "enter", label: "Show in next pane", kind: entryShow}
	target, ok := m.stageTargetAvailable(r)
	if !ok {
		reason, refused := m.stageRefusal(r)
		if !refused {
			reason = "names no agent"
		}
		e.reason, e.about = shortReason(reason), "Nothing to show: "+reason+"."
		return e
	}
	e.enabled = true
	if m.stage == nil {
		e.about = "No next pane: run mate console inside WezTerm or Ghostty, and Enter fills the pane beside it."
		return e
	}
	e.about = "Tells the host to run herdr agent attach " + stageLabel(target) +
		" in the next pane. mate stays on this tree; nothing opens here."
	return e
}

func (m Model) startMateEntry(r row) menuEntry {
	e := menuEntry{key: "s", label: "Start mate", kind: entryStartMate}
	mate, ok := m.mateForRow(r)
	if !ok {
		e.reason = "not a Mate"
		return e
	}
	c := m.mateStartChoice(r, mate)
	switch c.action {
	case ActionOnboard:
		e.label = "Create mate…"
	case ActionResume:
		e.label = "Resume mate"
	}
	e.enabled, e.about = c.enabled, c.desc
	if !c.enabled {
		e.reason = shortReason(c.desc)
	}
	return e
}

func (m Model) restartEntry() menuEntry {
	project := m.currentProject().ProjectID
	c := restartMateChoice(project)
	e := m.choiceEntry("R", "Restart mate…", c)
	if st := mateWord(m.currentProject().Mate); st != string(query.MateRunning) {
		e.enabled, e.choice.enabled = false, false
		e.reason = "not running"
		e.about = "The Mate is recorded " + st + "; there is nothing to restart."
	}
	return e
}

func (m Model) clearComposerEntry() menuEntry {
	e := m.choiceEntry("C", "Clear composer", clearComposerChoice(m.currentProject().ProjectID))
	e.about = "Clears whatever is typed in the Mate's composer, so a queued line can reach it."
	if st := mateWord(m.currentProject().Mate); st != string(query.MateRunning) {
		e.enabled, e.choice.enabled = false, false
		e.reason = "not running"
		e.about = "The Mate is recorded " + st + "; it has no composer to clear."
	}
	return e
}

func (m Model) modeEntry(project string) menuEntry {
	proj, _ := m.projectByID(project)
	to := "auto"
	if proj.Mode == query.ModeAuto {
		to = "manual"
	}
	e := menuEntry{key: "m", label: "Mode " + m.g.Arrow + " " + to, kind: entryMode, enabled: project != "" && m.action != nil}
	e.about = "Flips the Project to " + to + ". In auto the daemon types the box's answers into the Mate for you."
	if !e.enabled {
		e.reason = "not wired"
	}
	return e
}

func (m Model) mergeEntry(r row) menuEntry {
	if c, ok := m.mergeChoice(r); ok {
		return m.choiceEntry("M", "Merge…", c)
	}
	return menuEntry{key: "M", label: "Merge…", kind: entryAction, reason: "not waiting on merge",
		about: "A crew can be merged once it reports wait-mate."}
}

func (m Model) copyEntry(r row) menuEntry {
	e := menuEntry{key: "y", label: "Copy agent id", kind: entryCopy}
	id, ok := m.rowCopyValue(r)
	switch {
	case !ok:
		e.reason = "no id"
	case m.clipboard == nil:
		e.reason = "no clipboard"
	default:
		e.enabled = true
	}
	e.about = "Copies " + id + " to the terminal's clipboard (OSC 52)."
	return e
}

func (m Model) newProjectEntry() menuEntry {
	c, ok := m.newProjectChoice()
	e := menuEntry{key: "n", label: "New project…", kind: entryNewProject, enabled: ok,
		about: "Registers a Project in this workspace, like mate project add; no Mate is started."}
	if !ok {
		e.reason = shortReason(c.desc)
	}
	return e
}

// rowCopyValue is the id y copies from a list row: the agent a Mate row
// names, a Crew's id, a Project's name.
func (m Model) rowCopyValue(r row) (string, bool) {
	switch r.kind {
	case rowMate:
		if a := m.mateAgent(m.currentProject().Mate); a != "" {
			return a, true
		}
	case rowCrew:
		return r.id, r.id != ""
	case rowProject:
		if p, ok := m.projectByID(r.id); ok {
			return p.Name, true
		}
	}
	return "", false
}

// ---------- keys ----------

// beginActions is `a`: the sheet for the selected row.
func (m Model) beginActions() Model {
	r, ok := m.selectedRow()
	m = m.closeActions()
	m.actionRow = r
	m.menu = m.menuFor(r, ok)
	if len(m.menu) == 0 {
		return m
	}
	m.actions = true
	m.msg = footerMsg{}
	return m
}

// onMenuKey is a key while the actions sheet is open.
func (m Model) onMenuKey(key string) (Model, tea.Cmd) {
	switch key {
	case "esc", "backspace":
		return m.closeActions(), nil
	case "up", "k":
		m.actionIndex = clampInt(m.actionIndex-1, 0, len(m.menu)-1)
		return m, nil
	case "down", "j":
		m.actionIndex = clampInt(m.actionIndex+1, 0, len(m.menu)-1)
		return m, nil
	case "enter":
		return m.runEntry(m.actionIndex)
	}
	for i, e := range m.menu {
		if e.key == key {
			m.actionIndex = i
			return m.runEntry(i)
		}
	}
	return m, nil
}

// runEntry runs one entry of the open sheet. An entry that cannot run does
// nothing: its reason is already on the row.
func (m Model) runEntry(i int) (Model, tea.Cmd) {
	if i < 0 || i >= len(m.menu) {
		return m, nil
	}
	e := m.menu[i]
	if !e.enabled {
		return m, nil
	}
	r := m.actionRow
	switch e.kind {
	case entryShow:
		m = m.closeActions()
		return m.stageRow(r)
	case entryOpen:
		m = m.closeActions()
		model, cmd := m.onEnter()
		return model.(Model), cmd
	case entryCopy:
		m = m.closeActions()
		id, _ := m.rowCopyValue(r)
		return m.copyText(id)
	case entryNewProject:
		m = m.closeActions()
		return m.beginNewProject(), nil
	case entryStartMate:
		m = m.closeActions()
		return m.beginMateStart()
	case entryMode:
		project := m.currentProject().ProjectID
		if r.kind == rowProject {
			project = r.id
		}
		m = m.closeActions()
		return m.beginModeToggle(project)
	}
	if e.confirms() {
		m.confirm = &actionConfirmation{choice: e.choice, key: e.key, label: e.label}
		return m, nil
	}
	m = m.closeActions()
	return m.runAction(e.choice)
}

// onConfirmKey is a key while the confirm sheet is open: Enter cancels,
// the key that asked runs, Esc goes back to the actions (design G).
func (m Model) onConfirmKey(key string) (Model, tea.Cmd) {
	c := m.confirm
	switch key {
	case "enter":
		return m.closeActions(), nil
	case "esc", "backspace":
		m.confirm = nil
		if len(m.menu) == 0 {
			return m.closeActions(), nil
		}
		return m, nil
	}
	if key == c.key {
		choice := c.choice
		m = m.closeActions()
		return m.runAction(choice)
	}
	return m, nil
}

// copyText writes text to the terminal's clipboard and says so.
func (m Model) copyText(text string) (Model, tea.Cmd) {
	if text == "" {
		return m, nil
	}
	if m.clipboard == nil {
		m.msg = errMsg("copy unavailable: this Console has no terminal clipboard")
		return m, nil
	}
	write, seq := m.clipboard, clipboardSequence(text)
	m.msg = okMsg("copied " + text)
	return m, func() tea.Msg {
		write(seq)
		return nil
	}
}
