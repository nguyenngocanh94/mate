package box

import "time"

// Source is who produced an Entry.
type Source string

const (
	SourceUser     Source = "user"
	SourceMate     Source = "mate"
	SourceApp      Source = "app"
	SourceCrew     Source = "crew"
	SourceObserver Source = "observer"
)

// Kind is what kind of thing an Entry represents.
type Kind string

const (
	KindMessage  Kind = "message"
	KindStatus   Kind = "status"
	KindIncident Kind = "incident"
)

// Ref points an Entry back at the file and byte offset it came from, so a
// caller can act on the exact line (for example, jump the pane cursor or
// re-read from that point). An Entry with no file backing - an incident,
// which is supplied in memory - leaves Ref zero.
type Ref struct {
	File   string
	Offset int64
}

// Entry is one line of communication, of any kind, normalised to a common
// shape so it can be merged and sorted with lines of every other kind.
//
// Text carries the kind-specific payload verbatim:
//   - KindMessage: the sent.log text, as sent.
//   - KindStatus: the raw `crews/<id>.status` line ("state: text" or
//     whatever a crew actually echoed). Parse it with ParseStatus to get the
//     State and message apart.
//   - KindIncident: the incident's Text, as the observer produced it.
type Entry struct {
	At     time.Time
	Seq    int
	Source Source
	Target string
	Kind   Kind
	Crew   string
	Text   string
	Ref    Ref
}
