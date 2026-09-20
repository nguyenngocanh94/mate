// Package scene projects the timeline onto the office of docs/mvp.md M5:
// the Mate is a CEO sitting in its room, a crew is an employee at a desk, and
// a crew with a question carries the note to the CEO's door and waits there.
//
// The projection is a state machine written out as data. Every edge is one
// row of the table in docs/timeline.md - a from-state, an event kind, an
// optional payload predicate, a to-state and who the actor now faces - and
// nothing moves an actor except an edge. An event that reaches a machine and
// matches no edge is not dropped: it is recorded as a transition to the state
// the actor is already in, with `unexplained: <kind>` in `detail`, so the
// hole is in the data rather than in the reader's understanding of it.
//
// The whole projection is recomputed from the events every time it runs. It
// is not incremental on purpose: an ingest pass can insert an event whose
// time is older than events it already wrote - a commit read out of a
// transcript, a status line dated by the shell command that wrote it - and an
// incremental machine fed that event out of order would be wrong for ever.
// Recomputing costs one ordered read of the project's events, which is the
// same read `matev2 events` does, and it makes two reindexes byte-identical
// by construction.
package scene

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/db"
)

// State is one place an actor can be in the office. The vocabulary is the one
// docs/mvp.md M5 question 2 lists, with the parameter of a parameterised
// state - `walking_to_ceo(question)`, `leaving(merged)`, `reading(crew)` -
// split into `detail` and `target_actor_id` so the state vocabulary stays
// finite and a query can ask "how long was anybody at the CEO's door".
type State string

const (
	// Unknown is an actor no event has placed yet.
	Unknown State = ""

	// The crew's states.
	Arriving      State = "arriving"
	AtDeskWorking State = "at_desk_working"
	WalkingToCEO  State = "walking_to_ceo"
	WaitingAtCEO  State = "waiting_at_ceo"
	WaitingReview State = "waiting_review"
	Leaving       State = "leaving"
	Gone          State = "gone"

	// Blocked and Asleep belong to both machines: an incident is filed
	// against a crew by the observer and against the Mate by the daemon, and
	// it means the same thing in both rooms.
	Blocked State = "blocked"
	Asleep  State = "asleep"

	// The Mate's states.
	Idle            State = "idle"
	Reading         State = "reading"
	Deciding        State = "deciding"
	Answering       State = "answering"
	Reviewing       State = "reviewing"
	Merging         State = "merging"
	OnPhone         State = "on_phone"
	ReceivingDigest State = "receiving_digest"

	// stay is not a state. It is how an edge says "this kind is explained
	// here and changes nothing", which is what makes the table complete:
	// every kind the timeline can write has a row, so an unexplained
	// transition means a kind nobody has thought about rather than a kind
	// somebody chose to ignore.
	stay State = "stay"
	// resume is not a state either: it is how an edge says "back to where
	// the incident interrupted it".
	resume State = "resume"
)

// The `detail` vocabulary. A detail is the parameter of a parameterised
// state, and it is a fixed word rather than free text so a renderer can
// switch on it.
const (
	DetailQuestion = "question"
	DetailHandback = "handback"
	DetailMerged   = "merged"
	DetailClosed   = "closed"
	DetailFailed   = "failed"
	DetailDigest   = "digest"
	DetailAssign   = "assign"
)

// UnexplainedPrefix marks the transition an event that matched no edge
// produces. The depth test of task 26 fails on any row carrying it.
const UnexplainedPrefix = "unexplained: "

// Actor is one inhabitant of the office, as `actor` records it.
type Actor struct {
	ID      string
	Kind    string // mate | crew | user | app | observer
	Name    string
	Project string
}

// Event is the part of an `event` row the projection reads.
type Event struct {
	ID        int64
	At        time.Time
	Kind      string
	ActorID   string
	SubjectID string
	TaskActor string
	CauseID   int64
	Payload   map[string]any
}

// field reads one payload value as a string, the way StoryEvent.Field does.
func (e Event) field(key string) string {
	value, ok := e.Payload[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	default:
		return fmt.Sprint(v)
	}
}

// Transition is one row of the `transition` table: an actor moved, at a
// moment, because of one event.
type Transition struct {
	ID      string
	Project string
	ActorID string
	From    State
	To      State
	At      time.Time
	EventID int64
	Target  string // the actor it now faces, or ""
	Detail  string
}

// Unexplained reports whether a transition is the record of an event no edge
// explained.
func (t Transition) Unexplained() bool { return strings.HasPrefix(t.Detail, UnexplainedPrefix) }

// machine is one actor's position in the office.
type machine struct {
	actor  Actor
	state  State
	detail string
	target string

	// Where an incident interrupted it. An incident is a pause, not a
	// destination: a crew that falls asleep at the CEO's door is still the
	// crew whose question is unanswered, and `incident.resolved` puts it
	// back exactly where it was rather than guessing.
	resumeState  State
	resumeDetail string
	resumeTarget string
}

type projector struct {
	project  string
	actors   map[string]Actor
	machines map[string]*machine
	// mateID and crewByName are the two lookups the edges need and a map
	// iteration cannot give deterministically.
	mateID     string
	crewByName map[string]string
	kindOf     map[int64]string
	causeOf    map[int64]int64
	taskOf     map[int64]string
	seq        map[string]int
	out        []Transition
}

// Project runs the machine over one project's events and returns every
// transition, oldest first.
//
// The events are applied in (at, rank, id) order. The rank exists for one
// case the timeline genuinely records at the same instant: `matev2 merge`
// closes the crew it merged, so `merge.done` and `crew.finished` share a
// timestamp, and a crew that left merged must not be recorded as having left
// merely closed.
func Project(project string, actors []Actor, events []Event) []Transition {
	p := &projector{
		project:    project,
		actors:     make(map[string]Actor, len(actors)),
		machines:   map[string]*machine{},
		crewByName: map[string]string{},
		kindOf:     make(map[int64]string, len(events)),
		causeOf:    make(map[int64]int64, len(events)),
		taskOf:     make(map[int64]string, len(events)),
		seq:        map[string]int{},
	}
	for _, a := range actors {
		p.actors[a.ID] = a
		switch a.Kind {
		case ActorMate:
			if p.mateID == "" || a.ID < p.mateID {
				p.mateID = a.ID
			}
		case ActorCrew:
			p.crewByName[a.Name] = a.ID
		}
	}
	sorted := make([]Event, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		if ra, rb := rank(a.Kind), rank(b.Kind); ra != rb {
			return ra < rb
		}
		return a.ID < b.ID
	})
	for _, e := range sorted {
		p.kindOf[e.ID] = e.Kind
		p.causeOf[e.ID] = e.CauseID
		p.taskOf[e.ID] = e.TaskActor
	}
	for _, e := range sorted {
		p.apply(e, e.ActorID)
		if e.SubjectID != "" && e.SubjectID != e.ActorID {
			p.apply(e, e.SubjectID)
		}
	}
	return p.out
}

// tracer is told which row of the table matched each event. Only this
// package's own test sets it, and it sets it to prove that every row of the
// machine is exercised by a case: a table with a row nothing reaches is a
// table nobody can trust.
var tracer func(actorKind string, edge Edge)

// rank orders two events recorded at the same instant. Only the merge pair
// needs one: everything else keeps the order the events were written in,
// which is `event.id`.
func rank(kind string) int {
	switch kind {
	case KindCrewFinished, KindCrewFailed:
		return 1
	default:
		return 0
	}
}

// apply routes one event at one machine. An event reaches the machine of its
// actor and the machine of its subject: `crew.spawned` is something the Mate
// did to a crew, and it is the crew that walks in.
func (p *projector) apply(e Event, actorID string) {
	actor, ok := p.actors[actorID]
	if !ok {
		return
	}
	table, ok := tables[actor.Kind]
	if !ok {
		// user, app and observer have no scene: nobody draws the captain.
		return
	}
	m := p.machine(actor)
	v := view{ev: e, m: m, p: p}
	for _, edge := range table {
		if edge.Kind != e.Kind || !edge.matches(m.state) {
			continue
		}
		if edge.When != nil && !edge.When(v) {
			continue
		}
		if tracer != nil {
			tracer(actor.Kind, edge)
		}
		p.fire(m, e, edge, v)
		return
	}
	// Nothing in the table knows this kind here. Recording it is the whole
	// point: a story that silently drops a fact reads as if the fact never
	// happened.
	p.record(m, e, m.state, UnexplainedPrefix+e.Kind, m.target, false)
}

func (p *projector) machine(actor Actor) *machine {
	if m, ok := p.machines[actor.ID]; ok {
		return m
	}
	m := &machine{actor: actor, state: Unknown}
	p.machines[actor.ID] = m
	return m
}

func (p *projector) fire(m *machine, e Event, edge Edge, v view) {
	if edge.To == stay {
		return
	}
	detail, target := "", ""
	if edge.Detail != nil {
		detail = edge.Detail(v)
	}
	if edge.Target != nil {
		target = edge.Target(v)
	}
	to := edge.To
	if to == resume {
		to, detail, target = m.resumeState, m.resumeDetail, m.resumeTarget
		if to == Unknown || to == Asleep || to == Blocked {
			to, detail, target = defaultResume(m.actor.Kind), "", ""
		}
	}
	// The walk is instantaneous in the data: both rows carry the same `at`,
	// so a renderer can animate the walk while a duration query still
	// measures the waiting from the second one.
	if edge.Via != "" && edge.Via != m.state {
		p.record(m, e, edge.Via, detail, target, true)
	}
	if to != m.state {
		p.record(m, e, to, detail, target, true)
	}
}

// defaultResume is where an incident that interrupted nothing known puts an
// actor back: its desk, or its office.
func defaultResume(actorKind string) State {
	if actorKind == ActorMate {
		return Idle
	}
	return AtDeskWorking
}

// alone is every state whose occupant faces nobody and holds nothing: a desk
// and an empty office. An edge's detail and target are dropped when it ends
// in one of them, so `answering(k3) -> idle` does not leave the Mate idle
// while still pointing at k3.
func alone(state State) bool { return state == Idle || state == AtDeskWorking }

// record writes one row. `move` is false for the unexplained record, which
// must not change where the actor is or what it is holding.
func (p *projector) record(m *machine, e Event, to State, detail, target string, move bool) {
	if move && alone(to) {
		detail, target = "", ""
	}
	if move && (to == Asleep || to == Blocked) && m.state != Asleep && m.state != Blocked {
		m.resumeState, m.resumeDetail, m.resumeTarget = m.state, m.detail, m.target
	}
	p.out = append(p.out, Transition{
		ID:      p.id(e.At),
		Project: p.project,
		ActorID: m.actor.ID,
		From:    m.state,
		To:      to,
		At:      e.At,
		EventID: e.ID,
		Target:  target,
		Detail:  detail,
	})
	if move {
		m.state, m.detail, m.target = to, detail, target
	}
}

// id is the transition's primary key, and its shape is what `v_now` and
// `--follow` rely on: the project, the timestamp in the fixed-width form
// `internal/db` stores, and a counter inside that instant. It therefore sorts
// lexicographically into story order, which is why `v_now` can break a tie
// inside one instant with `ORDER BY at DESC, id DESC` and still read the
// state the machine actually ended in.
//
// Keying on the moment rather than on a running number also means a late
// event - one the ingest reads out of a transcript minutes after it happened
// - renumbers only the transitions of its own instant, so a follower's cursor
// stays valid.
func (p *projector) id(at time.Time) string {
	key := db.FormatTime(at)
	n := p.seq[key]
	p.seq[key]++
	return fmt.Sprintf("%s|%s|%03d", p.project, key, n)
}
