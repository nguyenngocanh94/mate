package box

import (
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
)

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

// Incident is one observer finding for a crew. The observer (internal/watch,
// task 18) is the only writer of `incidents.log`; this package only reads
// that file and merges it into the View - it never detects anything on its
// own.
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

// loadIncidents reads `incidents.log` and turns each `open` line into one
// Entry, carrying the Incident the line opened.
//
// Only `open` lines become entries. A `resolved` line is not a second
// finding - it is the end of the first one - so it is folded into that
// entry's Resolved flag instead of being drawn as an incident of its own.
// The pairing is positional, walking the file backwards: a `resolved` line
// closes the nearest `open` line above it for the same (crew, kind), and is
// then spent, so a crew that went stale, recovered and went stale again has
// one resolved entry and one open one rather than two of either.
//
// The whole file is read whatever the cursor says, because that pairing
// needs lines on both sides of it; from is applied only to which entries are
// returned, so LoadSince still never replays an incident a caller has seen.
// The consequence a caller must know: LoadSince reports an incident once,
// when it opens, and never re-reports the same one as resolved - a caller
// tracking resolution across polls reloads from the start.
func loadIncidents(ws *store.Workspace, project string, from int64) ([]Entry, int64, error) {
	lines, next, err := ws.ReadIncidents(project, 0)
	if err != nil {
		return nil, from, err
	}

	type pair struct{ crew, kind string }
	resolved := make([]bool, len(lines))
	pending := make(map[pair]int)
	for i := len(lines) - 1; i >= 0; i-- {
		key := pair{lines[i].Crew, lines[i].Kind}
		if !lines[i].Open() {
			pending[key]++
			continue
		}
		if pending[key] > 0 {
			pending[key]--
			resolved[i] = true
		}
	}

	path := ws.IncidentsLog(project)
	out := make([]Entry, 0, len(lines))
	for i, l := range lines {
		if !l.Open() || l.Offset < from {
			continue
		}
		inc := Incident{
			At:       l.Time,
			Crew:     l.Crew,
			Kind:     IncidentKind(l.Kind),
			Text:     l.Text,
			Resolved: resolved[i],
		}
		out = append(out, Entry{
			At:       inc.At,
			Source:   SourceObserver,
			Kind:     KindIncident,
			Crew:     inc.Crew,
			Text:     incidentText(inc.Kind, inc.Text),
			Ref:      Ref{File: path, Offset: l.Offset},
			Incident: &inc,
		})
	}
	return out, next, nil
}

// OpenIncidents is the observer's unresolved findings for one crew, oldest
// first, of every kind. It is what puts a crew in the inbox (mvp.md section
// 4b: "một incident, mà chưa có ai trả lời"), not what decides `blocked` -
// see BlockingIncidents for that.
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

// BlockingIncidents is OpenIncidents narrowed to the two kinds that make a
// crew `blocked` (mvp.md section 4b, decision 2026-09-20): `stale` and
// `runtime_lost` - a crew that has stopped being able to speak for itself.
// `budget` (and `wedged`, which is never filed under a real crew) are
// deliberately excluded: going over budget does not mean the crew stopped
// talking, so it must stay an inbox item, not a state that displaces
// `working`/`needs-decision`/`wait-mate`.
func BlockingIncidents(v View, crew string) []Incident {
	var out []Incident
	for _, inc := range OpenIncidents(v, crew) {
		switch inc.Kind {
		case IncidentStale, IncidentRuntimeLost:
			out = append(out, inc)
		}
	}
	return out
}
