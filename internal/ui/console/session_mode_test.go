package console

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// recordingSessionController is a minimal, mutex-protected SessionReader/
// SessionPrompt/SessionClose triple a test can script directly - simpler
// than FakeSessionController (session_fake.go) for the handful of tests
// here that care about call counts and errors more than seeded snapshots.
type recordingSessionController struct {
	mu sync.Mutex

	reads      int
	readErr    error
	snap       SessionSnapshot
	lastTarget SessionTarget

	prompts   []string
	promptErr error

	closed int
}

func (c *recordingSessionController) Read(_ context.Context, target SessionTarget) (SessionSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	c.lastTarget = target
	if c.readErr != nil {
		return SessionSnapshot{}, c.readErr
	}
	snap := c.snap
	snap.Target = target
	return snap, nil
}

func (c *recordingSessionController) lastTranscriptCapacity() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastTarget.TranscriptCapacity
}

func (c *recordingSessionController) Prompt(_ context.Context, _ SessionTarget, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prompts = append(c.prompts, text)
	return c.promptErr
}

func (c *recordingSessionController) Close(context.Context, SessionTarget) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
	return nil
}

func (c *recordingSessionController) readCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *recordingSessionController) closedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *recordingSessionController) sentPrompts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.prompts))
	copy(out, c.prompts)
	return out
}

// sessionFixture builds a Console on sampleTree, sized and positioned on the
// payments-api Project frame - whose Mate row (index 0) is a Known,
// OccupiesActiveSlot Mate, per sampleTree's own fixture (golden_test.go).
func sessionFixture(t *testing.T, ctrl *recordingSessionController) Model {
	t.Helper()
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := New(spy.load, spy.attach).WithSession(ctrl.Read, ctrl.Prompt, ctrl.Close)
	m.g = unicodeGlyphs
	m.p = plainPalette()
	// A fixed 1ms override: these tests replay the tick chain synchronously
	// by calling the returned Cmds directly, so the real 300-500ms cadence
	// would only slow the suite down without adding coverage.
	m.sessionPollIntervalOverride = time.Millisecond
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 48})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter")) // open the payments-api Project; Mate row selected
	return m
}

func mateSessionRow(t *testing.T, m Model) row {
	t.Helper()
	r, ok := m.selectedRow()
	if !ok || r.kind != rowMate {
		t.Fatalf("selected row = %+v, ok=%v, want the Mate row", r, ok)
	}
	return r
}

// ---------- entering session mode ----------

// TestEntryReadAndPollTicksCarryTranscriptCapacityFromWindowSize proves the
// SessionReader actually receives a bound derived from the Console's own
// live window size, not the zero value or a value frozen at session entry:
// sessionFixture sizes the Console at 160x48 before Enter, so both the
// entry read and the first poll tick's read must carry
// SessionTranscriptCapacity(SessionTargetMate, 160, 48); a resize while
// session mode is active must be reflected on the very next tick, matching
// view.go's own RenderSessionFrame(..., m.w, m.h, ...) reading the live
// window size on every draw rather than a value captured once.
func TestEntryReadAndPollTicksCarryTranscriptCapacityFromWindowSize(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := sessionFixture(t, ctrl)
	want := SessionTranscriptCapacity(SessionTargetMate, 160, 48)
	if want <= 0 {
		t.Fatalf("setup: want a positive capacity at 160x48, got %d", want)
	}

	m, cmd := send(t, m, key("enter"))
	entryMsg := cmd().(sessionSnapshotMsg)
	if got := ctrl.lastTranscriptCapacity(); got != want {
		t.Fatalf("entry read TranscriptCapacity = %d, want %d", got, want)
	}

	m, tickCmd := send(t, m, entryMsg)
	tickMsg := tickCmd().(sessionTickMsg)
	m, readCmd := send(t, m, tickMsg)
	readCmd()
	if got := ctrl.lastTranscriptCapacity(); got != want {
		t.Fatalf("poll tick TranscriptCapacity = %d, want %d", got, want)
	}

	// Resize while session mode is active; the next tick must reflect it.
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	wantAfterResize := SessionTranscriptCapacity(SessionTargetMate, 80, 24)
	if wantAfterResize == want {
		t.Fatalf("setup: 80x24 must yield a different capacity than 160x48 for this to be a meaningful check")
	}
	_, readCmd = send(t, m, sessionTickMsg{gen: m.sess.gen})
	readCmd()
	if got := ctrl.lastTranscriptCapacity(); got != wantAfterResize {
		t.Fatalf("TranscriptCapacity after resize = %d, want %d (recomputed from the live window size)", got, wantAfterResize)
	}
}

func TestEnterEntersSessionModeWhenAvailable(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
	}}
	m := sessionFixture(t, ctrl)

	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatalf("beginSession returned a nil Cmd; the entry read never happens")
	}
	msg := cmd()
	snapMsg, ok := msg.(sessionSnapshotMsg)
	if !ok {
		t.Fatalf("entry Cmd produced %T, want sessionSnapshotMsg", msg)
	}

	m, cmd = send(t, m, snapMsg)
	if m.sess.phase != sessionActive {
		t.Fatalf("phase = %v, want sessionActive after a successful entry read", m.sess.phase)
	}
	if cmd == nil {
		t.Fatalf("entering session mode did not schedule the poll ticker")
	}
	if ctrl.readCount() != 1 {
		t.Fatalf("reads = %d, want 1 (only the entry read so far)", ctrl.readCount())
	}
}

func TestSessionModeRequiresResolvableTargetAndRefusesUnresolvedRow(t *testing.T) {
	ctrl := &recordingSessionController{}
	m := sessionFixture(t, ctrl)
	m.tree.Projects[0].Mate.Designated = query.UnknownField[query.MateIdentity]("mate identity unavailable")

	m, _ = send(t, m, key("enter"))
	if m.sess.phase == sessionActive {
		t.Fatalf("session mode became active without a resolvable target")
	}
	if m.attachHoldsTerminal() {
		t.Fatalf("an unresolved target must not start the classic hand-off")
	}
	if m.msg.text == "" {
		t.Fatalf("an unresolved target must report a refusal")
	}
	if ctrl.readCount() != 0 {
		t.Fatalf("reads = %d, want 0: an unresolved target must never reach SessionReader", ctrl.readCount())
	}
}

func TestSessionReaderNilKeepsClassicHandoff(t *testing.T) {
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := New(spy.load, spy.attach) // WithSession never called
	m.g = unicodeGlyphs
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 48})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter"))

	m, _ = send(t, m, key("enter"))
	if m.sess.phase == sessionActive {
		t.Fatalf("session mode activated with no SessionReader wired at all")
	}
	if !m.attachHoldsTerminal() {
		t.Fatalf("a Console built without WithSession must always use the classic hand-off")
	}
}

// ---------- fallback on entry-read failure ----------

func TestEntryReadFailureFallsBackToClassicHandoffAndSaysWhy(t *testing.T) {
	ctrl := &recordingSessionController{
		readErr: observability.NewError(observability.CodeRuntimeUnavailable, "herdr server not running"),
	}
	m := sessionFixture(t, ctrl)

	m, cmd := send(t, m, key("enter"))
	msg := cmd().(sessionSnapshotMsg)
	m, cmd = send(t, m, msg)

	if m.sess.phase == sessionActive {
		t.Fatalf("session mode became active despite the entry read failing")
	}
	if !m.attachHoldsTerminal() {
		t.Fatalf("an entry-read failure must fall back to the classic mate attach hand-off")
	}
	if !containsSubstring(m.msg.text, "runtime_unavailable") {
		t.Fatalf("fallback message %q does not name the real taxonomy code", m.msg.text)
	}
	// Cause first, code second (session_failure.go): the reader must be told
	// what happened before being handed a code, and must know which step
	// failed and where the Console went instead.
	if !containsSubstring(m.msg.text, "the runtime is not reachable") {
		t.Fatalf("fallback message %q does not say what happened in plain language", m.msg.text)
	}
	if !containsSubstring(m.msg.text, "Snapshot view failed") {
		t.Fatalf("fallback message %q does not say which step failed", m.msg.text)
	}
	if !containsSubstring(m.msg.text, "mate attach") {
		t.Fatalf("fallback message %q does not say what the Console fell back to", m.msg.text)
	}
	if cmd == nil {
		t.Fatalf("fallback did not queue the classic attach hand-over Cmd")
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

// ---------- polling: cadence, and cancellation on leaving ----------

// TestSessionPollingChainStopsWhenLeavingSessionMode proves - rather than
// merely asserts - that leaving session mode stops the poll chain: a tick
// scheduled before Esc is fed in after Esc and must produce no further
// read, and a read result that was already in flight when Esc landed must
// not be applied or rescheduled either. Bubble Tea 1.2.4 gives a Cmd no way
// to be cancelled outright, so the generation fence is the only thing that
// can make this true; asserting the fence field directly would not prove
// the chain actually stops end-to-end the way replaying these two messages
// does.
func TestSessionPollingChainStopsWhenLeavingSessionMode(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := sessionFixture(t, ctrl)

	m, cmd := send(t, m, key("enter"))
	entryMsg := cmd().(sessionSnapshotMsg)
	m, tickCmd := send(t, m, entryMsg)
	if m.sess.phase != sessionActive {
		t.Fatalf("setup: session mode did not become active")
	}
	tickMsg, ok := tickCmd().(sessionTickMsg)
	if !ok {
		t.Fatalf("setup: expected a scheduled sessionTickMsg")
	}

	// A second poll cycle in flight: the tick already produced its read Cmd.
	m2, readCmd := send(t, m, tickMsg)
	if readCmd == nil {
		t.Fatalf("setup: the tick did not start a poll read")
	}
	inFlightRead := readCmd().(sessionSnapshotMsg)

	// The reader leaves session mode.
	m2, _ = send(t, m2, key("esc"))
	if m2.sess.phase == sessionActive {
		t.Fatalf("Esc did not leave session mode")
	}
	before := ctrl.readCount()

	// The in-flight read from before Esc lands late.
	m2, cmd = send(t, m2, inFlightRead)
	if cmd != nil {
		if _, isSnapshot := cmd().(sessionSnapshotMsg); isSnapshot {
			t.Fatalf("a stale in-flight read was applied and rescheduled a new poll after leaving session mode")
		}
	}
	if m2.sess.phase == sessionActive {
		t.Fatalf("a stale in-flight read reactivated session mode")
	}

	// A tick that was already scheduled before Esc also lands late.
	m2, cmd = send(t, m2, tickMsg)
	if cmd != nil {
		t.Fatalf("a stale tick after leaving session mode scheduled a new read")
	}
	if ctrl.readCount() != before {
		t.Fatalf("reads = %d, want %d: a stale tick must not reach SessionReader", ctrl.readCount(), before)
	}
}

func TestSessionPollFailureShowsUnknownWithoutFallingBackOrMutatingRecordedStatus(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
	}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd().(sessionSnapshotMsg))
	tickMsg := cmd().(sessionTickMsg)

	// The next poll fails.
	ctrl.mu.Lock()
	ctrl.readErr = observability.NewError(observability.CodeTimeout, "read timed out")
	ctrl.mu.Unlock()
	m, cmd = send(t, m, tickMsg)
	failMsg := cmd().(sessionSnapshotMsg)
	m, cmd = send(t, m, failMsg)

	if m.sess.phase != sessionActive {
		t.Fatalf("a poll failure must not fall back to the classic hand-off mid-session")
	}
	if m.sess.snapshot.Runtime.Status != query.Unknown {
		t.Fatalf("Runtime.Status = %v, want Unknown after a failed poll", m.sess.snapshot.Runtime.Status)
	}
	if !containsSubstring(m.sess.snapshot.Runtime.Reason, "timeout") {
		t.Fatalf("Runtime.Reason %q does not carry the real taxonomy code", m.sess.snapshot.Runtime.Reason)
	}
	if m.sess.snapshot.RecordedStatus.Value != "running" {
		t.Fatalf("RecordedStatus was mutated by a runtime poll failure: %+v", m.sess.snapshot.RecordedStatus)
	}
	if cmd == nil {
		t.Fatalf("a poll failure must still reschedule the next tick")
	}
}

// ---------- Esc / Ctrl+b q: leave without stopping the agent ----------

func TestEscLeavesSessionModeAndCallsSessionClose(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd().(sessionSnapshotMsg))

	m, closeCmd := send(t, m, key("esc"))
	if m.sess.phase == sessionActive {
		t.Fatalf("Esc did not leave session mode")
	}
	if closeCmd == nil {
		t.Fatalf("leaving session mode did not queue SessionClose")
	}
	closeCmd() // run it synchronously; the fixture's Close is not a subprocess
	if ctrl.closedCount() != 1 {
		t.Fatalf("SessionClose was called %d times, want 1", ctrl.closedCount())
	}
}

// TestEscFromBoxFocusLeavesSessionModeWithoutStoppingTheAgent is the detach
// the Ctrl+b prefix used to carry: with the box focused, Esc is the way back
// to the project frame. Leaving is never a stop.
func TestEscFromBoxFocusLeavesSessionModeWithoutStoppingTheAgent(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd().(sessionSnapshotMsg))

	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	if m.sess.zone != zoneBox {
		t.Fatalf("F2 did not focus the box (zone %v)", m.sess.zone)
	}
	m, _ = send(t, m, key("esc"))
	if m.sess.phase == sessionActive {
		t.Fatalf("Esc under box focus did not leave session mode")
	}
}

// TestAKeyTheBoxDoesNotBindIsSwallowedUnderBoxFocus: nothing reaches the
// composer or the PTY while the box has focus, so an unbound key changes
// nothing rather than landing somewhere the reader cannot see.
func TestAKeyTheBoxDoesNotBindIsSwallowedUnderBoxFocus(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd().(sessionSnapshotMsg))

	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	m, _ = send(t, m, key("x"))
	if m.sess.phase != sessionActive {
		t.Fatalf("an unbound key under box focus left session mode")
	}
	if m.sess.composer != "" {
		t.Fatalf("composer = %q, want empty: a key under box focus never reaches the composer", m.sess.composer)
	}
}

func TestQWhileSessionActiveIsComposerInputNotQuit(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd().(sessionSnapshotMsg))

	m, _ = send(t, m, key("q"))
	if m.quitting {
		t.Fatalf("q quit the Console while the composer owns the keyboard")
	}
	if m.sess.composer != "q" {
		t.Fatalf("composer = %q, want %q", m.sess.composer, "q")
	}
}

// ---------- composer: send through SessionPrompt ----------

func TestComposerSendsThroughSessionPromptAndClears(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd().(sessionSnapshotMsg))

	for _, r := range "hello" {
		m, _ = send(t, m, key(string(r)))
	}
	if m.sess.composer != "hello" {
		t.Fatalf("composer = %q, want %q", m.sess.composer, "hello")
	}
	m, sendCmd := send(t, m, key("enter"))
	if m.sess.composer != "" {
		t.Fatalf("composer was not cleared after Enter: %q", m.sess.composer)
	}
	if sendCmd == nil {
		t.Fatalf("Enter did not queue a SessionPrompt call")
	}
	sentMsg := sendCmd().(sessionPromptSentMsg)
	m, _ = send(t, m, sentMsg)

	if got := ctrl.sentPrompts(); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("sent prompts = %v, want [\"hello\"]", got)
	}
}

func TestComposerBackspaceTrimsLastCharacter(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd().(sessionSnapshotMsg))

	for _, r := range "hi" {
		m, _ = send(t, m, key(string(r)))
	}
	m, _ = send(t, m, key("backspace"))
	if m.sess.composer != "h" {
		t.Fatalf("composer = %q, want %q", m.sess.composer, "h")
	}
}

func TestPromptFailureReportsRealTaxonomyNotGenericFailure(t *testing.T) {
	ctrl := &recordingSessionController{
		snap:      SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}},
		promptErr: observability.NewError(observability.CodePermission, "prompt refused"),
	}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd().(sessionSnapshotMsg))
	m, _ = send(t, m, key("h"))
	m, sendCmd := send(t, m, key("enter"))

	sentMsg := sendCmd().(sessionPromptSentMsg)
	m, _ = send(t, m, sentMsg)
	if !containsSubstring(m.msg.text, "permission") {
		t.Fatalf("prompt failure message %q does not carry the real taxonomy code", m.msg.text)
	}
}

// ---------- errors package sanity ----------

func TestSessionErrorReasonUnwrapsCodedErrors(t *testing.T) {
	err := observability.WrapError(observability.CodeNotFound, "agent gone", errors.New("agent_not_found"))
	got := sessionErrorReason(err)
	if !containsSubstring(got, "not_found") {
		t.Fatalf("sessionErrorReason(%v) = %q, want it to carry not_found", err, got)
	}
}

// ---------- the key line names what Enter does (B2 of PR #72's review) ----------

// TestKeyLineNamesWhatEnterDoesForAMateRow is B2 of PR #72's counter-review:
// the key line said "Enter Attach mate" unconditionally, advertising the panel
// the Agent View replaced, and gated "(unavailable)" on attachRefusal even
// after that stopped deciding Enter. Enter and the key line now read the same
// predicate (sessionAvailableFor), so the label cannot drift from the action,
// and a row the view cannot open is the only one that reads as attach - which
// is exactly what Enter then attempts, and refuses with this suffix. The line
// is rendered, not just enterLabel called, because a label nothing draws
// proves nothing.
func TestKeyLineNamesWhatEnterDoesForAMateRow(t *testing.T) {
	build := func(t *testing.T, tree query.Snapshot) Model {
		t.Helper()
		ctrl := &recordingSessionController{snap: SessionSnapshot{
			RecordedStatus: query.KnownField("running"),
			Runtime:        SessionRuntime{Status: query.Known},
		}}
		spy := &attachSpy{tree: sameTree(tree)}
		m := New(spy.load, spy.attach).WithSession(ctrl.Read, ctrl.Prompt, ctrl.Close)
		m.g = unicodeGlyphs
		m.p = plainPalette()
		m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 48})
		m, _ = send(t, m, m.Init()())
		m, _ = send(t, m, key("enter")) // the payments-api Project; Mate row selected
		return m
	}

	t.Run("the view opens: Enter opens the agent view", func(t *testing.T) {
		m := build(t, sampleTree())
		r, ok := m.selectedRow()
		if !ok || r.kind != rowMate {
			t.Fatalf("selected row = %+v (ok=%v), want the Mate row", r, ok)
		}
		line := m.keysLine(layout(160, 48)).render(160)
		if !strings.Contains(line, "Enter Open agent view") {
			t.Fatalf("key line %q does not name the Agent View Enter opens", line)
		}
		if strings.Contains(line, "Attach mate") {
			t.Fatalf("key line %q still advertises the hand-off the Agent View replaced", line)
		}
		m, cmd := send(t, m, key("enter"))
		if cmd == nil {
			t.Fatalf("the key line named an action Enter cannot perform")
		}
	})

	t.Run("the view cannot open: Enter falls back and the label says so", func(t *testing.T) {
		tree := sampleTree()
		tree.Projects[0].Mate.Designated.Value.Status = query.MateStopped
		m := build(t, tree)
		r, ok := m.selectedRow()
		if !ok || r.kind != rowMate {
			t.Fatalf("selected row = %+v (ok=%v), want the Mate row", r, ok)
		}
		if got := m.enterLabel(r); got != "Attach mate (unavailable)" {
			t.Fatalf("enterLabel = %q, want %q", got, "Attach mate (unavailable)")
		}
		if line := m.keysLine(layout(160, 48)).render(160); !strings.Contains(line, "Attach mate (unavailable)") {
			t.Fatalf("key line %q does not mark the attach unavailable up front", line)
		}
	})
}
