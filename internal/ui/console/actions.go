package console

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nguyenngocanh94/matev2/internal/query"
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
	req       ActionRequest
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
	choices := make([]actionChoice, 0, 5)
	choices = append(choices, m.startChoice(selected), m.stopChoice(selected), m.resumeChoice(selected), m.repairChoice(selected), m.onboardChoice(selected))
	// Capability is authored by query.LoadSnapshot. The local builders above
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
	return choices
}

// actionCapability finds query.LoadSnapshot's capability for one
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
	if crew.Status == query.CrewNeedsRepair {
		c.desc = "unavailable · no stale binding is recorded; inspect worktree state before repair"
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
	m.actionChoices = m.actionChoicesForSelected()
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
	m.msg = footerMsg{}
	return m
}

// newProjectChoice is the workspace-level onboard action as the 'n' key
// reaches it. Availability is query.LoadSnapshot's own (m.tree.Actions), the
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
		m.pendingChoice = choice
		return m, nil
	}
	if choice.dangerous {
		m.confirm = &actionConfirmation{choice: choice}
		return m, nil
	}
	return m.runAction(choice)
}

func (m Model) onActionInputKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		if msg.String() == "backspace" && m.actionInput != "" {
			r := []rune(m.actionInput)
			m.actionInput = string(r[:len(r)-1])
			return m, nil
		}
		// Esc unwinds exactly one step: back to the menu when the menu is
		// what opened this input, and out of the overlay entirely when the
		// 'n' key did (beginNewProject leaves m.actions false).
		if m.actions {
			m.actionInputMode = false
			m.actionInput = ""
			m.pendingChoice = actionChoice{}
			return m, nil
		}
		return m.closeActions(), nil
	case "enter":
		if strings.TrimSpace(m.actionInput) == "" {
			m.msg = errMsg("Action refused: project name is required · nothing started")
			return m, nil
		}
		// The pending choice is the input's own, not whatever the menu
		// cursor happens to sit on: the 'n' key never opened a menu.
		choice := m.pendingChoice
		if choice.action == "" {
			return m.closeActions(), nil
		}
		choice.req.Input = m.actionInput
		return m.runAction(choice)
	}
	for _, r := range msg.Runes {
		if unicode.IsPrint(r) && !unicode.IsSpace(r) || r == ' ' {
			if len([]rune(m.actionInput)) < 120 {
				m.actionInput += string(r)
				m.msg = footerMsg{}
			} else {
				m.msg = infoMsg("Project name limit reached (120 characters); further input is ignored")
			}
		}
	}
	return m, nil
}

func (m Model) runAction(choice actionChoice) (Model, tea.Cmd) {
	m.actions = false
	m.actionInputMode = false
	m.harnessPick = false
	m.pendingChoice = actionChoice{}
	m.actionBusy = true
	m.actionRunningDesc = string(choice.action) + " " + actionObject(choice)
	m.msg = infoMsg("Running " + m.actionRunningDesc + " " + m.g.Ellipsis)
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

func actionObject(c actionChoice) string {
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
	m.actionAfterRead = &result
	m.msg = result
	return m, loadCmd(m.load)
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
	if r, ok := m.selectedRow(); ok {
		out[0].add("  "+r.id, m.p.Dim)
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
	out := []*line{
		newLine().pad(2).add("CONFIRM "+string(choice.action)+"?", m.p.Bold),
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
	out := []*line{
		newLine().pad(2).add("NEW PROJECT", m.p.Bold),
		newLine().pad(2).add(strings.Repeat(m.g.HRule, maxInt(1, w-4)), m.p.Faint),
		newLine().pad(2).add("Name    ", m.p.Dim).add(m.actionInput+"_", m.p.Fg),
		newLine().pad(2).add("Create a Project record; no runtime agent is started.", m.p.Dim),
		newLine(),
		newLine().pad(2).add("Enter ", m.p.Fg).add("Create project", m.p.Dim).add("  Esc ", m.p.Fg).add("Cancel", m.p.Dim),
	}
	return fitLines(out, h)
}

// ---------- the Mate keys: s (create/start/resume) and h (change harness) ----------
//
// These are the captain's Project-screen affordances: on a Project screen the
// Mate actions are reachable by their own key rather than five keystrokes
// through the `a` menu, whose other entries mostly refuse there. `a` still
// lists everything; these are shortcuts into the same choices, so both
// surfaces read availability from query.LoadSnapshot and cannot disagree.

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
