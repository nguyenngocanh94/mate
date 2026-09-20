package timeline

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Narrate renders one event as one sentence of the office story docs/mvp.md
// M5 asks for: "Mate là CEO ngồi trong phòng, crew là nhân viên, crew hỏi thì
// cầm giấy chạy vào phòng CEO đứng đợi". The phrase table is written out in
// docs/timeline.md, and the rule behind it is that every sentence names who
// acted, what they did, and - where the event carries it - to whom.
//
// The time is local and to the second, because this is read beside a wall
// clock, not diffed.
func Narrate(e StoryEvent) string { return NarrateIn(e, time.Local) }

// NarrateIn is Narrate in a named zone. A golden test uses time.UTC, because
// a story whose sentences move with the reader's timezone cannot be compared
// between two machines.
func NarrateIn(e StoryEvent, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return fmt.Sprintf("%s %s", e.Time().In(loc).Format("15:04:05"), narratePhrase(e))
}

// quotedRunes bounds an agent's own words inside a sentence. Long enough for
// a real question, short enough that a story stays a story.
const quotedRunes = 90

func narratePhrase(e StoryEvent) string {
	who := actorPhrase(e.Actor, e.ActorKind)
	switch e.Kind {
	case KindMateStarted:
		return fmt.Sprintf("the Mate starts up (%s)", orUnknown(e.Field("harness")))
	case KindMateStopped:
		return "the Mate shuts down"
	case KindCrewSpawned:
		return fmt.Sprintf("the Mate hires %s for %s", e.Field("crew"), quote(e.Field("task"), quotedRunes))
	case KindCrewFinished:
		return fmt.Sprintf("%s is closed as finished", e.Field("crew"))
	case KindCrewFailed:
		if reason := e.Field("reason"); reason != "" {
			return fmt.Sprintf("%s is closed as failed: %s", e.Field("crew"), reason)
		}
		return fmt.Sprintf("%s is closed as failed", e.Field("crew"))
	case KindModeChanged:
		return fmt.Sprintf("the project switches to %s mode", e.Field("to"))
	case KindTurnStarted:
		return fmt.Sprintf("%s starts a turn", who)
	case KindTurnEnded:
		return fmt.Sprintf("%s ends a turn after %s (%s tool call(s), %s output tokens)",
			who, durationPhrase(e.Field("duration_ms")), orZero(e.Field("tool_count")), orZero(e.Field("output_tokens")))
	case KindToolCalled:
		return fmt.Sprintf("%s %s", who, toolPhrase(e.Field("class"), e.Field("tool"), e.Field("target")))
	case KindToolFinished:
		if e.Field("ok") == "false" {
			return fmt.Sprintf("%s's %s fails", who, e.Field("tool"))
		}
		return fmt.Sprintf("%s finishes %s in %s", who, e.Field("tool"), durationPhrase(e.Field("duration_ms")))
	case KindGitCommitted:
		return fmt.Sprintf("%s commits %s %s", who, e.Field("short"), quote(e.Field("subject"), quotedRunes))
	case KindStatusAppend:
		return statusPhrase(who, e)
	case KindMessageSent:
		return messagePhrase(e)
	case KindQuestionAsked:
		return fmt.Sprintf("crew %s asks the Mate: %s", e.Field("crew"), quote(e.Field("text"), quotedRunes))
	case KindQuestionAnsw:
		answerer := "the Mate"
		if e.Field("by") == "user" {
			answerer = "the captain"
		}
		return fmt.Sprintf("%s answers %s: %s (it waited %s)",
			answerer, e.Field("crew"), quote(e.Field("text"), quotedRunes), durationPhrase(e.Field("waited_ms")))
	case KindDigestSent:
		return fmt.Sprintf("matev2 walks a digest into the Mate's office: %s", quote(e.Field("text"), quotedRunes))
	case KindAssignClicked:
		return fmt.Sprintf("the captain hands the Mate a question to resolve: %s", quote(e.Field("text"), quotedRunes))
	case KindIncidentOpen:
		return fmt.Sprintf("the observer flags %s: %s", orDash(e.Field("crew")), e.Field("incident"))
	case KindIncidentResol:
		return fmt.Sprintf("the observer clears %s's %s", orDash(e.Field("crew")), e.Field("incident"))
	case KindReviewStarted:
		return fmt.Sprintf("the Mate starts reviewing %s", e.Subject)
	case KindMergeDone:
		by := "the captain"
		if e.Field("by") == "mate" {
			by = "the Mate"
		}
		return fmt.Sprintf("%s merges %s into %s", by, e.Field("crew"), e.Field("into"))
	case KindContextCompac:
		return fmt.Sprintf("%s's context is compacted", who)
	case KindHealthChanged:
		return fmt.Sprintf("%s's composer goes %s", who, e.Field("to"))
	case KindIngestUnresolved:
		return fmt.Sprintf("matev2 cannot find %s's transcript (%s)", who, e.Field("reason"))
	default:
		return fmt.Sprintf("%s: %s", who, e.Kind)
	}
}

// actorPhrase is how a sentence names an actor. The Mate and the captain get
// an article because there is one of each; a crew is called by its name,
// which is what everybody in the story calls it.
func actorPhrase(name, kind string) string {
	switch kind {
	case ActorMate:
		return "the Mate"
	case ActorUser:
		return "the captain"
	case ActorApp:
		return "matev2"
	case ActorObserver:
		return "the observer"
	default:
		return name
	}
}

// toolPhrase turns a tool call into a verb. The class is the harness-neutral
// grouping internal/harness already assigns, so a Claude `Edit` and a Codex
// `apply_patch` read the same way.
func toolPhrase(class, tool, target string) string {
	switch class {
	case "read":
		return "reads " + orSomething(target)
	case "edit":
		return "edits " + orSomething(target)
	case "shell":
		return "runs: " + orSomething(target)
	case "search":
		return "searches for " + orSomething(target)
	case "agent":
		return "delegates to a subagent"
	default:
		if target == "" {
			return "uses " + tool
		}
		return fmt.Sprintf("uses %s on %s", tool, target)
	}
}

func statusPhrase(who string, e StoryEvent) string {
	switch e.Field("verb") {
	case "working":
		return fmt.Sprintf("%s reports: %s", who, e.Field("text"))
	case "wait-mate":
		return fmt.Sprintf("%s hands back: %s", who, e.Field("text"))
	case "needs-decision":
		return fmt.Sprintf("%s writes a question into its status file", who)
	default:
		return fmt.Sprintf("%s writes: %s", who, e.Field("line"))
	}
}

func messagePhrase(e StoryEvent) string {
	text := quote(e.Field("text"), quotedRunes)
	switch {
	case e.Field("confirms") == "true":
		// The Mate's own hook, recording that the line matev2 typed reached
		// the model. It is the second half of one handover, not a second one.
		return "the Mate reads it"
	case e.Field("channel") == ChannelHook:
		return fmt.Sprintf("matev2 notes: %s", e.Field("text"))
	case e.ActorKind == ActorUser && e.Field("to") == "mate":
		return fmt.Sprintf("the captain tells the Mate: %s", text)
	case e.ActorKind == ActorMate && e.Field("to") == "user":
		return fmt.Sprintf("the Mate reports to the captain: %s", text)
	case e.ActorKind == ActorMate:
		return fmt.Sprintf("the Mate sends %s: %s", e.Field("crew"), text)
	case e.ActorKind == ActorUser:
		return fmt.Sprintf("the captain sends %s: %s", e.Field("crew"), text)
	default:
		return fmt.Sprintf("matev2 sends %s: %s", e.Field("to"), text)
	}
}

// durationPhrase renders a millisecond count the way a person says it.
func durationPhrase(ms string) string {
	value, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return "an unrecorded time"
	}
	d := time.Duration(value) * time.Millisecond
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", value)
	case d < time.Minute:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", d.Seconds()), ".0") + "s"
	default:
		return d.Round(time.Second).String()
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "harness unknown"
	}
	return s
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orSomething(s string) string {
	if s == "" {
		return "something the transcript does not name"
	}
	return s
}
