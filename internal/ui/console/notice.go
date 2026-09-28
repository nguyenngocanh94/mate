package console

import "github.com/nguyenngocanh94/mate/internal/query"

// WithNoticeClassifier offers a manual notice explanation. Snapshot refreshes
// never trigger classification; only the explicit menu action does.
func (m Model) WithNoticeClassifier(enabled bool) Model {
	m.noticeClassifier = enabled
	return m
}

func (m Model) withNoticeEntry(r row, entries []menuEntry) []menuEntry {
	if !m.noticeClassifier {
		return entries
	}
	_, live := m.stageTargetAvailable(r)
	binding := m.bindingForRow(r)
	live = live && binding.IsKnown() && binding.Value.Status == query.BindingActive
	req := ActionRequest{Action: ActionNotice, Target: m.currentProject().ProjectID, TargetKind: "mate"}
	if r.kind == rowCrew {
		req.TargetKind, req.Crew = "crew", r.id
	}
	c := actionChoice{action: ActionNotice, req: req, enabled: live && m.action != nil,
		desc: "Sends up to 40 terminal lines to TypeSafe. Shows an advisory about this capture; does not act on it."}
	if !c.enabled {
		c.desc = "unavailable · no active agent to read"
	}
	return append(entries, m.choiceEntry("e", "Explain notice (Jev)", c))
}
