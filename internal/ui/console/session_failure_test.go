package console

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The session-open failure chain, from the outside: the cause-first one-line
// summary, the re-openable detail view it stands on, the chain of fallback
// steps, and the next step - asserted to be derived from the error's own
// evidence and never from the agent's lifecycle (issue #60: a failed attach
// establishes nothing about whether the Crew is alive).

// failureModel is the shortest way to put a chain of failures on a model: an
// ordinary gallery fixture with the recorded failures attached, so the tests
// below read the same fields and the same renderer the live path fills.
func failureModel(t *testing.T, fails ...sessionFailure) Model {
	t.Helper()
	m := attachFixture(t, &attachSpy{tree: sameTree(sampleTree())}, 160, 48, unicodeGlyphs)
	m.openFailures = append(m.openFailures, fails...)
	return m
}

func classified(t *testing.T, step sessionFailureStep, err error) sessionFailure {
	t.Helper()
	return classifyOpenFailure(step, "payments-api-mate", "mate_01J9P4Q7X2K8W3N5R6T7V8YZAB", err)
}

// TestTheFailureSummaryLeadsWithTheCauseAndKeepsTheCode is the first
// requirement (cause first, code second) over the shapes a failure can have:
// a coded error, an exit that names exactly one code, an exit only the Console
// can classify, and an attach that never ran. In every one of them the
// plain-language cause comes before any code, and the code is still there.
//
// The evidence half is the PR #93 counter-review's blocking finding: the line
// must carry what the step's error actually said, because a code plus an
// authored phrase cannot distinguish a runtime that shut down from a binary
// that was never on PATH - the loss session_failure.go exists to repair.
func TestTheFailureSummaryLeadsWithTheCauseAndKeepsTheCode(t *testing.T) {
	cases := []struct {
		name     string
		step     sessionFailureStep
		err      error
		want     string
		contains string // evidence that must survive on the line
	}{
		{
			// PR #93 B1, the exact shape the counter-review reproduced: the
			// child's message is the distinguishing fact and must be on the
			// line, not only in the overlay.
			name:     "coded error keeps the child's message",
			step:     stepSnapshotView,
			err:      observability.NewError(observability.CodeRuntimeUnavailable, "herdr: server shut down"),
			want:     "Snapshot view failed: the runtime is not reachable (runtime_unavailable: herdr: server shut down)",
			contains: "herdr: server shut down",
		},
		{
			name: "exit naming one code",
			step: stepAttach,
			err:  exitErr(t, observability.ExitTargetBlocked),
			want: "Attach failed: another agent holds the pane (target_blocked, exit 30)",
		},
		{
			name: "exit shared by several codes",
			step: stepAttach,
			err:  exitErr(t, observability.ExitGeneric),
			want: "Attach failed: no reason was reported (exit 1)",
		},
		{
			// A report that carries a message but no code: the message is the
			// cause, and it must not be replaced by the exit's generic phrase
			// ("no reason was reported" while the report held the reason).
			name:     "report with a message and no code",
			step:     stepAttach,
			err:      &attachReportError{Cause: exitErr(t, observability.ExitGeneric), Message: "herdr attach exited 1"},
			want:     "Attach failed: herdr attach exited 1 (exit 1)",
			contains: "herdr attach exited 1",
		},
		{
			name: "attach that never ran",
			step: stepAttach,
			err:  errors.New("without an attach command"),
			want: "Attach failed: mate attach did not run (without an attach command)",
		},
		{
			// A step that reported neither a code nor an exit: its own text is
			// the only fact there is, so it leads the line. The Console does
			// not dress it up as a taxonomy it does not have, and it does not
			// say "cause undetermined" beside the error's own words either -
			// that would read worse and cost the line the words.
			name:     "error with no code",
			step:     stepSnapshotView,
			err:      errors.New("herdr attach exited 1"),
			want:     "Snapshot view failed: herdr attach exited 1",
			contains: "herdr attach exited 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := classified(t, tc.step, tc.err)
			m := failureModel(t, f)
			line := m.openFailureLine(false)
			if line != tc.want {
				t.Fatalf("summary = %q, want %q", line, tc.want)
			}
			// The cause is before the evidence: the plain words come first,
			// and the parenthetical - when there is one - is after them.
			if i, j := strings.Index(line, f.Cause), strings.Index(line, "("); i >= 0 && j >= 0 && j < i {
				t.Fatalf("summary %q puts the evidence before the cause %q", line, f.Cause)
			}
			if tc.contains != "" && !strings.Contains(line, tc.contains) {
				t.Fatalf("summary %q dropped the step's own evidence %q", line, tc.contains)
			}
			if f.Next == "" {
				t.Fatalf("%s recorded no next step; every failure must carry one", tc.name)
			}
		})
	}

	// Evidence is never invented: a failure the Console has no evidence for
	// stops after the cause instead of printing an empty "()".
	bare := openFailureShortLine(sessionFailure{Step: stepSnapshotView, Cause: causeUndetermined, ExitCode: -1})
	if bare != "Snapshot view failed: cause undetermined" {
		t.Fatalf("a failure with no evidence rendered %q, want the cause and nothing invented", bare)
	}
}

// TestNoCauseMerelyRestatesItsCode pins the counter-review's N1: a phrase
// that is the taxonomy code with its underscores replaced says the same thing
// twice on one line and costs the cells the error's own message needs. Every
// authored phrase must add something.
func TestNoCauseMerelyRestatesItsCode(t *testing.T) {
	for code, cause := range openFailureCauses {
		restated := strings.ReplaceAll(string(code), "_", " ")
		if strings.EqualFold(cause, restated) {
			t.Errorf("the phrase for %s is the code spelled out (%q); a phrase must add what the code does not say", code, cause)
		}
	}
	// CodeUnknown is deliberately absent from the map. The Console knows it
	// could not classify the failure; any phrase for it would either restate
	// the code ("unspecified runtime failure", the counter-review's N1) or
	// name a cause the code does not name. The failure's own message is what
	// carries the detail instead (openFailureEvidence).
	if cause, ok := openFailureCauses[observability.CodeUnknown]; ok {
		t.Errorf("CodeUnknown has the cause %q; a code with no phrase is not dressed up as one", cause)
	}
}

// TestTheFailureDetailCarriesWhatTheOneLineCannot is the second requirement:
// the one-line summary may abbreviate, but the target, the step, the cause,
// the code, the exit and the full message must all be readable somewhere -
// here, in the overlay 'e' opens.
func TestTheFailureDetailCarriesWhatTheOneLineCannot(t *testing.T) {
	// A message no renderer may trust: newlines, a tab, a raw SGR escape and
	// trailing control bytes. The detail view draws it through line.add like
	// every other string, so it cannot add a line or move the cursor.
	hostile := "herdr: attach\trefused\n\x1b[31mterminal already has an attached client\x1b[0m\r"
	f := classified(t, stepAttach, &attachReportError{
		Cause:   exitErr(t, observability.ExitRuntimeUnavailable),
		Message: hostile,
	})
	m := failureModel(t, f)

	m, _ = send(t, m, key("e"))
	if !m.failureDetail {
		t.Fatal("e did not open the failure detail view")
	}
	frame := renderFrame(t, m)
	for _, want := range []string{
		"Session open failed",
		"payments-api-mate",
		"1. Attach failed",
		"the runtime is not reachable",
		"runtime_unavailable (derived from exit 20)",
		"mate attach exit 20",
		"herdr: attach refused",
		"terminal already has an attached client",
		"Esc closes this view",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("detail view is missing %q; frame:\n%s", want, frame)
		}
	}
	// The hostile message reached the frame as text on one line, not as
	// control bytes: the sanitised form is there and neither the raw newline
	// nor the escape sequence is.
	if strings.Contains(frame, "\x1b[31m") || strings.Contains(frame, "attach\n") || strings.Contains(frame, "\t") {
		t.Errorf("the recorded message reached the frame unsanitised")
	}
	// Esc closes it and leaves the session exactly as it was: nothing here
	// changes the session (the overlay's own footer says so).
	before := m.sess.phase
	m, _ = send(t, m, key("esc"))
	if m.failureDetail {
		t.Error("Esc did not close the failure detail view")
	}
	if m.sess.phase != before {
		t.Errorf("closing the detail view moved the session phase: %v -> %v", before, m.sess.phase)
	}
}

// threeStepFailureModel drives the real wiring - session_mode.go's fallbacks
// and attach.go's return path - through all three failure shapes: the live
// stream dies with a coded runtime error, the snapshot view it falls back to
// refuses with another code, and the classic hand-off runs and exits 20. This
// is the one fixture for the whole chain, so the summary test and the detail
// goldens cannot drift from it or from each other.
func threeStepFailureModel(t *testing.T, w, h int) Model {
	t.Helper()
	ch := &blockingChannel{}
	factory := func(_ context.Context, _ SessionTarget, s TerminalSize) (SessionChannel, error) {
		ch.recordOpen(s)
		return ch, nil
	}
	reader := &recordingSessionController{
		readErr: observability.NewError(observability.CodeRuntimeUnavailable, "herdr: server shut down"),
	}
	spy := &attachSpy{tree: sameTree(sampleTree()),
		cmd: func(string) *exec.Cmd { return exec.Command("sh", "-c", "exit 20") }}
	m := New(spy.load, spy.attach).WithSession(reader.Read, reader.Prompt, reader.Close).WithSessionStream(factory)
	m.g = unicodeGlyphs
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter")) // open payments-api; its Mate row is selected

	// 1. The live stream opens, then dies with a coded runtime error.
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd())
	if m.sess.stream == nil {
		t.Fatal("the stream did not open, so the chain starts one step short")
	}
	m, cmd = send(t, m, sessionStreamChunkMsg{gen: m.sess.gen, stream: m.sess.stream,
		chunk: streamChunk{err: observability.NewError(observability.CodeRuntimeUnavailable, "herdr: server shut down")}})
	if m.sess.phase != sessionFallback {
		t.Fatalf("phase = %v, want the snapshot fallback", m.sess.phase)
	}
	if cmd == nil {
		t.Fatal("a failed stream scheduled no snapshot read")
	}

	// 2. The snapshot view's entry read fails too.
	m, cmd = send(t, m, sessionSnapshotMsg{gen: m.sess.gen,
		err: observability.NewError(observability.CodeTargetBlocked, "pane p3 is held by another agent")})
	if !m.attachHoldsTerminal() {
		t.Fatal("the snapshot failure did not fall back to the classic hand-off")
	}
	_ = cmd

	// 3. The hand-off runs and comes back failed.
	m, _ = send(t, m, AttachHandedOverMsg{})
	m, cmd = send(t, m, AttachFinishedMsg{Err: exitErr(t, observability.ExitRuntimeUnavailable)})
	m, _ = send(t, m, cmd())
	return m
}

// TestTheWholeChainSurvivesAllThreeFallbacks is the third requirement: three
// things failed in sequence and the reader must be able to see that, not just
// the last one.
func TestTheWholeChainSurvivesAllThreeFallbacks(t *testing.T) {
	m := threeStepFailureModel(t, 160, 48)

	want := "Attach failed: the runtime is not reachable (runtime_unavailable, exit 20) · +2 earlier"
	if !strings.Contains(m.msg.text, want) {
		t.Fatalf("summary = %q, want it to say %q", m.msg.text, want)
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, want) {
		t.Fatalf("the frame does not carry the summary; frame:\n%s", frame)
	}

	// All three are readable, in order, with their own causes - which is what
	// "three failures read as three" means.
	m, _ = send(t, m, key("e"))
	detail := renderFrame(t, m)
	for _, want := range []string{
		"1. Live stream failed",
		"2. Snapshot view failed",
		"3. Attach failed",
		"the runtime is not reachable",
		"another agent holds the pane",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail view is missing %q; frame:\n%s", want, detail)
		}
	}
	// The order is the chain's own order, oldest first, and each numbered row
	// carries its own cause - not one shared cause repeated.
	if i, j := strings.Index(detail, "1. Live stream failed"), strings.Index(detail, "3. Attach failed"); i < 0 || j < 0 || i > j {
		t.Errorf("detail view does not list the steps in chain order")
	}
	// Each numbered row carries its own cause: the middle step's cause is its
	// own text, not the first step's repeated. ("the runtime is not reachable"
	// appears once for the stream and once for the attach; the snapshot's is
	// different, which is the whole point of keeping the chain.)
	for _, want := range []string{
		"Cause            the runtime is not reachable",
		"Cause            another agent holds the pane",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail view does not carry %q; frame:\n%s", want, detail)
		}
	}
	if strings.Count(detail, "Cause            the runtime is not reachable") != 2 {
		t.Errorf("detail view does not carry one cause per failure; frame:\n%s", detail)
	}
	// The one line carries the last step's own evidence and says how many are
	// behind it, so the reader knows the earlier two causes exist (and that
	// 'e' is where they are) - which is what "three read as three" needs from
	// a one-line surface.
	if !strings.Contains(m.msg.text, "+2 earlier") {
		t.Errorf("the one-line summary does not say how many earlier failures there were: %q", m.msg.text)
	}
}

// TestTheNextStepFollowsTheEvidenceOnly is the fourth requirement. Every next
// step comes from the error's own code, and none of them may infer the
// agent's lifecycle from a failed open - the regression this whole file
// exists to prevent is a reader being told a live Crew is dead.
func TestTheNextStepFollowsTheEvidenceOnly(t *testing.T) {
	cases := []struct {
		name string
		step sessionFailureStep
		err  error
		want string
	}{
		{"timeout is retryable", stepLiveStream, observability.NewError(observability.CodeTimeout, "no answer"), "retryable"},
		{"deadline is retryable", stepLiveStream, readDeadline, "retryable"},
		{"runtime down", stepSnapshotView, observability.NewError(observability.CodeRuntimeUnavailable, "server shut down"), "check the runtime is running"},
		{"pane held is a conflict", stepAttach, observability.NewError(observability.CodeTargetBlocked, "pane held"), "conflict"},
		{"state moved", stepAttach, observability.NewError(observability.CodeStateConflict, "moved"), "re-read"},
		{"repair", stepAttach, observability.NewError(observability.CodeNeedsRepair, "dirty"), "mate doctor"},
		{"name may be stale", stepAttach, observability.NewError(observability.CodeNotFound, "gone"), "recorded name may be out of date"},
		{"undetermined", stepSnapshotView, errors.New("something"), "cause undetermined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := classified(t, tc.step, tc.err)
			if !strings.Contains(f.Next, tc.want) {
				t.Fatalf("next step = %q, want it to contain %q", f.Next, tc.want)
			}
		})
	}

	// The negative half, over every failure shape this Console can produce:
	// no authored text may say the agent is dead, stopped or gone, because a
	// failed attach cannot establish any of that (ADR 0025; issue #60).
	errs := []error{
		observability.NewError(observability.CodeNotFound, "agent not found"),
		observability.NewError(observability.CodeTargetBlocked, "pane held"),
		observability.NewError(observability.CodeRuntimeUnavailable, "server shut down"),
		observability.NewError(observability.CodeUnknown, "?"),
		observability.NewError(observability.CodeNeedsRepair, "dirty"),
		exitErr(t, 1), exitErr(t, 2), exitErr(t, 10), exitErr(t, 20), exitErr(t, 30), exitErr(t, 31), exitErr(t, 40),
		errors.New("plain"),
		readDeadline,
	}
	for _, step := range []sessionFailureStep{stepLiveStream, stepSnapshotView, stepAttach} {
		for _, err := range errs {
			f := classifyOpenFailure(step, "agent", "crew_1", err)
			text := f.Cause + " " + f.Next
			for _, claim := range []string{"is dead", "has died", "died", "is stopped", "was stopped", "no longer running", "has exited"} {
				if strings.Contains(strings.ToLower(text), claim) {
					t.Errorf("step %v, err %v: authored text claims %q: %q", step, err, claim, text)
				}
			}
		}
	}
}

// TestTheAttachExitTableIsTheOneSourceForExitFacts replaced the counter-
// review's three-table arrangement (N4): the exit-keyed facts a reader meets
// come from one table, so the same exit cannot be explained two ways on two
// paths. What is pinned here is what the table claims, not that two tables
// agree.
func TestTheAttachExitTableIsTheOneSourceForExitFacts(t *testing.T) {
	// Every exit the CLI publishes has a phrase, so no exit-only failure falls
	// through to the bare "mate attach exited N".
	published := map[int]string{
		observability.ExitGeneric:            "generic failure (several codes)",
		observability.ExitUsage:              "usage",
		observability.ExitStateConflict:      "state conflict (several codes)",
		observability.ExitRuntimeUnavailable: "runtime unavailable",
		observability.ExitTargetBlocked:      "target blocked",
		observability.ExitInteractionWait:    "interaction wait (several codes)",
		observability.ExitNeedsRepair:        "needs repair",
	}
	for exit, what := range published {
		facts, ok := attachExits[exit]
		if !ok {
			t.Errorf("exit %d (%s) has no entry in attachExits", exit, what)
			continue
		}
		if facts.Cause == "" {
			t.Errorf("exit %d has no plain-language cause", exit)
		}
		// One phrase per code, authored once: when the exit names a code, the
		// phrase must be that code's own phrase, or the same code would be
		// explained differently depending on whether it arrived as a code or
		// as an exit.
		if facts.Code != "" {
			if want := openFailureCauses[facts.Code]; want != facts.Cause {
				t.Errorf("exit %d names %s but phrases it %q; the code's own phrase is %q", exit, facts.Code, facts.Cause, want)
			}
		}
	}
	// The four the contract calls unambiguous must carry a code: that is what
	// lets an exit-only failure still reach the reader as a taxonomy code.
	for _, exit := range []int{observability.ExitUsage, observability.ExitRuntimeUnavailable,
		observability.ExitTargetBlocked, observability.ExitNeedsRepair} {
		if attachExits[exit].Code == "" {
			t.Errorf("exit %d names exactly one code but attachExits records none", exit)
		}
	}
	// And the shared ones must not: deriving a code from them would be the
	// same overclaim as guessing whether the agent is alive.
	for _, exit := range []int{observability.ExitGeneric, observability.ExitStateConflict,
		observability.ExitInteractionWait} {
		if code := attachExits[exit].Code; code != "" {
			t.Errorf("exit %d is shared by several codes but attachExits derives %s", exit, code)
		}
	}
	// No exit's phrase is the old label it replaced: those labels restated
	// either the code or nothing, and the counter-review found the redundancy
	// (N1) was costing the cells the message needs.
	for exit, label := range map[int]string{
		observability.ExitGeneric:         "generic failure",
		observability.ExitStateConflict:   "state conflict",
		observability.ExitInteractionWait: "interaction wait",
	} {
		if strings.EqualFold(attachExits[exit].Cause, label) {
			t.Errorf("exit %d's cause is the bare label %q it replaced", exit, label)
		}
	}
}

// TestALongFailureLineIsCutWithAVisibleMark: the message line is cut to the
// frame's width like every other line, but the cut is marked. An unmarked
// abbreviation reads as a complete statement, which is how a reader ends up
// trusting half of someone else's error text.
func TestALongFailureLineIsCutWithAVisibleMark(t *testing.T) {
	long := strings.Repeat("herdr: the attach command failed because ", 4)
	// A report with a message: the message has no code of its own to be
	// explained by, so it is the cause (the shape issue #60 produced), and it
	// is arbitrary text - which is exactly the case the cut exists for.
	f := classified(t, stepAttach, &attachReportError{
		Cause:   exitErr(t, observability.ExitGeneric),
		Message: long,
	})
	if len(f.Cause) <= 100 {
		t.Fatalf("fixture bug: cause is %d cells, too short to force a cut", len(f.Cause))
	}
	m := attachFixture(t, &attachSpy{tree: sameTree(sampleTree())}, 60, 16, unicodeGlyphs)
	m.openFailures = append(m.openFailures, f)
	m.msg = m.openFailureMsg()
	frame := renderFrame(t, m) // asserts exactly 16 lines of exactly 60 cells

	lines := strings.Split(frame, "\n")
	message := lines[len(lines)-2]
	if want := m.g.Ellipsis; !strings.Contains(message, want) {
		t.Fatalf("the cut message line does not mark its cut with %q: %q", want, message)
	}
	// The one thing that must survive the cut is the step and the beginning
	// of the cause, which is what the reader needs first.
	if !strings.Contains(message, "Attach failed: herdr: the attach command") {
		t.Fatalf("the cut removed the cause from the start of the line: %q", message)
	}
}

// TestTheDetailsKeyIsOfferedOnlyWhenThereIsSomethingToSee keeps the key line
// honest: a key that does nothing is indistinguishable from a lost key, so
// 'e' appears only while the chain is non-empty, and a deliberate new open
// starts a new chain rather than carrying the last one's failures into it.
func TestTheDetailsKeyIsOfferedOnlyWhenThereIsSomethingToSee(t *testing.T) {
	m := attachFixture(t, &attachSpy{tree: sameTree(sampleTree())}, 160, 48, unicodeGlyphs)
	if keys := m.keysLine(layout(m.w, m.h)); strings.Contains(keys.render(m.w), "Details") {
		t.Fatalf("the key line offers the details view with no failure recorded: %q", keys.render(m.w))
	}
	// The key is bound but does nothing, and nothing is drawn.
	if m2, _ := send(t, m, key("e")); m2.failureDetail {
		t.Fatal("e opened an empty detail view")
	}

	m, _ = send(t, m, key("enter")) // open the Project; Mate row selected
	m = m.recordOpenFailure(stepAttach, "payments-api-mate", "mate_1",
		observability.NewError(observability.CodeRuntimeUnavailable, "server shut down"))
	m.msg = m.openFailureMsg()
	if keys := m.keysLine(layout(m.w, m.h)); !strings.Contains(keys.render(m.w), "Details") {
		t.Fatalf("the key line does not offer the details view after a failure: %q", keys.render(m.w))
	}

	// A deliberate open (Enter on the row) starts a new chain: the previous
	// failure must not be counted as an earlier step of this one.
	before := len(m.openFailures)
	if before == 0 {
		t.Fatal("fixture bug: no failure recorded")
	}
	m, _ = send(t, m, key("enter"))
	if len(m.openFailures) != 0 {
		t.Fatalf("a new open kept %d earlier failure(s); the chain must start fresh", len(m.openFailures))
	}
	if m.failureDetail {
		t.Error("a new open left the detail view open")
	}
}

// TestOpeningTheDetailsViewDoesNotEraseTheFailure pins the counter-review's
// N2: 'e' is how a reader sees more of a failure, so it must not be what
// removes the only sign on the frame that anything failed - before, during or
// after.
func TestOpeningTheDetailsViewDoesNotEraseTheFailure(t *testing.T) {
	f := classified(t, stepAttach, observability.NewError(observability.CodeRuntimeUnavailable, "herdr: server shut down"))
	m := failureModel(t, f)
	m.msg = m.openFailureMsg()
	summary := m.msg.text
	if summary == "" {
		t.Fatal("fixture bug: the failure summary is empty")
	}

	m, _ = send(t, m, key("e"))
	if m.msg.text != summary {
		t.Fatalf("opening the detail view changed the summary line: %q -> %q", summary, m.msg.text)
	}
	if !strings.Contains(renderFrame(t, m), summary) {
		t.Fatal("the summary is not on the frame while the detail view is open")
	}

	m, _ = send(t, m, key("esc"))
	if m.msg.text != summary {
		t.Fatalf("closing the detail view lost the failure: %q -> %q", summary, m.msg.text)
	}
	// And the key that reopens it is still advertised, so the reader can get
	// back to what they just read.
	if keys := m.keysLine(layout(m.w, m.h)); !strings.Contains(keys.render(m.w), "Details") {
		t.Fatalf("the key line no longer offers the details view: %q", keys.render(m.w))
	}
}

// TestARefusalStillNamesTheEarlierFailures pins the counter-review's N3: an
// attach refusal is the one line in this flow the chain did not build, and
// before this it silently hid the failures behind it. The path is beginAttach
// with a chain already recorded - a re-read made the binding stale between the
// stream failing and the hand-off being attempted - which is exactly where the
// chain is longest and the refusal is least informative on its own.
func TestARefusalStillNamesTheEarlierFailures(t *testing.T) {
	staleMate := func() query.Snapshot {
		tree := sampleTree()
		tree.Projects[0].Mate.Binding = query.KnownField(query.BindingValue{
			Status: query.BindingStale, AgentName: "mate-payments-api",
		})
		return tree
	}
	// m.w is the frame width the marker has to fit inside; refusal messages
	// never expand to fill it, so both widths can be checked with one fixture
	// builder.
	build := func(t *testing.T, w int) Model {
		t.Helper()
		m := attachFixture(t, &attachSpy{tree: sameTree(staleMate())}, w, 24, unicodeGlyphs)
		m, _ = send(t, m, key("enter")) // open the Project; the Mate row is selected
		m = m.recordOpenFailure(stepLiveStream, "payments-api-mate", "mate_1",
			observability.NewError(observability.CodeRuntimeUnavailable, "herdr: server shut down"))
		m = m.recordOpenFailure(stepSnapshotView, "payments-api-mate", "mate_1",
			observability.NewError(observability.CodeTargetBlocked, "pane p3 is held by another agent"))
		return m
	}

	m := build(t, 160)
	r, ok := m.selectedRow()
	if !ok || r.kind != rowMate {
		t.Fatalf("selected row = %+v (ok=%v), want the Mate row", r, ok)
	}
	if _, refused := m.attachRefusal(r); !refused {
		t.Fatal("fixture bug: a stale recorded binding must be refused")
	}
	m, _ = m.beginAttach(r)
	if !strings.Contains(m.msg.text, "Attach refused") {
		t.Fatalf("the stale binding was not refused; message = %q", m.msg.text)
	}
	if !strings.Contains(m.msg.text, "+2 earlier") {
		t.Fatalf("the refusal hides the two failures behind it: %q", m.msg.text)
	}
	if frame := renderFrame(t, m); !strings.Contains(frame, "+2 earlier") {
		t.Fatalf("the frame does not carry the earlier-failure marker; frame:\n%s", frame)
	}

	// At 80 columns the marker is the fact that yields, not the refusal's own
	// reason or its "nothing started" clause: the reader still learns why
	// nothing was attempted (PR #72's counter-review B1).
	narrow := build(t, 80)
	r, _ = narrow.selectedRow()
	narrow, _ = narrow.beginAttach(r)
	for _, want := range []string{"Attach refused", "binding recorded stale", "nothing started"} {
		if !strings.Contains(narrow.msg.text, want) {
			t.Fatalf("at 80 columns the refusal no longer says %q: %q", want, narrow.msg.text)
		}
	}
	if got := cells(" ! " + narrow.msg.text); got > 80 {
		t.Fatalf("the refusal is %d cells wide at an 80-cell frame: %q", got, narrow.msg.text)
	}
}

// TestAFailureMessageCannotAddAFrameLine is the frame contract applied to the
// one string in the Console that is arbitrary recorded text: the error's own
// message. It is drawn through line.add like everything else, so a newline in
// it cannot add a line to View.
func TestAFailureMessageCannotAddAFrameLine(t *testing.T) {
	f := classified(t, stepAttach, &attachReportError{
		Cause:   exitErr(t, observability.ExitNeedsRepair),
		Message: "line one\nline two\nline three",
	})
	m := failureModel(t, f)
	m.openFailures = m.openFailures[:len(m.openFailures)-1]
	m.openFailures = append(m.openFailures, f)
	m.msg = m.openFailureMsg()

	// The summary carries only the cause, and the cause never carries the
	// message, so nothing arbitrary reaches the one-line message here - but
	// the detail view draws the message, and its shape is what must hold.
	m, _ = send(t, m, key("e"))
	frame := renderFrame(t, m) // asserts exactly h lines of exactly w cells
	if !strings.Contains(frame, "line one line two line three") {
		t.Errorf("the newlines in the recorded message were not folded to one line; frame:\n%s", frame)
	}
}

// TestTheFailureLineNamesTheModeTheReaderIsInNow covers the clause the
// fallbacks add: after the snapshot view fails, the reader is in the classic
// hand-off, and the line says so - read live, not stored on the failure.
func TestTheFailureLineNamesTheModeTheReaderIsInNow(t *testing.T) {
	stale := sampleTree()
	m := attachFixture(t, &attachSpy{tree: sameTree(stale)}, 160, 48, unicodeGlyphs)
	m = m.recordOpenFailure(stepSnapshotView, "payments-api-mate", "mate_1",
		observability.NewError(observability.CodeRuntimeUnavailable, "server shut down"))
	if got := m.openFailureLine(true); !strings.Contains(got, "no session open") {
		t.Fatalf("line = %q, want the mode the reader is actually in", got)
	}
	// In the hand-off the mode clause names the hand-off instead.
	m.att = attachFlow{phase: attachHeld, target: attachTargetRef{kind: rowMate, id: "mate_1", label: "payments-api-mate"}}
	if got := m.openFailureLine(true); !strings.Contains(got, "mate attach") {
		t.Fatalf("line = %q, want it to name the hand-off", got)
	}
}

// readDeadline is a wrapped read deadline: the shape a caller gets from a
// context-bound read, used by the sites above where the point is that it is a
// deadline and not a cancel. isDeadline accepts it alongside
// os.ErrDeadlineExceeded.
var readDeadline = fmt.Errorf("read: %w", context.DeadlineExceeded)

// TestGoldenFailureDetailFrames pins the detail view at the two breakpoints
// the design has galleries for. The overlay is a full-region surface, and
// this package's convention is that one gets a pinned frame: a layout or
// emphasis change is invisible in a substring assertion, which is all the
// other tests in this file use. The fixture is the three-step chain (live
// stream, snapshot, attach) the fallback contract is written around, so a
// regression that drops a step, a field or the footer sentence shows up as a
// byte diff a reviewer can read.
//
// Accept a deliberate change with:
//
//	go test ./internal/ui/console -run TestGoldenFailureDetailFrames -update
func TestGoldenFailureDetailFrames(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"session-failure-detail-120x36-unicode", 120, 36},
		{"session-failure-detail-80x24-unicode", 80, 24},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := threeStepFailureModel(t, tc.w, tc.h)
			m, _ = send(t, m, key("e"))
			if !m.failureDetail {
				t.Fatal("e did not open the detail view")
			}
			assertGolden(t, tc.name, renderFrame(t, m))
		})
	}
}
