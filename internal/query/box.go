package query

import (
	"fmt"
	"strings"
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
// the entry needs attention, which entries are still waiting on a decision
// (the inbox), whether [resolve] may hand one to the Mate, and the exact
// line [resolve] would send.
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
	// Resolve is the one line `[resolve]` hands to the Mate for this entry:
	// the crew's own question, the status file to read, and the command to
	// answer the crew with. It is empty for an entry nothing can be decided
	// about - a message never has one, because the Mate either sent it or
	// was sent it, so handing it back says nothing new.
	Resolve string
}

// Resolvable reports whether [resolve] may hand this entry to the Mate.
func (e BoxEntry) Resolvable() bool { return e.Resolve != "" }

// BoxView is a project's whole box plus the digest a narrow frame shows
// instead of it.
type BoxView struct {
	// Entries are every message, status line and incident, oldest first -
	// so the rail draws the newest at the bottom, the way a chat log reads.
	// This is the whole log, unfiltered; the rail's `[all]` toggle is what
	// puts it on screen.
	Entries []BoxEntry
	// Inbox is the subset a human or the Mate still has to decide on
	// (box.Inbox): an unresolved needs-decision or blocked status line, or
	// an unresolved incident, in the same oldest-first order. Every surface
	// draws this by default - the rail, the project panel and the narrow
	// digest - because a line nobody can act on is noise on a surface whose
	// whole purpose is to be acted on.
	Inbox []BoxEntry
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

// ToResolve is how many things are waiting on a decision: the inbox's own
// length, spelled as a method so the rail header, the digest line and the
// project panel all count the same thing.
func (v BoxView) ToResolve() int { return len(v.Inbox) }

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
	// The inbox is built from the same Entry values, through the same
	// flattener, so an item and its line in the full log are the same row
	// with the same Seq - a reader who toggles [all] sees the entry they
	// were looking at, not a second copy of it built by other code.
	for _, item := range box.Inbox(v) {
		out.Inbox = append(out.Inbox, boxEntry(ws, project, item.Entry))
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
		// The line carries the question *and* the pointer. The question is
		// what makes the Mate's job legible at a glance - the reader saw
		// those words in the rail and the Mate should see the same ones -
		// and the path is what the Mate reads before deciding, because a
		// copy the console made is not the record. The path is absolute:
		// the Mate's cwd is its own workspace directory, not the project's,
		// so `crews/<id>.status` resolves to nothing there.
		if e.Crew != "" {
			out.Resolve = BoxResolveLine(project, e.Crew, st.Text, ws.CrewStatus(project, e.Crew))
		}
	case box.KindIncident:
		kind, text := box.ParseIncidentText(e.Text)
		out.Kind = BoxIncident
		out.Verb = string(kind)
		out.Text = text
		out.Attention = true
		out.Resolve = BoxIncidentResolveLine(string(kind), e.Crew, text)
	default:
		out.Kind = BoxMessage
	}
	return out
}

// BoxResolveLine and BoxIncidentResolveLine are the two lines `[resolve]`
// sends into the Mate's pane. They are spelled once, here, so the console,
// the action that sends them, the manual the Mate reads (assets/mate,
// section 10) and any test asserting on `sent.log` all agree.
//
// They replace the `signal: <path>` lines this file used to build. A bare
// path says only "here is a file"; it does not say that the crew is waiting,
// what it asked, or that answering it is the Mate's job. A `resolve:` line
// says all three, which is what the inbox exists for - an item leaves the
// inbox when the crew has an answer, not when somebody has read about it.
//
// statusPath is already absolute (ws.CrewStatus(project, crew)): the Mate's
// cwd is its own workspace directory, not the project's, so a path relative
// to the project never resolves there.
func BoxResolveLine(project, crew, question, statusPath string) string {
	return fmt.Sprintf("resolve: %s asked: %q — read %s, decide, and answer with matev2 send %s %s \"<one line>\"",
		crew, oneLine(question, resolveQuestionRunes), statusPath, project, crew)
}

func BoxIncidentResolveLine(kind, crew, text string) string {
	return fmt.Sprintf("resolve: incident %s %s — %s", kind, crew, oneLine(text, resolveQuestionRunes))
}

// resolveQuestionRunes bounds the quoted question. A status line is one line
// by protocol (mvp.md section 4), but nothing enforces its length, and a
// composer handed a thousand-rune line is one that wraps and re-flows, which
// is exactly what internal/send's verification then cannot read back. The
// file the line points at holds the whole of it.
const resolveQuestionRunes = 200

// oneLine flattens text to a single line of at most n runes: every run of
// whitespace, newlines included, collapses to one space, and a double quote
// becomes a single one so the quoted question cannot close its own quotes.
func oneLine(text string, n int) string {
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, "\"", "'")), " ")
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return strings.TrimRight(string(r[:n]), " ") + "…"
}
