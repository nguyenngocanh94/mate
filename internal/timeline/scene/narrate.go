package scene

import (
	"fmt"
	"strings"
	"time"
)

// Row is a transition with the names a reader would say out loud instead of
// the ids a join needs. It is what `mate events --scene` prints and what
// the narrator reads.
type Row struct {
	Transition
	ActorKind  string
	ActorName  string
	TargetName string
}

// NowRow is one line of `v_now`: where an actor is standing right now, since
// when, who it is facing, and what it has spent today.
type NowRow struct {
	ActorID     string
	ActorKind   string
	ActorName   string
	Project     string
	State       State
	Since       time.Time
	TargetID    string
	TargetName  string
	Detail      string
	TokensToday int64
}

// Narrate renders one transition as one sentence of the office story,
// prefixed with the local time to the second - the same shape
// timeline.Narrate prints an event in, so the two can be read side by side.
func Narrate(r Row) string { return NarrateIn(r, time.Local) }

// NarrateIn is Narrate in a named zone. The golden test uses time.UTC,
// because a story whose sentences move with the reader's timezone cannot be
// compared between two machines.
func NarrateIn(r Row, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return fmt.Sprintf("%s %s", r.At.In(loc).Format("15:04:05"), r.Sentence())
}

// NarrateNow renders one row of the snapshot. It is deliberately not a
// timestamped line: it is where somebody is standing, not something that
// happened.
func NarrateNow(n NowRow) string { return NarrateNowIn(n, time.Local) }

// NarrateNowIn is NarrateNow in a named zone.
func NarrateNowIn(n NowRow, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	sentence := fmt.Sprintf("%s is %s", who(n.ActorKind, n.ActorName),
		clause(n.State, n.Detail, n.TargetName))
	if n.Since.IsZero() {
		return fmt.Sprintf("%-8s %s", "now", sentence)
	}
	return fmt.Sprintf("%-8s %s (since %s)", "now", sentence, n.Since.In(loc).Format("15:04:05"))
}

// Sentence is the office phrase for one transition. The phrases are written
// out in docs/timeline.md beside the edge that produces each of them.
func (r Row) Sentence() string {
	actor := who(r.ActorKind, r.ActorName)
	if r.Unexplained() {
		kind := strings.TrimPrefix(r.Detail, UnexplainedPrefix)
		return fmt.Sprintf("%s: nothing in the scene explains %s", actor, kind)
	}
	// Coming out of an incident is told as coming out of it, whatever the
	// state underneath turns out to be.
	switch r.From {
	case Asleep:
		return fmt.Sprintf("%s wakes up, back %s", actor, clause(r.To, r.Detail, r.TargetName))
	case Blocked:
		return fmt.Sprintf("%s is back in touch, %s", actor, clause(r.To, r.Detail, r.TargetName))
	}
	switch r.To {
	case Arriving:
		return actor + " arrives at the office"
	case AtDeskWorking:
		if r.From == Unknown || r.From == Arriving {
			return actor + " sits down at its desk"
		}
		if r.From == WaitingReview {
			return actor + " goes back to its desk with more to do"
		}
		return actor + " goes back to its desk"
	case WalkingToCEO:
		if r.Detail == DetailHandback {
			return actor + " walks to the CEO's office with the finished work"
		}
		return actor + " walks to the CEO's office with a question"
	case WaitingAtCEO:
		return actor + " waits at the CEO's door"
	case WaitingReview:
		return actor + " waits for the Mate to review the work"
	case Leaving:
		switch r.Detail {
		case DetailMerged:
			return actor + " leaves, merged"
		case DetailFailed:
			return actor + " leaves, its task failed"
		default:
			return actor + " leaves, its task closed"
		}
	case Gone:
		if r.ActorKind == ActorMate {
			return actor + " shuts the office"
		}
		return actor + " is out of the building"
	case Asleep:
		return fmt.Sprintf("%s falls asleep (%s)", actor, orDash(r.Detail))
	case Blocked:
		return fmt.Sprintf("%s cannot be reached (%s)", actor, orDash(r.Detail))
	case Idle:
		if r.From == Unknown {
			return actor + " takes the office"
		}
		return actor + " is alone in its office again"
	case OnPhone:
		return "the captain calls " + actor
	case ReceivingDigest:
		if r.Detail == DetailAssign {
			return fmt.Sprintf("mate walks the captain's note into %s's office", actor)
		}
		return fmt.Sprintf("mate walks a digest into %s's office", actor)
	case Reading:
		if r.TargetName == "" {
			return actor + " reads the note"
		}
		return fmt.Sprintf("%s reads %s's note", actor, r.TargetName)
	case Deciding:
		return actor + " thinks it over"
	case Answering:
		return fmt.Sprintf("%s answers %s", actor, orSomebody(r.TargetName))
	case Reviewing:
		return fmt.Sprintf("%s reviews %s's work", actor, orSomebody(r.TargetName))
	case Merging:
		return fmt.Sprintf("%s lands %s's work", actor, orSomebody(r.TargetName))
	default:
		return fmt.Sprintf("%s is %s", actor, orDash(string(r.To)))
	}
}

// clause is where somebody is, with no verb of its own, so it reads both
// after "is" (the snapshot) and after "back" (waking up).
func clause(state State, detail, target string) string {
	switch state {
	case Arriving:
		return "on its way in"
	case AtDeskWorking:
		return "at its desk"
	case WalkingToCEO:
		if detail == DetailHandback {
			return "walking to the CEO's office with the finished work"
		}
		return "walking to the CEO's office with a question"
	case WaitingAtCEO:
		return "waiting at the CEO's door"
	case WaitingReview:
		return "waiting for the Mate to review the work"
	case Leaving:
		return "on its way out, " + orDash(detail)
	case Gone:
		return "gone"
	case Asleep:
		return fmt.Sprintf("asleep (%s)", orDash(detail))
	case Blocked:
		return fmt.Sprintf("out of reach (%s)", orDash(detail))
	case Idle:
		return "alone in its office"
	case OnPhone:
		return "on the phone with the captain"
	case ReceivingDigest:
		if detail == DetailAssign {
			return "taking the captain's note from mate"
		}
		return "taking a digest from mate"
	case Reading:
		if target == "" {
			return "reading the note"
		}
		return "reading " + target + "'s note"
	case Deciding:
		return "thinking it over"
	case Answering:
		return "answering " + orSomebody(target)
	case Reviewing:
		return "reviewing " + orSomebody(target) + "'s work"
	case Merging:
		return "landing " + orSomebody(target) + "'s work"
	case Unknown:
		return "nowhere the scene has placed yet"
	default:
		return string(state)
	}
}

// who names an actor the way the office does: the Mate has an article
// because there is one of it, a crew is called by its name.
func who(actorKind, name string) string {
	switch actorKind {
	case ActorMate:
		return "the Mate"
	case ActorUser:
		return "the captain"
	case ActorApp:
		return "the app"
	default:
		if name == "" {
			return "somebody"
		}
		return name
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orSomebody(s string) string {
	if s == "" {
		return "somebody"
	}
	return s
}
