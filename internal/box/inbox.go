package box

import "time"

// The inbox: the subset of a View that a human or the Mate still has to
// decide on.
//
// The View itself stays complete - every `working` line, every `wait-mate`,
// every message in `sent.log` - because the merge is the record and a
// record that drops lines is not one. The inbox is a filter over it, and
// only the surfaces that ask a reader to *do* something use it.
// `wait-mate` is not in it while the project is in auto mode (decision
// 2026-09-18, mvp.md section 4b): it is the crew's report, the crews table's
// STATE column already carries it, the user can type into the crew's pane at
// any time without being told to, and the digest tells the Mate. In manual
// mode no digest is sent, so a crew's latest `wait-mate` is an item too
// (mvp.md M18): at least one of the captain and the Mate is always told.
//
// # What is in the inbox
//
// One Item per unresolved thing:
//
//   - a status entry whose state is needs-decision - the crew asked and
//     stopped its turn (mvp.md section 4b);
//   - an open incident - the observer noticed something the crew itself
//     cannot report, which is what `blocked` means, and has not yet
//     written the `resolved` line that ends it. A resolved incident is
//     history and is not in the inbox.
//
// # When a thing stops being in the inbox
//
// Two rules, either of which resolves an item:
//
//  1. A later status line from the same crew. This one is exact: status
//     lines from the same file are ordered by byte offset, so "later" is a
//     fact about the file, not about a clock. A crew that appended anything
//     after its question has moved on - it was answered, it gave up, or it
//     finished - and the question is no longer waiting on anybody.
//
//  2. A `sent.log` entry addressed to `crew:<id>` after the question's time -
//     somebody (the user through `r`, or the Mate through `mate send`)
//     replied. This one is an approximation, and the package doc says why:
//     a status line carries no time of its own, so every line in a file is
//     stamped with the file's mtime, which is when its *last* line was
//     written. A reply timestamped after that mtime is therefore after every
//     line the file currently holds, which is the direction that matters: it
//     never calls an item resolved that was asked afterwards. It can miss a
//     reply that landed between two appends - but then the second append
//     exists, and rule 1 resolves the item exactly. Rule 1 is the primary
//     rule for that reason; rule 2 is what closes an item in the ordinary
//     case where the crew has not spoken again yet.
//
// Rule 2 compares against the status file's mtime truncated to the second,
// because `sent.log` writes RFC3339 with no fractional part (store/log.go)
// and a reply typed in the same second as the crew's append would otherwise
// read as older than the question it answers - which is the ordinary case,
// not a rare one: a reader answering an entry the rail just drew does it
// within the same second often enough that an exact comparison would leave
// answered items in the inbox. The cost is a one-second window in the other
// direction, where a line sent to the crew just before it asked reads as an
// answer; a second question, or any further status line, closes that out
// through rule 1.
//
// Neither rule touches an incident: the observer does not write `.status`,
// so a crew's own line never resolves a finding about it, and only the
// observer's own `resolved` line takes an incident out of the inbox
// (mvp.md section 4b).
//
// An item with no crew (an incident the observer could not attribute) can be
// resolved by neither rule and stays in the inbox until somebody deals with
// it, which is the honest answer: nothing recorded says it was handled.

// Item is one thing the inbox is asking about: the entry it came from, with
// the kind-specific fields already split out so a caller never re-parses
// Entry.Text.
type Item struct {
	// Entry is the merged line this item stands for, verbatim.
	Entry Entry
	// State is the status verb for a status item (needs-decision), empty
	// for an incident.
	State State
	// Kind is the incident kind for an incident item, empty for a status.
	Kind IncidentKind
	// Text is the question the crew asked, or the incident's message.
	Text string
}

// Crew is the crew the item is about, empty for an unattributed incident.
func (i Item) Crew() string { return i.Entry.Crew }

// Incident reports whether this item came from the observer rather than
// from a crew's own status file.
func (i Item) Incident() bool { return i.Entry.Kind == KindIncident }

// Inbox returns the unresolved items of a View, oldest first - the same
// order Entries is in, so a rail that draws the newest at the bottom needs
// no second sort.
func Inbox(v View) []Item {
	lastStatus := make(map[string]int)
	replies := make(map[string][]time.Time)
	for _, e := range v.Entries {
		switch e.Kind {
		case KindStatus:
			if e.Crew != "" {
				lastStatus[e.Crew] = e.Seq
			}
		case KindMessage:
			if crew := crewFromTarget(e.Target); crew != "" {
				replies[crew] = append(replies[crew], e.At)
			}
		}
	}

	var out []Item
	for _, e := range v.Entries {
		if e.Crew != "" && v.Closed[e.Crew] {
			// A closed crew's open question is moot: the Mate or the
			// captain ended the task, and there is no pane to answer into.
			continue
		}
		item, ok := inboxItem(e, v.Manual)
		if !ok || inboxResolved(e, lastStatus, replies) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// inboxItem reports whether one entry is the kind of thing the inbox asks
// about, and builds the Item if it is. A message never is: it is a record of
// something already said, and nothing about it is waiting on a decision. A
// `wait-mate` is one only in manual mode; the caller's later-status rule then
// leaves just each crew's latest.
func inboxItem(e Entry, manual bool) (Item, bool) {
	switch e.Kind {
	case KindStatus:
		st := ParseStatus(e.Text)
		if !Attention(st.State) && !(manual && st.State == StateWaitMate) {
			return Item{}, false
		}
		return Item{Entry: e, State: st.State, Text: st.Text}, true
	case KindIncident:
		if e.Incident != nil && e.Incident.Resolved {
			// The observer wrote a `resolved` line for it: the condition
			// cleared, and mvp.md section 4b says that is exactly when the
			// incident leaves the inbox.
			return Item{}, false
		}
		kind, text := ParseIncidentText(e.Text)
		return Item{Entry: e, Kind: kind, Text: text}, true
	default:
		return Item{}, false
	}
}

// inboxResolved applies the two rules above, in that order.
func inboxResolved(e Entry, lastStatus map[string]int, replies map[string][]time.Time) bool {
	if e.Crew == "" || e.Kind == KindIncident {
		return false
	}
	if seq, ok := lastStatus[e.Crew]; ok && seq > e.Seq {
		return true
	}
	for _, at := range replies[e.Crew] {
		if !at.Before(e.At.Truncate(time.Second)) {
			return true
		}
	}
	return false
}
