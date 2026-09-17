package console

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The attach lifecycle's tests. What each group is evidence for:
//
//	the hand-over        announce, then exactly one subprocess, then exactly
//	                     one re-read - counted, not asserted in prose
//	coming back          same task, same row, re-found by id across a
//	                     snapshot whose rows moved
//	refused vs failed    three outcomes that must not read alike, plus the
//	                     Mate refusal decided from the snapshot's binding
//	the taxonomy         every published exit of docs/phase1/agent.md, and
//	                     the three results that are not an exit at all
//	the frame            the attach frames under the hostile sweep, and the
//	                     messages inside an 80-column line

// attachSpy counts what the attach flow actually did: every snapshot read,
// every target handed to AttachCmdFunc, and what each of those returned.
// Counting is the point - "re-reads once" is only evidence if a second read
// would fail the test.
type attachSpy struct {
	loads   int
	targets []string
	// tree answers the nth load. n is 1 for the first (Init) load, so a
	// test can hand back a moved or newer snapshot on the return read.
	tree func(n int) (query.Snapshot, error)
	// cmd overrides the subprocess for one target; nil builds a command
	// that exits 0.
	cmd func(target string) *exec.Cmd
}

func (s *attachSpy) load(context.Context) (query.Snapshot, error) {
	s.loads++
	if s.tree == nil {
		return query.Snapshot{AsOf: goldenAsOf}, nil
	}
	return s.tree(s.loads)
}

func (s *attachSpy) attach(target string) *exec.Cmd {
	s.targets = append(s.targets, target)
	if s.cmd == nil {
		return exec.Command("true")
	}
	return s.cmd(target)
}

// sameTree is the usual spy: the same snapshot on every read, with the
// fixtures' pinned clock.
func sameTree(tree query.Snapshot) func(int) (query.Snapshot, error) {
	return func(int) (query.Snapshot, error) {
		tree.AsOf = goldenAsOf
		return tree, nil
	}
}

func attachFixture(t *testing.T, spy *attachSpy, w, h int, g glyphSet) Model {
	t.Helper()
	m := New(spy.load, spy.attach)
	m.g = g
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m, _ = send(t, m, m.Init()())
	return m
}

// toRunningAttempt walks to the second Crew attempt of sampleTree's first
// Task: the one whose binding is recorded active, and so the one row in the
// fixture an attach may be tried on.
func toRunningAttempt(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = send(t, m, key("enter")) // the payments-api Project
	m, _ = send(t, m, key("down"))  // its first Task
	m, _ = send(t, m, key("enter")) // that Task's attempts: the running Crew is first; the failed one sits in Completed
	if r, ok := m.selectedRow(); !ok || r.kind != rowCrew || r.id != sampleTree().Projects[0].Tasks[0].Crews[1].CrewID {
		t.Fatalf("selected row = %+v (ok=%v), want the running Crew attempt", r, ok)
	}
	return m
}

// toFailedAttempt walks to sampleTree's first Crew attempt, which is
// recorded failed and so lives in the Completed group until revealed.
func toFailedAttempt(t *testing.T, m Model) Model {
	t.Helper()
	failedID := sampleTree().Projects[0].Tasks[0].Crews[0].CrewID
	var ok bool
	m, ok = m.jumpToCrew(failedID)
	if !ok {
		t.Fatal("jumpToCrew failed to locate the finished Crew")
	}
	if r, ok := m.selectedRow(); !ok || r.kind != rowCrew || r.id != failedID {
		t.Fatalf("selected row = %+v (ok=%v), want the failed Crew attempt", r, ok)
	}
	return m
}

// stoppedMateTree adds the design's docs-site Project: a Mate recorded
// stopped whose binding was released with it. There is nothing to attach
// to, and the snapshot alone establishes that.
func stoppedMateTree() query.Snapshot {
	tree := sampleTree()
	tree.Projects = append(tree.Projects, query.ProjectNode{
		ProjectID: "proj_01J9M6R2S4T6U8V0W2X4Y6Z8AB",
		Name:      "docs-site",
		Mate: query.MateNode{
			Designated: query.KnownField(query.MateIdentity{
				MateID: "mate_01J9M7S3T5U7V9W1X3Y5Z7A9BC", HarnessKind: query.HarnessClaude,
				Status: query.MateStopped, IsDefault: true,
			}),
			AgentName: query.KnownField("mate-docs-site"),
			Binding:   query.AbsentField[query.BindingValue]("the Mate was stopped and its binding released"),
			LastEvent: query.AbsentField[query.EventValue]("no event recorded for this Mate"),
			Error:     query.AbsentField[query.ErrorReason](notErrorState),
		},
	})
	return tree
}

// exitErr is a real *exec.ExitError with the given exit code: the taxonomy
// classifies what the subprocess did, so the test has to hand it what a
// subprocess actually produces rather than a hand-rolled error type.
func exitErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("sh -c 'exit %d' produced %T (%v), want *exec.ExitError", code, err, err)
	}
	if ee.ExitCode() != code {
		t.Fatalf("exit code = %d, want %d", ee.ExitCode(), code)
	}
	return err
}

// ---------- the hand-over ----------

// TestAttachAnnouncesHandsOverAndReReadsExactlyOnce is the acceptance
// criterion, counted end to end: the announcement is on the frame before
// anything is executed, one subprocess is built for the selected row, and
// the return re-reads the snapshot once - not twice, and not once per
// message in the flow.
func TestAttachAnnouncesHandsOverAndReReadsExactlyOnce(t *testing.T) {
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := attachFixture(t, spy, 120, 36, unicodeGlyphs)
	if spy.loads != 1 {
		t.Fatalf("loads after the first read = %d, want 1", spy.loads)
	}
	m = toRunningAttempt(t, m)

	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatalf("Enter on an attachable row returned no Cmd")
	}
	if want := sampleTree().Projects[0].Tasks[0].Crews[1].CrewID; len(spy.targets) != 1 || spy.targets[0] != want {
		t.Fatalf("targets = %v, want exactly one attach of %q", spy.targets, want)
	}
	if spy.loads != 1 {
		t.Fatalf("announcing an attach read the snapshot again (loads=%d)", spy.loads)
	}
	view := renderFrame(t, m)
	for _, want := range []string{"Attaching to crew-payments-api-2", "via mate attach", "detach: Ctrl+b then q"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the announcement frame does not say %q:\n%s", want, view)
		}
	}

	// The hand-over itself reads nothing: the Console is going quiet, not
	// refreshing.
	m, handCmd := send(t, m, AttachHandedOverMsg{})
	if handCmd != nil {
		t.Fatalf("the hand-over returned a Cmd; it must not read or execute anything")
	}
	if spy.loads != 1 {
		t.Fatalf("the hand-over read the snapshot (loads=%d)", spy.loads)
	}
	if m.att.phase != attachHeld {
		t.Fatalf("phase after the hand-over = %v, want attachHeld", m.att.phase)
	}

	// Coming back: exactly one read, and nothing after it.
	m, doneCmd := send(t, m, AttachFinishedMsg{})
	if doneCmd == nil {
		t.Fatalf("returning from an attach must re-read the snapshot")
	}
	msg := doneCmd()
	if _, ok := msg.(treeLoadedMsg); !ok {
		t.Fatalf("the return Cmd produced %T, want a snapshot read", msg)
	}
	if spy.loads != 2 {
		t.Fatalf("loads after the return = %d, want exactly 2 (the first read plus one re-read)", spy.loads)
	}
	m, afterCmd := send(t, m, msg)
	if afterCmd != nil {
		t.Fatalf("applying the re-read returned another Cmd (%T); the return re-reads once", afterCmd())
	}
	if spy.loads != 2 {
		t.Fatalf("loads after applying the re-read = %d, want 2", spy.loads)
	}
	if len(spy.targets) != 1 {
		t.Fatalf("targets = %v, want the one attach the keystroke asked for", spy.targets)
	}
	if m.att.phase != attachIdle || m.att.ret != returnNone {
		t.Fatalf("attach flow after the return = %+v, want it closed", m.att)
	}
}

// TestTheAttachCmdIsTheAnnouncementThenTheSubprocess proves the announced
// frame is a real state in production and not something only the tests
// drive: the Cmd Enter returns is a sequence whose first message is the
// hand-over and whose second is Bubble Tea's own exec, in that order. With
// tea.Batch the two would race and the reader could lose the last frame
// before the terminal leaves.
func TestTheAttachCmdIsTheAnnouncementThenTheSubprocess(t *testing.T) {
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := toRunningAttempt(t, attachFixture(t, spy, 120, 36, unicodeGlyphs))
	_, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatalf("Enter returned no Cmd")
	}
	msg := cmd()
	// The concrete type is the ordering guarantee: tea.Batch's own message
	// is also a []tea.Cmd, and Bubble Tea runs its elements concurrently
	// (tea.go's batchMsg branch) while a sequenceMsg is sent one at a time,
	// in order. Asserting the shape alone would pass for Batch and lose the
	// last frame before the terminal leaves, so the name is asserted too -
	// if a future Bubble Tea renames it, this fails loudly rather than
	// silently accepting a race.
	if got := fmt.Sprintf("%T", msg); got != "tea.sequenceMsg" {
		t.Fatalf("attach Cmd produced %s, want tea.sequenceMsg (Batch would race the announcement)", got)
	}
	seq := reflect.ValueOf(msg)
	if seq.Kind() != reflect.Slice || seq.Len() != 2 {
		t.Fatalf("attach Cmd produced %T (kind %v), want a two-element tea.Sequence", msg, seq.Kind())
	}
	first, ok := seq.Index(0).Interface().(tea.Cmd)
	if !ok {
		t.Fatalf("sequence element 0 is %T, want tea.Cmd", seq.Index(0).Interface())
	}
	if _, ok := first().(AttachHandedOverMsg); !ok {
		t.Fatalf("the first element produced %T, want AttachHandedOverMsg", first())
	}
	second, ok := seq.Index(1).Interface().(tea.Cmd)
	if !ok {
		t.Fatalf("sequence element 1 is %T, want tea.Cmd", seq.Index(1).Interface())
	}
	// Calling it only builds Bubble Tea's exec message; the process runs
	// when the program handles it, not here.
	if got := fmt.Sprintf("%T", second()); !strings.Contains(got, "execMsg") {
		t.Fatalf("the second element produced %s, want Bubble Tea's exec message", got)
	}
}

// TestDetachSaysTheAgentWasNotStoppedAndNamesTheNewSnapshot: detaching is
// Ctrl+b then q, which leaves the agent running (ADR 0010), so the message
// says so - and it names the time of the snapshot that is now on screen,
// only once that read has actually landed.
func TestDetachSaysTheAgentWasNotStoppedAndNamesTheNewSnapshot(t *testing.T) {
	later := goldenAsOf.Add(3*time.Minute + time.Second) // 14:05:12
	spy := &attachSpy{tree: func(n int) (query.Snapshot, error) {
		tree := sampleTree()
		tree.AsOf = goldenAsOf
		if n > 1 {
			tree.AsOf = later
		}
		return tree, nil
	}}
	m := toRunningAttempt(t, attachFixture(t, spy, 120, 36, unicodeGlyphs))
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, AttachHandedOverMsg{})
	m, cmd := send(t, m, AttachFinishedMsg{})

	if m.msg.tone != toneOK || !strings.Contains(m.msg.text, "agent not stopped") {
		t.Fatalf("message while the re-read is in flight = %+v, want a detach that says the agent was not stopped", m.msg)
	}
	if strings.Contains(m.msg.text, later.Format("15:04:05")) {
		t.Fatalf("message = %q, want no snapshot time before the re-read has landed", m.msg.text)
	}

	m, _ = send(t, m, cmd())
	view := renderFrame(t, m)
	for _, want := range []string{
		"Detached from crew-payments-api-2",
		"agent not stopped",
		"re-read " + later.Format("15:04:05"),
		"As of " + later.Format("15:04:05"),
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the frame after a detach does not say %q:\n%s", want, view)
		}
	}
}

// TestADetachWhoseReReadFailsKeepsBothFacts: the detach happened and the
// re-read did not. The standard failed-refresh wording would drop the first
// half, and the snapshot on screen would be the pre-attach one with nothing
// saying so.
func TestADetachWhoseReReadFailsKeepsBothFacts(t *testing.T) {
	spy := &attachSpy{tree: func(n int) (query.Snapshot, error) {
		if n > 1 {
			return query.Snapshot{}, errFake("database is locked")
		}
		tree := sampleTree()
		tree.AsOf = goldenAsOf
		return tree, nil
	}}
	m := toRunningAttempt(t, attachFixture(t, spy, 120, 36, unicodeGlyphs))
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, AttachHandedOverMsg{})
	m, cmd := send(t, m, AttachFinishedMsg{})
	m, _ = send(t, m, cmd())

	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want the earlier snapshot still on screen", m.phase)
	}
	for _, want := range []string{"Detached from crew-payments-api-2", "agent not stopped", "re-read failed"} {
		if !strings.Contains(m.msg.text, want) {
			t.Fatalf("message = %q, want it to say %q", m.msg.text, want)
		}
	}
	if !strings.Contains(renderFrame(t, m), "As of "+goldenAsOf.Format("15:04:05")) {
		t.Fatalf("the header must still say how old the snapshot on screen is")
	}
}

// TestAFailureWhoseReReadFailsKeepsTheCause: the same rule on the other
// path. The attach failure is the fact the reader needs first, so it stays
// on the line - cause first, code after - and the failed re-read is named
// beside it. The standard refresh wording would replace the taxonomy with
// "Refresh failed" and the reason the attach broke would be gone. The exit
// number the code was derived from is not dropped, only moved: the one-line
// summary cannot hold both, and the 'e' detail view carries it.
func TestAFailureWhoseReReadFailsKeepsTheCause(t *testing.T) {
	spy := &attachSpy{tree: func(n int) (query.Snapshot, error) {
		if n > 1 {
			return query.Snapshot{}, errFake("database is locked")
		}
		tree := sampleTree()
		tree.AsOf = goldenAsOf
		return tree, nil
	}}
	m := toRunningAttempt(t, attachFixture(t, spy, 120, 36, unicodeGlyphs))
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, AttachHandedOverMsg{})
	m, cmd := send(t, m, AttachFinishedMsg{Err: exitErr(t, observability.ExitRuntimeUnavailable)})
	m, _ = send(t, m, cmd())

	for _, want := range []string{"Attach failed", "the runtime is not reachable", "runtime_unavailable", "the re-read failed too"} {
		if !strings.Contains(m.msg.text, want) {
			t.Fatalf("message = %q, want it to say %q", m.msg.text, want)
		}
	}
	if strings.Contains(m.msg.text, "snapshot re-read") {
		t.Fatalf("message = %q, must not claim a re-read that failed", m.msg.text)
	}
	if m.phase != phaseReady || !strings.Contains(renderFrame(t, m), "As of "+goldenAsOf.Format("15:04:05")) {
		t.Fatalf("the frame must still show the snapshot it has, and say how old it is")
	}
	// The full evidence is one keystroke away, and it is what makes the
	// abbreviation on the line safe: the code the line shows was read off the
	// exit, and the detail view says so and names the exit.
	m, _ = send(t, m, key("e"))
	if !m.failureDetail {
		t.Fatalf("e must open the failure detail view while a failure is recorded")
	}
	detail := renderFrame(t, m)
	for _, want := range []string{"Session open failed", "runtime_unavailable (derived from exit 20)", "mate attach exit 20", "Exit"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail view must say %q, got:\n%s", want, detail)
		}
	}
}

// ---------- coming back ----------

// TestTheReturnKeepsTheTaskAndTheRowByIdAcrossAMovedSnapshot: the re-read
// after a hand-over is a refresh like any other, so it must re-find the
// selection by identity. Here the returning snapshot has a new attempt
// inserted ahead of the selected one and a new Project ahead of the
// current one, which moves every index the pre-attach frame held.
func TestTheReturnKeepsTheTaskAndTheRowByIdAcrossAMovedSnapshot(t *testing.T) {
	selected := sampleTree().Projects[0].Tasks[0].Crews[1]
	moved := func() query.Snapshot {
		tree := sampleTree()
		// A Project inserted at the front moves the Project index; a Crew
		// inserted at the front of the Task moves the row index.
		tree.Projects = append([]query.ProjectNode{{
			ProjectID: "proj_01J9M9ZZZZZZZZZZZZZZZZZZZZ",
			Name:      "inserted-first",
			Mate:      absentMate("this project has no designated Mate"),
		}}, tree.Projects...)
		crews := tree.Projects[1].Tasks[0].Crews
		inserted := crews[1]
		inserted.CrewID = "crew_01J9PZZZZZZZZZZZZZZZZZZZZZ"
		inserted.Attempt = 0
		tree.Projects[1].Tasks[0].Crews = append([]query.CrewNode{inserted}, crews...)
		return tree
	}
	spy := &attachSpy{tree: func(n int) (query.Snapshot, error) {
		if n > 1 {
			tree := moved()
			tree.AsOf = goldenAsOf
			return tree, nil
		}
		tree := sampleTree()
		tree.AsOf = goldenAsOf
		return tree, nil
	}}
	m := toRunningAttempt(t, attachFixture(t, spy, 120, 36, unicodeGlyphs))
	beforeIndex := m.cur().sel
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, AttachHandedOverMsg{})
	m, cmd := send(t, m, AttachFinishedMsg{})
	m, _ = send(t, m, cmd())

	if m.cur().kind != frameTask || m.cur().id != sampleTree().Projects[0].Tasks[0].TaskID {
		t.Fatalf("frame after the return = %+v, want the same Task", m.cur())
	}
	r, ok := m.selectedRow()
	if !ok || r.id != selected.CrewID {
		t.Fatalf("selected row after the return = %+v (ok=%v), want %q", r, ok, selected.CrewID)
	}
	if m.cur().sel == beforeIndex {
		t.Fatalf("the inserted attempt did not move the row index (%d); the test proves nothing", beforeIndex)
	}
	if !strings.Contains(renderFrame(t, m), selected.CrewID) {
		t.Fatalf("the selected attempt is not on the frame after the return")
	}
}

// TestTheAttachLabelTrustsTheFieldStateNotTheValue: Field.State is
// authoritative. query.UnknownField and AbsentField zero the value today,
// so a label that read Value alone would still work by accident - which is
// exactly why this is tested directly: a value carried on a non-Known field
// is not a fact, and naming a session by it would be the "mate:none" class
// of defect (an unreadable field rendered as its own placeholder).
func TestTheAttachLabelTrustsTheFieldStateNotTheValue(t *testing.T) {
	m := attachFixture(t, &attachSpy{tree: sameTree(sampleTree())}, 120, 36, unicodeGlyphs)
	const id = "crew_01J9P6Q6W0E5V8XK2M4B8DT"
	for _, tc := range []struct {
		name  string
		field query.Field[string]
		want  string
	}{
		{"known", query.KnownField("crew-payments-api-2"), "crew-payments-api-2"},
		{"unknown with a leaked value", query.Field[string]{State: query.Unknown, Value: "leaked-name", Reason: "binding lookup failed"}, shortID(id, m.g)},
		{"absent with a leaked value", query.Field[string]{State: query.Absent, Value: "leaked-name", Reason: "no binding"}, shortID(id, m.g)},
		{"unset", query.Field[string]{}, shortID(id, m.g)},
		{"known but empty", query.KnownField(""), shortID(id, m.g)},
	} {
		if got := m.attachLabel(id, tc.field); got != tc.want {
			t.Errorf("%s: label = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ---------- refused, failed, and nothing to attach to ----------

// TestRefusedFailedAndStoppedAreThreeDistinctOutcomes is the acceptance
// criterion that the three do not read alike: a stale binding (refused, no
// subprocess), a runtime failure (tried, exited 20) and a stopped Mate
// (refused, nothing to attach to) each produce a different message, and the
// two refusals never spawn anything.
func TestRefusedFailedAndStoppedAreThreeDistinctOutcomes(t *testing.T) {
	type outcome struct {
		name    string
		frame   string
		spawned int
	}
	var got []outcome

	// 1. Stale binding: attempt 1 of the sample Task (finished, in Completed).
	staleSpy := &attachSpy{tree: sameTree(sampleTree())}
	stale := toFailedAttempt(t, attachFixture(t, staleSpy, 120, 36, unicodeGlyphs))
	stale, staleCmd := send(t, stale, key("enter"))
	if staleCmd != nil {
		t.Fatalf("a stale binding returned a Cmd; the refusal must start nothing")
	}
	got = append(got, outcome{"stale", stale.msg.text, len(staleSpy.targets)})

	// 2. Runtime failure: the binding is recorded active, the subprocess
	//    exits 20.
	failSpy := &attachSpy{tree: sameTree(sampleTree()),
		cmd: func(string) *exec.Cmd { return exec.Command("sh", "-c", "exit 20") }}
	fail := toRunningAttempt(t, attachFixture(t, failSpy, 120, 36, unicodeGlyphs))
	fail, _ = send(t, fail, key("enter"))
	fail, _ = send(t, fail, AttachHandedOverMsg{})
	fail, failCmd := send(t, fail, AttachFinishedMsg{Err: exitErr(t, observability.ExitRuntimeUnavailable)})
	fail, _ = send(t, fail, failCmd())
	got = append(got, outcome{"runtime failure", fail.msg.text, len(failSpy.targets)})

	// 3. Mate recorded stopped.
	stopSpy := &attachSpy{tree: sameTree(stoppedMateTree())}
	stop := attachFixture(t, stopSpy, 120, 36, unicodeGlyphs)
	stop, _ = send(t, stop, key("down"))
	stop, _ = send(t, stop, key("down"))
	stop, _ = send(t, stop, key("enter")) // docs-site; its Mate row is selected
	stop, stopCmd := send(t, stop, key("enter"))
	if stopCmd != nil {
		t.Fatalf("a stopped Mate returned a Cmd; there is nothing to attach to")
	}
	got = append(got, outcome{"stopped mate", stop.msg.text, len(stopSpy.targets)})

	want := []struct {
		name    string
		says    []string
		notSays []string
		spawned int
	}{
		{"stale", []string{"Attach refused", "stale", "stop unconfirmed", "nothing started"}, []string{"mate attach exit", "failed"}, 0},
		{"runtime failure", []string{"Attach failed", "the runtime is not reachable", "runtime_unavailable"}, []string{"refused", "nothing started"}, 1},
		{"stopped mate", []string{"Attach refused", "stopped", "no session to attach", "nothing started"}, []string{"stale", "mate attach exit"}, 0},
	}
	for i, w := range want {
		if got[i].name != w.name {
			t.Fatalf("outcome %d is %q, want %q", i, got[i].name, w.name)
		}
		for _, s := range w.says {
			if !strings.Contains(got[i].frame, s) {
				t.Errorf("%s message = %q, want it to say %q", w.name, got[i].frame, s)
			}
		}
		for _, s := range w.notSays {
			if strings.Contains(got[i].frame, s) {
				t.Errorf("%s message = %q, must not say %q", w.name, got[i].frame, s)
			}
		}
		if got[i].spawned != w.spawned {
			t.Errorf("%s built %d subprocesses, want %d", w.name, got[i].spawned, w.spawned)
		}
	}
	for i := range got {
		for j := i + 1; j < len(got); j++ {
			if got[i].frame == got[j].frame {
				t.Errorf("%s and %s render the same message %q", got[i].name, got[j].name, got[i].frame)
			}
		}
	}
}

// TestTheMateRefusalIsDecidedFromTheSnapshotBinding: the Mate branch reads
// MateNode.Binding exactly as the Crew branch reads CrewNode.Binding. A
// Mate recorded running whose binding is stale, absent or unknown is
// refused here, with no subprocess - not handed to `mate attach` to refuse.
func TestTheMateRefusalIsDecidedFromTheSnapshotBinding(t *testing.T) {
	for _, tc := range []struct {
		name    string
		binding query.Field[query.BindingValue]
		says    string
	}{
		{"stale", query.KnownNote(query.BindingValue{Status: query.BindingStale, AgentName: "mate-payments-api"},
			"mate could not confirm the agent stopped"), "stop unconfirmed"},
		{"absent", query.AbsentField[query.BindingValue]("no binding is held for this Mate"), "no runtime binding recorded"},
		{"unknown", query.UnknownField[query.BindingValue]("binding lookup failed"), "could not be read"},
		{"no status", query.KnownField(query.BindingValue{AgentName: "mate-payments-api"}), "records no status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := sampleTree()
			tree.Projects[0].Mate.Binding = tc.binding
			if !tree.Projects[0].Mate.Designated.IsKnown() ||
				!tree.Projects[0].Mate.Designated.Value.Status.OccupiesActiveSlot() {
				t.Fatalf("the fixture's Mate must be recorded running, or this tests the status branch instead")
			}
			spy := &attachSpy{tree: sameTree(tree)}
			m := attachFixture(t, spy, 120, 36, unicodeGlyphs)
			m, _ = send(t, m, key("enter")) // the Project; its Mate row is selected
			m, cmd := send(t, m, key("enter"))
			if cmd != nil || len(spy.targets) != 0 {
				t.Fatalf("the Mate refusal deferred to the subprocess: cmd=%v targets=%v", cmd != nil, spy.targets)
			}
			if !strings.Contains(m.msg.text, "Attach refused") || !strings.Contains(m.msg.text, tc.says) {
				t.Fatalf("message = %q, want a refusal saying %q", m.msg.text, tc.says)
			}
			if !strings.Contains(renderFrame(t, m), "Attach mate (unavailable)") {
				t.Fatalf("the key line did not mark the attach unavailable up front")
			}
		})
	}
}

// TestAReservedBindingIsNotARefusal pins the deliberate decision this PR's
// counter-review asked for (N1): a held-but-not-active (reserved) binding is
// attachable, in both the Agent View and the classic hand-off. It is the
// state a SIGKILL mid-spawn leaves behind - `preparing` + a reserved binding
// + a live agent (ADR 0012) - and application.ResolveAttachTarget accepts any
// held binding that is not stale, so a Console refusing it would refuse an
// attach the CLI allows. The key line must say so too: Enter opens the view,
// so it must not be labelled unavailable.
func TestAReservedBindingIsNotARefusal(t *testing.T) {
	reservedTree := func() query.Snapshot {
		tree := sampleTree()
		tree.Projects[0].Mate.Binding = query.KnownField(query.BindingValue{
			Status: query.BindingReserved, AgentName: "mate-payments-api",
		})
		return tree
	}

	t.Run("classic hand-off without session ports", func(t *testing.T) {
		spy := &attachSpy{tree: sameTree(reservedTree())}
		m := attachFixture(t, spy, 120, 36, unicodeGlyphs)
		m, _ = send(t, m, key("enter")) // the Project; its Mate row is selected
		r, ok := m.selectedRow()
		if !ok || r.kind != rowMate {
			t.Fatalf("selected row = %+v (ok=%v), want the Mate row", r, ok)
		}
		if frame := renderFrame(t, m); strings.Contains(frame, "Attach mate (unavailable)") {
			t.Fatalf("the key line marked a reserved binding's attach unavailable:\n%s", frame)
		}
		m, cmd := send(t, m, key("enter"))
		if cmd == nil || m.attachHoldsTerminal() == false || len(spy.targets) == 0 {
			t.Fatalf("a reserved binding was refused instead of attached: cmd=%v targets=%v", cmd != nil, spy.targets)
		}
	})

	t.Run("agent view with session ports", func(t *testing.T) {
		ctrl := &recordingSessionController{snap: SessionSnapshot{RecordedStatus: query.KnownField("running"), Runtime: SessionRuntime{Status: query.Known}}}
		spy := &attachSpy{tree: sameTree(reservedTree())}
		m := New(spy.load, spy.attach).WithSession(ctrl.Read, ctrl.Prompt, ctrl.Close)
		m.g = unicodeGlyphs
		m.p = plainPalette()
		m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 48})
		m, _ = send(t, m, m.Init()())
		m, _ = send(t, m, key("enter")) // the Project; its Mate row is selected

		r, ok := m.selectedRow()
		if !ok || r.kind != rowMate {
			t.Fatalf("selected row = %+v (ok=%v), want the Mate row", r, ok)
		}
		if got := m.enterLabel(r); got != "Open agent view" {
			t.Fatalf("enterLabel = %q, want %q for a reserved binding", got, "Open agent view")
		}
		m, cmd := send(t, m, key("enter"))
		if cmd == nil {
			t.Fatalf("Enter did not issue the entry read")
		}
		cmd()
		if ctrl.readCount() == 0 {
			t.Fatalf("Enter never reached SessionReader: a reserved binding must open the Agent View")
		}
	})
}

// TestARefusalNeverLeavesTheFlowHoldingTheTerminal: a refusal keeps the
// Console interactive. Leaving the flow in attachAnnouncing would ignore
// every later key (see onKey), which is the wedge this guards.
func TestARefusalNeverLeavesTheFlowHoldingTheTerminal(t *testing.T) {
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := attachFixture(t, spy, 120, 36, unicodeGlyphs)
	m = toFailedAttempt(t, m)
	m, _ = send(t, m, key("enter")) // refused
	if m.attachHoldsTerminal() {
		t.Fatalf("a refusal left the flow holding the terminal: %+v", m.att)
	}
	before := m.cur().sel
	m, _ = send(t, m, key("up"))
	if m.cur().sel == before {
		t.Fatalf("the Console stopped responding to keys after a refusal")
	}
}

// TestAnAttachThatCannotBeStartedIsAFailureNotAWedge: a Console built
// without a working AttachCmdFunc has tried and failed, which is a failure
// message - and it must not sit in attachAnnouncing waiting for a
// subprocess that will never run.
func TestAnAttachThatCannotBeStartedIsAFailureNotAWedge(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(string) *exec.Cmd
		says  string
	}{
		{"no closure", nil, "without an attach command"},
		{"nil command", func(string) *exec.Cmd { return nil }, "could not be built"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(sameTreeLoad(sampleTree()), tc.build)
			m.p = plainPalette()
			m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
			m, _ = send(t, m, m.Init()())
			m = toRunningAttempt(t, m)
			m, cmd := send(t, m, key("enter"))
			if cmd != nil {
				t.Fatalf("a subprocess that cannot be built returned a Cmd")
			}
			if m.attachHoldsTerminal() {
				t.Fatalf("the flow is holding the terminal for a subprocess that never ran: %+v", m.att)
			}
			if m.msg.tone != toneError || !strings.Contains(m.msg.text, "Attach failed") || !strings.Contains(m.msg.text, tc.says) {
				t.Fatalf("message = %+v, want a failure saying %q", m.msg, tc.says)
			}
			if strings.Contains(m.msg.text, "refused") {
				t.Fatalf("message = %q, want a failure rather than a refusal: the Console did try", m.msg.text)
			}
		})
	}
}

func sameTreeLoad(tree query.Snapshot) LoadFunc {
	tree.AsOf = goldenAsOf
	return func(context.Context) (query.Snapshot, error) { return tree, nil }
}

// TestKeysAreIgnoredWhileTheTerminalIsHandedOverExceptQuit: while the
// subprocess owns the terminal, a key that reached this process was aimed
// at the agent session. Moving the selection on it would change where the
// reader lands on return. q still works, because a reader must always be
// able to leave.
func TestKeysAreIgnoredWhileTheTerminalIsHandedOverExceptQuit(t *testing.T) {
	for _, phase := range []struct {
		name  string
		drive func(t *testing.T, m Model) Model
	}{
		{"announcing", func(t *testing.T, m Model) Model { m, _ = send(t, m, key("enter")); return m }},
		{"held", func(t *testing.T, m Model) Model {
			m, _ = send(t, m, key("enter"))
			m, _ = send(t, m, AttachHandedOverMsg{})
			return m
		}},
	} {
		t.Run(phase.name, func(t *testing.T) {
			spy := &attachSpy{tree: sameTree(sampleTree())}
			m := toRunningAttempt(t, attachFixture(t, spy, 120, 36, unicodeGlyphs))
			m = phase.drive(t, m)
			before := m
			for _, k := range []string{"down", "up", "enter", "esc", "tab", "r", "pgdn"} {
				next, cmd := send(t, m, key(k))
				if cmd != nil {
					t.Fatalf("%q returned a Cmd while the terminal was handed over", k)
				}
				if next.cur() != before.cur() || next.msg != before.msg || next.att != before.att {
					t.Fatalf("%q changed the model while the terminal was handed over", k)
				}
				m = next
			}
			if spy.loads != 1 || len(spy.targets) != 1 {
				t.Fatalf("loads=%d targets=%v, want nothing extra read or spawned", spy.loads, spy.targets)
			}
			quit, cmd := send(t, m, key("q"))
			if !quit.Quitting() || cmd == nil {
				t.Fatalf("q must still quit while the terminal is handed over")
			}
		})
	}
}

// TestAHandOverWithNoAnnouncementBehindItIsIgnored: AttachHandedOverMsg
// only ever follows this Console's own announcement. Acting on a stray one
// would make the Console ignore keys for a subprocess it never started.
func TestAHandOverWithNoAnnouncementBehindItIsIgnored(t *testing.T) {
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := toRunningAttempt(t, attachFixture(t, spy, 120, 36, unicodeGlyphs))
	before := m
	m, cmd := send(t, m, AttachHandedOverMsg{})
	if cmd != nil {
		t.Fatalf("a stray hand-over returned a Cmd")
	}
	if m.attachHoldsTerminal() {
		t.Fatalf("a stray hand-over took the terminal: %+v", m.att)
	}
	if m.msg != before.msg {
		t.Fatalf("a stray hand-over wrote a message: %+v", m.msg)
	}
}

// ---------- the failure taxonomy ----------

// TestAttachFailureTaxonomyCoversEveryPublishedExit walks every error.code
// in docs/phase1/agent.md section 4 through its own exit code and requires
// the failure chain to classify it. The mapping keys come from
// internal/observability, so a new code or a changed exit fails here rather
// than silently rendering as an uncoded exit.
//
// Two facts are asserted structurally rather than by reading the text, because
// they are what the detail view's Code field reports: an exit that names
// exactly one code must carry that code (marked as derived from the exit), and
// an exit several codes share must carry none - guessing which of them it was
// is the same overclaim as guessing whether the agent is alive.
func TestAttachFailureTaxonomyCoversEveryPublishedExit(t *testing.T) {
	codes := []observability.Code{
		observability.CodeUsage, observability.CodeStateConflict, observability.CodeRuntimeUnavailable,
		observability.CodeTargetBlocked, observability.CodeInteractionTimeout, observability.CodeInteractionExpired,
		observability.CodeNeedsRepair, observability.CodeNotFound, observability.CodeAlreadyExists,
		observability.CodePermission, observability.CodeTimeout, observability.CodeUnknown,
	}
	shared := map[int][]observability.Code{}
	for _, c := range codes {
		exit := observability.NewError(c, "x").ExitCode()
		shared[exit] = append(shared[exit], c)
	}
	if len(shared) == 0 {
		t.Fatal("no published code maps to an exit; this sweep is checking nothing")
	}
	for exit, sharing := range shared {
		f := classified(t, stepAttach, exitErr(t, exit))
		line := openFailureShortLine(f)
		if !strings.Contains(line, fmt.Sprintf("exit %d", exit)) {
			t.Errorf("exit %d: line = %q, want it to name the exit", exit, line)
		}
		if f.ExitCode != exit {
			t.Errorf("exit %d was classified with exit code %d", exit, f.ExitCode)
		}
		switch {
		case len(sharing) == 1:
			if f.Code != string(sharing[0]) {
				t.Errorf("exit %d belongs to %q alone; classified code = %q", exit, sharing[0], f.Code)
			}
			if !f.CodeFromExit {
				t.Errorf("exit %d: the code %q came from an exit and must be marked derived", exit, f.Code)
			}
			if !strings.Contains(line, string(sharing[0])) {
				t.Errorf("exit %d belongs to %q alone; line = %q, want the code named", exit, sharing[0], line)
			}
		default:
			if f.Code != "" {
				t.Errorf("exit %d is shared by %v; classified code = %q, want none", exit, sharing, f.Code)
			}
			for _, c := range sharing {
				if strings.Contains(line, string(c)) {
					t.Errorf("exit %d is shared by %v; line = %q must not claim %q", exit, sharing, line, c)
				}
			}
		}
	}
}

// TestTheFailureLineForResultsThatAreNotAnExitCode: the three shapes the exit
// table does not cover, plus the nil error a caller may still hand over. Each
// says which it is, none of them is reported as a coded exit, and none of them
// claims anything about the agent.
func TestTheFailureLineForResultsThatAreNotAnExitCode(t *testing.T) {
	unmapped := classified(t, stepAttach, exitErr(t, 7))
	if unmapped.ExitCode != 7 || unmapped.Code != "" {
		t.Errorf("exit 7 = %+v, want the exit recorded and no code invented", unmapped)
	}
	if line := openFailureShortLine(unmapped); !strings.Contains(line, "7") {
		t.Errorf("unmapped exit = %q, want it named", line)
	}

	signalled := exec.Command("sh", "-c", "kill -9 $$").Run()
	var ee *exec.ExitError
	if !errors.As(signalled, &ee) || ee.ExitCode() >= 0 {
		t.Fatalf("kill -9 produced %v (code %d), want an ExitError with no exit code", signalled, observability.ExitCode(signalled))
	}
	signalledFailure := classified(t, stepAttach, signalled)
	if signalledFailure.ExitCode >= 0 {
		t.Errorf("signalled = %+v, want no exit code", signalledFailure)
	}
	line := openFailureShortLine(signalledFailure)
	if !strings.Contains(line, "signalled") {
		t.Errorf("signalled = %q, want it to say the subprocess was signalled", line)
	}
	if strings.Contains(line, "exit") {
		t.Errorf("signalled = %q, must not report an exit code it does not have", line)
	}

	notRun := classified(t, stepAttach, errFake("fork/exec /nope: no such file or directory"))
	if notRun.ExitNote != exitNoteNotRun {
		t.Errorf("start failure = %+v, want it recorded as an attach that did not run", notRun)
	}
	if line := openFailureShortLine(notRun); !strings.Contains(line, "did not run") || !strings.Contains(line, "no such file") {
		t.Errorf("start failure = %q, want it to say the subprocess did not run, and why", line)
	}

	if line := openFailureShortLine(classified(t, stepAttach, nil)); line == "" {
		t.Errorf("a nil error must still produce a line rather than an empty message")
	}
}

// ---------- the frame ----------

// TestEveryAttachMessageFitsAnEightyColumnFrame: the message line is the one
// line a reader cannot scroll, so a message longer than the design's narrowest
// gallery frame must not silently lose its own reason. Every message the flow
// can produce for the fixtures' recorded names is measured here in display
// cells, the only measurement this package uses.
//
// What must fit differs by line, and the difference is deliberate rather than
// a relaxation. A refusal, the announcement and the detach note are authored
// or bounded, so the whole message must fit (PR #72's B1 was a refusal losing
// its "nothing started" clause). A failure line carries the step's own
// message - arbitrary text handed over by a subprocess - so its head (the
// chain's bounded cause) must fit and the evidence after it may be cut; that
// is why messageLine marks the cut, and why the cause phrases are bounded
// (TestNoCauseMerelyRestatesItsCode, session_failure.go).
func TestEveryAttachMessageFitsAnEightyColumnFrame(t *testing.T) {
	const width = 80
	spy := &attachSpy{tree: sameTree(sampleTree())}
	base := attachFixture(t, spy, width, 24, unicodeGlyphs)

	// measured is what the final sweep checks for one state: the message
	// itself, and head - the part of it that must survive at 80 cells. For a
	// failure line the head is the chain's own bounded cause (the step's
	// evidence after it is arbitrary text and may be cut, marked); for every
	// other line - a refusal, the announcement, the detach note - the head is
	// the whole message, because those are authored or bounded and losing
	// their tail is exactly PR #72's B1.
	type measured struct {
		msg  footerMsg
		head string
	}
	messages := map[string]measured{}
	collect := func(name string, m Model) {
		head := m.msg.text
		if n := len(m.openFailures); n > 0 {
			last := m.openFailures[n-1]
			if prefix := last.Step.label() + " failed: "; strings.HasPrefix(m.msg.text, prefix) {
				head = prefix + last.Cause
			}
		}
		messages[name] = measured{msg: m.msg, head: head}
	}

	// Every state the key line and Enter can meet on a Mate row, through the
	// model that produces it. binding-reserved is no longer a refusal (a
	// held-but-not-active slot is attachable - see
	// TestAReservedBindingIsNotARefusal), but it is swept here anyway because it
	// is a message a reader on that row can see, and it must fit too.
	staleTree := sampleTree()
	mutations := map[string]func(*query.Snapshot){
		"mate-unknown":  func(s *query.Snapshot) { s.Projects[0].Mate = unknownMate("designation lookup failed") },
		"mate-absent":   func(s *query.Snapshot) { s.Projects[0].Mate = absentMate("this project has no designated Mate") },
		"mate-stopped":  func(s *query.Snapshot) { s.Projects[0].Mate.Designated.Value.Status = query.MateStopped },
		"mate-starting": func(s *query.Snapshot) { s.Projects[0].Mate.Designated.Value.Status = query.MateCreated },
		"binding-absent": func(s *query.Snapshot) {
			s.Projects[0].Mate.Binding = query.AbsentField[query.BindingValue]("no binding is held")
		},
		"binding-unknown": func(s *query.Snapshot) {
			s.Projects[0].Mate.Binding = query.UnknownField[query.BindingValue]("binding lookup failed")
		},
		"binding-reserved": func(s *query.Snapshot) {
			s.Projects[0].Mate.Binding = query.KnownField(query.BindingValue{Status: query.BindingReserved})
		},
		"binding-stale": func(s *query.Snapshot) {
			s.Projects[0].Mate.Binding = query.KnownField(query.BindingValue{Status: query.BindingStale})
		},
		"binding-no-status": func(s *query.Snapshot) {
			s.Projects[0].Mate.Binding = query.KnownField(query.BindingValue{})
		},
	}
	for name, mutate := range mutations {
		tree := sampleTree()
		mutate(&tree)
		m := attachFixture(t, &attachSpy{tree: sameTree(tree)}, width, 24, unicodeGlyphs)
		m, _ = send(t, m, key("enter"))
		m, _ = send(t, m, key("enter"))
		collect(name, m)
	}

	// The same states through the Model shape cmd/mate actually builds:
	// session ports wired and a reader that fails every entry read, so a row
	// the Agent View can open takes the entry-read-failure fallback into the
	// classic hand-off, and a row it cannot takes the refusal directly. Before
	// this sweep the 80-cell rule was enforced only on the portless fixture,
	// which production no longer builds (issue #60); the fallback composed
	// the session reason onto the hand-off's message into a 250+ cell line
	// and the refusal reason and its "nothing started" clause fell off the
	// end at every gallery width (B1 of PR #72's counter-review). A failing
	// reader is the worst case for length, because the fallback's own message
	// is added to whatever the hand-off says.
	entryErr := observability.NewError(observability.CodeRuntimeUnavailable, "herdr server not running")
	sessionEnter := func(tree query.Snapshot, steps ...string) Model {
		ctrl := &recordingSessionController{readErr: entryErr}
		spy := &attachSpy{tree: sameTree(tree)}
		m := New(spy.load, spy.attach).WithSession(ctrl.Read, ctrl.Prompt, ctrl.Close)
		m.g = unicodeGlyphs
		m.p = plainPalette()
		m, _ = send(t, m, tea.WindowSizeMsg{Width: width, Height: 24})
		m, _ = send(t, m, m.Init()())
		for _, k := range steps {
			m, _ = send(t, m, key(k))
		}
		m, cmd := send(t, m, key("enter"))
		if cmd != nil {
			m, _ = send(t, m, cmd())
		}
		return m
	}
	for name, mutate := range mutations {
		tree := sampleTree()
		mutate(&tree)
		collect("session-"+name, sessionEnter(tree, "enter"))
	}
	// A live Mate (the fixture's own running + active binding): the Agent
	// View opens, the entry read fails, and the fallback's compact message is
	// what must fit.
	collect("session-live-entry-read-fails", sessionEnter(sampleTree(), "enter"))

	// The Crew refusal, the announcement, the hand-over, the detach and
	// every failure classification.
	crewStale := toFailedAttempt(t, attachFixture(t, &attachSpy{tree: sameTree(staleTree)}, width, 24, unicodeGlyphs))
	crewStale, _ = send(t, crewStale, key("enter"))
	collect("crew-stale", crewStale)
	collect("session-crew-stale", sessionEnter(staleTree, "enter", "down", "enter", "down", "enter", "down"))

	live := toRunningAttempt(t, base)
	live, _ = send(t, live, key("enter"))
	collect("announcing", live)
	live, _ = send(t, live, AttachHandedOverMsg{})
	collect("held", live)
	detached, doneCmd := send(t, live, AttachFinishedMsg{})
	collect("detach-pending", detached)
	detached, _ = send(t, detached, doneCmd())
	collect("detached", detached)

	for _, exit := range []int{observability.ExitGeneric, observability.ExitUsage, observability.ExitStateConflict,
		observability.ExitRuntimeUnavailable, observability.ExitTargetBlocked, observability.ExitInteractionWait,
		observability.ExitNeedsRepair} {
		failed, cmd := send(t, live, AttachFinishedMsg{Err: exitErr(t, exit)})
		collect(fmt.Sprintf("failed-exit-%d", exit), failed)
		done, _ := send(t, failed, cmd())
		collect(fmt.Sprintf("failed-exit-%d-after-read", exit), done)
	}

	// Issue #60's own shape, at 80 columns: the child reports a code *and* its
	// own message, and that message is the one fact that distinguishes a
	// runtime that shut down from a binary never on PATH. It must be on the
	// line - the detail view is not a substitute for it (PR #93 B1).
	reported, _ := send(t, live, AttachFinishedMsg{Err: &attachReportError{
		Cause:   exitErr(t, observability.ExitGeneric),
		Code:    observability.CodeUnknown,
		Message: "herdr attach exited 1",
	}})
	if got := reported.openFailureLine(false); !strings.Contains(got, "herdr attach exited 1") {
		t.Errorf("the failure line dropped the child's own message at 80 columns: %q", got)
	}
	collect("failed-reported-unknown", reported)

	if len(messages) < 30 {
		t.Fatalf("only %d messages were collected; the sweep is not covering the flow", len(messages))
	}
	for name, m := range messages {
		if m.msg.tone == toneNone || m.msg.text == "" {
			t.Errorf("%s produced no message at all", name)
			continue
		}
		// The line is one line at 80 cells, and a failure line ends with the
		// step's own message, which is arbitrary text: that part may be cut
		// (messageLine marks the cut with the glyph set's ellipsis, and the
		// 'e' detail view carries it in full). What must survive is the head
		// - the chain's bounded cause for a failure line, the whole message
		// for everything else - so this measures exactly that and nothing
		// weaker.
		if got := cells(" ! " + m.head); got > width {
			t.Errorf("%s overflows the %d-cell line at %d cells: %q (head %q)", name, width, got, m.msg.text, m.head)
		}
	}
}

// TestAttachFramesSurviveHostileText drives the whole attach lifecycle at
// every breakpoint, in both glyph sets, over hostileTree - whose recorded
// agent names carry a variation-selector emoji followed by more text, and
// whose values include a literal newline, a tab and a raw SGR escape. The
// announcement, the hand-over notice and the failure line all interpolate
// those recorded names and an error string, so this is the sweep that
// reaches the frames this file adds: without it a newline in an agent name
// or an error would add a line to View and no other test would notice.
func TestAttachFramesSurviveHostileText(t *testing.T) {
	hostileErr := errFake("herdr said:\n\x1b[31mno\x1b[0m\tpane\u0085more " + strings.Repeat("‼️deep/", 20))
	for _, g := range []glyphSet{unicodeGlyphs, asciiGlyphs} {
		for _, size := range frameSizes {
			t.Run(g.Name+"/"+size.name, func(t *testing.T) {
				spy := &attachSpy{tree: sameTree(hostileTree())}
				m := attachFixture(t, spy, size.w, size.h, g)
				assertFrameShape(t, m.View(), size.w, size.h)
				// Into the Project, its Task, and the attempt whose binding
				// is recorded active - the one row a hand-over can start
				// from, and the one carrying the hostile agent name.
				steps := []string{"enter", "down", "enter", "down", "down", "enter", "down"}
				for _, k := range steps {
					m, _ = send(t, m, key(k))
					assertFrameShape(t, m.View(), size.w, size.h)
				}
				attached := m
				if l := layout(size.w, size.h); !l.TooSmall {
					r, ok := m.selectedRow()
					if !ok || r.kind != rowCrew {
						t.Fatalf("selected row = %+v (ok=%v), want a Crew row to attach from", r, ok)
					}
					if _, refused := m.attachRefusal(r); refused {
						t.Fatalf("the hostile fixture's selected attempt is refused; the sweep never reaches the hand-over")
					}
				}
				for _, step := range []struct {
					name string
					msg  tea.Msg
				}{
					{"announce", key("enter")},
					{"handover", AttachHandedOverMsg{}},
					{"failed", AttachFinishedMsg{Err: hostileErr}},
				} {
					attached, _ = send(t, attached, step.msg)
					assertFrameShape(t, attached.View(), size.w, size.h)
					if step.name == "announce" && !layout(size.w, size.h).TooSmall && !attached.attachHoldsTerminal() {
						t.Fatalf("the announcement did not take the terminal; the sweep is not rendering the attach frames")
					}
				}
				attached, _ = send(t, attached, treeLoadedMsg{tree: hostileTree()})
				assertFrameShape(t, attached.View(), size.w, size.h)

				// And the clean detach, from the same row.
				clean := m
				clean, _ = send(t, clean, key("enter"))
				clean, _ = send(t, clean, AttachHandedOverMsg{})
				clean, cmd := send(t, clean, AttachFinishedMsg{})
				assertFrameShape(t, clean.View(), size.w, size.h)
				if cmd != nil {
					clean, _ = send(t, clean, cmd())
				}
				assertFrameShape(t, clean.View(), size.w, size.h)
			})
		}
	}
}

// TestTheKeyLineDuringAHandOverOffersTheChildsDetachOnly: while the
// subprocess owns the keyboard, the Console's own keys do not reach this
// process, so the key line names Ctrl+b q - and does not offer keys it
// cannot answer.
func TestTheKeyLineDuringAHandOverOffersTheChildsDetachOnly(t *testing.T) {
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := toRunningAttempt(t, attachFixture(t, spy, 120, 36, unicodeGlyphs))
	for _, step := range []struct {
		name string
		msg  tea.Msg
	}{{"announcing", key("enter")}, {"held", AttachHandedOverMsg{}}} {
		m, _ = send(t, m, step.msg)
		keys := lastLine(renderFrame(t, m))
		if !strings.Contains(keys, "Ctrl+b q") || !strings.Contains(keys, "does not stop the agent") {
			t.Fatalf("%s key line = %q, want the child's detach keystroke and what it does not do", step.name, keys)
		}
		for _, forbidden := range []string{"Refresh", "Quit", "Move", "Inspector", "Attach"} {
			if strings.Contains(keys, forbidden) {
				t.Fatalf("%s key line = %q, must not offer %q while the child owns the keyboard", step.name, keys, forbidden)
			}
		}
	}
}

func lastLine(frame string) string {
	lines := strings.Split(frame, "\n")
	return lines[len(lines)-1]
}

// ---------- the gallery ----------

// ---------- the gallery, and what each of its states has to say ----------

// attachGalleryState is one of the design gallery's attach states
// (design/mate-console-states.html, "Attach, detach, và từ chối attach"),
// with the text it must carry.
//
// The golden fixture and the text live in one table on purpose. Three
// sibling tasks replace the list, inspector and footer seams these frames
// are drawn through, so the seven attach goldens will legitimately change
// body content on rebase - and a regenerated fixture accepted mechanically
// would then be able to drop the refusal, failure or detach sentence
// without a single test noticing. says/notSays are asserted against the
// rendered frame independently of the golden bytes, so the fixture can move
// and the meaning cannot.
type attachGalleryState struct {
	name string
	// build renders the state. It returns the frame rather than the Model so
	// each state can end wherever it needs to.
	build func(t *testing.T) string
	// says is text the reader must find on this frame.
	says []string
	// notSays is text that would make this state read as another one.
	notSays []string
}

// attachGallery builds all seven states. later is the gallery's 14:05:12,
// the time the post-attach re-read lands on.
func attachGallery() []attachGalleryState {
	later := goldenAsOf.Add(3*time.Minute + time.Second)
	// ageingTree answers the first read at 14:02:11 and every later one at
	// 14:05:12, so a re-read is visible as a new "As of".
	ageingTree := func(n int) (query.Snapshot, error) {
		tree := sampleTree()
		tree.AsOf = goldenAsOf
		if n > 1 {
			tree.AsOf = later
		}
		return tree, nil
	}
	// announcing walks to the attachable row and presses Enter.
	announcing := func(t *testing.T) Model {
		t.Helper()
		m := toRunningAttempt(t, attachFixture(t, &attachSpy{tree: ageingTree}, 120, 36, unicodeGlyphs))
		m, _ = send(t, m, key("enter"))
		return m
	}
	// runtimeFailure hands over and comes back from exit 20 at the given size.
	// The failure states deliberately re-read the *same* snapshot: what is
	// being pinned there is the failure wording, and a moving "As of" would
	// make the fixture about the clock instead.
	runtimeFailure := func(t *testing.T, w, h int) string {
		t.Helper()
		m := toRunningAttempt(t, attachFixture(t, &attachSpy{tree: sameTree(sampleTree())}, w, h, unicodeGlyphs))
		m, _ = send(t, m, key("enter"))
		m, _ = send(t, m, AttachHandedOverMsg{})
		m, cmd := send(t, m, AttachFinishedMsg{Err: exitErr(t, observability.ExitRuntimeUnavailable)})
		m, _ = send(t, m, cmd())
		return renderFrame(t, m)
	}
	return []attachGalleryState{{
		name: "attach-announcing-120x36-unicode",
		build: func(t *testing.T) string {
			return renderFrame(t, announcing(t))
		},
		says: []string{
			"Attaching to crew-payments-api-2", "via mate attach",
			"detach: Ctrl+b then q",
			// the key line, which is all the reader has once the child owns
			// the keyboard
			"Ctrl+b q", "does not stop the agent",
		},
		notSays: []string{"Attach refused", "Attach failed", "Detached"},
	}, {
		name: "attach-held-120x36-unicode",
		build: func(t *testing.T) string {
			m := announcing(t)
			m, _ = send(t, m, AttachHandedOverMsg{})
			return renderFrame(t, m)
		},
		says: []string{
			"crew-payments-api-2", "owns this terminal",
			"Console idle until it returns", "Ctrl+b q",
		},
		notSays: []string{"Attach refused", "Attach failed", "Detached"},
	}, {
		name: "attach-detached-120x36-unicode",
		build: func(t *testing.T) string {
			m := announcing(t)
			m, _ = send(t, m, AttachHandedOverMsg{})
			m, cmd := send(t, m, AttachFinishedMsg{})
			m, _ = send(t, m, cmd())
			return renderFrame(t, m)
		},
		says: []string{
			"Detached from crew-payments-api-2", "agent not stopped",
			"re-read " + goldenAsOf.Add(3*time.Minute+time.Second).Format("15:04:05"),
			"As of " + goldenAsOf.Add(3*time.Minute+time.Second).Format("15:04:05"),
		},
		// A detach is neither of the two bad outcomes, and it establishes
		// nothing about the agent beyond "not stopped by this".
		notSays: []string{"Attach refused", "Attach failed", "nothing started"},
	}, {
		name: "attach-refused-stale-120x36-unicode",
		build: func(t *testing.T) string {
			// Attempt 1 of the sample Task: its binding is recorded stale.
			m := toFailedAttempt(t, attachFixture(t, &attachSpy{tree: sameTree(sampleTree())}, 120, 36, unicodeGlyphs))
			m, _ = send(t, m, key("enter"))
			return renderFrame(t, m)
		},
		says: []string{
			"Attach refused", "binding recorded stale", "stop unconfirmed",
			"nothing started",
		},
		// The one thing a stale binding must never read as: a failure that
		// ran, or a confirmed stop.
		notSays: []string{"Attach failed", "mate attach exit", "not active"},
	}, {
		name:  "attach-failed-runtime-120x36-unicode",
		build: func(t *testing.T) string { return runtimeFailure(t, 120, 36) },
		says: []string{
			"Attach failed", "the runtime is not reachable", "runtime_unavailable",
			"snapshot re-read", "e Details",
		},
		notSays: []string{"Attach refused", "nothing started"},
	}, {
		// The same failure in the narrowest gallery frame, where the message
		// line has 80 cells and there is no inspector column.
		name:  "attach-failed-runtime-80x24-unicode",
		build: func(t *testing.T) string { return runtimeFailure(t, 80, 24) },
		says: []string{
			"Attach failed", "the runtime is not reachable", "runtime_unavailable",
			"e Details",
		},
		notSays: []string{"Attach refused", "nothing started"},
	}, {
		name: "attach-mate-stopped-80x24-unicode",
		build: func(t *testing.T) string {
			// docs-site's Mate is recorded stopped: there is no session.
			m := attachFixture(t, &attachSpy{tree: sameTree(stoppedMateTree())}, 80, 24, unicodeGlyphs)
			m, _ = send(t, m, key("down"))
			m, _ = send(t, m, key("down"))
			m, _ = send(t, m, key("enter"))
			m, _ = send(t, m, key("enter"))
			return renderFrame(t, m)
		},
		says: []string{
			"Attach refused", "Mate recorded stopped", "no session to attach",
			"nothing started",
		},
		notSays: []string{"Attach failed", "mate attach exit", "stale"},
	}}
}

// TestGoldenAttachFrames pins the gallery's attach states byte for byte.
// Accept a deliberate change with:
//
//	go test ./internal/ui/console -run TestGoldenAttachFrames -update
//
// The gallery's "Attached" state draws the harness session's own panel;
// that panel belongs to the harness, which owns the terminal after the
// hand-over, and the Console cannot and must not draw it. What is golden
// here is the frame the Console itself would show for that state - the one
// the terminal carries for the moment between the child exiting and
// AttachFinishedMsg landing - which says the terminal is not the Console's.
func TestGoldenAttachFrames(t *testing.T) {
	for _, st := range attachGallery() {
		t.Run(st.name, func(t *testing.T) {
			assertGolden(t, st.name, st.build(t))
		})
	}
}

// TestEveryAttachGalleryStateSaysWhatItMeans is the same seven states
// asserted as text rather than as bytes: a golden regenerated after a
// sibling task moves the list, inspector or footer seam cannot drop the
// sentence that distinguishes refused from failed from detached, and cannot
// quietly lose the detach keystroke while the child owns the keyboard.
func TestEveryAttachGalleryStateSaysWhatItMeans(t *testing.T) {
	for _, st := range attachGallery() {
		t.Run(st.name, func(t *testing.T) {
			frame := st.build(t)
			for _, want := range st.says {
				if !strings.Contains(frame, want) {
					t.Errorf("%s does not say %q:\n%s", st.name, want, frame)
				}
			}
			for _, banned := range st.notSays {
				if strings.Contains(frame, banned) {
					t.Errorf("%s says %q, which belongs to another outcome:\n%s", st.name, banned, frame)
				}
			}
		})
	}
}
