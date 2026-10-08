package console

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

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
	// confirmKey is the key that confirms, when it is not the entry's own.
	confirmKey string
	req        ActionRequest
}

type actionConfirmation struct {
	choice actionChoice
	// key is the key that asked, and the only one that confirms; label is
	// the entry's own words ("Stop mate…"), which the sheet asks back.
	key, label string
}

type actionDoneMsg struct {
	choice actionChoice
	text   string
	err    error
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
	if r.kind == rowCrew {
		c.desc = "Close the task: stop the agent, remove worktree and branch"
	}
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

// restartCrewChoice is the Crew row's recovery entry (mvp.md M13): start the
// crew's harness again in the worktree it already has, for a pane or agent
// that a Herdr restart or a dead harness took away. `crew spawn` cannot do
// it - the branch and worktree already exist and a spawn refuses both - so
// the entry names the exact recovery the command performs.
//
// It is offered whenever an open crew records a worktree with a branch, because
// the console cannot tell a dead agent from a live one through the
// snapshot's recorded fields alone: the binding is a recorded fact, not a
// liveness probe, and the health column is Absent exactly when Herdr is down
// - the case this exists for. It is dangerous because a live crew is stopped
// first, and the confirmation says so.
func (m Model) restartCrewChoice(r row) actionChoice {
	c := actionChoice{action: ActionRestartCrew, dangerous: true, desc: "unavailable · applies to a Crew"}
	if r.kind != rowCrew || m.cur().kind != frameProject {
		return c
	}
	project := m.currentProject().ProjectID
	c.req = ActionRequest{Action: ActionRestartCrew, Target: project, TargetKind: "crew", Crew: r.id}
	if project == "" {
		c.desc = "unavailable · no Project is open"
		return c
	}
	crew, ok := m.crewByID(r.id)
	if !ok {
		c.desc = "unavailable · this Crew is not in the snapshot"
		return c
	}
	switch {
	case crew.Closed:
		c.desc = "unavailable · this Crew is closed; spawn a new one if the task is not over"
	case crew.Worktree.IsKnown() && crew.Worktree.Value.Branch != "":
		c.enabled, c.desc = true, "Start this Crew's harness again in its worktree ("+crew.Worktree.Value.Branch+")"
	case crew.Worktree.IsKnown(), crew.Worktree.State == query.Absent:
		c.desc = "unavailable · this Crew records no branch to relaunch into; spawn a new crew"
	default:
		c.desc = "unavailable · the worktree record could not be read; r re-reads"
	}
	return c
}

// removeProjectChoice is the Project row's `Remove project…` entry (mvp.md
// task 75). The confirmation counts what the removal will stop, from the
// snapshot the row was drawn from; the command re-reads the live state and
// refuses, changing nothing more, if a stop fails.
func (m Model) removeProjectChoice(r row) actionChoice {
	c := actionChoice{action: ActionRemoveProject, desc: "unavailable · applies to a Project"}
	if r.kind != rowProject {
		return c
	}
	p, ok := m.projectByID(r.id)
	if !ok {
		c.desc = "unavailable · this Project is not in the snapshot"
		return c
	}
	crews := "no running crews"
	switch n := len(p.Crews); n {
	case 0:
	case 1:
		crews = "1 running crew"
	default:
		crews = itoa(n) + " running crews"
	}
	c.enabled, c.dangerous, c.confirmKey = true, true, "y"
	c.desc = "Stop the Mate and " + crews + ", then take " + p.Name + " out of the workspace"
	c.confirmPrompt = "Remove project " + p.Name + "? This stops its Mate and " + crews + ". Its memory, backlog, crew records and repos stay on disk; `mate project add " + p.Name + "` brings it back."
	c.req = ActionRequest{Action: ActionRemoveProject, Target: p.ProjectID, TargetKind: "project"}
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
		// A crew is named within its Project, as every crew action names it.
		req.Target, req.TargetKind, req.Crew = m.currentProject().ProjectID, "crew", r.id
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
	m.menu = nil
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
	m.menu = nil
	m.actionIndex = 0
	return m
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
		// A name the store would refuse is not sent: the rule line under
		// the field already says why, in amber (design K).
		if why := m.newProjectNameProblem(strings.TrimSpace(m.actionInput)); why != "" {
			m.actionField = fieldName
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
	m.actionStartedAt = time.Now()
	m.msg = m.runningLine(0)
	// The context is a child of the program's own (baseCtx), not
	// context.Background(): cancel is stored so quitting mid-action (see
	// onKey's actionBusy branch) has a real signal to send the goroutine
	// Bubble Tea otherwise leaks until it returns on its own.
	ctx, cancel := context.WithCancel(m.baseCtx())
	m.actionCancel = cancel
	done := make(chan struct{})
	m.actionDone = done
	action, req := m.action, choice.req
	return m, func() tea.Msg {
		defer close(done)
		if action == nil {
			return actionDoneMsg{choice: choice, err: fmt.Errorf("Console actions are not wired")}
		}
		text, err := action(ctx, req)
		return actionDoneMsg{choice: choice, text: text, err: err}
	}
}

// runningLine is the footer while an action runs. Past the first second it
// carries the time spent, so a slow start reads as progress rather than a
// hang.
func (m Model) runningLine(elapsed time.Duration) footerMsg {
	// The elapsed time leads: on a 40-column status line the object's name
	// is what gets cut, never how long it has been running.
	text := "Running " + m.actionRunningDesc + " " + m.g.Ellipsis
	if elapsed >= time.Second {
		text = elapsed.Truncate(time.Second).String() + " " + m.g.Dot + " " + text
	}
	return infoMsg(text)
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
	m.menu = nil
	if msg.choice.action == ActionNotice {
		if msg.err != nil {
			m.msg = errMsg("Jev notice unavailable: " + msg.err.Error())
			return m, nil
		}
		m = m.openDiff("", "", msg.text)
		m.diff.title = "Jev notice · " + shortID(actionObject(msg.choice), m.g)
		m.diff.wrap = true
		return m, nil
	}
	// A diff's whole result is the overlay (diff.go): the text is a
	// screenful, and the message line would show a truncated first line of
	// it. A diff that *failed* still takes the message line - there is no
	// overlay to put a refusal in, and an empty frame would read as success.
	if msg.choice.action == ActionDiff && msg.err == nil {
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
	}
	m.actionAfterRead = &result
	m.msg = result
	if msg.err == nil && msg.choice.action == ActionOnboard && msg.choice.req.TargetKind == "workspace" && m.cur().kind == frameWorkspace {
		// The new Project lands in order and becomes the selection (design
		// K): the re-read finds it by id, which is its name.
		f := m.cur()
		f.selID = msg.choice.req.Input
		m = m.setCur(f)
	}
	return m.startLoad()
}

// ---------- the Mate keys: s (create/start/resume) and h (change harness) ----------
//
// These are the captain's Project-screen affordances: on a Project screen the
// Mate actions are reachable by their own key rather than five keystrokes
// through the `a` menu, whose other entries mostly refuse there. `a` still
// lists everything; these are shortcuts into the same choices, so both
// surfaces read availability from the store-backed loader and cannot disagree.

// harnessOrder is the picker's order: the default Mate harness (the
// workspace's `mate_harness`, else the catalog's own, which query.Load
// resolves into the snapshot) first, so the cursor starts on it and Enter
// alone creates the Mate the workspace is configured for, then the rest of
// the snapshot's harness catalog in its order. A harness that cannot run a
// Mate (query.Harness.Mate) is never offered, even as the default.
func (m Model) harnessOrder() []query.HarnessKind {
	def := query.HarnessKind("")
	if m.tree.Workspace.IsKnown() {
		def = m.tree.Workspace.Value.MateHarness
	}
	order := make([]query.HarnessKind, 0, len(m.tree.Harnesses))
	for _, h := range m.tree.Harnesses {
		if h.Mate && h.Kind == def {
			order = append(order, h.Kind)
		}
	}
	for _, h := range m.tree.Harnesses {
		if h.Mate && h.Kind != def {
			order = append(order, h.Kind)
		}
	}
	return order
}

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

// beginHarnessPick opens the agent chooser for an already-built choice. Like
// beginNewProject it leaves the menu closed, so Esc returns to the list
// rather than a menu the reader never opened.
func (m Model) beginHarnessPick(choice actionChoice) Model {
	m.actions = false
	m.confirm = nil
	m.actionInputMode = false
	m.menu = nil
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
	for i, kind := range m.harnessOrder() {
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
		if m.harnessIndex+1 < len(m.harnessOrder()) {
			m.harnessIndex++
		}
		return m, nil
	case "enter":
		order := m.harnessOrder()
		if m.harnessIndex >= len(order) {
			// A snapshot that carries no harness catalog offers nothing
			// to pick; Enter is not a choice of a harness nobody listed.
			return m.closeHarnessPick(), nil
		}
		choice := m.pendingChoice
		choice.req.Harness = order[m.harnessIndex]
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
