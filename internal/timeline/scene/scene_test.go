package scene

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// These cases are written from docs/mvp.md M5 question 2 and the Scene
// section of docs/timeline.md - the sentences that say which event moves whom
// and where to - and not from the table below them. Each case is one of those
// sentences played out as the events a real run would leave behind.

const project = "shop"

const (
	mateID = "mate:shop"
	crewID = "crew:shop:k3"
	userID = "user:shop"
	appID  = "app:shop"
	obsID  = "observer:shop"
)

var testActors = []Actor{
	{ID: mateID, Kind: ActorMate, Name: "mate", Project: project},
	{ID: crewID, Kind: ActorCrew, Name: "k3", Project: project},
	{ID: userID, Kind: ActorUser, Name: "captain", Project: project},
	{ID: appID, Kind: ActorApp, Name: "app", Project: project},
	{ID: obsID, Kind: "observer", Name: "observer", Project: project},
}

var base = time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

// script builds the events of one case. Ids are handed out in order, which is
// what the ingest does, and every event is dated by its own second so the
// order a reader would read them in is the order they are applied in.
type script struct {
	events []Event
}

func (s *script) add(sec int, kind, actor, subject string, kv ...any) int64 {
	e := Event{
		ID:        int64(len(s.events) + 1),
		At:        base.Add(time.Duration(sec) * time.Second),
		Kind:      kind,
		ActorID:   actor,
		SubjectID: subject,
		Payload:   map[string]any{},
	}
	for i := 0; i+1 < len(kv); i += 2 {
		e.Payload[kv[i].(string)] = kv[i+1]
	}
	s.events = append(s.events, e)
	return e.ID
}

// task marks the last event as being about one crew's task, which is how the
// ingest records a question and how the projection finds the crew a note is
// about.
func (s *script) task(crew string) { s.events[len(s.events)-1].TaskActor = crew }

// caused marks the last event as caused by another, which is what
// `cause_event_id` carries.
func (s *script) caused(by int64) { s.events[len(s.events)-1].CauseID = by }

// render is one transition in the form the cases are written in:
// `k3 at_desk_working->walking_to_ceo(question)>mate`.
func render(t Transition) string {
	out := fmt.Sprintf("%s %s->%s", name(t.ActorID), t.From, t.To)
	if t.Detail != "" {
		out += "(" + t.Detail + ")"
	}
	if t.Target != "" {
		out += ">" + name(t.Target)
	}
	return out
}

func name(actorID string) string {
	for _, a := range testActors {
		if a.ID == actorID {
			return a.Name
		}
	}
	return actorID
}

type sceneCase struct {
	name   string
	build  func(s *script)
	expect []string
}

var cases = []sceneCase{
	{
		name: "a crew is hired, works, asks, is answered, hands back and leaves merged",
		build: func(s *script) {
			s.add(0, KindCrewSpawned, mateID, crewID, "crew", "k3", "task", "add a Buy button")
			s.add(1, KindTurnStarted, crewID, "")
			s.add(2, KindToolCalled, crewID, "", "class", "shell", "target", "git status")
			s.add(3, KindStatusAppend, crewID, mateID, "verb", "working", "text", "reading the brief")
			s.add(4, KindQuestionAsked, crewID, mateID, "crew", "k3", "text", "A or B?")
			s.add(5, KindTurnEnded, crewID, "")
			s.add(6, KindQuestionAnsw, mateID, crewID, "crew", "k3", "by", "mate", "text", "A")
			s.add(7, KindGitCommitted, crewID, "", "short", "0d2d20d")
			s.add(8, KindStatusAppend, crewID, mateID, "verb", "wait-mate", "text", "ready in branch")
			// The captain merged from the console, which closes the crew:
			// the two events share an instant and the merge is applied first.
			s.add(9, KindCrewFinished, crewID, "", "crew", "k3", "state", "finished")
			s.add(9, KindMergeDone, mateID, crewID, "crew", "k3", "by", "captain", "into", "main")
		},
		expect: []string{
			"k3 ->arriving>mate",
			"k3 arriving->at_desk_working",
			"k3 at_desk_working->walking_to_ceo(question)>mate",
			"k3 walking_to_ceo->waiting_at_ceo(question)>mate",
			"mate ->answering>k3",
			"mate answering->idle",
			"k3 waiting_at_ceo->at_desk_working",
			"k3 at_desk_working->walking_to_ceo(handback)>mate",
			"k3 walking_to_ceo->waiting_review(handback)>mate",
			"k3 waiting_review->leaving(merged)",
			"k3 leaving->gone(merged)",
		},
	},
	{
		name: "a crew nobody merged leaves closed, and a crew that failed leaves failed",
		build: func(s *script) {
			s.add(0, KindCrewSpawned, mateID, crewID, "crew", "k3")
			s.add(1, KindStatusAppend, crewID, mateID, "verb", "working", "text", "on it")
			s.add(2, KindCrewFinished, crewID, "", "crew", "k3", "state", "finished")
		},
		expect: []string{
			"k3 ->arriving>mate",
			"k3 arriving->at_desk_working",
			"k3 at_desk_working->leaving(closed)",
			"k3 leaving->gone(closed)",
		},
	},
	{
		name: "a crew whose spawn failed leaves failed",
		build: func(s *script) {
			s.add(0, KindCrewSpawned, mateID, crewID, "crew", "k3")
			s.add(1, KindCrewFailed, crewID, "", "crew", "k3", "state", "failed", "reason", "trust dialog")
		},
		expect: []string{
			"k3 ->arriving>mate",
			"k3 arriving->leaving(failed)",
			"k3 leaving->gone(failed)",
		},
	},
	{
		name: "an incident holds a crew and gives it back exactly where it was",
		build: func(s *script) {
			s.add(0, KindCrewSpawned, mateID, crewID, "crew", "k3")
			s.add(1, KindTurnStarted, crewID, "")
			s.add(2, KindQuestionAsked, crewID, mateID, "crew", "k3", "text", "A or B?")
			s.add(3, KindIncidentOpen, obsID, crewID, "incident", "stale", "crew", "k3")
			s.add(4, KindToolCalled, crewID, "", "class", "read", "target", "README.md")
			s.add(5, KindIncidentResol, obsID, crewID, "incident", "stale", "crew", "k3")
		},
		expect: []string{
			"k3 ->arriving>mate",
			"k3 arriving->at_desk_working",
			"k3 at_desk_working->walking_to_ceo(question)>mate",
			"k3 walking_to_ceo->waiting_at_ceo(question)>mate",
			"k3 waiting_at_ceo->asleep(stale)",
			"k3 asleep->waiting_at_ceo(question)>mate",
		},
	},
	{
		name: "a crew the runtime lost is blocked, and the resolve finds it where it stood",
		build: func(s *script) {
			s.add(0, KindCrewSpawned, mateID, crewID, "crew", "k3")
			s.add(1, KindTurnStarted, crewID, "")
			s.add(2, KindIncidentOpen, obsID, crewID, "incident", "runtime_lost", "crew", "k3")
			s.add(3, KindIncidentOpen, obsID, crewID, "incident", "stale", "crew", "k3")
			s.add(4, KindIncidentResol, obsID, crewID, "incident", "runtime_lost", "crew", "k3")
		},
		expect: []string{
			"k3 ->arriving>mate",
			"k3 arriving->at_desk_working",
			"k3 at_desk_working->blocked(runtime_lost)",
			"k3 blocked->at_desk_working",
		},
	},
	{
		name: "a crew that speaks while the observer thinks it is asleep is awake",
		build: func(s *script) {
			s.add(0, KindCrewSpawned, mateID, crewID, "crew", "k3")
			s.add(1, KindStatusAppend, crewID, mateID, "verb", "working", "text", "on it")
			s.add(2, KindIncidentOpen, obsID, crewID, "incident", "stale", "crew", "k3")
			s.add(3, KindStatusAppend, crewID, mateID, "verb", "wait-mate", "text", "ready")
			s.add(4, KindIncidentResol, obsID, crewID, "incident", "stale", "crew", "k3")
		},
		expect: []string{
			"k3 ->arriving>mate",
			"k3 arriving->at_desk_working",
			"k3 at_desk_working->asleep(stale)",
			"k3 asleep->walking_to_ceo(handback)>mate",
			"k3 walking_to_ceo->waiting_review(handback)>mate",
		},
	},
	{
		name: "a budget incident is an inbox item and not a state",
		build: func(s *script) {
			s.add(0, KindCrewSpawned, mateID, crewID, "crew", "k3")
			s.add(1, KindStatusAppend, crewID, mateID, "verb", "working", "text", "on it")
			s.add(2, KindIncidentOpen, obsID, crewID, "incident", "budget", "crew", "k3")
			s.add(3, KindIncidentResol, obsID, crewID, "incident", "budget", "crew", "k3")
		},
		expect: []string{
			"k3 ->arriving>mate",
			"k3 arriving->at_desk_working",
		},
	},
	{
		name: "the Mate sends a crew that handed back to fix something",
		build: func(s *script) {
			s.add(0, KindCrewSpawned, mateID, crewID, "crew", "k3")
			s.add(1, KindStatusAppend, crewID, mateID, "verb", "wait-mate", "text", "ready")
			s.add(2, KindStatusAppend, crewID, mateID, "verb", "wait-mate", "text", "still ready")
			s.add(3, KindMessageSent, mateID, crewID, "channel", "pane", "to", "crew:k3",
				"crew", "k3", "text", "rebase onto main first")
		},
		expect: []string{
			"k3 ->arriving>mate",
			"k3 arriving->walking_to_ceo(handback)>mate",
			"k3 walking_to_ceo->waiting_review(handback)>mate",
			"k3 waiting_review->at_desk_working",
		},
	},
	{
		name: "the captain calls, the Mate works, and the turn ends with it alone again",
		build: func(s *script) {
			s.add(0, KindMateStarted, mateID, "", "harness", "claude")
			call := s.add(1, KindMessageSent, userID, mateID, "channel", "pane", "from", "user",
				"to", "mate", "text", "add a Buy button")
			s.add(2, KindTurnStarted, mateID, "")
			s.caused(call)
			s.add(3, KindToolCalled, mateID, "", "class", "shell", "target", "mate crew spawn shop k3")
			s.add(4, KindCrewSpawned, mateID, crewID, "crew", "k3")
			s.add(5, KindTurnEnded, mateID, "")
			s.add(6, KindMateStopped, mateID, "")
			s.add(7, KindTurnStarted, mateID, "")
		},
		expect: []string{
			"mate ->idle",
			"mate idle->on_phone>captain",
			"mate on_phone->deciding>captain",
			"k3 ->arriving>mate",
			"mate deciding->idle",
			"mate idle->gone",
		},
	},
	{
		name: "mate walks the captain's note in, the Mate reads it, thinks, and answers",
		build: func(s *script) {
			s.add(0, KindMateStarted, mateID, "", "harness", "claude")
			asked := s.add(1, KindQuestionAsked, crewID, mateID, "crew", "k3", "text", "A or B?")
			s.task(crewID)
			assign := s.add(2, KindAssignClicked, appID, mateID, "channel", "assign",
				"text", `resolve: k3 asked: "A or B?"`)
			s.caused(asked)
			s.add(3, KindTurnStarted, mateID, "")
			s.caused(assign)
			s.add(4, KindQuestionAnsw, mateID, crewID, "crew", "k3", "by", "mate", "text", "A")
			s.task(crewID)
			s.add(5, KindTurnEnded, mateID, "")
		},
		expect: []string{
			"mate ->idle",
			// The crew asking is the same event seen from the other room.
			"k3 ->walking_to_ceo(question)>mate",
			"k3 walking_to_ceo->waiting_at_ceo(question)>mate",
			"mate idle->receiving_digest(assign)>k3",
			"mate receiving_digest->reading>k3",
			"mate reading->deciding>k3",
			"mate deciding->answering>k3",
			"mate answering->idle",
			"k3 waiting_at_ceo->at_desk_working",
		},
	},
	{
		name: "the Mate's hook echoing the note is the Mate picking it up",
		build: func(s *script) {
			s.add(0, KindMateStarted, mateID, "", "harness", "claude")
			asked := s.add(1, KindQuestionAsked, crewID, mateID, "crew", "k3", "text", "A or B?")
			s.task(crewID)
			s.add(2, KindAssignClicked, appID, mateID, "channel", "assign",
				"text", `resolve: k3 asked: "A or B?"`)
			s.caused(asked)
			// The Mate's UserPromptSubmit hook writes the same line back when
			// the model reads it, which is what `confirms` records.
			s.add(3, KindMessageSent, appID, mateID, "channel", "hook", "confirms", true,
				"text", `resolve: k3 asked: "A or B?"`)
			// The turn after it is caused by the echo, not by the [assign]:
			// the causality rule gives a Mate turn the last line that reached
			// its composer.
			s.add(4, KindTurnStarted, mateID, "")
			s.add(5, KindTurnEnded, mateID, "")
		},
		expect: []string{
			"mate ->idle",
			"k3 ->walking_to_ceo(question)>mate",
			"k3 walking_to_ceo->waiting_at_ceo(question)>mate",
			"mate idle->receiving_digest(assign)>k3",
			"mate receiving_digest->reading>k3",
			"mate reading->deciding>k3",
			"mate deciding->idle",
		},
	},
	{
		name: "a digest that arrives mid-turn is still unread when the turn ends",
		build: func(s *script) {
			s.add(0, KindMateStarted, mateID, "", "harness", "claude")
			s.add(1, KindTurnStarted, mateID, "")
			asked := s.add(2, KindQuestionAsked, crewID, mateID, "crew", "k3", "text", "A or B?")
			s.task(crewID)
			digest := s.add(3, KindDigestSent, appID, mateID, "channel", "digest",
				"text", `digest: 1 item(s) — k3 needs-decision: "A or B?"`)
			s.caused(asked)
			s.add(4, KindTurnEnded, mateID, "")
			s.add(5, KindTurnStarted, mateID, "")
			s.caused(digest)
			s.add(6, KindContextCompac, mateID, "", "harness", "claude")
		},
		expect: []string{
			"mate ->idle",
			"mate idle->deciding",
			"k3 ->walking_to_ceo(question)>mate",
			"k3 walking_to_ceo->waiting_at_ceo(question)>mate",
			"mate deciding->receiving_digest(digest)>k3",
			"mate receiving_digest->reading>k3",
			"mate reading->deciding>k3",
		},
	},
	{
		name: "the Mate reviews when it reads a crew's diff, and lands the work itself after a review",
		build: func(s *script) {
			s.add(0, KindMateStarted, mateID, "", "harness", "claude")
			s.add(1, KindTurnStarted, mateID, "")
			s.add(2, KindToolCalled, mateID, "", "class", "shell", "target", "mate diff shop k3 --stat")
			s.add(3, KindToolFinished, mateID, "", "tool", "Bash", "ok", true)
			s.add(4, KindMergeDone, mateID, crewID, "crew", "k3", "by", "mate", "into", "main")
			s.add(5, KindTurnEnded, mateID, "")
		},
		expect: []string{
			"mate ->idle",
			"mate idle->deciding",
			"mate deciding->reviewing>k3",
			"mate reviewing->merging>k3",
			"mate merging->idle",
			// The same merge, seen from the crew's room.
			"k3 ->leaving(merged)",
			"k3 leaving->gone(merged)",
		},
	},
	{
		name: "review.started puts the Mate in front of a crew's work",
		build: func(s *script) {
			s.add(0, KindMateStarted, mateID, "", "harness", "claude")
			s.add(1, KindReviewStarted, mateID, crewID, "crew", "k3")
		},
		expect: []string{
			"mate ->idle",
			"mate idle->reviewing>k3",
		},
	},
	{
		name: "the daemon's wedged finding blocks the Mate until it clears",
		build: func(s *script) {
			s.add(0, KindMateStarted, mateID, "", "harness", "claude")
			s.add(1, KindIncidentOpen, obsID, mateID, "incident", "wedged", "crew", "mate")
			s.add(2, KindIncidentResol, obsID, mateID, "incident", "wedged", "crew", "mate")
			s.add(3, KindIncidentResol, obsID, mateID, "incident", "wedged", "crew", "mate")
		},
		expect: []string{
			"mate ->idle",
			"mate idle->blocked(wedged)",
			"mate blocked->idle",
		},
	},
	{
		name: "a crew nobody saw spawned is placed by the first thing it does",
		build: func(s *script) {
			s.add(0, KindToolCalled, crewID, "", "class", "read", "target", "README.md")
			s.add(1, KindTurnStarted, crewID, "")
			s.add(2, KindMessageSent, mateID, crewID, "channel", "pane", "to", "crew:k3",
				"crew", "k3", "text", "keep going")
			s.add(3, KindCrewFinished, crewID, "", "crew", "k3", "state", "finished")
			s.add(4, KindQuestionAnsw, mateID, crewID, "crew", "k3", "by", "mate", "text", "A")
		},
		expect: []string{
			"k3 ->at_desk_working",
			"k3 at_desk_working->leaving(closed)",
			"k3 leaving->gone(closed)",
			"mate ->answering>k3",
			"mate answering->idle",
		},
	},
	{
		name: "a crew whose first word is a commit is at its desk",
		build: func(s *script) {
			s.add(0, KindGitCommitted, crewID, "", "short", "0d2d20d", "subject", "docs: add Buy link")
		},
		expect: []string{
			"k3 ->at_desk_working",
		},
	},
	{
		name: "the Mate can be asleep, and a budget finding about it is an inbox item",
		build: func(s *script) {
			s.add(0, KindMateStarted, mateID, "", "harness", "claude")
			s.add(1, KindIncidentOpen, obsID, mateID, "incident", "budget", "crew", "mate")
			s.add(2, KindIncidentOpen, obsID, mateID, "incident", "stale", "crew", "mate")
			s.add(3, KindIngestUnresolved, mateID, "", "reason", "transcript_not_found")
			s.add(4, KindIncidentResol, obsID, mateID, "incident", "stale", "crew", "mate")
		},
		expect: []string{
			"mate ->idle",
			"mate idle->asleep(stale)",
			"mate asleep->idle",
		},
	},
	{
		name: "an event no edge knows is recorded rather than dropped",
		build: func(s *script) {
			s.add(0, KindCrewSpawned, mateID, crewID, "crew", "k3")
			s.add(1, "weather.changed", crewID, "")
		},
		expect: []string{
			"k3 ->arriving>mate",
			"k3 arriving->arriving(unexplained: weather.changed)>mate",
		},
	},
	{
		name: "the rest of the vocabulary moves nobody",
		build: func(s *script) {
			s.add(0, KindMateStarted, mateID, "", "harness", "claude")
			s.add(1, KindCrewSpawned, mateID, crewID, "crew", "k3")
			s.add(2, KindTurnStarted, crewID, "")
			s.add(3, KindModeChanged, appID, "", "from", "manual", "to", "auto")
			s.add(4, KindHealthChanged, crewID, "", "from", "empty", "to", "busy")
			s.add(5, KindToolFinished, crewID, "", "tool", "exec", "ok", true)
			s.add(6, KindTurnEnded, crewID, "")
			s.add(7, KindContextCompac, crewID, "", "harness", "codex")
			s.add(8, KindIngestUnresolved, crewID, "", "reason", "rollout_not_adopted")
			s.add(9, KindGitCommitted, crewID, "", "short", "0d2d20d")
			s.add(10, KindMessageSent, mateID, userID, "channel", "pane", "to", "user", "text", "k3 is ready")
			s.add(11, KindMessageSent, appID, mateID, "channel", "hook", "confirms", true, "text", "digest: …")
			s.add(12, KindReviewStarted, mateID, crewID, "crew", "k3")
			s.add(13, KindMateStopped, mateID, "")
			s.add(14, KindDigestSent, appID, mateID, "channel", "digest", "text", "digest: 1 item(s)")
			s.add(15, KindAssignClicked, appID, mateID, "channel", "assign", "text", "resolve: k3 asked")
			s.add(16, KindQuestionAnsw, userID, crewID, "crew", "k3", "by", "user", "text", "A")
			s.add(17, KindCrewFinished, crewID, "", "crew", "k3", "state", "finished")
			s.add(18, KindMergeDone, mateID, crewID, "crew", "k3", "by", "captain")
			s.add(19, KindQuestionAsked, crewID, mateID, "crew", "k3", "text", "A or B?")
			s.add(20, KindStatusAppend, crewID, mateID, "verb", "wait-mate", "text", "ready")
			s.add(21, KindCrewFailed, crewID, "", "crew", "k3", "state", "failed")
			s.add(22, KindCrewSpawned, mateID, crewID, "crew", "k3")
			s.add(23, KindReviewStarted, mateID, crewID, "crew", "k3")
		},
		expect: []string{
			"mate ->idle",
			"k3 ->arriving>mate",
			"k3 arriving->at_desk_working",
			"mate idle->reviewing>k3",
			"mate reviewing->gone",
			"k3 at_desk_working->leaving(closed)",
			"k3 leaving->gone(closed)",
		},
	},
}

func runCase(t *testing.T, c sceneCase) []Transition {
	t.Helper()
	s := &script{}
	c.build(s)
	rows := Project(project, testActors, s.events)
	var got []string
	for _, r := range rows {
		got = append(got, render(r))
	}
	if strings.Join(got, "\n") != strings.Join(c.expect, "\n") {
		t.Fatalf("%s\n--- got ---\n%s\n--- want ---\n%s",
			c.name, strings.Join(got, "\n"), strings.Join(c.expect, "\n"))
	}
	return rows
}

func TestTheMachineMovesEverybodyTheSpecSays(t *testing.T) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runCase(t, c) })
	}
}

// neverRoutedHere is every row of the machine no event can reach, because the
// kind never names that actor. `health.changed` is written about a crew's
// pane and carries no subject, so it cannot reach the Mate's table;
// `mode.changed` belongs to mate itself and names nobody; `mate.started`
// never mentions a crew.
//
// The rows exist anyway, because a kind with no unconditional row in a table
// would make `unexplained` mean "a state nobody thought about" instead of "a
// kind nobody thought about" (TestEveryKindHasACatchAllRow), and because the
// day one of these grows a subject - a `health.changed` for the Mate, a
// `budget` incident from task 27 - the machine already has an answer.
var neverRoutedHere = map[string]bool{
	"crew.mate.started":  true,
	"crew.mate.stopped":  true,
	"crew.mode":          true,
	"crew.digest":        true,
	"crew.assign":        true,
	"mate.answers.other": true, // an answer reaches the Mate's table only when the Mate sent it
	"mate.crew.finished": true,
	"mate.crew.failed":   true,
	"mate.commit":        true,
	"mate.health":        true,
	"mate.mode":          true,
}

// Every row of the table is exercised by a case above, and every state of the
// machine is reached. A row nothing reaches is a rule nobody has checked, and
// a state nothing reaches is either a state nobody needs or a producer
// nobody wrote.
func TestEveryEdgeAndEveryStateIsExercised(t *testing.T) {
	fired := map[string]bool{}
	tracer = func(actorKind string, edge Edge) { fired[edge.ID] = true }
	t.Cleanup(func() { tracer = nil })

	reached := map[State]bool{}
	for _, c := range cases {
		s := &script{}
		c.build(s)
		for _, r := range Project(project, testActors, s.events) {
			reached[r.To] = true
		}
	}

	var missed []string
	for kind, table := range tables {
		for _, edge := range table {
			if !fired[edge.ID] && !neverRoutedHere[edge.ID] {
				missed = append(missed, kind+" "+edge.ID)
			}
		}
	}
	if len(missed) > 0 {
		t.Fatalf("%d row(s) of the machine no case reaches:\n%s", len(missed), strings.Join(missed, "\n"))
	}

	for _, state := range append(append([]State{}, crewStates...), mateStates...) {
		if state == Unknown {
			continue
		}
		if !reached[state] {
			t.Fatalf("no case puts anybody in %s", state)
		}
	}
}

// The table's last row for a kind catches everything the rows before it did
// not, which is what makes `unexplained` mean "a kind nobody has thought
// about" rather than "a state nobody has thought about".
func TestEveryKindHasACatchAllRow(t *testing.T) {
	for kind, table := range tables {
		for _, want := range Kinds() {
			found := false
			for _, edge := range table {
				if edge.Kind == want && edge.From == nil && edge.When == nil {
					found = true
				}
			}
			if !found {
				t.Errorf("the %s table has no unconditional row for %s", kind, want)
			}
		}
	}
}

func TestEdgeIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, table := range tables {
		for _, edge := range table {
			if seen[edge.ID] {
				t.Errorf("two rows share the id %q", edge.ID)
			}
			seen[edge.ID] = true
			if edge.Why == "" {
				t.Errorf("row %q has no reason written beside it", edge.ID)
			}
		}
	}
}

// The document is the machine: docs/timeline.md carries a row for every edge,
// keyed by the same id, so a reader who wants to know what moves a crew reads
// the document and not this file.
func TestEveryEdgeIsInTheDocument(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/timeline.md")
	if err != nil {
		t.Fatalf("read docs/timeline.md: %v", err)
	}
	text := string(doc)
	for _, table := range tables {
		for _, edge := range table {
			if !strings.Contains(text, "`"+edge.ID+"`") {
				t.Errorf("docs/timeline.md has no row for the edge %q", edge.ID)
			}
		}
	}
}

// The ids sort into story order, which is what `v_now` reads the newest state
// with and what `--follow` uses as a cursor.
func TestTransitionIDsSortIntoStoryOrder(t *testing.T) {
	c := cases[0]
	s := &script{}
	c.build(s)
	rows := Project(project, testActors, s.events)
	for i := 1; i < len(rows); i++ {
		if rows[i-1].ID >= rows[i].ID {
			t.Fatalf("id %q does not sort before %q", rows[i-1].ID, rows[i].ID)
		}
		if rows[i-1].At.After(rows[i].At) {
			t.Fatalf("the projection produced %s after %s", rows[i].At, rows[i-1].At)
		}
	}
}
