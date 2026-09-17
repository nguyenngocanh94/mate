package store

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The two append-only logs. `crews/<id>.status` is what a crew writes with
// `echo "state: one line" >> $MATEV2_STATUS`, so its format is whatever the
// crew echoed: one line, no escaping. `sent.log` records every line the app
// put into a pane, so it is written only by this package and can afford a
// fixed shape.
//
// Both are appended under an exclusive flock with O_APPEND, and both are read
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
