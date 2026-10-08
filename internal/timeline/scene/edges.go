package scene

import (
	"regexp"
	"sort"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/box"
)

// The event vocabulary, spelled here because this package must not import
// `internal/timeline` - the timeline imports this one to run the projection.
// TestSceneKnowsEveryKindTheTimelineWrites keeps the two spellings equal, and
// the table below has a row for every one of them, so a kind that grows a new
// meaning cannot quietly stop moving anybody.
const (
	KindMateStarted      = "mate.started"
	KindMateStopped      = "mate.stopped"
	KindCrewSpawned      = "crew.spawned"
	KindCrewFinished     = "crew.finished"
	KindCrewFailed       = "crew.failed"
	KindModeChanged      = "mode.changed"
	KindTurnStarted      = "turn.started"
	KindTurnEnded        = "turn.ended"
	KindToolCalled       = "tool.called"
	KindToolFinished     = "tool.finished"
	KindGitCommitted     = "git.committed"
	KindStatusAppend     = "status.appended"
	KindMessageSent      = "message.sent"
	KindQuestionAsked    = "question.asked"
	KindQuestionAnsw     = "question.answered"
	KindDigestSent       = "digest.sent"
	KindAssignClicked    = "assign.clicked"
	KindIncidentOpen     = "incident.opened"
	KindIncidentResol    = "incident.resolved"
	KindReviewStarted    = "review.started"
	KindMergeDone        = "merge.done"
	KindContextCompac    = "context.compacted"
	KindHealthChanged    = "health.changed"
	KindIngestUnresolved = "ingest.unresolved"
)

// Actor kinds, matching `actor.kind`. Only two of them have a scene.
const (
	ActorMate = "mate"
	ActorCrew = "crew"
	ActorUser = "user"
	ActorApp  = "app"
)

// The message channels of `message.channel`, for the predicates below.
const (
	channelPane = "pane"
)

// Predicate is a payload test on an edge: the part of "which event kind moves
// an actor" that the kind alone does not answer, like a `wait-mate` status
// line or a merge the Mate ran itself.
type Predicate func(view) bool

// A Detail or a Target is read off the event when the edge fires.
type (
	detailFunc func(view) string
	targetFunc func(view) string
)

// Edge is one row of the state machine. It is data and not code so that the
// machine can be printed, counted and compared with the table in
// docs/timeline.md, which is what "the state machine spelled out as data"
// means in docs/mvp.md M5 question 2.
type Edge struct {
	// ID names the row in docs/timeline.md. TestEveryEdgeIsInTheDocument
	// fails when an edge here has no row there.
	ID string
	// Kind is the event kind that fires it.
	Kind string
	// From is every state it applies from. Nil means every state, which is
	// how the table's last row for a kind catches what the earlier rows did
	// not.
	From []State
	// When is the payload predicate, if the kind alone is not enough.
	When Predicate
	// Via is an instantaneous intermediate state, recorded with the same
	// `at` as To: the walk to the CEO's door, and the step out of the door.
	Via State
	// To is where the actor ends up, `stay` for an edge that explains an
	// event without moving anybody, or `resume` for the step out of an
	// incident.
	To     State
	Detail detailFunc
	Target targetFunc
	// Why is the sentence this row exists for, and it is the sentence
	// docs/timeline.md carries beside it.
	Why string
}

func (e Edge) matches(state State) bool {
	if e.From == nil {
		return true
	}
	for _, s := range e.From {
		if s == state {
			return true
		}
	}
	return false
}

// view is what a predicate sees: the event, the machine it is being applied
// to, and the rest of the projection for the few questions that need it.
type view struct {
	ev Event
	m  *machine
	p  *projector
}

// The state sets the table's From columns are written in terms of.
var (
	crewStates = []State{Unknown, Arriving, AtDeskWorking, WalkingToCEO, WaitingAtCEO,
		WaitingReview, Leaving, Gone, Asleep, Blocked}
	mateStates = []State{Unknown, Idle, OnPhone, ReceivingDigest, Reading, Deciding,
		Answering, Reviewing, Merging, Gone, Asleep, Blocked}

	// present is every state an actor can still be found working in. It is
	// the From of the two edges that open an incident, so a second finding
	// about an actor an incident already holds does not overwrite the first.
	crewPresent = except(crewStates, Gone, Asleep, Blocked)
	matePresent = except(mateStates, Gone, Asleep, Blocked)

	// here is everything but `gone`, which is the From of every ordinary
	// edge. An incident is a report about an actor and not a cage: a crew
	// the observer called asleep and which then writes a question into its
	// status file is demonstrably awake and standing at the door, and the
	// `resolved` line that follows finds it there and moves nobody.
	crewHere = except(crewStates, Gone)
	mateHere = except(mateStates, Gone)

	// beforeTheDesk is a crew that has not yet been seen working.
	beforeTheDesk = []State{Unknown, Arriving}
	// inAndOut is the Mate in the middle of a turn's work.
	inAndOut = []State{Reading, Deciding, Answering, Reviewing, Merging}
	// interrupted is an actor an incident is holding.
	interrupted = []State{Asleep, Blocked}
	// atTheDoor is the crew already waiting for a review.
	atTheDoor = []State{WaitingReview}
)

func except(all []State, drop ...State) []State {
	out := make([]State, 0, len(all))
	for _, s := range all {
		skip := false
		for _, d := range drop {
			if s == d {
				skip = true
			}
		}
		if !skip {
			out = append(out, s)
		}
	}
	return out
}

// tables is the machine, one table per actor kind that has a scene.
var tables = map[string][]Edge{
	ActorCrew: crewEdges,
	ActorMate: mateEdges,
}

// Tables returns the machine for printing and for the tests. The order is the
// order the projection tries the rows in: first match wins.
func Tables() map[string][]Edge { return tables }

// Kinds is every event kind the machine has a row for, sorted.
func Kinds() []string {
	seen := map[string]bool{}
	for _, table := range tables {
		for _, e := range table {
			seen[e.Kind] = true
		}
	}
	out := make([]string, 0, len(seen))
	for kind := range seen {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

// crewEdges is the employee's day: hired, at a desk, at the CEO's door with a
// question, back at the desk, at the door again with the finished work, and
// out of the building.
var crewEdges = []Edge{
	{ID: "crew.hired", Kind: KindCrewSpawned, From: crewHere, To: Arriving, Target: theMate,
		Why: "the Mate hired it, so it walks in"},
	{ID: "crew.hired.gone", Kind: KindCrewSpawned, To: stay,
		Why: "a crew that has left is not hired again"},

	{ID: "crew.merged", Kind: KindMergeDone, From: crewHere, Via: Leaving, To: Gone,
		Detail: word(DetailMerged), Why: "its branch landed, so it leaves merged"},
	{ID: "crew.merged.gone", Kind: KindMergeDone, To: stay,
		Why: "it has already left"},
	{ID: "crew.closed", Kind: KindCrewFinished, From: crewHere, Via: Leaving, To: Gone,
		Detail: word(DetailClosed), Why: "it was closed, so it leaves"},
	{ID: "crew.closed.gone", Kind: KindCrewFinished, To: stay,
		Why: "the merge already walked it out, one instant earlier by rank"},
	{ID: "crew.failed", Kind: KindCrewFailed, From: crewHere, Via: Leaving, To: Gone,
		Detail: word(DetailFailed), Why: "it was closed as failed, so it leaves"},
	{ID: "crew.failed.gone", Kind: KindCrewFailed, To: stay,
		Why: "it has already left"},

	{ID: "crew.asleep", Kind: KindIncidentOpen, From: crewPresent, When: incidentIs(string(box.IncidentStale)),
		To: Asleep, Detail: incidentKind, Why: "nothing has moved: it has fallen asleep at its desk"},
	{ID: "crew.blocked", Kind: KindIncidentOpen, From: crewPresent,
		When: incidentIs(string(box.IncidentRuntimeLost), string(box.IncidentWedged)),
		To:   Blocked, Detail: incidentKind, Why: "it cannot be reached at all any more"},
	{ID: "crew.incident.other", Kind: KindIncidentOpen, To: stay,
		Why: "`budget` is an inbox item and not a state (docs/mvp.md section 4b), and an incident on a crew that has left changes nothing"},
	{ID: "crew.awake", Kind: KindIncidentResol, From: interrupted, To: resume,
		Why: "the observer cleared it, so it is back where the incident found it"},
	{ID: "crew.awake.other", Kind: KindIncidentResol, To: stay,
		Why: "nothing was holding it"},

	{ID: "crew.asks", Kind: KindQuestionAsked, From: crewHere, Via: WalkingToCEO, To: WaitingAtCEO,
		Detail: word(DetailQuestion), Target: theMate,
		Why: "it carries the note to the CEO's office and waits at the door"},
	{ID: "crew.asks.away", Kind: KindQuestionAsked, To: stay,
		Why: "a question recorded for a crew that is asleep, blocked or gone moves nobody"},
	{ID: "crew.answered", Kind: KindQuestionAnsw, From: crewHere, To: AtDeskWorking,
		Why: "it has its answer, so it goes back to its desk"},
	{ID: "crew.answered.away", Kind: KindQuestionAnsw, To: stay,
		Why: "an answer to a crew that is not at the door moves nobody"},

	{ID: "crew.handback", Kind: KindStatusAppend, From: except(crewHere, WaitingReview),
		When: verbIs("wait-mate"), Via: WalkingToCEO, To: WaitingReview,
		Detail: word(DetailHandback), Target: theMate,
		Why: "it carries the finished work to the CEO's office and waits for the review"},
	{ID: "crew.at.desk", Kind: KindStatusAppend, From: beforeTheDesk, To: AtDeskWorking,
		Why: "its first word is proof it sat down"},
	{ID: "crew.status", Kind: KindStatusAppend, To: stay,
		Why: "a `working:` line is the crew saying what it is doing, not moving"},

	{ID: "crew.back.to.work", Kind: KindMessageSent, From: atTheDoor, When: toThisActor, To: AtDeskWorking,
		Why: "the Mate sent it back with something to change, so it returns to its desk"},
	{ID: "crew.message", Kind: KindMessageSent, To: stay,
		Why: "a line into its pane while it works changes no scene"},

	{ID: "crew.turn.first", Kind: KindTurnStarted, From: beforeTheDesk, To: AtDeskWorking,
		Why: "its first model call is proof it sat down"},
	{ID: "crew.turn", Kind: KindTurnStarted, To: stay,
		Why: "a crew waiting at the door still burns turns; the turn does not fetch it back"},
	{ID: "crew.tool.first", Kind: KindToolCalled, From: beforeTheDesk, To: AtDeskWorking,
		Why: "its first tool call is proof it sat down"},
	{ID: "crew.tool", Kind: KindToolCalled, To: stay,
		Why: "the work inside a turn is the turn's, not a scene change"},
	{ID: "crew.commit.first", Kind: KindGitCommitted, From: beforeTheDesk, To: AtDeskWorking,
		Why: "a commit is proof it sat down"},
	{ID: "crew.commit", Kind: KindGitCommitted, To: stay,
		Why: "committing is desk work"},

	{ID: "crew.turn.ended", Kind: KindTurnEnded, To: stay,
		Why: "a crew's turn ending says nothing about where it is standing"},
	{ID: "crew.tool.finished", Kind: KindToolFinished, To: stay, Why: "desk work"},
	{ID: "crew.compacted", Kind: KindContextCompac, To: stay, Why: "its memory, not its position"},
	{ID: "crew.health", Kind: KindHealthChanged, To: stay,
		Why: "the composer is an observation of a pane and keeps nothing a rebuild could read back"},
	{ID: "crew.unresolved", Kind: KindIngestUnresolved, To: stay,
		Why: "mate cannot find its transcript; that is mate's problem, not a move"},
	{ID: "crew.review", Kind: KindReviewStarted, To: stay,
		Why: "being reviewed is where it already is: waiting for the review"},
	{ID: "crew.mate.started", Kind: KindMateStarted, To: stay, Why: "the CEO's day, not the crew's"},
	{ID: "crew.mate.stopped", Kind: KindMateStopped, To: stay, Why: "the CEO's day, not the crew's"},
	{ID: "crew.mode", Kind: KindModeChanged, To: stay, Why: "the project's mode, not the crew's position"},
	{ID: "crew.digest", Kind: KindDigestSent, To: stay, Why: "addressed to the Mate"},
	{ID: "crew.assign", Kind: KindAssignClicked, To: stay, Why: "addressed to the Mate"},
}

// mateEdges is the CEO's day: alone in the office, on the phone to the
// captain, handed a note, reading it, deciding, answering, reviewing,
// landing the work, alone again.
var mateEdges = []Edge{
	{ID: "mate.opens", Kind: KindMateStarted, To: Idle, Why: "it takes the office"},
	{ID: "mate.closes", Kind: KindMateStopped, To: Gone, Why: "it leaves the office"},

	{ID: "mate.asleep", Kind: KindIncidentOpen, From: matePresent, When: incidentIs(string(box.IncidentStale)),
		To: Asleep, Detail: incidentKind, Why: "nothing has moved in its room"},
	{ID: "mate.blocked", Kind: KindIncidentOpen, From: matePresent,
		When: incidentIs(string(box.IncidentRuntimeLost), string(box.IncidentWedged)),
		To:   Blocked, Detail: incidentKind,
		Why: "the daemon files `wedged` against the Mate when a line will not go in (docs/mvp.md task 19)"},
	{ID: "mate.incident.other", Kind: KindIncidentOpen, To: stay, Why: "`budget` is an inbox item, not a state"},
	{ID: "mate.awake", Kind: KindIncidentResol, From: interrupted, To: resume, Why: "it is back where it was"},
	{ID: "mate.awake.other", Kind: KindIncidentResol, To: stay, Why: "nothing was holding it"},

	{ID: "mate.phone", Kind: KindMessageSent, From: mateHere, When: captainsOwnLine, To: OnPhone, Target: fromActor,
		Why: "the captain typed into its pane: the phone is ringing in the CEO's office"},
	{ID: "mate.reads.echo", Kind: KindMessageSent, From: []State{ReceivingDigest}, When: theHooksEcho,
		To: Reading, Target: keepTarget,
		Why: "the Mate's own hook recording that it read the line mate typed: the note is off the desk and in its hands"},
	{ID: "mate.message", Kind: KindMessageSent, To: stay,
		Why: "its own line out, or an echo of a line already counted"},

	{ID: "mate.digest", Kind: KindDigestSent, From: mateHere, To: ReceivingDigest,
		Detail: word(DetailDigest), Target: crewOfTheNote,
		Why: "mate walks the digest in and puts it on the desk"},
	{ID: "mate.digest.away", Kind: KindDigestSent, To: stay, Why: "there is nobody in the office"},
	{ID: "mate.assign", Kind: KindAssignClicked, From: mateHere, To: ReceivingDigest,
		Detail: word(DetailAssign), Target: crewOfTheNote,
		Why: "mate walks the captain's note in and puts it on the desk"},
	{ID: "mate.assign.away", Kind: KindAssignClicked, To: stay, Why: "there is nobody in the office"},

	{ID: "mate.reads", Kind: KindTurnStarted, From: []State{ReceivingDigest}, Via: Reading, To: Deciding,
		Target: noteInHand,
		Why:    "the first turn after a note landed is the turn that picks it up: it reads it, then thinks"},
	{ID: "mate.turn", Kind: KindTurnStarted, From: mateHere, To: Deciding, Target: keepTarget,
		Why: "a turn with nothing new in its hands is the CEO working at its desk, still on whatever it was on"},
	{ID: "mate.turn.away", Kind: KindTurnStarted, To: stay, Why: "asleep, blocked or gone"},
	{ID: "mate.turn.ended", Kind: KindTurnEnded, From: inAndOut, To: Idle,
		Why: "the turn is over and nothing else is in its hands"},
	{ID: "mate.turn.ended.holding", Kind: KindTurnEnded, To: stay,
		Why: "a note that arrived mid-turn is still unread when the turn ends, and the phone is still ringing"},

	{ID: "mate.answers", Kind: KindQuestionAnsw, From: mateHere, When: byThisActor,
		Via: Answering, To: Idle, Target: subject,
		Why: "it answers the crew at its door and the door is clear again"},
	{ID: "mate.answers.other", Kind: KindQuestionAnsw, To: stay,
		Why: "the captain answered it, which is the captain's doing and not the Mate's"},

	{ID: "mate.reviews", Kind: KindReviewStarted, From: mateHere, To: Reviewing, Target: subject,
		Why: "the review starts"},
	{ID: "mate.reviews.away", Kind: KindReviewStarted, To: stay, Why: "there is nobody in the office"},
	{ID: "mate.reviews.diff", Kind: KindToolCalled, From: mateHere, When: runsMateDiff,
		To: Reviewing, Target: crewOfTheDiff,
		Why: "`mate diff <project> <crew>` is the Mate reading a crew's work; nothing emits `review.started` yet"},
	{ID: "mate.tool", Kind: KindToolCalled, To: stay, Why: "the work inside a turn is the turn's"},

	{ID: "mate.merges", Kind: KindMergeDone, From: mateHere, When: mergedByTheMate,
		Via: Merging, To: Idle, Target: subject,
		Why: "the Mate ran `mate merge` itself after a review, so it lands the work"},
	{ID: "mate.merges.captain", Kind: KindMergeDone, To: stay,
		Why: "the captain merged from the console: the crew leaves, the Mate did nothing"},

	{ID: "mate.hires", Kind: KindCrewSpawned, To: stay, Why: "hiring happens inside a turn it is already in"},
	{ID: "mate.status", Kind: KindStatusAppend, To: stay, Why: "a crew's status line reaches the Mate as a question, not as a move"},
	{ID: "mate.asked", Kind: KindQuestionAsked, To: stay,
		Why: "a crew at the door does not move the Mate; the note reaching its pane does"},
	{ID: "mate.crew.finished", Kind: KindCrewFinished, To: stay, Why: "the crew's leaving, not the Mate's"},
	{ID: "mate.crew.failed", Kind: KindCrewFailed, To: stay, Why: "the crew's leaving, not the Mate's"},
	{ID: "mate.commit", Kind: KindGitCommitted, To: stay, Why: "crews commit, not the Mate"},
	{ID: "mate.tool.finished", Kind: KindToolFinished, To: stay, Why: "desk work"},
	{ID: "mate.compacted", Kind: KindContextCompac, To: stay, Why: "its memory, not its position"},
	{ID: "mate.health", Kind: KindHealthChanged, To: stay, Why: "an observation of a pane"},
	{ID: "mate.unresolved", Kind: KindIngestUnresolved, To: stay, Why: "mate cannot find its transcript"},
	{ID: "mate.mode", Kind: KindModeChanged, To: stay, Why: "the project's mode, not the Mate's position"},
}

// word is a constant detail.
func word(value string) detailFunc { return func(view) string { return value } }

// incidentKind is the detail of an `asleep` or a `blocked`: which finding put
// the actor there.
func incidentKind(v view) string { return v.ev.field("incident") }

// verbIs matches a status line's verb.
func verbIs(verb string) Predicate {
	return func(v view) bool { return v.ev.field("verb") == verb }
}

// incidentIs matches an incident's kind.
func incidentIs(kinds ...string) Predicate {
	return func(v view) bool {
		got := v.ev.field("incident")
		for _, kind := range kinds {
			if got == kind {
				return true
			}
		}
		return false
	}
}

// toThisActor matches a message addressed to the machine being projected.
func toThisActor(v view) bool { return v.ev.SubjectID == v.m.actor.ID }

// byThisActor matches an event the machine's own actor did.
func byThisActor(v view) bool { return v.ev.ActorID == v.m.actor.ID }

// captainsOwnLine is a line the captain typed into the Mate's pane. It is
// neither a digest nor an `[assign]` - those are mate's own, and have their
// own kinds - and it is not the hook's echo, whose channel is `hook`.
func captainsOwnLine(v view) bool {
	return v.p.actors[v.ev.ActorID].Kind == ActorUser &&
		v.ev.field("channel") == channelPane &&
		v.ev.SubjectID == v.m.actor.ID
}

// theHooksEcho is the Mate's own UserPromptSubmit hook writing back the line
// mate typed into its pane, which is the one fact in `sent.log` that proves
// the model read it (docs/timeline.md section 4, `"confirms": true`).
func theHooksEcho(v view) bool {
	return v.ev.field("confirms") == "true" && v.ev.SubjectID == v.m.actor.ID
}

// mergedByTheMate reads the `by` the causality pass wrote: `mate` when a Mate
// turn ran `mate merge`, `captain` when the console did and no event exists
// to point at (docs/timeline.md section 5).
func mergedByTheMate(v view) bool { return v.ev.field("by") == ActorMate }

// theMate is the project's Mate: who a crew faces when it walks to the door.
func theMate(v view) string { return v.p.mateID }

// fromActor is whoever sent the line: the captain for a pane message, mate
// for a digest or an `[assign]`.
func fromActor(v view) string { return v.ev.ActorID }

// subject is who the event was done to.
func subject(v view) string { return v.ev.SubjectID }

// crewOfTheNote is the crew a note walked into the office is about. It
// follows the cause chain: an `[assign]` is caused by the `question.asked` it
// carries, whose task is the crew, and a Mate turn by the line that reached
// its composer. Four hops is more than the chain is ever long; it bounds a
// cycle a bug could otherwise spin in.
//
// When the chain says nothing - which is what happens to the turn after the
// hook has echoed the line, because the echo is a `message.sent` with no
// cause of its own - the answer is whoever the Mate is already facing: the
// note is in its hands and the state it is in remembers whose it is.
func crewOfTheNote(v view) string {
	id := v.ev.CauseID
	for hop := 0; hop < 4 && id != 0; hop++ {
		if task := v.p.taskOf[id]; task != "" {
			return task
		}
		id = v.p.causeOf[id]
	}
	return ""
}

// noteInHand is crewOfTheNote for the two edges that pick a note up. When the
// chain says nothing - which is what happens to the turn after the hook has
// echoed the line, because the echo is a `message.sent` with no cause of its
// own - the answer is whoever the Mate is already facing: it is holding the
// note, and `receiving_digest` remembers whose it is.
func noteInHand(v view) string {
	if crew := crewOfTheNote(v); crew != "" {
		return crew
	}
	return v.m.target
}

// keepTarget leaves the Mate facing whoever it was already facing.
func keepTarget(v view) string { return v.m.target }

// mateDiff finds `mate diff <project> <crew>` in a shell command. The
// command is the `target` of the tool call, which internal/timeline already
// unwrapped and shortened to its first 80 runes - long enough for the two
// words after `diff`, which is all this needs.
var mateDiff = regexp.MustCompile(`mate diff\s+(\S+)\s+(\S+)`)

func crewOfTheDiff(v view) string {
	if v.ev.field("class") != "shell" {
		return ""
	}
	match := mateDiff.FindStringSubmatch(v.ev.field("target"))
	if match == nil {
		return ""
	}
	return v.p.crewByName[strings.Trim(match[2], `"'`)]
}

func runsMateDiff(v view) bool { return crewOfTheDiff(v) != "" }
