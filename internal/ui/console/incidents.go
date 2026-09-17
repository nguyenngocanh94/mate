package console

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// ADR 0019 G7-04a2 part 3: the workspace-level incident list. It is a modal
// overlay over the tree (the same "own the whole body" shape actions.go's
// menu already uses - pushBody, frame.go), reachable with 'i' from any
// screen, so the captain sees every open incident's count while looking at
// a different Project entirely. Selecting one and pressing Enter jumps the
// main tree to the affected Crew and closes the overlay; 'a' acknowledges
// without leaving.

// incidentsFlow is the overlay's own state.
type incidentsFlow struct {
	open  bool
	index int
	// acking is the incident id an acknowledge call is in flight for, so a
	// second 'a' on the same row before the first call returns cannot fire
	// twice.
	acking string
}

// incidentsAvailable reports whether 'i' does anything: there must be a
// loaded tree to jump into, and a health port must actually be wired - a
// Console built without one (every existing test/fixture that predates
// this feature, and any future caller that wants a read-only navigation-
// only Console) has nothing the incidents view could ever show, so the key
// does not appear at all rather than opening onto a screen that can only
// ever say "No open incidents." (mirrors sessionAvailableFor's own gate on
// sessionReader/sessionStream, session_mode.go).
func (m Model) incidentsAvailable() bool {
	return m.phase == phaseReady && m.healthCycle != nil
}

// beginIncidents opens the overlay, clamped to a row that still exists.
func (m Model) beginIncidents() Model {
	if !m.incidentsAvailable() {
		return m
	}
	m.incidents = incidentsFlow{open: true, index: clampInt(m.incidents.index, 0, len(m.health.Incidents)-1)}
	m.msg = footerMsg{}
	return m
}

func (m Model) closeIncidents() Model {
	m.incidents = incidentsFlow{}
	return m
}

// onIncidentsKey handles every key while the incident list owns the body.
func (m Model) onIncidentsKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		return m.closeIncidents(), nil
	case "up", "k":
		if m.incidents.index > 0 {
			m.incidents.index--
		}
		return m, nil
	case "down", "j":
		if m.incidents.index+1 < len(m.health.Incidents) {
			m.incidents.index++
		}
		return m, nil
	case "enter":
		return m.jumpToSelectedIncident()
	case "a":
		return m.acknowledgeSelectedIncident()
	default:
		return m, nil
	}
}

// jumpToSelectedIncident closes the overlay and moves the main tree's
// selection to the Crew the highlighted incident concerns - its own scope
// id for a crew-scope incident, or the first affected Crew for a
// session-scope one (a lost Herdr connection can name several; there is
// one Crew for Enter to open). A crew the tree no longer contains (it was
// removed, or belongs to a Project the current Snapshot has not loaded)
// leaves the overlay open with a message rather than silently doing
// nothing.
func (m Model) jumpToSelectedIncident() (Model, tea.Cmd) {
	inc, ok := m.selectedIncident()
	if !ok {
		return m, nil
	}
	target := inc.ScopeID
	if inc.Scope == "session" && len(inc.AffectedCrewIDs) > 0 {
		target = inc.AffectedCrewIDs[0]
	}
	nm, ok := m.jumpToCrew(target)
	if !ok {
		nm = m
		nm.msg = errMsg("Crew " + target + " is not in the loaded snapshot " + m.g.Dot + " r refreshes")
		return nm, nil
	}
	nm = nm.closeIncidents()
	return nm, nil
}

func (m Model) selectedIncident() (query.HealthIncident, bool) {
	if m.incidents.index < 0 || m.incidents.index >= len(m.health.Incidents) {
		return query.HealthIncident{}, false
	}
	return m.health.Incidents[m.incidents.index], true
}

// acknowledgeSelectedIncident calls through the existing ActionFunc port
// (part 4: "reuse the existing actions") with ActionAcknowledgeIncident.
// Acknowledging has no lifecycle side effect and needs no confirmation
// (ADR 0019 §8), so it runs directly rather than through the confirm/menu
// machinery actions.go builds for Crew/Mate lifecycle changes.
func (m Model) acknowledgeSelectedIncident() (Model, tea.Cmd) {
	inc, ok := m.selectedIncident()
	if !ok || m.incidents.acking != "" {
		return m, nil
	}
	m.incidents.acking = inc.IncidentID
	return m, acknowledgeIncidentCmd(m.baseCtx(), m.action, inc.IncidentID)
}

type incidentAckDoneMsg struct {
	incidentID string
	err        error
}

func acknowledgeIncidentCmd(ctx context.Context, action ActionFunc, incidentID string) tea.Cmd {
	return func() tea.Msg {
		if action == nil {
			return incidentAckDoneMsg{incidentID: incidentID, err: fmt.Errorf("console has no action port wired")}
		}
		_, err := action(ctx, ActionRequest{Action: ActionAcknowledgeIncident, Target: incidentID, TargetKind: "health_incident"})
		return incidentAckDoneMsg{incidentID: incidentID, err: err}
	}
}

// onIncidentAckDone applies one acknowledge call's result. The incident
// itself is not removed from m.health here - acknowledging does not resolve
// it (ADR 0019 §8: "chỉ đánh dấu đã xem") - the next health tick's own read
// is what shows AcknowledgedAt once it lands.
func (m Model) onIncidentAckDone(msg incidentAckDoneMsg) Model {
	if m.incidents.acking != msg.incidentID {
		return m
	}
	m.incidents.acking = ""
	if msg.err != nil {
		m.msg = errMsg("Acknowledge failed: " + msg.err.Error())
		return m
	}
	m.msg = infoMsg("Acknowledged " + msg.incidentID)
	return m
}

// ---------- rendering ----------

// incidentsLines draws the overlay body: a title with the open count, then
// one line per open incident (kind, scope/affected label, reason,
// acknowledged marker), newest-need-attention first (HealthView.Incidents'
// own first-observed order).
func (m Model) incidentsLines(w, h int) []*line {
	var out []*line
	title := newLine().add(fmt.Sprintf("Incidents  %d open", len(m.health.Incidents)), m.p.Bold)
	out = append(out, title)
	// The header's own "? monitoring error" marker (frame.go) is
	// deliberately short; this is the spacious surface that carries the
	// full reason (PR 92 counter-review B2). What is listed below it is
	// still the last successfully committed view - a failing cycle degrades
	// what is shown, it does not blank it.
	if m.healthErr != "" {
		out = append(out, newLine().add("Monitoring error: "+m.healthErr, m.p.Amber))
	}
	out = append(out, newLine())
	if len(m.health.Incidents) == 0 {
		out = append(out, newLine().add("No open incidents.", m.p.Dim))
		return windowContent(out, 0, h, m.g, m.p)
	}
	for i, inc := range m.health.Incidents {
		selected := i == m.incidents.index
		out = append(out, m.incidentLine(inc, selected))
	}
	return windowContent(out, 0, h, m.g, m.p)
}

func (m Model) incidentLine(inc query.HealthIncident, selected bool) *line {
	l := newLine()
	l.addSpans(markerSpan(selected, true, m.g, m.p), span{text: " ", style: m.p.Dim})
	l.addSpan(healthIncidentKindSpan(inc.Kind, m.p))
	l.add("  "+m.incidentSubjectLabel(inc), m.p.Fg)
	if inc.Reason != "" {
		l.add("  "+m.g.Dot+" "+inc.Reason, m.p.Dim)
	}
	if inc.AcknowledgedAt != nil {
		l.add("  "+m.g.Dot+" acknowledged", m.p.Dim)
	}
	return selectRow(l, selected, m.p)
}

// incidentSubjectLabel resolves an incident's scope id to what a captain
// recognizes: a crew-scope incident names its Task/attempt; a session-scope
// one (one failing Herdr session) names every Crew it affects, never one
// incident per Crew (ADR 0019 §1/§6).
func (m Model) incidentSubjectLabel(inc query.HealthIncident) string {
	if inc.Scope == "crew" {
		return m.crewSubjectLabel(inc.ScopeID)
	}
	if len(inc.AffectedCrewIDs) == 0 {
		return "session " + inc.ScopeID
	}
	labels := make([]string, 0, len(inc.AffectedCrewIDs))
	for _, id := range inc.AffectedCrewIDs {
		labels = append(labels, m.crewSubjectLabel(id))
	}
	out := "session " + inc.ScopeID + " " + m.g.Dot + " affects "
	for i, l := range labels {
		if i > 0 {
			out += ", "
		}
		out += l
	}
	return out
}

func (m Model) crewSubjectLabel(crewID string) string {
	loc, ok := m.locateCrew(crewID)
	if !ok {
		return crewID
	}
	return fmt.Sprintf("%s / %s attempt %d", loc.Project.Name, loc.Task.Title, loc.Crew.Attempt)
}

// healthIncidentKindSpan renders an incident's Kind through healthWord
// (health.go), the same mapping the Crew row and inspector use, so a Crew's
// incident here and its own row/inspector never disagree on the word for
// the same state (PR 92 counter-review B1).
func healthIncidentKindSpan(kind string, p palette) span {
	switch kind {
	case "runtime_missing", "identity_mismatch":
		return failureSpan(healthWord(kind), p)
	default:
		return attentionSpan(healthWord(kind), p)
	}
}

// ---------- navigation ----------

// crewLocation is one Crew resolved against the whole tree, not just the
// current frame - crewByID (seams.go) only searches the Task frame that is
// currently open, which is not enough for an incident naming a Crew
// anywhere in the workspace.
type crewLocation struct {
	Project query.ProjectNode
	Task    query.TaskNode
	Crew    query.CrewNode
}

func (m Model) locateCrew(id string) (crewLocation, bool) {
	for _, p := range m.tree.Projects {
		for _, t := range p.Tasks {
			for _, c := range t.Crews {
				if c.CrewID == id {
					return crewLocation{Project: p, Task: t, Crew: c}, true
				}
			}
		}
	}
	return crewLocation{}, false
}

// jumpToCrew rebuilds the navigation stack to Workspace -> Project -> Task
// and selects the given Crew, reusing reconcileSelection's own by-identity
// positioning (model.go) rather than hand-computing a row index.
func (m Model) jumpToCrew(crewID string) (Model, bool) {
	loc, ok := m.locateCrew(crewID)
	if !ok {
		return m, false
	}
	m.stack = []frame{
		{kind: frameWorkspace, selID: loc.Project.ProjectID},
		{kind: frameProject, id: loc.Project.ProjectID, selID: loc.Task.TaskID},
		{kind: frameTask, id: loc.Task.TaskID, selID: loc.Crew.CrewID},
	}
	m = m.reconcileSelection()
	m.focus = paneList
	m.detail = false
	m.inspTop = 0
	m.msg = footerMsg{}
	return m, true
}
