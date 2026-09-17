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
}
