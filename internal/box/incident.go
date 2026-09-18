package box

import "time"

// IncidentKind is the kind of trouble the observer (task 18) detected.
type IncidentKind string

const (
	// IncidentStale is a status file that has not moved and the pane is not
	// provably working.
	IncidentStale IncidentKind = "stale"
	// IncidentRuntimeLost is a crew whose Herdr pane or session disappeared.
	IncidentRuntimeLost IncidentKind = "runtime_lost"
	// IncidentWedged is a send that could not be verified into a pane for
	// too long.
	IncidentWedged IncidentKind = "wedged"
	// IncidentBudget is a token/usage budget signal (post-MVP producer, the
	// type exists now so box does not need to change shape later).
	IncidentBudget IncidentKind = "budget"
)

// Incident is one observer finding for a crew. The observer (task 18)
// produces these; this package only merges a caller-supplied slice into the
// View - it never detects anything on its own.
type Incident struct {
	At   time.Time
	Crew string
	Kind IncidentKind
	Text string
	// Resolved is set when the observer has since written a `resolved`
	// line for this (crew, kind) in incidents.log (mvp.md section 4b): the
	// condition cleared. A resolved incident is history, not an inbox item
	// and not a `blocked` state.
	Resolved bool
}

// OpenIncidents is the observer's unresolved findings for one crew, oldest
// first. A non-empty result is what makes the crew's displayed state
// `blocked` (mvp.md section 4b); the observer is the only writer of the
// incidents this reads, so a crew's own status lines never affect it.
func OpenIncidents(v View, crew string) []Incident {
	var out []Incident
	for _, e := range v.Entries {
		if e.Kind != KindIncident || e.Crew != crew || e.Incident == nil || e.Incident.Resolved {
			continue
		}
		out = append(out, *e.Incident)
	}
	return out
}
