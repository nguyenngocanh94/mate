// Package crewstate decides `matev2 state <crew>`'s one deterministic line
// (docs/mvp.md task 13). Decide is pure: it takes what the CLI already
// gathered from `crews/<id>.meta`, Herdr, send.ClassifyComposer and the
// crew's status file, and returns a verdict with no I/O of its own. That
// keeps the decision table testable without a runtime, a pane, or a
// filesystem, and keeps the CLI's job limited to gathering the four inputs.
package crewstate

import "github.com/nguyenngocanh94/matev2/internal/send"

// State is the crew health `matev2 state` reports.
type State string

const (
	StateWorking State = "working"
	StateParked  State = "parked"
	StateDone    State = "done"
	StateFailed  State = "failed"
	StateStopped State = "stopped"
	StateUnknown State = "unknown"
)

// Source names where a verdict's evidence came from.
type Source string

const (
	SourcePane      Source = "pane"
	SourceStatusLog Source = "status-log"
	SourceMeta      Source = "meta"
	SourceNone      Source = "none"
)

// StatusVerb is the subset of box.State this package reads. It is spelled
// out here rather than importing internal/box, so crewstate stays a leaf
// package a CLI, a console and a test can all depend on without pulling in
// box's file-merging machinery; the CLI parses the raw status line with
// box.ParseStatus and hands over just the verb and text this table needs.
type StatusVerb string

const (
	VerbWorking       StatusVerb = "working"
	VerbNeedsDecision StatusVerb = "needs-decision"
	VerbBlocked       StatusVerb = "blocked"
	VerbDone          StatusVerb = "done"
	VerbFailed        StatusVerb = "failed"
	VerbUnknown       StatusVerb = "unknown"
	VerbNone          StatusVerb = "" // no status line at all
)

// Status is the crew's last `crews/<id>.status` line, already parsed.
type Status struct {
	Verb StatusVerb
	Text string
}

// Input is everything Decide needs, gathered by the CLI:
//   - Meta: `crews/<id>.meta`, read whole. A nil/empty map is "no meta at
//     all"; a non-empty map missing "agent" is a stopped crew.
//   - AgentFound: whether Herdr's own inventory (`agent get`) still knows
//     the agent meta named. Only meaningful when Meta carries an agent.
//   - Composer: send.ClassifyComposer's verdict on the crew's pane. Only
//     meaningful when AgentFound is true.
//   - Status: the crew's last status line. IsZero (Verb == VerbNone) means
//     the crew has never written one.
type Input struct {
	Meta       map[string]string
	AgentFound bool
	Composer   send.Classification
	Status     Status
}

// Result is one verdict: `state: <State> · source: <Source> · <Detail>`.
type Result struct {
	State  State
	Source Source
	Detail string
}

// Line renders the firstmate-derived one-line format docs/mvp.md task 13
// specifies.
func (r Result) Line() string {
	line := "state: " + string(r.State) + " · source: " + string(r.Source)
	if r.Detail != "" {
		line += " · " + r.Detail
	}
	return line
}

// Decide runs the task 13 decision table, in order:
//
//  1. No meta at all: unknown · none.
//  2. Meta recorded but carries no agent (a stopped crew): stopped · meta,
//     detail stopped_at=... .
//  3. Herdr cannot find the recorded agent: unknown · meta, agent recorded
//     but absent.
//  4. The pane classifies Busy: working · pane, the busy evidence.
//  5. The last status verb is done or failed: that state · status-log, the
//     status text.
//  6. The last status verb is needs-decision or blocked (and the pane is
//     not busy, already excluded by 4): parked · status-log, the status
//     text.
//  7. Otherwise: working · pane · idle composer when the composer reads
//     Empty, else unknown · pane, whatever composer evidence there is.
func Decide(in Input) Result {
	if len(in.Meta) == 0 {
		return Result{State: StateUnknown, Source: SourceNone, Detail: "no crew metadata"}
	}
	agent := in.Meta["agent"]
	if agent == "" {
		detail := "stopped_at=" + in.Meta["stopped_at"]
		return Result{State: StateStopped, Source: SourceMeta, Detail: detail}
	}
	if !in.AgentFound {
		return Result{State: StateUnknown, Source: SourceMeta, Detail: "agent recorded but absent"}
	}
	if in.Composer.State == send.StateBusy {
		return Result{State: StateWorking, Source: SourcePane, Detail: in.Composer.Evidence}
	}
	switch in.Status.Verb {
	case VerbDone:
		return Result{State: StateDone, Source: SourceStatusLog, Detail: in.Status.Text}
	case VerbFailed:
		return Result{State: StateFailed, Source: SourceStatusLog, Detail: in.Status.Text}
	case VerbNeedsDecision, VerbBlocked:
		return Result{State: StateParked, Source: SourceStatusLog, Detail: in.Status.Text}
	}
	if in.Composer.State == send.StateEmpty {
		return Result{State: StateWorking, Source: SourcePane, Detail: "idle composer"}
	}
	detail := in.Composer.Evidence
	if detail == "" {
		detail = "no composer recognised on screen"
	}
	return Result{State: StateUnknown, Source: SourcePane, Detail: detail}
}
