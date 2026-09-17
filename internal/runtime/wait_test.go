package runtime

import (
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/observability"
)

func TestDefaultWaitTreatsBlockedAsSuccess(t *testing.T) {
	t.Parallel()
	w := DefaultWait(2 * time.Second)
	if !w.Matches(AgentBlocked) {
		t.Fatal("default wait must match blocked (Herdr wait --until blocked exits 0)")
	}
	if !w.BlockedIsSuccess() {
		t.Fatal("blocked matching the wait is not an error")
	}
	if !w.Matches(AgentIdle) || !w.Matches(AgentDone) {
		t.Fatal("default wait matches idle|done|blocked")
	}
	if w.Matches(AgentWorking) {
		t.Fatal("working is not a default terminal wait")
	}
}

func TestCheckWaitTimeoutRefusesNonPositive(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{0, -time.Second} {
		err := CheckWaitTimeout(d)
		if err == nil {
			t.Fatalf("timeout %v must be refused; Herdr wait without --timeout waits forever", d)
		}
		if observability.ExitCode(err) != observability.ExitUsage {
			t.Fatalf("timeout %v: err = %v", d, err)
		}
	}
	if err := CheckWaitTimeout(time.Millisecond); err != nil {
		t.Fatalf("positive timeout is valid: %v", err)
	}
}

func TestClassifyObservationDistinguishesReadyBlockedAndFailure(t *testing.T) {
	t.Parallel()
	idle := ClassifyObservation(ObservedAgent{Status: AgentIdle, LiveHandleOK: true})
	if idle.Kind != ReadinessReady {
		t.Fatalf("idle = %+v, want ready", idle)
	}
	done := ClassifyObservation(ObservedAgent{Status: AgentDone, LiveHandleOK: true})
	if done.Kind != ReadinessReady {
		t.Fatalf("done is settled-ready, got %+v", done)
	}
	blocked := ClassifyObservation(ObservedAgent{Status: AgentBlocked, LiveHandleOK: true, LaunchPending: true})
	if blocked.Kind != ReadinessBlocked {
		t.Fatalf("blocked = %+v, want blocked-as-normal", blocked)
	}
	if blocked.Err != nil {
		t.Fatalf("blocked is not an error: %v", blocked.Err)
	}
	working := ClassifyObservation(ObservedAgent{Status: AgentWorking, LiveHandleOK: true})
	if working.Kind != ReadinessPending {
		t.Fatalf("working = %+v", working)
	}
	unknown := ClassifyObservation(ObservedAgent{Status: AgentUnknown, LiveHandleOK: true})
	if unknown.Kind != ReadinessUnknown {
		t.Fatalf("unknown must not be treated as ready or failed-for-cleanup, got %+v", unknown)
	}
	stale := ClassifyObservation(ObservedAgent{Status: AgentIdle, LiveHandleOK: false})
	if stale.Kind != ReadinessFailed {
		t.Fatalf("stale handle = %+v, want failed (not ready from a missing pane)", stale)
	}
}

func TestClassifyObservationIgnoresFocus(t *testing.T) {
	t.Parallel()
	// focused is UI state (ADR 0007). parseAgentInfo does not copy it onto
	// ObservedAgent; Classify must not grow a focus-based branch.
	focusedJSON := []byte(`{"id":"cli:agent:get","result":{"agent":{"agent":"claude","agent_status":"blocked","focused":true,"launch_pending":true,"name":"mate-g4-01","pane_id":"w1:p2","tab_id":"w1:t2","workspace_id":"w1"},"type":"agent_info"}}`)
	unfocusedJSON := []byte(`{"id":"cli:agent:get","result":{"agent":{"agent":"claude","agent_status":"blocked","focused":false,"launch_pending":true,"name":"mate-g4-01","pane_id":"w1:p2","tab_id":"w1:t2","workspace_id":"w1"},"type":"agent_info"}}`)
	a, err := parseAgentInfo(focusedJSON)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseAgentInfo(unfocusedJSON)
	if err != nil {
		t.Fatal(err)
	}
	ra := ClassifyObservation(ObservedAgent{Status: a.Status, LaunchPending: a.LaunchPending, LiveHandleOK: true})
	rb := ClassifyObservation(ObservedAgent{Status: b.Status, LaunchPending: b.LaunchPending, LiveHandleOK: true})
	if ra.Kind != ReadinessBlocked || rb.Kind != ReadinessBlocked {
		t.Fatalf("focus must not change readiness: focused=%+v unfocused=%+v", ra, rb)
	}
	if a.Status != b.Status {
		t.Fatalf("parser used focused to invent a status: %#v vs %#v", a, b)
	}
}

func TestWaitUntilBlockedDoesNotMatchIdle(t *testing.T) {
	t.Parallel()
	w := WaitCondition{Until: []AgentStatus{AgentBlocked}}
	if !w.Matches(AgentBlocked) {
		t.Fatal("until blocked must match blocked")
	}
	if w.Matches(AgentIdle) || w.Matches(AgentDone) {
		t.Fatal("until blocked must not treat idle/done as success")
	}
}

func TestDoneIsDistinctFromIdle(t *testing.T) {
	t.Parallel()
	if AgentDone == AgentIdle {
		t.Fatal("done is unseen-idle, not the same status as idle")
	}
	idleOnly := WaitCondition{Until: []AgentStatus{AgentIdle}}
	if idleOnly.Matches(AgentDone) {
		t.Fatal("wait-until idle must not match done")
	}
}
