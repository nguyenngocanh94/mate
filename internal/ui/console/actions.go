package console

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// actionChoice is the complete action menu, including unavailable actions.
// Showing the refusal reason before Enter is important: a disabled-looking
// omission would make the operator guess whether an action was forgotten or
// is unsafe for this row.
type actionChoice struct {
	action    Action
	desc      string
	enabled   bool
	dangerous bool
	// confirmPrompt replaces the confirmation overlay's generic "CONFIRM
	// <action>?" headline for the actions whose question is worth asking in
	// full. `merge` is one: "CONFIRM merge?" does not say which branch moves
	// where, and that is the whole of what the reader is agreeing to.
	confirmPrompt string
	req           ActionRequest
}

type actionConfirmation struct {
	choice actionChoice
}

type actionDoneMsg struct {
	choice actionChoice
	text   string
	err    error
}

func (m Model) actionChoicesForSelected() []actionChoice {
	unavailable := func(a Action, desc string) actionChoice {
		return actionChoice{action: a, desc: "unavailable · " + desc}
	}
	selected, ok := m.selectedRow()
	if !ok {
		choices := []actionChoice{
			unavailable(ActionStart, "no row selected"), unavailable(ActionStop, "no row selected"),
			unavailable(ActionResume, "no row selected"), unavailable(ActionRepair, "no row selected"),
			unavailable(ActionOnboard, "select a Project or the workspace"),
		}
		if m.cur().kind == frameWorkspace {
			choices[4] = actionChoice{action: ActionOnboard, desc: "Add a Project to this workspace", enabled: true, req: ActionRequest{Action: ActionOnboard, TargetKind: "workspace"}}
		}
		return choices
	}
	return m.actionChoicesForRow(selected)
}

// actionChoicesForRow is the menu one row offers. It is its own function
// because two surfaces ask for it: the list's `a`, which means the row
// under the cursor, and the box zone's `o` (box_keys.go), which means the
// pane the box belongs to - the Mate whose session is open is not
// necessarily the row the tree's cursor is sitting on.
func (m Model) actionChoicesForRow(selected row) []actionChoice {
	choices := make([]actionChoice, 0, 8)
	choices = append(choices, m.startChoice(selected), m.stopChoice(selected), m.resumeChoice(selected), m.repairChoice(selected), m.onboardChoice(selected))
	// The Crew row's own entry (mvp.md task 21). It is appended only on a
	// Crew row rather than listed as "unavailable · applies to a Crew"
	// everywhere else, for the same reason the Mate's two recovery actions
	// are appended only on a Mate row: a menu that names every action the
	// Console has, on every row, is a menu of refusals.
	if selected.kind == rowCrew {
		choices = append(choices, m.diffChoice(selected))
	}
	// Capability is authored by the store-backed loader. The local builders above
	// only supply row-specific wording and target identity; availability is
	// replaced from the DTO so this surface cannot drift from other clients.
	for i := range choices {
		if available, reason, found := m.actionCapability(selected, choices[i]); found {
			choices[i].enabled = available
			if !available {
				choices[i].desc = "unavailable · " + reason
			}
		}
	}
	// The Mate's two recovery actions. They are here rather than on the rail
	// header (2026-09-19, session_focus.go): both are rare, both are about a
	// Mate that is already misbehaving, and a menu is where a reader goes
	// looking for something they do not do every day. The restart carries
	// `dangerous`, so the menu's own confirmation stands in front of the one
	// that stops a live agent.
	if selected.kind == rowMate {
		if project := m.currentProject().ProjectID; project != "" {
			choices = append(choices, restartMateChoice(project), clearComposerChoice(project))
		}
	}
	if c, ok := m.mergeChoice(selected); ok {
		choices = append(choices, c)
	}
	return choices
}

// mergeChoice is the `merge` entry of a Crew row's menu (mvp.md task 22).
// The second return is whether the entry exists at all: unlike every other
// action here, merge is absent rather than disabled on a row it does not
// apply to, because "merge" on a Crew that has not handed back is not a
// refusal a reader needs explained - it is an action that does not belong
// to that row yet. A Crew announces it has handed back by appending
// `wait-mate`, and that is exactly the state the entry appears in.
//
// The branch and the default branch are read from the snapshot the row was
// drawn from, so the confirmation names the same two branches the command
// will move; a Crew whose worktree or repo field could not be read still
// gets the entry, worded generically, because the command re-reads both
// facts itself and refuses on its own if they disagree.
func (m Model) mergeChoice(r row) (actionChoice, bool) {
	if r.kind != rowCrew {
		return actionChoice{}, false
	}
	crew, ok := m.crewByID(r.id)
	if !ok || crew.Status != query.CrewWaitMate {
		return actionChoice{}, false
	}
	project := m.currentProject().ProjectID
	if project == "" {
		return actionChoice{}, false
	}
	branch := "this crew's branch"
	if crew.Worktree.IsKnown() && crew.Worktree.Value.Branch != "" {
		branch = crew.Worktree.Value.Branch
	}
	into := "the default branch"
	if crew.Repo.IsKnown() && crew.Repo.Value.DefaultBranch != "" {
		into = crew.Repo.Value.DefaultBranch
	}
	return actionChoice{
		action: ActionMerge, enabled: true, dangerous: true,
		desc:          "Merge " + branch + " into " + into + " and finish this crew",
		confirmPrompt: fmt.Sprintf("Merge %s into %s and finish crew %s?", branch, into, crew.CrewID),
		req: ActionRequest{
			Action: ActionMerge, Target: project, TargetKind: "crew", Crew: crew.CrewID,
		},
	}, true
}

// actionCapability finds the store-backed loader's capability for one
// already-built choice. The source is the choice's own target, never just
// the selected row: at the Workspace level a selected Project row carries
// its *own* onboard capability ("this Project already has a Mate"), which
// says nothing about whether another Project may be added to the Workspace -
// reading it there disabled "Add a Project to this workspace" and explained
// that refusal with a fact about a different object.
func (m Model) actionCapability(r row, c actionChoice) (bool, string, bool) {
	if c.req.TargetKind == "workspace" {
		return lookupAction(m.tree.Actions, c.action)
	}
	return m.queryAction(r, c.action)
}

func (m Model) queryAction(r row, name Action) (bool, string, bool) {
	var actions []query.ActionAvailability
	if m.cur().kind == frameWorkspace {
		if r.kind == rowProject {
			if p, ok := m.projectByID(r.id); ok {
				actions = p.Actions
			}
		} else {
			actions = m.tree.Actions
		}
	} else {
		switch r.kind {
		case rowMate:
			actions = m.currentProject().Mate.Actions
		case rowCrew:
			if c, ok := m.crewByID(r.id); ok {
				actions = c.Actions
			}
		case rowProject:
			if p, ok := m.projectByID(r.id); ok {
				actions = p.Actions
			}
		default:
			actions = nil
		}
	}
	return lookupAction(actions, name)
}

func lookupAction(actions []query.ActionAvailability, name Action) (bool, string, bool) {
	for _, a := range actions {
		if a.Action == string(name) {
			return a.Available, a.Reason, true
		}
	}
	return false, "", false
}

func (m Model) startChoice(r row) actionChoice {
	c := actionChoice{action: ActionStart, desc: "unavailable · applies to a Mate", req: m.actionRequest(ActionStart, r)}
	mate, ok := m.mateForRow(r)
	if !ok {
		return c
	}
	if !mate.Designated.IsKnown() || mate.Designated.Value.MateID == "" {
		c.desc = "unavailable · this Project has no Mate; use onboard"
		return c
	}
	if mate.Designated.Value.Status != query.MateCreated {
		c.desc = "unavailable · Mate is recorded " + string(mate.Designated.Value.Status)
		return c
	}
	c.enabled, c.desc = true, "Start the Project Mate"
	return c
}

func (m Model) stopChoice(r row) actionChoice {
	c := actionChoice{action: ActionStop, dangerous: true, desc: "unavailable · no confirmed binding to stop", req: m.actionRequest(ActionStop, r)}
	if r.kind != rowMate && r.kind != rowCrew {
		return c
	}
	binding := m.bindingForRow(r)
	if binding.State != query.Known {
		if binding.State == query.Unknown {
			c.desc = "unavailable · binding unknown; r re-reads"
		}
		return c
	}
	if binding.Value.Status != query.BindingActive {
		c.desc = "unavailable · no active binding to stop"
		return c
	}
	c.enabled, c.desc = true, "Stop the recorded runtime agent"
	return c
}

func (m Model) resumeChoice(r row) actionChoice {
	c := actionChoice{action: ActionResume, desc: "unavailable · applies to a stopped Mate", req: m.actionRequest(ActionResume, r)}
	mate, ok := m.mateForRow(r)
	if !ok {
		return c
	}
	if mate.Designated.State != query.Known || mate.Designated.Value.MateID == "" {
		c.desc = "unavailable · this Project has no Mate; use onboard"
		return c
	}
	if mate.Designated.Value.Status != query.MateStopped {
		c.desc = "unavailable · Mate is recorded " + string(mate.Designated.Value.Status)
		return c
	}
	c.enabled, c.desc = true, "Resume the Project Mate"
	return c
}

func (m Model) repairChoice(r row) actionChoice {
	c := actionChoice{action: ActionRepair, dangerous: true, desc: "unavailable · no recorded repair is needed", req: m.actionRequest(ActionRepair, r)}
	if r.kind != rowCrew {
		return c
	}
	crew, ok := m.crewByID(r.id)
	if !ok {
		c.desc = "unavailable · attempt is not in the snapshot"
		return c
	}
	if crew.Binding.State == query.Known && crew.Binding.Value.Status == query.BindingStale {
		c.enabled, c.desc = true, "Clear the stale binding record; keep worktree and branch"
		return c
	}
	if crew.Binding.State == query.Unknown {
		c.desc = "unavailable · binding is unknown; refresh before repair"
		return c
	}
	return c
}

func (m Model) onboardChoice(r row) actionChoice {
	c := actionChoice{action: ActionOnboard, desc: "unavailable · use this action at the workspace or a Project without a Mate", req: m.actionRequest(ActionOnboard, r)}
	if m.cur().kind == frameWorkspace {
		c.enabled, c.desc = true, "Add a Project to this workspace"
		c.req.TargetKind = "workspace"
		return c
	}
	if r.kind == rowMate {
		mate := m.currentProject().Mate
		if mate.Designated.State == query.Absent {
			c.enabled, c.desc = true, "Create and start this Project's Mate"
			c.req.Target = m.currentProject().ProjectID
			c.req.TargetKind = "project-mate"
		}
	}
	return c
}

// diffChoice is the Crew row's review entry (mvp.md task 21). It is offered
// in every state a crew can be in as long as the crew records a branch:
// reviewing is what a reader does *before* deciding a crew is done, so
// restricting it to `wait-mate` would withhold it exactly when somebody is
// trying to find out whether a crew has got anywhere.
//
// It is not dangerous and it takes no confirmation, because it writes
// nothing: no ref, no file, no pane. A crew mid-turn is untouched by it.
//
// The request names the Project in Target and the Crew in Crew, the shape
// the box's own actions use: the bridge needs both to find crews/<id>.meta,
// and a single string would make it guess which one it had been handed.
func (m Model) diffChoice(r row) actionChoice {
	c := actionChoice{action: ActionDiff, desc: "unavailable · applies to a Crew"}
	if r.kind != rowCrew || m.cur().kind != frameProject {
		return c
	}
	project := m.currentProject().ProjectID
	c.req = ActionRequest{Action: ActionDiff, Target: project, TargetKind: "crew", Crew: r.id}
	crew, ok := m.crewByID(r.id)
	if !ok {
		c.desc = "unavailable · this Crew is not in the snapshot"
		return c
	}
	switch {
	case crew.Worktree.IsKnown() && crew.Worktree.Value.Branch != "":
		c.enabled, c.desc = true, "Show "+crew.Worktree.Value.Branch+" against the default branch"
	case crew.Worktree.IsKnown(), crew.Worktree.State == query.Absent:
		// The read succeeded and there is no branch. That is a fact.
		c.desc = "unavailable · this Crew records no branch to compare"
	default:
		// The read failed, which is a different thing and must not be
		// reported as "there is no branch" (query.FieldState's whole point).
		c.desc = "unavailable · the worktree record could not be read; r re-reads"
	}
	return c
}

func (m Model) actionRequest(a Action, r row) ActionRequest {
	req := ActionRequest{Action: a}
	switch r.kind {
	case rowProject:
		req.Target, req.TargetKind = r.id, "project"
		if a == ActionStart || a == ActionResume {
			req.TargetKind = "mate"
		}
	case rowMate:
		req.Target, req.TargetKind = m.currentProject().ProjectID, "mate"
	case rowCrew:
		req.Target, req.TargetKind = r.id, "crew"
	default:
		req.TargetKind = "workspace"
	}
	return req
}

func (m Model) mateForRow(r row) (query.MateNode, bool) {
	switch r.kind {
	case rowMate:
		return m.currentProject().Mate, true
	case rowProject:
		p, ok := m.projectByID(r.id)
		if ok {
			return p.Mate, true
		}
	}
	return query.MateNode{}, false
}

func (m Model) bindingForRow(r row) query.Field[query.BindingValue] {
	if r.kind == rowMate {
		return m.currentProject().Mate.Binding
	}
	if r.kind == rowCrew {
		if c, ok := m.crewByID(r.id); ok {
			return c.Binding
		}
	}
	return query.Field[query.BindingValue]{State: query.Unknown, Reason: "row is not a bindable entity"}
}

func (m Model) beginActions() Model {
	m.actions = true
	m.confirm = nil
	m.actionInputMode = false
	m.actionInput = ""
	m.actionRepo, m.actionField = "", fieldName
	m.actionChoices = m.actionChoicesForSelected()
	m.actionRow, _ = m.selectedRow()
	m.actionIndex = 0
	m.msg = footerMsg{}
	return m
}

// beginNewProject is the 'n' key: it opens the name input for a new
// Project directly, without the action menu in between. Creating a Project
// is the one thing a reader can do on a workspace that has none (ADR 0023
// dropped the default Project), so it is the one action that gets its own
// key rather than five keystrokes through a menu of entries that all refuse.
//
// The menu is deliberately NOT opened behind it: Esc from here closes the
// overlay and returns the reader to the list they were on, rather than
// stranding them in a menu they never asked for.
func (m Model) beginNewProject() Model {
	choice, ok := m.newProjectChoice()
	if !ok {
		m.msg = errMsg("Action refused: " + strings.TrimPrefix(choice.desc, "unavailable · ") + " · nothing started")
		return m
	}
	m.actions = false
	m.confirm = nil
	m.actionChoices = nil
	m.actionIndex = 0
	m.pendingChoice = choice
	m.actionInputMode = true
	m.actionInput = ""
	m.actionRepo, m.actionField = "", fieldName
	m.msg = footerMsg{}
	return m
}

// newProjectChoice is the workspace-level onboard action as the 'n' key
// reaches it. Availability is the store-backed loader's own (m.tree.Actions), the
// same source the menu entry uses, so the shortcut and the menu cannot
// disagree about whether a Project may be added.
func (m Model) newProjectChoice() (actionChoice, bool) {
	if m.cur().kind != frameWorkspace {
		return actionChoice{action: ActionOnboard,
			desc: "unavailable · a Project is created at the workspace level; Esc goes back there",
		}, false
	}
	c := actionChoice{
		action: ActionOnboard, desc: "Add a Project to this workspace", enabled: true,
		req: ActionRequest{Action: ActionOnboard, TargetKind: "workspace"},
	}
	if available, reason, found := lookupAction(m.tree.Actions, ActionOnboard); found && !available {
		c.enabled, c.desc = false, "unavailable · "+reason
	}
	return c, c.enabled
}

func (m Model) closeActions() Model {
	m.actions = false
	m.confirm = nil
	m.actionInputMode = false
	m.actionInput = ""
	m.actionRepo, m.actionField = "", fieldName
	m.pendingChoice = actionChoice{}
	m.harnessPick = false
	m.harnessIndex = 0
	m.actionChoices = nil
	m.actionIndex = 0
	return m
}

func (m Model) selectedAction() (actionChoice, bool) {
	if m.actionIndex < 0 || m.actionIndex >= len(m.actionChoices) {
		return actionChoice{}, false
	}
	return m.actionChoices[m.actionIndex], true
}

func (m Model) handleActionEnter() (Model, tea.Cmd) {
	choice, ok := m.selectedAction()
	if !ok {
		return m, nil
	}
	if !choice.enabled {
		m.msg = errMsg("Action refused: " + strings.TrimPrefix(choice.desc, "unavailable · ") + " · nothing started")
		return m, nil
	}
	if choice.action == ActionOnboard && choice.req.TargetKind == "workspace" {
		m.actionInputMode = true
		m.actionInput = ""
		m.actionRepo, m.actionField = "", fieldName
		m.pendingChoice = choice
		return m, nil
	}
	if choice.dangerous {
		m.confirm = &actionConfirmation{choice: choice}
		return m, nil
	}
	return m.runAction(choice)
}

// onActionOverlayKey is the menu's and the confirmation's own keyboard. It
// is one implementation because two surfaces route to it: onKey, on the
// project frame, and the session view's box zone (session_mode.go), where
// the overlay is drawn over the whole frame (view.go) and nothing behind it
// may answer a key.
func (m Model) onActionOverlayKey(key string) (Model, tea.Cmd) {
	if m.confirm != nil {
		switch key {
		case "esc", "backspace":
			m.confirm = nil
			m.actions = true
			return m, nil
		case "enter":
			choice := m.confirm.choice
			m.confirm = nil
			return m.runAction(choice)
		}
		return m, nil
	}
	switch key {
	case "esc", "backspace":
		return m.closeActions(), nil
	case "up", "k":
		if m.actionIndex > 0 {
			m.actionIndex--
		}
		return m, nil
	case "down", "j":
		if m.actionIndex+1 < len(m.actionChoices) {
			m.actionIndex++
		}
		return m, nil
	case "enter":
		return m.handleActionEnter()
	}
	return m, nil
}

// onboardField is one field of the new-project form: the Project's name,
// and optionally the git repository it starts with (`mate project add
// <name> [<repo>]`). A Project may be created with no repo and given repos
// later with `mate project repo add` (docs/mvp.md M9).
type onboardField int

const (
	fieldName onboardField = iota
	fieldRepo
)

// Input limits: a name longer than this is no name anyone types, and a path
// longer than PATH_MAX-ish is not a path this filesystem holds.
const (
	onboardNameLimit = 120
	onboardRepoLimit = 1024
)

func (m Model) onActionInputKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "shift+tab", "up", "down":
		m.actionField = 1 - m.actionField
		return m, nil
	case "esc", "backspace":
		if msg.String() == "backspace" {
			if field := m.onboardValue(); *field != "" {
				r := []rune(*field)
				*field = string(r[:len(r)-1])
				return m, nil
			}
			// Backspace on an empty Repo steps back into Name, the way
			// it would in any two-field form, rather than throwing the
			// typed name away.
			if m.actionField == fieldRepo {
				m.actionField = fieldName
				return m, nil
			}
		}
		// Esc unwinds exactly one step: back to the menu when the menu is
		// what opened this input, and out of the overlay entirely when the
		// 'n' key did (beginNewProject leaves m.actions false).
		if m.actions {
			m.actionInputMode = false
			m.actionInput = ""
			m.actionRepo, m.actionField = "", fieldName
			m.pendingChoice = actionChoice{}
			return m, nil
		}
		return m.closeActions(), nil
	case "enter":
		if strings.TrimSpace(m.actionInput) == "" {
			m.actionField = fieldName
			m.msg = errMsg("Action refused: project name is required · nothing started")
			return m, nil
		}
		// Enter on the name moves on to the repo, so the common case is
		// name, Enter, path, Enter - never a submit with the path unasked.
		// Enter on an empty repo is the answer "no repo yet": the Project
		// is created without one.
		if m.actionField == fieldName && strings.TrimSpace(m.actionRepo) == "" {
			m.actionField = fieldRepo
			m.msg = footerMsg{}
			return m, nil
		}
		// The pending choice is the input's own, not whatever the menu
		// cursor happens to sit on: the 'n' key never opened a menu.
		choice := m.pendingChoice
		if choice.action == "" {
			return m.closeActions(), nil
		}
		choice.req.Input = strings.TrimSpace(m.actionInput)
		choice.req.Repo = strings.TrimSpace(m.actionRepo)
		return m.runAction(choice)
	}
	field, limit, label := &m.actionInput, onboardNameLimit, "Project name"
	if m.actionField == fieldRepo {
		field, limit, label = &m.actionRepo, onboardRepoLimit, "Repo path"
	}
	for _, r := range msg.Runes {
		if unicode.IsPrint(r) && !unicode.IsSpace(r) || r == ' ' {
			if len([]rune(*field)) < limit {
				*field += string(r)
				m.msg = footerMsg{}
			} else {
				m.msg = infoMsg(fmt.Sprintf("%s limit reached (%d characters); further input is ignored", label, limit))
			}
		}
	}
	return m, nil
}

// onboardValue is the field the keyboard is typing into.
func (m *Model) onboardValue() *string {
	if m.actionField == fieldRepo {
		return &m.actionRepo
	}
	return &m.actionInput
}

func (m Model) runAction(choice actionChoice) (Model, tea.Cmd) {
	m.actions = false
	m.actionInputMode = false
	m.harnessPick = false
	m.pendingChoice = actionChoice{}
	m.actionBusy = true
	m.actionRunningDesc = string(choice.action) + " " + actionObject(choice)
	m.msg = infoMsg("Running " + m.actionRunningDesc + " " + m.g.Ellipsis)
	if boxAction(choice.action) {
		m.boxMsg = m.msg
	}
	// The context is a child of the program's own (baseCtx), not
	// context.Background(): cancel is stored so quitting mid-action (see
	// onKey's actionBusy branch) has a real signal to send the goroutine
	// Bubble Tea otherwise leaks until it returns on its own.
	ctx, cancel := context.WithCancel(m.baseCtx())
	m.actionCancel = cancel
	action, req := m.action, choice.req
	return m, func() tea.Msg {
		if action == nil {
			return actionDoneMsg{choice: choice, err: fmt.Errorf("Console actions are not wired")}
		}
		text, err := action(ctx, req)
		return actionDoneMsg{choice: choice, text: text, err: err}
	}
}

// actionObject is what the running line and the confirmation name as the
// thing being acted on. A request that carries a Crew is about that crew
// whatever object it routes through - the box's own actions and the Crew
// row's diff both put the Project in Target, because that is what the
// bridge needs to find the files - so the crew wins when there is one.
func actionObject(c actionChoice) string {
	if c.req.Crew != "" {
		return c.req.Crew
	}
	if c.req.Target != "" {
		return c.req.Target
	}
	return "workspace"
}

func (m Model) onActionDone(msg actionDoneMsg) (Model, tea.Cmd) {
	m.actionBusy = false
	m.actionCancel = nil
	m.actionRunningDesc = ""
	m.confirm = nil
	m.actions = false
	m.actionChoices = nil
	// A diff's whole result is the overlay (diff.go): the text is a
	// screenful, and the message line would show a truncated first line of
	// it. A diff that *failed* still takes the message line - there is no
	// overlay to put a refusal in, and an empty frame would read as success.
	//
	// The session view has no body region to draw an overlay into (view.go),
	// so an ActionDiff that somehow arrived from there is answered the
	// ordinary way rather than opening a surface nothing would render.
	if msg.choice.action == ActionDiff && msg.err == nil && m.sess.phase == sessionIdle {
		// Nothing is re-read: a diff changes no recorded state, so asking
		// the loader again would only be a chance to drop the overlay.
		return m.openDiff(msg.choice.req.Crew, m.diffBranch(msg.choice.req.Crew), msg.text), nil
	}
	var result footerMsg
	if msg.err != nil {
		// This path means the service was actually called. It is therefore a
		// failure, not the preflight refusal used by disabled menu entries.
		result = errMsg("Action failed: " + msg.err.Error() + " " + m.g.Dot + " r re-reads")
	} else {
		text := msg.text
		if text == "" {
			text = string(msg.choice.action) + " completed"
		}
		result = okMsg("Action " + string(msg.choice.action) + " completed " + m.g.Dot + " " + text)
	}
	if boxAction(msg.choice.action) {
		// The box actions get their own wording. "Action forward failed" is
		// the shape of a menu entry's report, and these are not menu entries:
		// the reader pressed a key on a line in the rail, and what they need
		// back is whether that line reached the Mate and why not.
		result = boxOutcome(msg, m.g)
		m.boxMsg = result
	}
	m.actionAfterRead = &result
	m.msg = result
	// A box action changes exactly what the box shows: an assign records a
	// line to the Mate, and the Mate's own answer then drops the item by
	// rule 2 of the inbox on a later read. Waiting for the ordinary
	// one-second metadata tick would leave the answered item under the
	// reader's cursor long enough for them to act on it twice, so the
	// session's own box is re-read now.
	m, load := m.startLoad()
	return m, tea.Batch(load, m.sessionBoxRefreshCmd())
}

func (m Model) actionObjectDescription(c actionChoice) (string, string, string) {
	object := c.req.Target
	if object == "" {
		object = "workspace"
	}
	scope := "Recorded state and the runtime resources named by this action."
	effect := "The snapshot will be re-read after the application service returns."
	switch c.req.Action {
	case ActionStop:
		scope = "The recorded runtime agent only; the Crew's worktree and branch are untouched."
		effect = "The runtime binding is released only after the service confirms its outcome."
	case ActionRepair:
		scope = "The recorded binding/worktree metadata only; no live agent is assumed."
		effect = "A stale binding may be cleared. Unknown state is never treated as proof of safe cleanup."
	case ActionMerge:
		object = c.req.Target + "/" + c.req.Crew
		scope = "The Project's default branch in the primary repo, and this Crew's branch, worktree and pane."
		effect = "A fast-forward only; anything else refuses and changes nothing. If it lands, the Crew is finished and its branch and worktree are removed."
	}
	return object, scope, effect
}

func (m Model) actionLines(w, h int) []*line {
	if h <= 0 {
		return nil
	}
	if m.confirm != nil {
		return m.confirmationLines(w, h, m.confirm.choice)
	}
	if m.actionInputMode {
		return m.onboardInputLines(w, h)
	}
	if m.harnessPick {
		return m.harnessPickerLines(w, h)
	}
	out := []*line{newLine().pad(2).add("ACTIONS", m.p.Bold)}
	// The row the choices were built for, which is not always the row under
	// the list's cursor: the box zone's `o` builds the menu for the pane it
	// belongs to (box_keys.go), and a title naming a different row would be
	// naming something none of the entries act on.
	if m.actionRow.id != "" {
		out[0].add("  "+m.actionRow.id, m.p.Dim)
	}
	out = append(out, newLine().pad(2).add(strings.Repeat(m.g.HRule, maxInt(1, w-4)), m.p.Faint))
	for i, c := range m.actionChoices {
		style := m.p.Fg
		if i == m.actionIndex {
			style = m.p.Bold
		}
		l := selectRow(newLine(), i == m.actionIndex, m.p).pad(2)
		if i == m.actionIndex {
			l.add(m.g.Selected+" ", m.p.Acc)
		} else {
			l.add("  ", m.p.Dim)
		}
		l.add(string(c.action), style).add("  ", m.p.Dim)
		l.add(c.desc, m.p.Dim)
		out = append(out, l)
	}
	out = append(out, newLine().pad(2).add(strings.Repeat(m.g.HRule, maxInt(1, w-4)), m.p.Faint))
	out = append(out, newLine().pad(2).add("↑↓ Move  Enter Run  Esc Close", m.p.Dim))
	return out
}

// confirmLabelWidth is the confirmation overlay's own label column: "Object  ",
// "Scope   " and "Effect  " are each 8 cells including their padding.
const confirmLabelWidth = 8

// confirmField renders one Object/Scope/Effect line, wrapping its value the
// way the inspector wraps its own fields (seams.go's field/note) rather than
// letting line.render cut it silently at the pane edge. Scope and Effect are
// mandatory prose (the design's own rule), so losing their tail with no "…"
// marker - the counter-review's B7, an 80-column Scope that lost its
// "...are untouched" clause - is exactly what wrapping instead of cutting
// prevents.
func (m Model) confirmField(label, value string, style lipgloss.Style, w int) []*line {
	valueWidth := maxInt(1, w-4-confirmLabelWidth)
	wrapped := wrapAfterSlash(value, valueWidth)
	out := []*line{newLine().pad(2).add(padRight(label, confirmLabelWidth), m.p.Dim).add(wrapped[0], style)}
	for _, cont := range wrapped[1:] {
		out = append(out, newLine().pad(2+confirmLabelWidth).add(cont, style))
	}
	return out
}

func (m Model) confirmationLines(w, h int, choice actionChoice) []*line {
	object, scope, effect := m.actionObjectDescription(choice)
	headline := "CONFIRM " + string(choice.action) + "?"
	if choice.confirmPrompt != "" {
		headline = choice.confirmPrompt
	}
	out := []*line{
		newLine().pad(2).add(headline, m.p.Bold),
		newLine().pad(2).add(strings.Repeat(m.g.HRule, maxInt(1, w-4)), m.p.Faint),
	}
	out = append(out, m.confirmField("Object", object, m.p.Fg, w)...)
	out = append(out, m.confirmField("Scope", scope, m.p.Fg, w)...)
	out = append(out, m.confirmField("Effect", effect, m.p.Amber, w)...)
	out = append(out,
		newLine(),
		newLine().pad(2).add("Enter ", m.p.Fg).add(string(choice.action)+" "+object, m.p.Dim).add("  Esc ", m.p.Fg).add("Cancel", m.p.Dim),
	)
	return fitLines(out, h)
}

func (m Model) onboardInputLines(w, h int) []*line {
	field := func(f onboardField, label, value string) *line {
		l := newLine().pad(2)
		style := m.p.Dim
		if m.actionField == f {
			l.add(m.g.Selected+" ", m.p.Acc)
			style = m.p.Fg
			value += "_"
		} else {
			l.add("  ", m.p.Dim)
		}
		return l.add(padRight(label, onboardLabelWidth), m.p.Dim).add(value, style)
	}
	// The root gets a line of its own: a real workspace path is long, and
	// after the sentence that introduces it, it would be the part cut off
	// at the pane edge.
	root := "the workspace root"
	if ws := m.tree.Workspace; ws.IsKnown() && ws.Value.Root != "" {
		root = ws.Value.Root
	}
	enter := m.onboardEnterLabel()
	out := []*line{
		newLine().pad(2).add("NEW PROJECT", m.p.Bold),
		newLine().pad(2).add(strings.Repeat(m.g.HRule, maxInt(1, w-4)), m.p.Faint),
		field(fieldName, "Name", m.actionInput),
		field(fieldRepo, "Repo (optional)", m.actionRepo),
		newLine(),
		newLine().pad(2).add("Repo is a git repo root, absolute or relative to the workspace:", m.p.Dim),
		newLine().pad(4).add(root, m.p.Fg),
		newLine().pad(2).add("Leave it empty to create the Project with no repo; add repos later with `mate project repo add`.", m.p.Dim),
		newLine().pad(2).add("Registers a Project, like `mate project add`; no runtime agent is started.", m.p.Dim),
		newLine(),
		newLine().pad(2).add("Enter ", m.p.Fg).add(enter, m.p.Dim).add("  Tab ", m.p.Fg).add("Switch field", m.p.Dim).add("  Esc ", m.p.Fg).add("Cancel", m.p.Dim),
	}
	return fitLines(out, h)
}

// onboardLabelWidth fits the form's longest label, "Repo (optional)", plus
// a gap.
const onboardLabelWidth = 17

// onboardEnterLabel is what Enter does in the new-project form right now,
// for the key line: move on to the repo, create the Project with the typed
// repo, or create it with none.
func (m Model) onboardEnterLabel() string {
	switch {
	case strings.TrimSpace(m.actionRepo) != "":
		return "Create project"
	case m.actionField == fieldRepo:
		return "Create without repo"
	default:
		return "Next"
	}
}

// ---------- the Mate keys: s (create/start/resume) and h (change harness) ----------
//
// These are the captain's Project-screen affordances: on a Project screen the
// Mate actions are reachable by their own key rather than five keystrokes
// through the `a` menu, whose other entries mostly refuse there. `a` still
// lists everything; these are shortcuts into the same choices, so both
// surfaces read availability from the store-backed loader and cannot disagree.

// harnessOrder is the picker's order, and claude is first because it is the
// configured default for a new Mate (config.DefaultMateHarness).
var harnessOrder = []query.HarnessKind{query.HarnessClaude, query.HarnessCodex}

// beginMateStart is the 's' key. One key covers create, start and resume
// because they are one thing to the reader - "get this Project's Mate
// running" - and because the service behind them is one call. Only a create
// asks which agent to use: an existing Mate already records its harness.
func (m Model) beginMateStart() (Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m.refuseMateKey("no row is selected"), nil
	}
	mate, ok := m.mateForRow(r)
	if !ok {
		return m.refuseMateKey("this applies to a Project's Mate row"), nil
	}
	choice := m.mateStartChoice(r, mate)
	if !choice.enabled {
		return m.refuseMateKey(strings.TrimPrefix(choice.desc, "unavailable · ")), nil
	}
	if choice.action == ActionOnboard {
		return m.beginHarnessPick(choice), nil
	}
	return m.runPending(choice)
}

// beginModeToggle is the 'm' key: flip one Project's communication mode
// (mvp.md section 5). It runs through the same ActionFunc seam every other
// action does - this package never writes the `.auto` flag itself - and the
// refreshed label comes back on the re-read onActionDone schedules, never
// from optimistically flipping a local copy.
//
// project is passed in rather than read from the selection because the two
// call sites name it differently: the project frame's selected row belongs
// to m.currentProject(), while the session view has left the navigation
// stack behind and knows only its target's ProjectID.
func (m Model) beginModeToggle(project string) (Model, tea.Cmd) {
	if project == "" {
		return m.refuseModeKey("no Project is selected"), nil
	}
	if m.action == nil {
		return m.refuseModeKey("Console actions are not wired"), nil
	}
	return m.runAction(actionChoice{
		action:  ActionMode,
		enabled: true,
		desc:    "Toggle the communication mode",
		req:     ActionRequest{Action: ActionMode, Target: project, TargetKind: "project"},
	})
}

func (m Model) refuseModeKey(reason string) Model {
	m.msg = errMsg("Action refused: " + reason + " " + m.g.Dot + " mode unchanged")
	return m
}

func (m Model) refuseMateKey(reason string) Model {
	m.msg = errMsg("Action refused: " + reason + " · nothing started")
	return m
}

// mateStartChoice is the 's' key's choice: which of create/start/resume this
// Mate's recorded status allows, with the wording the key line also uses.
// Availability is replaced from the query DTO by actionCapability, exactly
// like every menu entry.
func (m Model) mateStartChoice(r row, mate query.MateNode) actionChoice {
	c := actionChoice{action: ActionStart, req: m.actionRequest(ActionStart, r)}
	switch {
	case mate.Designated.State == query.Unknown:
		c.action = ActionStart
		c.desc = "unavailable · the Mate could not be read; r re-reads"
	case mate.Designated.State == query.Absent || mate.Designated.Value.MateID == "":
		c = m.onboardChoice(r)
		c.req.Target, c.req.TargetKind = m.currentProject().ProjectID, "project-mate"
		c.enabled, c.desc = true, "Create and start this Project's Mate"
	case mate.Designated.Value.Status == query.MateStopped:
		c = actionChoice{action: ActionResume, enabled: true, desc: "Resume the Project Mate", req: m.actionRequest(ActionResume, r)}
	case mate.Designated.Value.Status == query.MateCreated:
		c = actionChoice{action: ActionStart, enabled: true, desc: "Start the Project Mate", req: m.actionRequest(ActionStart, r)}
	default:
		c.desc = "unavailable · Mate is recorded " + string(mate.Designated.Value.Status)
	}
	if available, reason, found := m.actionCapability(r, c); found {
		c.enabled = available
		if !available {
			c.desc = "unavailable · " + reason
		}
	}
	return c
}

// mateStartLabel is what the key line says 's' will do, and false when the
// key has nothing to offer on this row. It follows the same choice the key
// runs, so the label can never promise a different action than the keystroke
// performs.
func (m Model) mateStartLabel(r row) (string, bool) {
	mate, ok := m.mateForRow(r)
	if !ok {
		return "", false
	}
	c := m.mateStartChoice(r, mate)
	if !c.enabled {
		return "", false
	}
	switch c.action {
	case ActionOnboard:
		return "Create mate", true
	case ActionResume:
		return "Resume mate", true
	default:
		return "Start mate", true
	}
}

// beginHarnessPick opens the agent chooser for an already-built choice. Like
// beginNewProject it leaves the menu closed, so Esc returns to the list
// rather than a menu the reader never opened.
func (m Model) beginHarnessPick(choice actionChoice) Model {
	m.actions = false
	m.confirm = nil
	m.actionInputMode = false
	m.actionChoices = nil
	m.actionIndex = 0
	m.pendingChoice = choice
	m.harnessPick = true
	m.harnessIndex = m.recordedHarnessIndex()
	m.msg = footerMsg{}
	return m
}

// recordedHarnessIndex starts the cursor on the harness the Mate already
// records, so the picker begins from what is true rather than from the top
// of the list. A Mate with no readable harness starts at the default.
func (m Model) recordedHarnessIndex() int {
	mate := m.currentProject().Mate
	if !mate.Designated.IsKnown() {
		return 0
	}
	for i, kind := range harnessOrder {
		if kind == mate.Designated.Value.HarnessKind {
			return i
		}
	}
	return 0
}

func (m Model) closeHarnessPick() Model {
	m.harnessPick = false
	m.harnessIndex = 0
	m.pendingChoice = actionChoice{}
	return m
}

// onHarnessKey drives the picker. Enter completes the pending choice with the
// selected kind and then takes the same branch handleActionEnter would: a
// dangerous action opens the confirmation, everything else runs.
func (m Model) onHarnessKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		return m.closeHarnessPick(), nil
	case "up", "k":
		if m.harnessIndex > 0 {
			m.harnessIndex--
		}
		return m, nil
	case "down", "j":
		if m.harnessIndex+1 < len(harnessOrder) {
			m.harnessIndex++
		}
		return m, nil
	case "enter":
		choice := m.pendingChoice
		choice.req.Harness = harnessOrder[m.harnessIndex]
		m = m.closeHarnessPick()
		return m.runPending(choice)
	default:
		return m, nil
	}
}

// runPending is handleActionEnter's tail: confirm first when the action is
// destructive, otherwise run it.
func (m Model) runPending(choice actionChoice) (Model, tea.Cmd) {
	if choice.dangerous {
		m.confirm = &actionConfirmation{choice: choice}
		return m, nil
	}
	return m.runAction(choice)
}

func (m Model) harnessPickerLines(w, h int) []*line {
	// TODO(task 10): a switch_harness pending choice retitled this
	// "CHANGE HARNESS". Restarting a Mate under another harness is task
	// 10's surface.
	const title = "CHOOSE AN AGENT"
	out := []*line{
		newLine().pad(2).add(title, m.p.Bold),
		newLine().pad(2).add(strings.Repeat(m.g.HRule, maxInt(1, w-4)), m.p.Faint),
	}
	for i, kind := range harnessOrder {
		style := m.p.Fg
		if i == m.harnessIndex {
			style = m.p.Bold
		}
		l := selectRow(newLine(), i == m.harnessIndex, m.p).pad(2)
		if i == m.harnessIndex {
			l.add(m.g.Selected+" ", m.p.Acc)
		} else {
			l.add("  ", m.p.Dim)
		}
		l.add(string(kind), style)
		if m.recordedHarness() == kind {
			l.add("  ", m.p.Dim).add("current", m.p.Dim)
		}
		out = append(out, l)
	}
	out = append(out, newLine())
	out = append(out, newLine().pad(2).add("The new Mate is created and started with this agent.", m.p.Dim))
	out = append(out,
		newLine(),
		newLine().pad(2).add("Enter ", m.p.Fg).add("Use "+string(harnessOrder[m.harnessIndex]), m.p.Dim).add("  Esc ", m.p.Fg).add("Cancel", m.p.Dim),
	)
	return fitLines(out, h)
}

// recordedHarness is the harness the selected Project's Mate records, or ""
// when there is none or it could not be read - never a guess.
func (m Model) recordedHarness() query.HarnessKind {
	mate := m.currentProject().Mate
	if !mate.Designated.IsKnown() {
		return ""
	}
	return mate.Designated.Value.HarnessKind
}

// boxAction reports whether an action's outcome belongs on the rail's own
// line as well as the frame's: the assign a box row issues, and the two
// recovery actions, whose whole subject is the Mate the rail sits beside.
// They are answered in their own words (boxOutcome) rather than in the
// menu's, because what the reader needs back is whether the line reached a
// composer, not that "an action completed".
func boxAction(a Action) bool {
	return a == ActionResolve || a == ActionRestartMate || a == ActionClearComposer
}

// boxOutcome is the one line a box action leaves on the outcome line. A
// refusal and a failure share the tone because they share the consequence:
// nothing was typed into anybody's composer. The reason is the error's own -
// internal/send already says which composer state it observed and quotes the
// screen it read that from, and rewording it here would drop exactly the
// detail that tells a reader whether to retry or to go look at the pane.
func boxOutcome(msg actionDoneMsg, g glyphSet) footerMsg {
	// ActionResolve is "Assign" here: the wire word stays `resolve` for the
	// Mate's manual, and the word a reader is answered in is the one on the
	// button they pressed.
	verb := map[Action]string{
		ActionResolve:     "Assign",
		ActionRestartMate: "Restart", ActionClearComposer: "Clear",
	}[msg.choice.action]
	if msg.err != nil {
		tail := " nothing was sent"
		if msg.choice.action == ActionRestartMate || msg.choice.action == ActionClearComposer {
			tail = " nothing was changed"
		}
		return errMsg(verb + " refused: " + msg.err.Error() + " " + g.Dot + tail)
	}
	text := msg.text
	if text == "" {
		text = strings.ToLower(verb) + " delivered"
	}
	if msg.choice.action == ActionResolve {
		// An assign that went through is already done from the reader's
		// side, whether the line is in the composer or queued for it
		// (mvp.md task 30): the past tense says so, and the text says which.
		verb = "Assigned"
	}
	return okMsg(verb + ": " + text)
}
