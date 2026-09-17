package runtime

import (
	"context"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/observability"
)

// AgentStatus is a Herdr-observed agent status. These are adapter facts,
// not Mate/Crew lifecycle states. idle/done never complete a Task.
type AgentStatus string

const (
	AgentIdle    AgentStatus = "idle"
	AgentWorking AgentStatus = "working"
	AgentBlocked AgentStatus = "blocked"
	AgentDone    AgentStatus = "done"
	AgentUnknown AgentStatus = "unknown"
)

// WaitCondition is the contract for RuntimeAdapter.WaitAgent.
//
// Empty Until matches idle|done|blocked — Herdr's default wait without
// --until. done is settled-ready after background work and is distinct
// from idle (seen). A wait that matches blocked is success (G1: `agent
// wait --until blocked` exits 0); callers must not treat every blocked
// observation as target_blocked.
type WaitCondition struct {
	Until   []AgentStatus
	Timeout time.Duration
}

// DefaultWait is idle|done|blocked.
func DefaultWait(timeout time.Duration) WaitCondition {
	return WaitCondition{
		Until:   []AgentStatus{AgentIdle, AgentDone, AgentBlocked},
		Timeout: timeout,
	}
}

// Matches reports whether observed satisfies the wait.
func (c WaitCondition) Matches(observed AgentStatus) bool {
	until := c.Until
	if len(until) == 0 {
		until = []AgentStatus{AgentIdle, AgentDone, AgentBlocked}
	}
	for _, want := range until {
		if observed == want {
			return true
		}
	}
	return false
}

// BlockedIsSuccess reports whether a blocked observation fulfills this wait.
func (c WaitCondition) BlockedIsSuccess() bool {
	return c.Matches(AgentBlocked)
}

// CheckWaitTimeout refuses a non-positive timeout. Herdr `agent wait`
// without --timeout waits forever; zero must not mean that.
func CheckWaitTimeout(timeout time.Duration) error {
	if timeout <= 0 || timeout.Milliseconds() <= 0 {
		return observability.NewError(
			observability.CodeUsage,
			"agent wait requires a positive timeout in whole milliseconds; omitting --timeout makes Herdr wait forever",
		)
	}
	return nil
}

// ReadinessKind is a classified observation. It is not a domain Mate/Crew
// lifecycle state and it is never derived from focus or active_tab_id.
type ReadinessKind string

const (
	// ReadinessReady is idle or done with a live handle. done is
	// settled-ready after background work (ADR 0003), distinct from idle.
	ReadinessReady ReadinessKind = "ready"
	// ReadinessBlocked is a matching wait condition, not an error.
	// `agent wait --until blocked` exits 0.
	ReadinessBlocked ReadinessKind = "blocked"
	// ReadinessPending is working: not yet ready, not a failure.
	ReadinessPending ReadinessKind = "pending"
	// ReadinessUnknown is Herdr's unknown status. Do not treat it as ready
	// and do not clean up (G0: unknown is not a cleanup signal).
	ReadinessUnknown ReadinessKind = "unknown"
	// ReadinessFailed is a stale live handle or a wait/inspect error.
	ReadinessFailed ReadinessKind = "failed"
)

// Readiness is ClassifyObservation's answer to "is this agent ready".
// Blocked is not stored as Err: a blocked observation is success.
type Readiness struct {
	Kind     ReadinessKind
	Status   AgentStatus
	Observed ObservedAgent
	Err      error
}

// ClassifyObservation maps a live observation onto ready / blocked-as-normal
// / pending / unknown / failed. It uses Status and LiveHandleOK only.
func ClassifyObservation(obs ObservedAgent) Readiness {
	r := Readiness{Status: obs.Status, Observed: obs}
	if !obs.LiveHandleOK {
		r.Kind = ReadinessFailed
		r.Err = observability.NewError(
			observability.CodeNeedsRepair,
			"live tab/pane handle does not match the observation; re-check before treating this agent as ready",
		)
		return r
	}
	switch obs.Status {
	case AgentIdle, AgentDone:
		r.Kind = ReadinessReady
	case AgentBlocked:
		r.Kind = ReadinessBlocked
	case AgentWorking:
		r.Kind = ReadinessPending
	default:
		r.Kind = ReadinessUnknown
	}
	return r
}

// AwaitReadiness waits until idle|done|blocked with an explicit timeout and
// classifies the observation. A blocked match is success (Kind=blocked,
// Err=nil). Timeout and not_found are Kind=failed with the adapter error.
func AwaitReadiness(ctx context.Context, a Adapter, handle AgentHandle, timeout time.Duration) (Readiness, error) {
	if err := CheckWaitTimeout(timeout); err != nil {
		return Readiness{Kind: ReadinessFailed, Err: err}, err
	}
	obs, err := a.WaitAgent(ctx, handle, DefaultWait(timeout))
	if err != nil {
		return Readiness{Kind: ReadinessFailed, Observed: obs, Err: err}, err
	}
	r := ClassifyObservation(obs)
	if r.Kind == ReadinessFailed {
		return r, r.Err
	}
	return r, nil
}
