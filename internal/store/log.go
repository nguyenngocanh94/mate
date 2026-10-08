package store

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The three append-only logs. `crews/<id>.status` is what a crew writes with
// `echo "state: one line" >> $MATE_STATUS`, so its format is whatever the
// crew echoed: one line, no escaping. `sent.log` records every line the app
// put into a pane, so it is written only by this package and can afford a
// fixed shape; `incidents.log` is the observer's (docs/mvp.md section 4b) and
// has the same property and the same fixed tab-separated shape.
//
// All three are appended under an exclusive flock with O_APPEND, and all are read
// from a byte offset so a watcher can keep a cursor and see only what is new.
// A trailing partial line - a writer caught mid-append by a reader that does
// not hold the lock - is never returned; the cursor stops at its start and the
// next read picks it up whole.

// Sources of a sent line.
const (
	SourceUser = "user"
	SourceMate = "mate"
	SourceApp  = "app"
)

// TargetMate is the pane of Mate; a crew pane is `crew:<id>`.
const TargetMate = "mate"

// CrewTarget is the sent.log target for a crew pane.
func CrewTarget(crew string) string { return "crew:" + crew }

// sentSep separates the fields of a sent.log line. Tabs are stripped from the
// text, so a line always splits into exactly four fields.
const sentSep = "\t"

// SentEntry is one line of `sent.log`.
type SentEntry struct {
	// Offset is the byte offset of this line in the file; it is set by the
	// reader and ignored by the writer. Passing the offset of an entry back to
	// ReadSent replays the file from that entry.
	Offset int64
	Time   time.Time
	// Source is who produced the line: user, mate or app.
	Source string
	// Target is the pane it went to: mate, or crew:<id>.
	Target string
	// Text is the line itself, with newlines and tabs turned into spaces.
	Text string
}

// StatusEntry is one line of `crews/<id>.status`.
type StatusEntry struct {
	// Offset is the byte offset of this line, for a caller keeping a cursor.
	Offset int64
	Line   string
}

// AppendStatus appends one line to `crews/<crew>.status`. The line is written
// whole, under an exclusive lock, so two writers interleave lines and never
// characters. Newlines inside the line are turned into spaces: a status is one
// line by definition, and a status pointing at a longer file is the protocol.
func (w *Workspace) AppendStatus(project, crew, line string) error {
	if err := validatePair(project, crew); err != nil {
		return err
	}
	return w.appendLine(w.CrewStatus(project, crew), oneLine(line))
}

// AppendSent appends one line to `sent.log`. Time defaults to now.
func (w *Workspace) AppendSent(project string, entry SentEntry) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	if entry.Time.IsZero() {
		entry.Time = time.Now()
	}
	line := strings.Join([]string{
		entry.Time.Format(time.RFC3339),
		oneLine(entry.Source),
		oneLine(entry.Target),
		oneLine(entry.Text),
	}, sentSep)
	return w.appendLine(w.SentLog(project), line)
}

// ReadStatus returns the status lines of a crew from a byte offset, and the
// offset to resume from. A crew that has written nothing yet has no file and
// is not an error.
func (w *Workspace) ReadStatus(project, crew string, from int64) ([]StatusEntry, int64, error) {
	if err := validatePair(project, crew); err != nil {
		return nil, from, err
	}
	lines, next, err := readLines(w.CrewStatus(project, crew), from)
	if err != nil {
		return nil, from, err
	}
	entries := make([]StatusEntry, 0, len(lines))
	for _, l := range lines {
		entries = append(entries, StatusEntry{Offset: l.offset, Line: l.text})
	}
	return entries, next, nil
}

// ReadSent returns the sent.log entries of a project from a byte offset, and
// the offset to resume from. A line that does not parse is skipped rather than
// failing the read: the log is history for a view, and one bad line must not
// hide the rest.
func (w *Workspace) ReadSent(project string, from int64) ([]SentEntry, int64, error) {
	if err := ValidateProjectName(project); err != nil {
		return nil, from, err
	}
	lines, next, err := readLines(w.SentLog(project), from)
	if err != nil {
		return nil, from, err
	}
	entries := make([]SentEntry, 0, len(lines))
	for _, l := range lines {
		entry, ok := parseSent(l.text)
		if !ok {
			continue
		}
		entry.Offset = l.offset
		entries = append(entries, entry)
	}
	return entries, next, nil
}

func parseSent(line string) (SentEntry, bool) {
	fields := strings.SplitN(line, sentSep, 4)
	if len(fields) != 4 {
		return SentEntry{}, false
	}
	ts, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		return SentEntry{}, false
	}
	return SentEntry{Time: ts, Source: fields[1], Target: fields[2], Text: fields[3]}, true
}

// AppendJevLog appends one line to `.mate/jev.log` under the same exclusive
// lock as the other logs. The line is the chain's (internal/screen/chain
// LogLine); newlines in it become spaces.
func (w *Workspace) AppendJevLog(line string) error {
	return w.appendLine(w.JevLog(), oneLine(line))
}

// appendLine writes line plus a newline to path in one O_APPEND write, holding
// an exclusive lock for the write.
func (w *Workspace) appendLine(path, line string) error {
	if _, err := w.resolve(path); err != nil {
		return err
	}
	if err := w.mkdirAll(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockFile(f, true); err != nil {
		return err
	}
	defer unlockFile(f)
	_, err = f.WriteString(line + "\n")
	return err
}

type rawLine struct {
	offset int64
	text   string
}

// readLines reads whole lines from from to end of file under a shared lock.
func readLines(path string, from int64) ([]rawLine, int64, error) {
	if from < 0 {
		from = 0
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, from, nil
		}
		return nil, from, err
	}
	defer f.Close()
	if err := lockFile(f, false); err != nil {
		return nil, from, err
	}
	defer unlockFile(f)

	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, from, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, from, err
	}

	var lines []rawLine
	offset := from
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, rawLine{offset: offset, text: string(data[:i])})
		offset += int64(i) + 1
		data = data[i+1:]
	}
	return lines, offset, nil
}

// oneLine flattens a value into something a line-oriented log can hold.
func oneLine(s string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
}

// The observer's incident log, docs/mvp.md section 4b: one line per
// transition, `RFC3339 \t crew \t kind \t open|resolved \t text`. An incident
// has no identity beyond its (crew, kind) pair and is never updated in place:
// it is open when the last line for that pair says `open`, which is a fact
// about the file and costs a reader no state of its own.

// The two states an incident line can record.
const (
	IncidentOpen     = "open"
	IncidentResolved = "resolved"
)

// incidentFields is how many tab-separated fields one incidents.log line has.
// Tabs are stripped from every field, so a line always splits into exactly
// that many.
const incidentFields = 5

// IncidentEntry is one line of `incidents.log`.
type IncidentEntry struct {
	// Offset is the byte offset of this line, for a caller keeping a
	// cursor. It is set by the reader and ignored by the writer.
	Offset int64
	Time   time.Time
	// Crew is the crew the incident is about, empty only for an incident
	// nothing could attribute to one.
	Crew string
	// Kind is the trouble detected: `stale`, `runtime_lost`, and later
	// `wedged` and `budget`.
	Kind string
	// State is IncidentOpen or IncidentResolved.
	State string
	// Text is the one-line evidence the observer decided from.
	Text string
}

// Open reports whether this line opens an incident rather than resolving one.
func (e IncidentEntry) Open() bool { return e.State == IncidentOpen }

// AppendIncident appends one line to `incidents.log`. Time defaults to now.
//
// The project name and the crew id are validated before anything is written,
// the same way AppendStatus validates them: an incident must not be able to
// name a path outside the workspace. The state must be one of the two words
// the contract allows, because a third would leave every reader unable to
// answer "is this incident open".
func (w *Workspace) AppendIncident(project string, entry IncidentEntry) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	if entry.Crew != "" {
		if err := ValidateCrewID(entry.Crew); err != nil {
			return err
		}
	}
	if entry.Kind == "" {
		return fmt.Errorf("store: an incident line needs a kind")
	}
	if entry.State != IncidentOpen && entry.State != IncidentResolved {
		return fmt.Errorf("store: invalid incident state %q: want %s or %s",
			entry.State, IncidentOpen, IncidentResolved)
	}
	if entry.Time.IsZero() {
		entry.Time = time.Now()
	}
	line := strings.Join([]string{
		entry.Time.Format(time.RFC3339),
		oneLine(entry.Crew),
		oneLine(entry.Kind),
		entry.State,
		oneLine(entry.Text),
	}, sentSep)
	return w.appendLine(w.IncidentsLog(project), line)
}

// ReadIncidents returns the incident lines of a project from a byte offset,
// and the offset to resume from. A line that does not parse is skipped rather
// than failing the read, for the reason ReadSent skips one: the log is
// history, and one bad line must not hide the rest.
func (w *Workspace) ReadIncidents(project string, from int64) ([]IncidentEntry, int64, error) {
	if err := ValidateProjectName(project); err != nil {
		return nil, from, err
	}
	lines, next, err := readLines(w.IncidentsLog(project), from)
	if err != nil {
		return nil, from, err
	}
	entries := make([]IncidentEntry, 0, len(lines))
	for _, l := range lines {
		entry, ok := parseIncident(l.text)
		if !ok {
			continue
		}
		entry.Offset = l.offset
		entries = append(entries, entry)
	}
	return entries, next, nil
}

func parseIncident(line string) (IncidentEntry, bool) {
	fields := strings.SplitN(line, sentSep, incidentFields)
	if len(fields) != incidentFields {
		return IncidentEntry{}, false
	}
	ts, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		return IncidentEntry{}, false
	}
	if fields[3] != IncidentOpen && fields[3] != IncidentResolved {
		return IncidentEntry{}, false
	}
	return IncidentEntry{
		Time:  ts,
		Crew:  fields[1],
		Kind:  fields[2],
		State: fields[3],
		Text:  fields[4],
	}, true
}
