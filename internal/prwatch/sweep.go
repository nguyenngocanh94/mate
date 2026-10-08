package prwatch

import (
	"regexp"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// statusPullURL is a GitHub pull request URL inside a status line.
var statusPullURL = regexp.MustCompile(`https://github\.com/[^/\s]+/[^/\s]+/pull/[0-9]+`)

// handBackVerbs are the status verbs a crew announces its pull request with:
// `pr-open:` as the brief asks, or a `wait-mate:` naming it, which is what a
// crew writes when it was told to open one in so many words (docs/mvp.md
// M19, the hellovietnam PR #5).
var handBackVerbs = map[string]bool{"pr-open": true, "wait-mate": true}

// LatestURL is the pull request URL of the newest `pr-open:` or `wait-mate:`
// line that names one, or "" when none does. A URL mentioned in a `working:`
// line is the crew thinking aloud, not a hand-back, and is not watched.
func LatestURL(entries []store.StatusEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		verb, rest, ok := strings.Cut(strings.TrimSpace(entries[i].Line), ":")
		if !ok || !handBackVerbs[strings.TrimSpace(verb)] {
			continue
		}
		if url := statusPullURL.FindString(rest); url != "" {
			return url
		}
	}
	return ""
}

// Swept is one watcher Sweep started, or failed to start.
type Swept struct {
	Project, Crew, URL string
	// PID is the new watcher's pid.
	PID int
	// Err is why it could not be started.
	Err error
}

// Sweep makes sure every open crew with a pull request that has not ended
// has a live watcher, without relying on anyone having run `mate pr watch`
// (docs/mvp.md M19). The pull request is the one in the crew's meta, or the
// newest one its status names: a URL the meta does not have yet is recorded
// (`pr_url`, `pr_state=open`) before its watcher starts. A merged or closed
// pull request is left alone - its watcher already reported it - and so is a
// crew whose watcher is alive. It is what recovery runs when a console
// opens, and what the console's daemon runs every tick.
func Sweep(w *store.Workspace, st Starter) []Swept {
	var out []Swept
	for _, ref := range w.Projects() {
		ids, err := w.CrewIDs(ref.Name)
		if err != nil {
			continue
		}
		for _, crew := range ids {
			if s, ok := sweepCrew(w, st, ref.Name, crew); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func sweepCrew(w *store.Workspace, st Starter, project, crew string) (Swept, bool) {
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil || len(meta) == 0 || crewstate.Declare(crewstate.Declaration{Meta: meta}).Closed() {
		return Swept{}, false
	}
	url := strings.TrimSpace(meta[crewstate.MetaPRURL])
	state := meta[crewstate.MetaPRState]
	if entries, _, err := w.ReadStatus(project, crew, 0); err == nil {
		if found := LatestURL(entries); found != "" && found != url {
			if err := w.UpdateCrewMeta(project, crew, map[string]string{
				crewstate.MetaPRURL:   found,
				crewstate.MetaPRState: crewstate.PRStateOpen,
			}); err != nil {
				return Swept{Project: project, Crew: crew, URL: found, Err: err}, true
			}
			url, state = found, crewstate.PRStateOpen
		}
	}
	if url == "" || state == crewstate.PRStateMerged || state == crewstate.PRStateClosed {
		return Swept{}, false
	}
	if Running(w, project, crew) {
		return Swept{}, false
	}
	started, err := Ensure(w, st, project, crew, url)
	return Swept{Project: project, Crew: crew, URL: url, PID: started.PID, Err: err}, true
}
