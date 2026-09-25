package console

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// stageSpy records every StageFunc call and answers with err.
type stageSpy struct {
	calls []StageTarget
	err   error
}

func (s *stageSpy) fn(_ context.Context, target StageTarget) error {
	s.calls = append(s.calls, target)
	return s.err
}

func TestEnterOnMateStagesItAndStaysOnTheTree(t *testing.T) {
	spy := &stageSpy{}
	m := projectFrame(t, sampleTree()).WithStage(spy.fn)
	before := len(m.stack)
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter on the Mate returned no Cmd")
	}
	m, _ = send(t, m, cmd())
	if len(spy.calls) != 1 {
		t.Fatalf("stage calls = %d, want 1", len(spy.calls))
	}
	mate := sampleTree().Projects[0].Mate
	if got := spy.calls[0]; got.Kind != StageMate || got.ID != mate.Designated.Value.MateID || got.AgentName != mate.AgentName.Value {
		t.Fatalf("stage target = %+v", got)
	}
	if len(m.stack) != before {
		t.Fatalf("stack changed across a stage: %d -> %d", before, len(m.stack))
	}
	if !strings.Contains(m.msg.text, "in the next pane") || m.msg.tone != toneOK {
		t.Fatalf("message = %+v, want the stage confirmation", m.msg)
	}
}

func TestEnterOnCrewStagesItByCrewID(t *testing.T) {
	spy := &stageSpy{}
	m := toRunningAttempt(t, loaded(t, sampleTree(), nil)).WithStage(spy.fn)
	_, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter on the Crew returned no Cmd")
	}
	cmd()
	if want := sampleTree().Projects[0].Crews[1].CrewID; len(spy.calls) != 1 || spy.calls[0].ID != want || spy.calls[0].Kind != StageCrew {
		t.Fatalf("stage calls = %+v, want one crew %s", spy.calls, want)
	}
}

func TestAStageFailureIsSaidOnTheMessageLine(t *testing.T) {
	spy := &stageSpy{err: errors.New("the pane to the right is not mate's stage")}
	m := projectFrame(t, sampleTree()).WithStage(spy.fn)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd())
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "not mate's stage") {
		t.Fatalf("message = %+v, want the host's refusal", m.msg)
	}
}

func TestEnterWithoutAHostSaysThereIsNoNextPane(t *testing.T) {
	m := projectFrame(t, sampleTree())
	m, cmd := send(t, m, key("enter"))
	if cmd != nil {
		t.Fatal("Enter without a StageFunc returned a Cmd")
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "no next pane") {
		t.Fatalf("message = %+v, want the no-host line", m.msg)
	}
}

// A stale binding means mate could not confirm the agent stopped (ADR
// 0027): the host is never asked, and the key line says so up front.
func TestAStaleBindingIsRefusedBeforeTheHostIsAsked(t *testing.T) {
	spy := &stageSpy{}
	m := toFailedAttempt(t, loaded(t, sampleTree(), nil)).WithStage(spy.fn)
	if !strings.Contains(renderFrame(t, m), "Show in next pane (unavailable)") {
		t.Fatalf("the key line did not mark the show unavailable:\n%s", renderFrame(t, m))
	}
	m, cmd := send(t, m, key("enter"))
	if cmd != nil || len(spy.calls) != 0 {
		t.Fatalf("a refused show reached the host: cmd=%v calls=%d", cmd != nil, len(spy.calls))
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "stale") {
		t.Fatalf("message = %+v, want a refusal naming the stale binding", m.msg)
	}
}

// A Mate recorded created or stopped has no session; the snapshot is
// enough to know it.
func TestAMateThatIsNotRunningIsRefusedFromTheSnapshotAlone(t *testing.T) {
	for _, tc := range []struct {
		status      query.MateStatus
		wantRefusal bool
	}{
		{query.MateCreated, true},
		{query.MateStopped, true},
		{query.MateRunning, false},
		{query.MateStarting, false},
		{query.MateUnknown, false}, // holds the active slot; the host decides
	} {
		tree := sampleTree()
		tree.Projects[0].Mate.Designated.Value.Status = tc.status
		spy := &stageSpy{}
		m := projectFrame(t, tree).WithStage(spy.fn)
		m, cmd := send(t, m, key("enter"))
		if tc.wantRefusal {
			if cmd != nil {
				t.Errorf("%s: the host was asked", tc.status)
			}
			if !strings.Contains(m.msg.text, string(tc.status)) {
				t.Errorf("%s: refusal = %q, want it to name the recorded status", tc.status, m.msg.text)
			}
			continue
		}
		if cmd == nil {
			t.Errorf("%s: the show was refused", tc.status)
		}
	}
}
