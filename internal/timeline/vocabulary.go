package timeline

import "sort"

// The event vocabulary of docs/mvp.md M5, plus the two kinds that section
// does not name and task 25 requires. Every kind's payload is written out in
// docs/timeline.md with an example.
const (
	KindMateStarted   = "mate.started"
	KindMateStopped   = "mate.stopped"
	KindCrewSpawned   = "crew.spawned"
	KindCrewFinished  = "crew.finished"
	KindCrewFailed    = "crew.failed"
	KindModeChanged   = "mode.changed"
	KindTurnStarted   = "turn.started"
	KindTurnEnded     = "turn.ended"
	KindToolCalled    = "tool.called"
	KindToolFinished  = "tool.finished"
	KindGitCommitted  = "git.committed"
	KindStatusAppend  = "status.appended"
	KindMessageSent   = "message.sent"
	KindQuestionAsked = "question.asked"
	KindQuestionAnsw  = "question.answered"
	KindDigestSent    = "digest.sent"
	KindAssignClicked = "assign.clicked"
	KindIncidentOpen  = "incident.opened"
	KindIncidentResol = "incident.resolved"
	KindReviewStarted = "review.started"
	KindMergeDone     = "merge.done"
	KindContextCompac = "context.compacted"
	KindHealthChanged = "health.changed"

	// KindIngestUnresolved is the one kind M5's list does not carry. A
	// transcript the locator cannot find is the most expensive silence in
	// the timeline - every turn, token and tool call of that agent is
	// missing and nothing says so - so it is an event, written once per
	// (actor, reason), not a log line nobody reads.
	KindIngestUnresolved = "ingest.unresolved"
)

// Kinds is every event kind this package can write, sorted. The scene
// projection has a rule for each of them, and TestSceneKnowsEveryKindTheTimelineWrites
// compares the two lists: a kind added here without a rule there would make
// an actor's scene stop at the moment the new fact first happened.
func Kinds() []string {
	out := []string{
		KindAssignClicked, KindContextCompac, KindCrewFailed, KindCrewFinished,
		KindCrewSpawned, KindDigestSent, KindGitCommitted, KindHealthChanged,
		KindIncidentOpen, KindIncidentResol, KindIngestUnresolved, KindMateStarted,
		KindMateStopped, KindMergeDone, KindMessageSent, KindModeChanged,
		KindQuestionAnsw, KindQuestionAsked, KindReviewStarted, KindStatusAppend,
		KindToolCalled, KindToolFinished, KindTurnEnded, KindTurnStarted,
	}
	sort.Strings(out)
	return out
}

// Actor kinds, matching `actor.kind`.
const (
	ActorMate     = "mate"
	ActorCrew     = "crew"
	ActorUser     = "user"
	ActorApp      = "app"
	ActorObserver = "observer"
)

// Message channels, matching `message.channel`.
const (
	ChannelPane   = "pane"
	ChannelStatus = "status"
	ChannelDigest = "digest"
	ChannelAssign = "assign"
	ChannelHook   = "hook"
)

// Actor ids are derived from the workspace layout, not minted, so a rebuild
// names the same actors and a reader can write one down. `user`, `app` and
// `observer` are per project because every table in the schema carries a
// project and a row that belonged to no project could not be filtered out of
// one.
func MateActorID(project string) string { return ActorMate + ":" + project }

// CrewActorID is the actor of one crew of one project.
func CrewActorID(project, crew string) string { return ActorCrew + ":" + project + ":" + crew }

// UserActorID is the captain, as seen from one project.
func UserActorID(project string) string { return ActorUser + ":" + project }

// AppActorID is mate itself: the daemon's digests and the console's
// `[assign]` lines are its, not the captain's.
func AppActorID(project string) string { return ActorApp + ":" + project }

// ObserverActorID is the watcher that writes `incidents.log`.
func ObserverActorID(project string) string { return ActorObserver + ":" + project }
