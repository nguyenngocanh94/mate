package box

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

// Cursor is a set of per-file read offsets: one for sent.log and one per
// crew status file. LoadSince uses it to read only what is new since a
// previous Load or LoadSince; a caller (the observer) keeps the Cursor a
// View returns and passes it back on the next poll.
type Cursor struct {
	Sent   int64
	Status map[string]int64
}

// View is a project's communication, merged and sorted by time.
type View struct {
	// Entries is every message, status line and incident, sorted by time
	// then by (file, offset) - see the package doc for why status lines can
	// only be ordered that coarsely.
	Entries []Entry
	// ByCrew groups Entries by Crew, in the same relative order, dropping
	// entries with no crew (a message to Mate's own pane, for instance).
	ByCrew map[string][]Entry
	// Attention is the subset that needs a human or Mate's eyes: a status
	// entry whose state is Attention, or any incident. Newest first.
	Attention []Entry
	// Cursor resumes a later LoadSince from exactly where this View ended.
	Cursor Cursor
}

// Summary is a header digest of a View.
type Summary struct {
	// Crews is the number of distinct crews present in the view.
	Crews int
	// Awaiting is the number of crews whose latest status entry is an
	// Attention state.
	Awaiting int
	// LastAt is the time of the most recent entry, zero if there is none.
	LastAt time.Time
}

// Load reads every crew status file and sent.log for project from the
// start, merges in the given incidents, and returns the full View.
func Load(ws *store.Workspace, project string, incidents []Incident) (View, error) {
	return load(ws, project, incidents, Cursor{})
}

// LoadSince reads only what is new since a previous Load or LoadSince,
// using since's per-file offsets, and merges in the given incidents (the
// observer's own new findings; box keeps no incident history of its own).
// The returned View's Entries hold only the new lines - a caller wanting the
// full history keeps its own accumulation across calls.
func LoadSince(ws *store.Workspace, project string, incidents []Incident, since Cursor) (View, error) {
	return load(ws, project, incidents, since)
}

func load(ws *store.Workspace, project string, incidents []Incident, cursor Cursor) (View, error) {
	crews, err := listCrews(ws, project)
	if err != nil {
		return View{}, err
	}

	newCursor := Cursor{Status: make(map[string]int64, len(crews))}
	var entries []Entry

	for _, crew := range crews {
		from := int64(0)
		if cursor.Status != nil {
			from = cursor.Status[crew]
		}
		lines, next, err := ws.ReadStatus(project, crew, from)
		if err != nil {
			return View{}, err
		}
		newCursor.Status[crew] = next
		if len(lines) == 0 {
			continue
		}
		statusPath := ws.CrewStatus(project, crew)
		mtime := time.Now()
		if fi, err := os.Stat(statusPath); err == nil {
			mtime = fi.ModTime()
		}
		for _, l := range lines {
			entries = append(entries, Entry{
				At:     mtime,
				Source: SourceCrew,
				Target: store.CrewTarget(crew),
				Kind:   KindStatus,
				Crew:   crew,
				Text:   l.Line,
				Ref:    Ref{File: statusPath, Offset: l.Offset},
			})
		}
	}

	sentPath := ws.SentLog(project)
	sentLines, sentNext, err := ws.ReadSent(project, cursor.Sent)
	if err != nil {
		return View{}, err
	}
	newCursor.Sent = sentNext
	for _, s := range sentLines {
		entries = append(entries, Entry{
			At:     s.Time,
			Source: Source(s.Source),
			Target: s.Target,
			Kind:   KindMessage,
			Crew:   crewFromTarget(s.Target),
			Text:   s.Text,
			Ref:    Ref{File: sentPath, Offset: s.Offset},
		})
	}

	for _, inc := range incidents {
		entries = append(entries, Entry{
			At:     inc.At,
			Source: SourceObserver,
			Kind:   KindIncident,
			Crew:   inc.Crew,
			Text:   incidentText(inc.Kind, inc.Text),
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		if a.Ref.File != b.Ref.File {
			return a.Ref.File < b.Ref.File
		}
		return a.Ref.Offset < b.Ref.Offset
	})
	for i := range entries {
		entries[i].Seq = i
	}

	byCrew := make(map[string][]Entry)
	for _, e := range entries {
		if e.Crew == "" {
			continue
		}
		byCrew[e.Crew] = append(byCrew[e.Crew], e)
	}

	var attention []Entry
	for _, e := range entries {
		if e.Kind == KindIncident || (e.Kind == KindStatus && Attention(ParseStatus(e.Text).State)) {
			attention = append(attention, e)
		}
	}
	for i, j := 0, len(attention)-1; i < j; i, j = i+1, j-1 {
		attention[i], attention[j] = attention[j], attention[i]
	}

	return View{Entries: entries, ByCrew: byCrew, Attention: attention, Cursor: newCursor}, nil
}

// listCrews returns the crew ids of a project, from the `<id>.status`
// filenames under `crews/`. A project with no crews dir yet has none.
func listCrews(ws *store.Workspace, project string) ([]string, error) {
	dir := ws.CrewsDir(project)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var crews []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if id, ok := strings.CutSuffix(name, ".status"); ok {
			crews = append(crews, id)
		}
	}
	sort.Strings(crews)
	return crews, nil
}

// crewFromTarget extracts a crew id from a sent.log target ("crew:<id>"),
// or "" for the mate target - a message to Mate is not tied to a crew.
func crewFromTarget(target string) string {
	if id, ok := strings.CutPrefix(target, "crew:"); ok {
		return id
	}
	return ""
}

// incidentText packs an incident's kind into Entry.Text the same way a
// status line packs its verb, so Entry needs no incident-specific field:
// "<kind>: <text>". ParseIncidentText reverses it.
func incidentText(kind IncidentKind, text string) string {
	return string(kind) + ": " + text
}

// ParseIncidentText reverses incidentText, splitting a KindIncident Entry's
// Text back into its IncidentKind and message.
func ParseIncidentText(raw string) (IncidentKind, string) {
	k, t, ok := strings.Cut(raw, ": ")
	if !ok {
		return "", raw
	}
	return IncidentKind(k), t
}

// LatestStatus returns the most recent status entry of a crew, parsed, and
// whether the crew has any status at all.
func LatestStatus(v View, crew string) (Status, bool) {
	entries := v.ByCrew[crew]
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == KindStatus {
			return ParseStatus(entries[i].Text), true
		}
	}
	return Status{}, false
}

// Summarize builds a header digest of a View.
func Summarize(v View) Summary {
	var s Summary
	s.Crews = len(v.ByCrew)
	for crew := range v.ByCrew {
		if st, ok := LatestStatus(v, crew); ok && Attention(st.State) {
			s.Awaiting++
		}
	}
	if n := len(v.Entries); n > 0 {
		s.LastAt = v.Entries[n-1].At
	}
	return s
}
