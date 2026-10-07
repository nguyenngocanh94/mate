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
	if m.staged.name != "payments-api" || m.staged.err != "" {
		t.Fatalf("staged = %+v, want the payments-api Mate", m.staged)
	}
	if frame := renderFrame(t, m); !strings.Contains(frame, "next pane  "+unicodeGlyphs.Mate+" payments-api") {
		t.Fatalf("the status line does not say what the next pane shows:\n%s", frame)
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

func TestAStageFailureIsSaidOnTheStatusLineAndRRetries(t *testing.T) {
	spy := &stageSpy{err: errors.New("the pane to the right is not mate's stage")}
	m := projectFrame(t, sampleTree()).WithStage(spy.fn)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd())
	if !strings.Contains(m.staged.err, "not mate's stage") {
		t.Fatalf("staged = %+v, want the host's refusal", m.staged)
	}
	if frame := renderFrame(t, m); !strings.Contains(frame, "failed: the pane") || !strings.Contains(frame, "r retry") {
		t.Fatalf("the status line does not carry the failure and r:\n%s", frame)
	}
	spy.err = nil
	m, cmd = send(t, m, key("r"))
	if cmd == nil {
		t.Fatal("r after a failed stage did not retry it")
	}
	m, _ = send(t, m, cmd())
	if len(spy.calls) != 2 || spy.calls[1] != spy.calls[0] || m.staged.err != "" {
		t.Fatalf("retry calls = %+v staged = %+v, want the same target again, now shown", spy.calls, m.staged)
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
	sheet, _ := send(t, m, key("a"))
	if e := sheet.menu[0]; e.kind != entryShow || e.enabled || !strings.Contains(e.reason, "stale") {
		t.Fatalf("the actions sheet does not mark the show unavailable up front: %+v", e)
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

func TestEOnACrewOpensItsReportAndEnterDoesNot(t *testing.T) {
	stage := &stageSpy{}
	review := &stageSpy{}
	m := toRunningAttempt(t, loaded(t, sampleTree(), nil)).WithStage(stage.fn).WithReview(review.fn)
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter on the Crew returned no Cmd")
	}
	m, _ = send(t, m, cmd())
	if len(stage.calls) != 1 || len(review.calls) != 0 {
		t.Fatalf("after Enter, stage=%d review=%d; Enter only shows the agent", len(stage.calls), len(review.calls))
	}
	m, cmd = send(t, m, key("e"))
	if cmd == nil {
		t.Fatal("e on the Crew returned no Cmd")
	}
	m, _ = send(t, m, cmd())
	if len(review.calls) != 1 || review.calls[0].Kind != StageCrew || review.calls[0].ID != stage.calls[0].ID {
		t.Fatalf("review calls = %+v, want the same crew", review.calls)
	}
	if len(stage.calls) != 1 {
		t.Fatal("e also asked the stage to show the agent")
	}
	if !strings.Contains(m.msg.text, "report") || !strings.Contains(m.msg.text, review.calls[0].ID) {
		t.Fatalf("message = %q, want the report status for %s", m.msg.text, review.calls[0].ID)
	}
}

func TestEOnAMateSaysThereIsNoReport(t *testing.T) {
	review := &stageSpy{}
	m := projectFrame(t, sampleTree()).WithReview(review.fn)
	m, cmd := send(t, m, key("e"))
	if cmd != nil || len(review.calls) != 0 {
		t.Fatal("e on a Mate opened a report")
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "e opens a crew's report") {
		t.Fatalf("message = %+v, want the refusal", m.msg)
	}
}

func TestEWithoutAHostSaysThereIsNoNextPane(t *testing.T) {
	m := toRunningAttempt(t, loaded(t, sampleTree(), nil))
	m, cmd := send(t, m, key("e"))
	if cmd != nil {
		t.Fatal("e without a ReviewFunc returned a Cmd")
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "no next pane") {
		t.Fatalf("message = %+v, want the no-host line", m.msg)
	}
}

func TestEnterWithoutAHostGivesTheHint(t *testing.T) {
	m := projectFrame(t, sampleTree()).WithNoHostHint("over ssh, do this")
	m, _ = send(t, m, key("enter"))
	if m.msg.text != "no next pane: over ssh, do this" {
		t.Fatalf("message = %q, want the hint after the no-host line", m.msg.text)
	}
}
