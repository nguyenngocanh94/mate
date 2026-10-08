package console

import (
	"context"
	"errors"
	"slices"
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

// toolSpy records the tool keys the Console hands cmd/mate.
type toolSpy struct {
	keys  []string
	calls []StageTarget
	err   error
}

func (s *toolSpy) fn(_ context.Context, key string, target StageTarget) error {
	s.keys = append(s.keys, key)
	s.calls = append(s.calls, target)
	return s.err
}

func TestEOnACrewOpensItsReportAndEnterDoesNot(t *testing.T) {
	stage := &stageSpy{}
	view := &toolSpy{}
	m := toRunningAttempt(t, loaded(t, sampleTree(), nil)).WithStage(stage.fn).WithToolView(view.fn)
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter on the Crew returned no Cmd")
	}
	m, _ = send(t, m, cmd())
	if len(stage.calls) != 1 || len(view.calls) != 0 {
		t.Fatalf("after Enter, stage=%d view=%d; Enter only shows the agent", len(stage.calls), len(view.calls))
	}
	m, cmd = send(t, m, key("e"))
	if cmd == nil {
		t.Fatal("e on the Crew returned no Cmd")
	}
	if want := "→ report · opening " + stage.calls[0].ID + "…"; m.msg.text != want {
		t.Fatalf("message = %q, want %q", m.msg.text, want)
	}
	m, _ = send(t, m, cmd())
	if len(view.calls) != 1 || view.keys[0] != "e" || view.calls[0].Kind != StageCrew || view.calls[0].ID != stage.calls[0].ID {
		t.Fatalf("tool view calls = %q %+v, want e on the same crew", view.keys, view.calls)
	}
	if len(stage.calls) != 1 {
		t.Fatal("e also asked the stage to show the agent")
	}
	if want := "→ report · " + view.calls[0].ID; m.msg.text != want {
		t.Fatalf("message = %q, want %q", m.msg.text, want)
	}
}

func TestEOnAMateSaysThereIsNoReport(t *testing.T) {
	view := &toolSpy{}
	m := projectFrame(t, sampleTree()).WithToolView(view.fn)
	m, cmd := send(t, m, key("e"))
	if cmd != nil || len(view.calls) != 0 {
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
		t.Fatal("e without a ToolViewFunc returned a Cmd")
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "no next pane") {
		t.Fatalf("message = %+v, want the no-host line", m.msg)
	}
}

// A binary that binds no tool to e has nothing for it to open.
func TestEWithNoToolBoundSaysSo(t *testing.T) {
	tree := sampleTree()
	tree.Tools = nil
	view := &toolSpy{}
	m := toRunningAttempt(t, loaded(t, tree, nil)).WithToolView(view.fn)
	m, cmd := send(t, m, key("e"))
	if cmd != nil || len(view.calls) != 0 {
		t.Fatal("e with no tool bound opened something")
	}
	if m.msg.tone != toneError || m.msg.text != "no tool bound to e" {
		t.Fatalf("message = %+v, want no tool bound to e", m.msg)
	}
}

// The tool's own label is what the status line says, and what the key
// sheet lists, whatever the tool is.
func TestToolKeyWordsComeFromTheSnapshot(t *testing.T) {
	tree := sampleTree()
	tree.Tools = []query.ToolBinding{{Key: "e", Label: "diff", Scope: "crew", Role: "diff", Tool: "differ"}}
	view := &toolSpy{}
	m := toRunningAttempt(t, loaded(t, tree, nil)).WithToolView(view.fn)
	m, cmd := send(t, m, key("e"))
	if cmd == nil || !strings.HasPrefix(m.msg.text, "→ diff · opening") {
		t.Fatalf("message = %q, want the tool's label", m.msg.text)
	}
	rows := m.keyRows()
	if !slices.Contains(rows, [2]string{"e", "crew diff"}) || slices.Contains(rows, [2]string{"e", "crew report"}) {
		t.Fatalf("key rows = %q, want e drawn from the snapshot", rows)
	}
	tree.Tools = nil
	if rows := loaded(t, tree, nil).keyRows(); slices.ContainsFunc(rows, func(r [2]string) bool { return r[0] == "e" }) {
		t.Fatalf("key rows = %q, want no e row when no tool binds it", rows)
	}
}

// t bound to a tool on a project row opens that tool, not the tasks seam.
// Unbound, t is TasksFunc's, as before.
func TestTBoundToAToolOpensItOnTheProject(t *testing.T) {
	tree := sampleTree()
	tree.Tools = append(tree.Tools, query.ToolBinding{Key: "t", Label: "plan", Scope: "project", Role: "plan", Tool: "planner"})
	view := &toolSpy{}
	m := loaded(t, tree, nil).WithToolView(view.fn).
		WithTasks(func(context.Context, string) error { t.Fatal("t went to the tasks seam"); return nil })
	want := m.modeTarget()
	m, cmd := send(t, m, key("t"))
	if cmd == nil {
		t.Fatalf("t returned no Cmd: %+v", m.msg)
	}
	m, _ = send(t, m, cmd())
	if len(view.calls) != 1 || view.keys[0] != "t" || view.calls[0].ProjectID != want || view.calls[0].ID != "" {
		t.Fatalf("tool view calls = %q %+v, want t on project %s", view.keys, view.calls, want)
	}
	if m.msg.text != "→ plan · "+want {
		t.Fatalf("message = %q", m.msg.text)
	}
	rows := m.keyRows()
	if !slices.Contains(rows, [2]string{"t", "project plan"}) || slices.Contains(rows, [2]string{"t", "project tasks"}) {
		t.Fatalf("key rows = %q, want t drawn from the snapshot", rows)
	}
}

func TestEnterWithoutAHostGivesTheHint(t *testing.T) {
	m := projectFrame(t, sampleTree()).WithNoHostHint("over ssh, do this")
	m, _ = send(t, m, key("enter"))
	if m.msg.text != "no next pane: over ssh, do this" {
		t.Fatalf("message = %q, want the hint after the no-host line", m.msg.text)
	}
}
