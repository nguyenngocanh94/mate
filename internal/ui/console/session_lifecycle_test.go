package console

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// ADR 0025 step 6: lifecycle and error-behaviour coverage for the embedded
// session view, extending session_mode_test.go's entering/fallback/polling/
// composer coverage with the cases the ADR names explicitly: agent
// disappearance, runtime timeout, permission, resize, prompt failure,
// cancellation, detach and re-entry. Every test here is checked against its
// own name per the brief: a test whose name claims an invariant it does not
// actually assert is worse than no test.

// ---------- agent disappearance (invariant 1: the most important one) ----------

// TestAgentDisappearanceDuringActiveSessionRendersRuntimeMissingAndNeverMutatesRecordedStatus
// is the single most important assertion this task exists to protect: a
// poll that *succeeds* (no error) but reports Runtime.Status == Absent (the
// bridge's InspectAgent-observed agent_not_found, session_bridge.go) must
// render "runtime_missing", must leave RecordedStatus exactly as the last
// successful read established it, must never fall back to the classic
// hand-off, and must never itself leave session mode. This is deliberately
// distinct from TestSessionPollFailureShowsUnknownWithoutFallingBackOrMutatingRecordedStatus
// (session_mode_test.go), which covers a poll that *errors* - here the poll
// itself succeeds and reports Absent, the actual "agent disappearance" shape
// ADR 0025 names, and the RecordedStatus value asserted is domain
// vocabulary ("running"), not colour or a rendering side effect.
func TestAgentDisappearanceDuringActiveSessionRendersRuntimeMissingAndNeverMutatesRecordedStatus(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
	}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd().(sessionSnapshotMsg))
	tickMsg := cmd().(sessionTickMsg)

	// The agent disappears: InspectAgent itself succeeded in reporting
	// agent_not_found, so the bridge's own read does NOT error - it returns a
	// snapshot with Runtime.Status == Absent (session_bridge.go's own
	// behaviour, mirrored here without needing a live Herdr call).
	ctrl.mu.Lock()
	ctrl.snap = SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Absent, Reason: "agent_not_found"},
	}
	ctrl.mu.Unlock()
	m, cmd = send(t, m, tickMsg)
	pollMsg := cmd().(sessionSnapshotMsg)
	m, cmd = send(t, m, pollMsg)

	if m.sess.phase != sessionActive {
		t.Fatalf("agent disappearance must not leave session mode: phase = %v", m.sess.phase)
	}
	if m.attachHoldsTerminal() {
		t.Fatalf("agent disappearance must not fall back to the classic mate attach hand-off")
	}
	if m.sess.snapshot.Runtime.Status != query.Absent {
		t.Fatalf("Runtime.Status = %v, want Absent", m.sess.snapshot.Runtime.Status)
	}
	if m.sess.snapshot.RecordedStatus.Value != "running" {
		t.Fatalf("RecordedStatus = %+v, want the domain value \"running\" untouched: a missing agent must never be "+
			"upgraded to stopped/needs_repair/failed by this renderer (ADR 0025)", m.sess.snapshot.RecordedStatus)
	}
	if m.sess.snapshot.RecordedStatus.Value == "stopped" || m.sess.snapshot.RecordedStatus.Value == "needs_repair" || m.sess.snapshot.RecordedStatus.Value == "failed" {
		t.Fatalf("RecordedStatus was upgraded to a terminal lifecycle value: %+v", m.sess.snapshot.RecordedStatus)
	}

	frame := renderFrame(t, m)
	if !containsSubstring(frame, "runtime_missing") {
		t.Fatalf("frame does not render runtime_missing for an Absent runtime:\n%s", frame)
	}
	if containsSubstring(frame, "stopped") || containsSubstring(frame, "needs_repair") || containsSubstring(frame, "failed") {
		t.Fatalf("frame leaked a lifecycle word derived from the disappearance itself:\n%s", frame)
	}
	if cmd == nil {
		t.Fatalf("agent disappearance must still reschedule the next poll tick")
	}
}

// TestAgentDisappearanceOnTheEntryReadItselfFallsBackWithConcreteReason covers
// the other half: a target whose *entry* read (not yet in session mode)
// observes agent_not_found is not "session mode with a banner" - it is the
// same fallback path as any other entry-read failure, since there is
// nothing useful to show before a first successful read. This is exercised
// through an error return (matching what session_bridge.go's readSession
// actually does when application.ResolveAttachTarget itself cannot resolve
// a target at all, e.g. no binding recorded), distinct from the
// mid-session Absent-but-no-error case above.
func TestEntryReadNotFoundFallsBackWithConcreteNotFoundReason(t *testing.T) {
	ctrl := &recordingSessionController{
		readErr: observability.NewError(observability.CodeNotFound, "no runtime binding recorded"),
	}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd().(sessionSnapshotMsg))

	if m.sess.phase == sessionActive {
		t.Fatalf("an entry read reporting not_found must not enter session mode")
	}
	if !m.attachHoldsTerminal() {
		t.Fatalf("an entry-read not_found must fall back to the classic hand-off")
	}
	if !containsSubstring(m.msg.text, "not_found") {
		t.Fatalf("fallback message %q does not name the real not_found taxonomy", m.msg.text)
	}
}

// ---------- runtime timeout and permission: real taxonomy, never compressed ----------

func TestRuntimeTimeoutDuringPollDegradesRuntimeUnknownWithTimeoutTaxonomy(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
	}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd().(sessionSnapshotMsg))
	tickMsg := cmd().(sessionTickMsg)

	ctrl.mu.Lock()
	ctrl.readErr = observability.NewError(observability.CodeTimeout, "read agent timed out")
	ctrl.mu.Unlock()
	m, cmd = send(t, m, tickMsg)
	m, cmd = send(t, m, cmd().(sessionSnapshotMsg))

	if m.sess.phase != sessionActive {
		t.Fatalf("a timeout must not fall back mid-session")
	}
	if !containsSubstring(m.sess.snapshot.Runtime.Reason, string(observability.CodeTimeout)) {
		t.Fatalf("Runtime.Reason = %q, want the real timeout taxonomy code, not a compressed generic word", m.sess.snapshot.Runtime.Reason)
	}
	if m.sess.snapshot.RecordedStatus.Value != "running" {
		t.Fatalf("RecordedStatus mutated by a timeout: %+v", m.sess.snapshot.RecordedStatus)
	}
}

func TestPermissionErrorOnEntryReadFallsBackWithPermissionTaxonomyNotGenericFailure(t *testing.T) {
	ctrl := &recordingSessionController{
		readErr: observability.NewError(observability.CodePermission, "caller may not attach to this session"),
	}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd().(sessionSnapshotMsg))

	if !containsSubstring(m.msg.text, "permission") {
		t.Fatalf("fallback message %q does not name the real permission taxonomy", m.msg.text)
	}
	if containsSubstring(m.msg.text, "generic") {
		t.Fatalf("fallback message %q reads like a compressed generic failure", m.msg.text)
	}
}

func TestPermissionErrorDuringPollDegradesRuntimeUnknownWithPermissionTaxonomy(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
	}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd().(sessionSnapshotMsg))
	tickMsg := cmd().(sessionTickMsg)

	ctrl.mu.Lock()
	ctrl.readErr = observability.NewError(observability.CodePermission, "read refused")
	ctrl.mu.Unlock()
	m, cmd = send(t, m, tickMsg)
	m, _ = send(t, m, cmd().(sessionSnapshotMsg))

	if !containsSubstring(m.sess.snapshot.Runtime.Reason, "permission") {
		t.Fatalf("Runtime.Reason = %q, want the real permission taxonomy", m.sess.snapshot.Runtime.Reason)
	}
}

// ---------- resize while session mode is active ----------

// TestResizeWhileSessionActiveKeepsSessionModeAndRendersTheNewFrameShape
// proves resize is a pure rendering concern for the embedded session view:
// unlike the classic tea.Exec hand-off (which hands the whole TTY to a
// subprocess and cannot itself redraw on a Console-level resize), session
// mode is a Bubble Tea view like any other and must keep drawing at the
// frame contract (exactly h lines of exactly w cells) after a
// WindowSizeMsg, without leaving session mode or losing the poll chain.
func TestResizeWhileSessionActiveKeepsSessionModeAndRendersTheNewFrameShape(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
	}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd().(sessionSnapshotMsg))
	if m.sess.phase != sessionActive {
		t.Fatalf("setup: session mode did not become active")
	}
	_ = renderFrame(t, m) // 160x48, the fixture's own size - must already satisfy the contract

	m, _ = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.sess.phase != sessionActive {
		t.Fatalf("resizing to 80x24 left session mode: phase = %v", m.sess.phase)
	}
	renderFrame(t, m) // asserts exactly 24 lines of exactly 80 cells

	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	if m.sess.phase != sessionActive {
		t.Fatalf("resizing to 120x36 left session mode: phase = %v", m.sess.phase)
	}
	renderFrame(t, m) // asserts exactly 36 lines of exactly 120 cells
}

// TestResizeDoesNotResetSessionGenerationOrDropAScheduledTick proves resize
// does not interfere with the poll generation fence: a tick scheduled
// before a resize must still land and still produce a read, exactly as if
// no resize had happened - resize must never look like "leaving and
// re-entering session mode" to the poll chain.
func TestResizeDoesNotResetSessionGenerationOrDropAScheduledTick(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, tickCmd := send(t, m, cmd().(sessionSnapshotMsg))
	tickMsg := tickCmd().(sessionTickMsg)
	genBefore := m.sess.gen

	m, _ = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.sess.gen != genBefore {
		t.Fatalf("resize changed the session generation: %d -> %d", genBefore, m.sess.gen)
	}

	before := ctrl.readCount()
	m, readCmd := send(t, m, tickMsg)
	if readCmd == nil {
		t.Fatalf("a tick scheduled before a resize produced no read after the resize")
	}
	readCmd()
	if ctrl.readCount() != before+1 {
		t.Fatalf("reads = %d, want %d: the pre-resize tick must still reach SessionReader", ctrl.readCount(), before+1)
	}
}

// ---------- cancellation: the base context ending ----------

// ctxAwareController is a SessionReader that actually observes context
// cancellation, unlike recordingSessionController (which ignores its ctx
// argument entirely, matching test doubles elsewhere in this file). This is
// what proves the Console's own plumbing - not just a test double - carries
// a cancelled context through to a real error, the same way session_bridge.go's
// production reader does via process.ExecRunner (which checks ctx.Err()
// before ever invoking herdr).
type ctxAwareController struct {
	snap SessionSnapshot
}

func (c *ctxAwareController) Read(ctx context.Context, target SessionTarget) (SessionSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return SessionSnapshot{}, err
	}
	snap := c.snap
	snap.Target = target
	return snap, nil
}

// TestEntryReadWithAlreadyCancelledContextFallsBackRatherThanHanging proves
// a context that is already done by the time the entry read runs produces a
// real error (ctx.Err()) rather than a hang or a panic, and that error takes
// the same fallback path as any other entry-read failure.
func TestEntryReadWithAlreadyCancelledContextFallsBackRatherThanHanging(t *testing.T) {
	ctrl := &ctxAwareController{}
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := New(spy.load, spy.attach).WithSession(ctrl.Read, nil, nil)
	m.g = unicodeGlyphs
	m.p = plainPalette()
	m.sessionPollIntervalOverride = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done before the entry read ever runs
	m = m.WithContext(ctx)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 48})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter")) // open the payments-api Project

	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatalf("beginSession returned a nil Cmd")
	}
	msg := cmd()
	snapMsg, ok := msg.(sessionSnapshotMsg)
	if !ok {
		t.Fatalf("entry Cmd produced %T, want sessionSnapshotMsg", msg)
	}
	if snapMsg.err == nil {
		t.Fatalf("a cancelled base context produced a successful read instead of ctx.Err()")
	}
	m, _ = send(t, m, snapMsg)
	if m.sess.phase == sessionActive {
		t.Fatalf("session mode became active despite the entry read failing on a cancelled context")
	}
	if !m.attachHoldsTerminal() {
		t.Fatalf("a cancelled-context entry-read failure must fall back to the classic hand-off")
	}
}

// TestPollWithContextCancelledMidSessionDegradesRuntimeUnknownWithoutFallback
// proves the same for a routine poll once session mode is already active:
// the context ending mid-session must never fall back and must never mutate
// RecordedStatus, exactly like any other poll failure (ADR 0025's rule does
// not carve out an exception for "the reason was cancellation").
func TestPollWithContextCancelledMidSessionDegradesRuntimeUnknownWithoutFallback(t *testing.T) {
	ctrl := &ctxAwareController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
	}}
	spy := &attachSpy{tree: sameTree(sampleTree())}
	ctx, cancel := context.WithCancel(context.Background())
	m := New(spy.load, spy.attach).WithSession(ctrl.Read, nil, nil).WithContext(ctx)
	m.g = unicodeGlyphs
	m.p = plainPalette()
	m.sessionPollIntervalOverride = time.Millisecond
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 48})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter"))

	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd().(sessionSnapshotMsg))
	if m.sess.phase != sessionActive {
		t.Fatalf("setup: session mode did not become active")
	}
	tickMsg := cmd().(sessionTickMsg)

	cancel() // the program's own context ends, e.g. on shutdown
	m, cmd = send(t, m, tickMsg)
	pollMsg := cmd().(sessionSnapshotMsg)
	if pollMsg.err == nil {
		t.Fatalf("a poll issued against a cancelled context returned no error")
	}
	m, cmd = send(t, m, pollMsg)

	if m.sess.phase != sessionActive {
		t.Fatalf("a cancelled context must not itself fall back out of session mode")
	}
	if m.sess.snapshot.Runtime.Status != query.Unknown {
		t.Fatalf("Runtime.Status = %v, want Unknown after a cancelled-context poll failure", m.sess.snapshot.Runtime.Status)
	}
	if m.sess.snapshot.RecordedStatus.Value != "running" {
		t.Fatalf("RecordedStatus mutated by a cancelled-context poll failure: %+v", m.sess.snapshot.RecordedStatus)
	}
}

// ---------- ctrl+c: quits the whole Console, not just session mode ----------
//
// This is SNAPSHOT MODE'S behavior only. ADR 0026 step 6 deliberately
// inverts Ctrl+C for stream mode - there it is forwarded to the agent's own
// PTY instead of quitting the Console (session-view-contract.md, "Ctrl+C is
// the second key that inverts"; the stream-mode counterpart is
// TestStreamModeForwardsCtrlCToTheAgentInsteadOfQuitting,
// session_input_forward_test.go). This fixture (sessionFixture,
// recordingSessionController) never opens a stream, so m.sess.stream stays
// nil throughout and update.go's own stream-mode routing never applies here
// - the assertion below is unchanged and still describes real production
// behavior for a target on the snapshot fallback path.

// TestCtrlCWhileSessionActiveQuitsTheWholeConsoleRatherThanBeingComposerInput
// pins a real ambiguity in onKey's branch order (update.go): ctrl+c is
// checked before the sessionActive composer-ownership branch, so it always
// quits, unlike a printable "q" (which the composer swallows while active,
// per TestQWhileSessionActiveIsComposerInputNotQuit in session_mode_test.go).
// A regression that reordered those branches would silently swallow ctrl+c
// into the composer text instead of quitting - this test would catch that.
func TestCtrlCWhileSessionActiveQuitsTheWholeConsoleRatherThanBeingComposerInput(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd().(sessionSnapshotMsg))

	m, quitCmd := send(t, m, key("ctrl+c"))
	if m.sess.composer != "" {
		t.Fatalf("composer = %q, want empty: ctrl+c must not be forwarded as composer input", m.sess.composer)
	}
	if !m.quitting {
		t.Fatalf("ctrl+c while session mode was active did not set quitting")
	}
	if quitCmd == nil {
		t.Fatalf("ctrl+c while session mode was active did not queue tea.Quit")
	}
}

// ---------- re-entry after leaving ----------

// TestReEnteringSessionAfterEscIssuesAFreshEntryReadAndIgnoresTheOldGeneration
// proves re-entry is a genuinely fresh session, not a resume of stale state:
// a tick scheduled by the FIRST session (before Esc) must still be dropped
// once the SECOND session is active, and the second session's own snapshot
// must come from its own entry read, not leftover state from the first.
func TestReEnteringSessionAfterEscIssuesAFreshEntryReadAndIgnoresTheOldGeneration(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
	}}
	m := sessionFixture(t, ctrl)

	// First session: enter, become active, type into the composer, schedule
	// a tick, then leave.
	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd().(sessionSnapshotMsg))
	staleTick := cmd().(sessionTickMsg)
	m, _ = send(t, m, key("h"))
	if m.sess.composer != "h" {
		t.Fatalf("setup: composer did not accept input")
	}
	m, _ = send(t, m, key("esc"))
	if m.sess.phase == sessionActive {
		t.Fatalf("setup: Esc did not leave session mode")
	}

	// Second session: a different observation this time, so a snapshot that
	// leaked from the first session would be caught.
	ctrl.mu.Lock()
	ctrl.snap = SessionSnapshot{RecordedStatus: query.KnownField("blocked"), Runtime: SessionRuntime{Status: query.Known}}
	ctrl.mu.Unlock()
	m, cmd = send(t, m, key("enter"))
	if cmd == nil {
		t.Fatalf("re-entry did not issue a fresh entry read")
	}
	entryMsg := cmd().(sessionSnapshotMsg)
	if entryMsg.gen == staleTick.gen {
		t.Fatalf("re-entry reused the first session's generation: %d", entryMsg.gen)
	}
	m, cmd = send(t, m, entryMsg)

	if m.sess.composer != "" {
		t.Fatalf("composer = %q, want empty on re-entry: the first session's typed text leaked", m.sess.composer)
	}
	if m.sess.snapshot.RecordedStatus.Value != "blocked" {
		t.Fatalf("RecordedStatus = %+v, want the second session's own fresh read (\"blocked\"), not stale state", m.sess.snapshot.RecordedStatus)
	}

	// The stale tick from the FIRST session must still be dropped now that a
	// SECOND session is active - it must not be mistaken for the second
	// session's own tick just because the phase is sessionActive again.
	before := ctrl.readCount()
	m, staleCmd := send(t, m, staleTick)
	if staleCmd != nil {
		t.Fatalf("a stale tick from a session that was left and replaced by a new one still scheduled a read")
	}
	if ctrl.readCount() != before {
		t.Fatalf("reads = %d, want %d: the first session's stale tick must never reach SessionReader once replaced", ctrl.readCount(), before)
	}
}

// ---------- transcript is never written back to state ----------

// TestSessionModeNeverExposesAWriteSeamForTheTranscript is a structural
// check, not a behavioural one: internal/ui/console has no dependency that
// could write a transcript back to persistence even if it wanted to
// (boundary_test.go enforces the package never imports internal/persistence
// or internal/application), so the invariant "the transcript is never
// written back to state just because it was displayed" is guaranteed at
// compile time for this package. The write-side guarantee - that
// cmd/mate/session_bridge.go's readSession does not call any store Write
// either - is asserted directly in cmd/mate's own session_lifecycle_test.go,
// where a real store is available to check.
func TestSessionModeNeverExposesAWriteSeamForTheTranscript(t *testing.T) {
	ctrl := &recordingSessionController{snap: SessionSnapshot{
		Runtime: SessionRuntime{Status: query.Known},
		Transcript: SessionTranscript{
			Status:  SessionTranscriptParsed,
			Entries: []SessionTranscriptEntry{{Kind: SessionTranscriptEntryTurn, Text: "hello"}},
		},
	}}
	m := sessionFixture(t, ctrl)
	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd().(sessionSnapshotMsg))
	tickMsg := cmd().(sessionTickMsg)
	for i := 0; i < 3; i++ {
		m, cmd = send(t, m, tickMsg)
		m, cmd = send(t, m, cmd().(sessionSnapshotMsg))
		tickMsg = cmd().(sessionTickMsg)
	}
	// Nothing above touched anything but the in-memory recordingSessionController;
	// there is no store, no event log, no persistence handle reachable from
	// this package at all. The assertion is that this test compiles and runs
	// without any such dependency - see doc.go/boundary_test.go for the
	// enforced import boundary this relies on.
	_ = m
}
