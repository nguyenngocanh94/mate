package query

import (
	"fmt"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// The message box as the Console reads it (mvp.md task 15, section 4).
//
// internal/box merges `crews/*.status`, `sent.log` and the observer's
// incidents into one time-ordered view, and it does that over
// internal/store. The Console may import neither (doc.go, boundary_test.go),
// so these are the flat DTOs it does see: one copy per load, made here,
// with every derived fact the rail needs already decided - the verb, whether
// the entry needs attention, whether Enter may hand it to the Mate, and the
// exact line Enter would send.
//
// Deciding those here rather than in the renderer is the same rule the rest
// of this package follows: a UI that recomputes "is this attention?" from a
// raw string is a second definition of the vocabulary, free to drift from
// box.Attention. The renderer's job is to draw what it is given.

// BoxEntryKind is what one BoxEntry represents. The values mirror
// box.Kind's.
type BoxEntryKind string

const (
	BoxMessage  BoxEntryKind = "message"
	BoxStatus   BoxEntryKind = "status"
	BoxIncident BoxEntryKind = "incident"
)

// BoxEntry is one line of a project's communication, flattened for display.
type BoxEntry struct {
	// Seq is the entry's position in the merged view, oldest first. It is
	// box.Entry.Seq, carried through so a caller can name an entry across a
	// refresh without holding the whole row.
	Seq int
	At  time.Time
	// Kind selects how the other fields are read.
	Kind BoxEntryKind
	// Source is who produced the line ("crew", "user", "mate", "app",
	// "observer"), and Target is the pane a message went to ("mate",
	// "crew:<id>"). Both are empty only when the merge produced neither.
	Source string
	Target string
	// Crew is the crew this entry belongs to, empty for a message addressed
	// to the Mate's own pane.
	Crew string
	// Verb is the status state ("needs-decision", "done", ...) for a status
	// entry, the incident kind ("stale", "wedged", ...) for an incident, and
	// empty for a message.
	Verb string
	// Text is the payload after the verb, or the message text as sent.
	Text string
	// Attention marks an entry a person or the Mate must see: box.Attention
	// over the parsed state, or any incident.
	Attention bool
	// Signal is the one line `Enter` hands to the Mate for this entry
	// (mvp.md section 5), empty for an entry that is not forwardable.
	// A message is never forwardable: the Mate either sent it or was sent
	// it, so handing it back says nothing new.
	Signal string
}

// Forwardable reports whether Enter may hand this entry to the Mate.
func (e BoxEntry) Forwardable() bool { return e.Signal != "" }

// BoxView is a project's whole box plus the digest a narrow frame shows
// instead of it.
type BoxView struct {
	// Entries are every message, status line and incident, oldest first -
	// so the rail draws the newest at the bottom, the way a chat log reads.
	Entries []BoxEntry
	// Crews is how many distinct crews appear in the view, and Awaiting how
	// many of them have an attention state as their latest status
	// (box.Summarize).
	Crews    int
	Awaiting int
	// LastAt is the time of the most recent entry, zero when there is none.
	LastAt time.Time
}

// Attention counts the entries needing eyes.
func (v BoxView) Attention() int {
	n := 0
	for _, e := range v.Entries {
		if e.Attention {
			n++
		}
	}
	return n
}

// LoadBox reads one project's box. It is exported because two callers need
// exactly this: Load, which hangs it on the ProjectNode the project frame
// draws, and the Console's session-metadata tick, which refreshes the rail
// of an open Mate session without re-reading the whole workspace tree.
//
// A read failure degrades to an Unknown field rather than an error, the way
// every other field in this package does: an unreadable sent.log must not
// blank the project.
func LoadBox(ws *store.Workspace, project string) Field[BoxView] {
	if ws == nil {
		return UnknownField[BoxView]("no workspace is open")
	}
	// No incidents are passed: the observer that produces them is mvp.md
	// task 18. An empty slice here is the honest "nothing has detected
	// anything", not "there is nothing wrong".
	v, err := box.Load(ws, project, nil)
	if err != nil {
		return UnknownField[BoxView](readFailureReason(err))
	}
	return KnownField(boxView(ws, project, v))
}

func boxView(ws *store.Workspace, project string, v box.View) BoxView {
	sum := box.Summarize(v)
	out := BoxView{
		Entries:  make([]BoxEntry, 0, len(v.Entries)),
		Crews:    sum.Crews,
		Awaiting: sum.Awaiting,
		LastAt:   sum.LastAt,
	}
	for _, e := range v.Entries {
		out.Entries = append(out.Entries, boxEntry(ws, project, e))
	}
	return out
}

func boxEntry(ws *store.Workspace, project string, e box.Entry) BoxEntry {
	out := BoxEntry{
		Seq:    e.Seq,
		At:     e.At,
		Source: string(e.Source),
		Target: e.Target,
		Crew:   e.Crew,
		Text:   e.Text,
	}
	switch e.Kind {
	case box.KindStatus:
		st := box.ParseStatus(e.Text)
		out.Kind = BoxStatus
		out.Verb = string(st.State)
		out.Text = st.Text
		out.Attention = box.Attention(st.State)
		// The status file is the pointer, not the line: mvp.md section 5
		// says Enter sends `signal: <absolute status file path>`, so the
		// Mate reads the file itself rather than trusting a line the
		// console copied. The path must be absolute: the Mate's cwd is its
		// own workspace directory, not the project's, so a path relative to
		// the project (`crews/<id>.status`) resolves to nothing there.
		if e.Crew != "" {
			out.Signal = BoxStatusSignal(ws.CrewStatus(project, e.Crew))
		}
	case box.KindIncident:
		kind, text := box.ParseIncidentText(e.Text)
		out.Kind = BoxIncident
		out.Verb = string(kind)
		out.Text = text
		out.Attention = true
		out.Signal = BoxIncidentSignal(string(kind), e.Crew)
	default:
		out.Kind = BoxMessage
	}
	return out
}

// BoxStatusSignal and BoxIncidentSignal are the two lines mvp.md section 5
// pins for the Enter key. They are spelled once, here, so the console, the
// action that sends them and any test asserting on `sent.log` all agree.
//
// BoxStatusSignal takes the crew's status file path, already resolved to an
// absolute path by the caller (ws.CrewStatus(project, crew)): the Mate's cwd
// is its own workspace directory, not the project's, so a path relative to
// the project never resolves there.
func BoxStatusSignal(statusPath string) string {
	return fmt.Sprintf("signal: %s", statusPath)
}

func BoxIncidentSignal(kind, crew string) string {
	return fmt.Sprintf("signal: incident %s %s", kind, crew)
}
