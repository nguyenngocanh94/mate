// Package crewstate holds the crew state machine of docs/mvp.md section 4b:
// the seven states, who is allowed to set each one, and the fixed order a
// displayed state is resolved in. It also decides the second column
// `mate state <project> <crew>` prints - health, which is an observation
// of the pane and not a state at all.
//
// Everything here is pure. Declare takes the three recorded facts (the
// crew's `.meta`, whether the observer has an open incident for it, the
// verb of its last `.status` line) and returns the one state; Observe takes
// what a caller saw of the runtime and returns the health line. Neither
// reads a file, a pane or Herdr, which is what lets the rules be tested as
// a table and lets every caller - the CLI, internal/query, a test - agree
// on one answer.
//
// The package deliberately imports nothing from the rest of mate. Its
// vocabulary for a status verb and for a composer reading is spelled out
// here rather than imported from internal/box and internal/send, so that
// crewstate stays a leaf a CLI, a read model and a console can all depend
// on without pulling in file merging or Herdr. The callers translate; the
// table stays free of both.
package crewstate

import "strings"

// State is one of the seven crew states of mvp.md section 4b. Each has
// exactly one owner:
//
//	spawned         the app, through `crew spawn` (`.meta` state=spawned)
//	working         the crew (`.status`)
//	needs-decision  the crew (`.status`)
//	wait-mate       the crew (`.status`)
//	blocked         the observer (`incidents.log`)
//	finished        the Mate or the user, through `crew stop` (`.meta`)
//	failed          the Mate or the user through `crew stop --discard`, or
//	                the app when a spawn failed (`.meta`)
//
// There is no `reserved`, `stopped`, `parked`, `done` or `unknown`: a state
// nobody owns is a state nobody can be held to.
type State string

const (
	StateSpawned       State = "spawned"
	StateWorking       State = "working"
	StateNeedsDecision State = "needs-decision"
	StateWaitMate      State = "wait-mate"
	StateBlocked       State = "blocked"
	StateFinished      State = "finished"
	StateFailed        State = "failed"
)

// Closed reports whether the state is terminal: the task is over and the
// crew is no longer work in flight. Closed crews leave the console tree,
// the inbox and `crew list` (mvp.md section 4b).
func (s State) Closed() bool { return s == StateFinished || s == StateFailed }

func (s State) String() string { return string(s) }

// StatusVerb is the subset of the vocabulary a crew itself may write, and
// the only part of `.status` this package reads. A caller parses the raw
// line (internal/box.ParseStatus, which also maps the legacy verbs) and
// hands over just the verb.
type StatusVerb string

const (
	VerbWorking       StatusVerb = "working"
	VerbNeedsDecision StatusVerb = "needs-decision"
	VerbWaitMate      StatusVerb = "wait-mate"
	// VerbNone is "the crew has written no line this package recognises",
	// which is not the same as "the crew is idle".
	VerbNone StatusVerb = ""
)

// Meta keys Declare reads out of `crews/<id>.meta`. They are spelled here
// because this package is the one that gives them meaning; internal/spawn
// writes them under the same names.
const (
	// MetaState is the state key only the app writes: spawned at spawn,
	// finished or failed at `crew stop`, failed when a spawn fails.
	MetaState = "state"
	// MetaStoppedAt is the pre-4b record of a closed crew. A meta with a
	// stopped_at and no state= was written before the state key existed and
	// reads as finished.
	MetaStoppedAt = "stopped_at"
)

// Meta keys of a crew's pull request (docs/mvp.md M18), written by
// `mate pr watch` and read by `crew stop`, recovery and the console.
const (
	// MetaPRURL is the pull request the crew opened.
	MetaPRURL = "pr_url"
	// MetaPRState is PRStateOpen until the watcher sees the pull request
	// end, then PRStateMerged or PRStateClosed.
	MetaPRState = "pr_state"
	// MetaMergeCommit is the commit a merged pull request produced on its
	// base branch; for a squash or rebase merge it is not on the crew's
	// branch at all.
	MetaMergeCommit = "merge_commit"
	// MetaPRSync says what the watcher did to the primary checkout after
	// a merge: `fast-forwarded`, or `skipped: <why>`.
	MetaPRSync = "pr_sync"
)

// The values of MetaPRState.
const (
	PRStateOpen   = "open"
	PRStateMerged = "merged"
	PRStateClosed = "closed"
)

// Declaration is everything the displayed state is resolved from.
type Declaration struct {
	// Meta is `crews/<id>.meta`, read whole. Only state= and stopped_at=
	// are consulted.
	Meta map[string]string
	// OpenIncident is true when the observer has an unresolved incident for
	// this crew in `incidents.log` (box.OpenIncidents is non-empty). The
	// observer never writes `.status`, so this is the only way `blocked`
	// can be reached.
	OpenIncident bool
	// LastVerb is the verb of the crew's most recent recognised `.status`
	// line, VerbNone when it has written none.
	LastVerb StatusVerb
}

// Declare resolves the displayed state in the fixed order of mvp.md
// section 4b:
//
//  1. `.meta` carries state=finished or state=failed: that, and nothing
//     later can override it - a closed crew stays closed.
//  2. Backward compatibility: `.meta` carries stopped_at= and no state=.
//     Only `crew stop` ever wrote that key, so the crew is finished.
//  3. The observer has an open incident: blocked.
//  4. The last verb in `.status`: working, needs-decision or wait-mate.
//  5. Nothing at all: spawned.
//
// A `.meta` carrying state=spawned adds nothing to step 5 and is not read
// as an override: a crew that has since written `working:` is working.
func Declare(d Declaration) State {
	switch State(strings.TrimSpace(d.Meta[MetaState])) {
	case StateFinished:
		return StateFinished
	case StateFailed:
		return StateFailed
	}
	if strings.TrimSpace(d.Meta[MetaState]) == "" && strings.TrimSpace(d.Meta[MetaStoppedAt]) != "" {
		return StateFinished
	}
	if d.OpenIncident {
		return StateBlocked
	}
	switch d.LastVerb {
	case VerbWorking:
		return StateWorking
	case VerbNeedsDecision:
		return StateNeedsDecision
	case VerbWaitMate:
		return StateWaitMate
	}
	return StateSpawned
}

// Composer is what a caller's composer classifier said about the crew's
// pane. It mirrors internal/send's four values without importing it.
type Composer string

const (
	ComposerBusy    Composer = "busy"
	ComposerEmpty   Composer = "empty"
	ComposerPending Composer = "pending"
	ComposerUnknown Composer = "unknown"
)

// HealthKind is the one-word observation beside the state. It is never a
// state: it says what the runtime looks like right now, and a crew can be
// `wait-mate` with a busy pane or `working` with an idle one without either
// column being wrong (mvp.md section 4b, and decision 8 - Herdr's own
// agent_status is not allowed to fill both columns).
type HealthKind string

const (
	// HealthBusy: the composer is mid-turn.
	HealthBusy HealthKind = "busy"
	// HealthIdle: the composer is empty and waiting for a line.
	HealthIdle HealthKind = "idle"
	// HealthPending: unsubmitted text is sitting in the composer, so
	// somebody is typing there.
	HealthPending HealthKind = "pending"
	// HealthUnrecognised: nothing on screen looks like a composer - a
	// dialog, a scrolled transcript, a harness still starting.
	HealthUnrecognised HealthKind = "unrecognised"
	// HealthAgentGone: the meta names an agent Herdr no longer has.
	HealthAgentGone HealthKind = "agent-gone"
	// HealthNoAgent: the meta names no agent at all - nothing was ever
	// started, or `crew stop` cleared it.
	HealthNoAgent HealthKind = "no-agent"
)

// Observation is what the caller saw of the runtime.
type Observation struct {
	// AgentRecorded is whether `.meta` names an agent.
	AgentRecorded bool
	// AgentFound is whether Herdr still has that agent. Only meaningful
	// when AgentRecorded is true.
	AgentFound bool
	// Composer is the pane's classification. Only meaningful when
	// AgentFound is true.
	Composer Composer
	// Evidence is the line the classifier recognised, carried through so
	// the health column can show what it was read from.
	Evidence string
}

// Health is the observation half of the `mate state` line.
type Health struct {
	Kind   HealthKind
	Detail string
}

func (h Health) String() string {
	if h.Detail == "" {
		return string(h.Kind)
	}
	return string(h.Kind) + " (" + h.Detail + ")"
}

// Observe turns what the caller saw into one health reading, in order: no
// agent recorded, agent recorded but gone, then the composer.
func Observe(o Observation) Health {
	if !o.AgentRecorded {
		return Health{Kind: HealthNoAgent, Detail: "no agent is recorded for this crew"}
	}
	if !o.AgentFound {
		return Health{Kind: HealthAgentGone, Detail: "the recorded agent is not in herdr"}
	}
	switch o.Composer {
	case ComposerBusy:
		return Health{Kind: HealthBusy, Detail: o.Evidence}
	case ComposerEmpty:
		return Health{Kind: HealthIdle, Detail: "composer empty"}
	case ComposerPending:
		return Health{Kind: HealthPending, Detail: detailOr(o.Evidence, "unsubmitted text in the composer")}
	default:
		return Health{Kind: HealthUnrecognised, Detail: detailOr(o.Evidence, "no composer recognised on screen")}
	}
}

func detailOr(evidence, fallback string) string {
	if strings.TrimSpace(evidence) == "" {
		return fallback
	}
	return evidence
}

// Input is everything Decide needs: the recorded facts behind the state and
// the live facts behind the health.
type Input struct {
	Declaration
	Observation
}

// Result is one verdict: `state: <state> · health: <health>`.
type Result struct {
	State  State
	Health Health
}

// Line renders the format mvp.md section 4b specifies.
func (r Result) Line() string {
	return "state: " + string(r.State) + " · health: " + r.Health.String()
}

// Decide is Declare and Observe together, which is exactly what
// `mate state` prints. The two halves never consult each other: a state
// is what the record says and health is what the pane looks like, and
// letting one correct the other is how the two columns stop meaning
// anything.
func Decide(in Input) Result {
	return Result{State: Declare(in.Declaration), Health: Observe(in.Observation)}
}
