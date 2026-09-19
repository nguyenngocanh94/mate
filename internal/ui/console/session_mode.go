package console

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/query"
)

// ADR 0025 step 4: the embedded session view's MVP bridge, wired into
// Model's own key handling and Bubble Tea message loop. This file owns
// only the Console-side state machine (entering/leaving session mode,
// polling cadence, the composer); the ports themselves (SessionReader/
// SessionPrompt/SessionClose) are built in cmd/matev2/console.go from
// internal/query, internal/application and runtime.Adapter - this package
// never reaches those directly (doc.go, boundary_test.go).

// sessionPollInterval is the ADR 0025 default poll cadence (300-500ms).
const sessionPollInterval = 400 * time.Millisecond

// sessionPhase is where session mode is relative to the Console's normal
// navigation.
type sessionPhase int

const (
	// sessionIdle: the Console is not in session mode, or an entry read is
	// still in flight and has not yet been decided (see beginSession).
	sessionIdle sessionPhase = iota
	// sessionActive: the entry read succeeded and the session frame is
	// what View draws; the poll ticker is running.
	sessionActive
	// sessionOpening and sessionClosing are stream-only phases. Snapshot mode
	// retains the historical idle/active path, while stream mode exposes its
	// transport lifecycle explicitly for cleanup and stale-message fencing.
	sessionOpening
	sessionClosing
	sessionFallback
)

// sessionFlow is the whole embedded session-view state (ADR 0025 step 4).
// One field on Model.
type sessionFlow struct {
	phase    sessionPhase
	target   SessionTarget
	snapshot SessionSnapshot
	composer string
	// entryRow is the row beginSession was called for. It is what the
	// fallback-to-hand-off path (onSessionSnapshot, entry-read failure)
	// replays through beginAttach, rather than whatever row happens to be
	// selected once the entry read returns - selection can move while an
	// entry read is in flight (nothing blocks the keyboard for it, unlike
	// the classic hand-off's attachHoldsTerminal).
	entryRow row
	// zone is which of the view's two halves owns the keyboard
	// (session_focus.go). It is zoneTerminal on entering the view: a reader
	// who opened a Mate opened it to talk to the Mate, and the rail is one
	// click or one F2 away.
	zone sessionZone
	// gen fences every scheduled tick and in-flight read: a message whose
	// gen no longer matches m.sess.gen is dropped instead of applied. This
	// is what lets leaving session mode (or starting a different entry
	// read) stop the poll chain without Bubble Tea offering a way to
	// cancel a Cmd outright (see cmd/matev2/console.go's own note on
	// leaked goroutines, and attach.go's onBusyQuit for the same
	// constraint elsewhere in this package).
	gen int
	// stream is non-nil only while the PTY-backed controller owns a channel.
	stream *streamSession
	// terminal remains visible while a failed stream is being handed to the
	// snapshot fallback. It is cleared only after a snapshot is accepted.
	terminal *TerminalBuffer
	// boxSel is the rail's selected entry, as an index into
	// SessionSnapshot.Box.Value.Entries, or -1 for "follow the newest" -
	// which is where the cursor sits until the reader moves it. An absolute
	// index stays pointing at the same entry across a refresh because the
	// box is append-only: internal/box returns the whole history in a stable
	// order, so a new line lands after every index already in hand.
	boxSel int
	// fallback means the next sessionSnapshotMsg belongs to stream → snapshot,
	// rather than to a normal snapshot-mode entry/poll.
	fallback   bool
	openCancel context.CancelFunc
}

// sessionSnapshotMsg is the result of one SessionReader call. Whether it is
// the entry read or a routine poll is decided by m.sess.phase at the time
// it arrives (sessionIdle: entry; sessionActive: poll), not by a flag on
// the message itself.
type sessionSnapshotMsg struct {
	gen      int
	target   SessionTarget
	snapshot SessionSnapshot
	err      error
}

// sessionTickMsg requests the next poll.
type sessionTickMsg struct{ gen int }

// sessionPromptSentMsg is the result of one SessionPrompt call.
type sessionPromptSentMsg struct {
	gen int
	err error
}

// sessionCloseSentMsg discards the result of SessionClose: the MVP composer
// has nothing to report a close failure to once the reader has already left
// session mode (there is no screen left showing that target), so this exists
// only to give the close call a Cmd to run on rather than a bare goroutine.
type sessionCloseSentMsg struct{}

// sessionAvailableFor reports whether Enter on row r opens the embedded
// Agent View, instead of falling back to the classic tea.Exec hand-off
// (attach.go). It is the one predicate enterLabel and onEnter both consult,
// so the key line and Enter cannot disagree about whether the view opens.
//
// The preconditions are the real ones this view has: the session ports must
// be wired, the row must name a resolvable target, and the snapshot must
// record something to open - the same refusal attachRefusal decides for the
// classic hand-off (a stale, absent or unreadable binding, or a Mate that
// does not occupy its slot). A binding that is held but not active is *not* a
// refusal here: it is attachable by the CLI's own resolution
// (application.ResolveAttachTarget, ADR 0010) and is frequently a live
// agent, so the Agent View looks rather than refuses (see attachRefusal's
// own note on reserved).
func (m Model) sessionAvailableFor(r row) (SessionTarget, bool) {
	if m.sessionReader == nil && m.sessionStream == nil {
		return SessionTarget{}, false
	}
	target, ok := m.sessionTargetFor(r)
	if !ok {
		return SessionTarget{}, false
	}
	if _, refused := m.attachRefusal(r); refused {
		return SessionTarget{}, false
	}
	return target, true
}

// sessionTargetFor builds the SessionTarget a row names, straight from the
// already-loaded snapshot - this package never resolves a target itself
// (session.go's own doc comment). It decides only what the row *names*, not
// whether the view can open: sessionAvailableFor applies the snapshot
// refusal (attachRefusal, attach.go) so the key line and Enter agree, and
// its refusal wording is not duplicated here.
func (m Model) sessionTargetFor(r row) (SessionTarget, bool) {
	switch r.kind {
	case rowMate:
		mate := m.currentProject().Mate
		if !mate.Designated.IsKnown() || mate.Designated.Value.MateID == "" {
			return SessionTarget{}, false
		}
		agent := ""
		if mate.AgentName.IsKnown() {
			agent = mate.AgentName.Value
		}
		return SessionTarget{
			Kind:        SessionTargetMate,
			ID:          mate.Designated.Value.MateID,
			ProjectID:   m.currentProject().ProjectID,
			HarnessKind: mate.Designated.Value.HarnessKind,
			AgentName:   agent,
			Mode:        m.currentProject().Mode,
		}, true
	case rowCrew:
		c, ok := m.crewByID(r.id)
		if !ok || c.CrewID == "" {
			return SessionTarget{}, false
		}
		agent := ""
		if c.AgentName.IsKnown() {
			agent = c.AgentName.Value
		}
		worktree := ""
		if c.Worktree.IsKnown() {
			worktree = c.Worktree.Value.Path
		}
		mode := query.Mode("")
		if p, ok := m.projectByID(c.ProjectID); ok {
			mode = p.Mode
		}
		return SessionTarget{
			Kind:        SessionTargetCrew,
			ID:          c.CrewID,
			ProjectID:   c.ProjectID,
			HarnessKind: c.HarnessKind,
			AgentName:   agent,
			Worktree:    worktree,
			Mode:        mode,
		}, true
	default:
		return SessionTarget{}, false
	}
}

// beginSession is Enter on a Mate/Crew row once sessionAvailableFor has
// already said session mode applies. It issues one entry read before
// committing to session mode: a target whose first read fails falls back to
// the classic hand-off (attach.go) rather than leaving the reader on a screen
// that shows nothing. ADR 0025's "Fallback chain (amended by ADR 0026)" is
// the authoritative statement: snapshot mode lỗi → `matev2 attach` hand-off.
// What the reader is told on that fallback is onSessionSnapshot's business.
func (m Model) beginSession(r row, target SessionTarget) (Model, tea.Cmd) {
	target.TranscriptCapacity = SessionTranscriptCapacity(target.Kind, m.w, m.h)
	m.sess = sessionFlow{boxSel: -1, gen: m.sess.gen + 1, target: target, entryRow: r,
		snapshot: SessionSnapshot{Target: target, RecordedStatus: query.UnknownField[string]("session metadata pending"),
			Runtime: SessionRuntime{Status: query.Unknown, Reason: "session metadata pending"}}}
	gen := m.sess.gen
	if m.sessionStream != nil {
		m.sess.phase = sessionOpening
		openCtx, cancel := context.WithCancel(m.baseCtx())
		m.sess.openCancel = cancel
		m.msg = infoMsg("Opening live session view for " + sessionLabel(target) + m.g.Ellipsis)
		return m, sessionStreamOpenCmd(openCtx, m.sessionStream, target, streamTerminalSize(target.Kind, m.w, m.h, sessionStreamReservedLines(m.sess.snapshot, target.Kind, m.w, m.boxAll), m.railWidth), gen)
	}
	m.msg = infoMsg("Opening session view for " + sessionLabel(target) + m.g.Ellipsis)
	return m, sessionReadCmd(m.baseCtx(), m.sessionReader, target, gen)
}

func sessionLabel(t SessionTarget) string {
	if t.AgentName != "" {
		return t.AgentName
	}
	return t.ID
}

func sessionReadCmd(ctx context.Context, reader SessionReader, target SessionTarget, gen int) tea.Cmd {
	return func() tea.Msg {
		snap, err := reader(ctx, target)
		return sessionSnapshotMsg{gen: gen, target: target, snapshot: snap, err: err}
	}
}

func sessionTickCmd(interval time.Duration, gen int) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return sessionTickMsg{gen: gen}
	})
}

// pollInterval is sessionPollInterval unless a test has overridden it via
// Model.sessionPollIntervalOverride (model.go), which exists solely so tests
// do not have to block on the real 300-500ms cadence to exercise the tick
// chain.
func (m Model) pollInterval() time.Duration {
	if m.sessionPollIntervalOverride > 0 {
		return m.sessionPollIntervalOverride
	}
	return sessionPollInterval
}

// onSessionSnapshot applies one SessionReader result. A gen mismatch means
// the read was issued for a session mode (or an entry attempt) the reader
// has since left or replaced - the ticker's own cancellation, since Bubble
// Tea gives a Cmd no other way to be stopped once it is in flight.
func (m Model) onSessionSnapshot(msg sessionSnapshotMsg) (Model, tea.Cmd) {
	if msg.gen != m.sess.gen {
		return m, nil
	}
	if m.sess.fallback {
		if msg.err != nil {
			entryRow := m.sess.entryRow
			target := m.sess.target
			m = m.recordOpenFailure(stepSnapshotView, sessionLabel(target), target.ID, msg.err)
			m.sess = sessionFlow{boxSel: -1, gen: m.sess.gen}
			nm, cmd := m.beginAttach(entryRow)
			if nm.attachHoldsTerminal() {
				nm.msg = nm.openFailureMsg()
			}
			return nm, cmd
		}
		msg.snapshot.ControllerNotice = m.sess.snapshot.ControllerNotice
		m.sess.snapshot = msg.snapshot
		m.sess.fallback = false
		m.sess.terminal = nil
		m.sess.phase = sessionActive
		m.msg = footerMsg{}
		return m, sessionTickCmd(m.pollInterval(), m.sess.gen)
	}
	if m.sess.phase == sessionIdle {
		// The entry read.
		if msg.err != nil {
			entryRow := m.sess.entryRow
			target := m.sess.target
			m = m.recordOpenFailure(stepSnapshotView, sessionLabel(target), target.ID, msg.err)
			m.sess = sessionFlow{boxSel: -1, gen: m.sess.gen}
			nm, cmd := m.beginAttach(entryRow)
			if !nm.attachHoldsTerminal() {
				// beginAttach refused, or could not build the hand-off. Its
				// own message is the actionable, sized one (the recorded
				// reason plus "nothing started").
				return nm, cmd
			}
			// The hand-off is going ahead. Its guaranteed notice
			// (handover.go) already names the attach and the detach key, so
			// the frame's single line is better spent on why the Agent View
			// did not open than on repeating that: the cause of the failed
			// step first, then its taxonomy code, then the mode now in use.
			// A coded message is arbitrary recorded text and would not fit
			// the single line; the full message and the whole chain are on
			// the 'e' detail view (session_failure.go).
			nm.msg = nm.openFailureMsg()
			return nm, cmd
		}
		m.sess.phase = sessionActive
		m.sess.snapshot = msg.snapshot
		m.msg = footerMsg{}
		return m, sessionTickCmd(m.pollInterval(), m.sess.gen)
	}
	// A routine poll while active. A failure never falls back and never
	// mutates RecordedStatus - it only degrades Runtime to Unknown, on top
	// of whatever the last successful poll showed (ADR 0025: runtime
	// disappearance does not mutate lifecycle, and this is not even a
	// disappearance, just a poll that could not complete).
	if msg.err != nil {
		m.sess.snapshot.Runtime = SessionRuntime{
			Status: query.Unknown,
			Reason: sessionErrorReason(msg.err),
		}
	} else {
		m.sess.snapshot = msg.snapshot
	}
	return m, sessionTickCmd(m.pollInterval(), m.sess.gen)
}

func (m Model) onSessionStreamOpened(msg sessionStreamOpenedMsg) (Model, tea.Cmd) {
	if msg.gen != m.sess.gen || m.sess.phase != sessionOpening {
		if msg.channel != nil {
			// An open that completed after the user left still owns a real
			// channel. Close it immediately; the generation fence protects the
			// view, and this protects the transport from leaking.
			return m, closeUnownedSessionChannelCmd(msg.channel, msg.gen)
		}
		return m, nil
	}
	if msg.err != nil || msg.channel == nil {
		if msg.channel != nil {
			// A factory is allowed to report both a partially-created channel
			// and an error. Close that channel before taking the fallback so a
			// failed halfway-open never leaks a PTY or child.
			_ = closeSessionChannel(msg.channel)
		}
		return m.beginStreamFallback(msg.err)
	}
	target := m.sess.target
	size := streamTerminalSize(target.Kind, m.w, m.h, sessionStreamReservedLines(m.sess.snapshot, target.Kind, m.w, m.boxAll), m.railWidth)
	stream := newStreamSession(m.baseCtx(), msg.channel, size)
	m.sess.stream = stream
	if m.sess.openCancel != nil {
		m.sess.openCancel()
		m.sess.openCancel = nil
	}
	m.sess.terminal = stream.buffer
	m.sess.phase = sessionActive
	m.sess.snapshot.Target = target
	m.sess.snapshot.Transcript = SessionTranscript{
		Source:      SessionTranscriptStreamed,
		HarnessKind: target.HarnessKind,
		Status:      SessionTranscriptUnknown,
	}
	m.msg = footerMsg{}
	read := sessionStreamReadCmd(m.baseCtx(), stream, msg.gen)
	if m.sessionMetadata == nil {
		return m, read
	}
	return m, tea.Batch(read, sessionStreamMetadataTickCmd(0, msg.gen))
}

func (m Model) beginStreamFallback(err error) (Model, tea.Cmd) {
	// The Console's own context ending surfaces here as a read error, but it is
	// not the stream failing and must not be reported as one: m.baseCtx is
	// cancelled when the reader quits or the Console is torn down, so a
	// "runtime_unavailable; falling back to snapshot" notice over a stream that
	// simply stopped because the reader left is a false diagnosis - and the
	// snapshot read it would schedule can never be delivered anyway. Tear the
	// stream down quietly, fence any in-flight read with a new generation, and
	// leave the reader on the tree view with no message (there is nothing to
	// tell them: nothing failed). Only context.Canceled is special-cased:
	// context/os.ErrDeadlineExceeded is a read deadline the runtime documents
	// as a read *result*, not a request to close the session, so it must keep
	// its normal fallback treatment.
	if errors.Is(err, context.Canceled) {
		stream := m.sess.stream
		m.sess.stream = nil
		if m.sess.openCancel != nil {
			m.sess.openCancel()
			m.sess.openCancel = nil
		}
		if stream != nil {
			_ = stream.close(context.Background())
		}
		m.sess = sessionFlow{boxSel: -1, gen: m.sess.gen + 1}
		return m, nil
	}
	entryRow := m.sess.entryRow
	stream := m.sess.stream
	m.sess.stream = nil
	if m.sess.openCancel != nil {
		m.sess.openCancel()
		m.sess.openCancel = nil
	}
	m.sess.fallback = true
	m = m.recordOpenFailure(stepLiveStream, sessionLabel(m.sess.target), m.sess.target.ID, err)
	m.sess.snapshot.ControllerNotice = m.openFailureLine(false)
	m.msg = m.openFailureMsg()
	if stream != nil {
		// Close synchronously on the failure boundary. This is not the output
		// hot path: it is the hand-off point, and waiting here guarantees that
		// no reader goroutine or PTY can still publish while the snapshot takes
		// over.
		_ = stream.close(context.Background())
		// The frozen buffer is deliberately NOT resized down to the fallback
		// frame's smaller height. The notice banner above it takes one frame
		// row, so the frame wants one row fewer than the buffer holds - but the
		// buffer is the agent's last live screen, and Resize drops its tail,
		// destroying the bottom row before the reader ever sees it. The renderer
		// crops the frozen frame's HEAD instead (sessionPaneLines' frozen
		// branch), so the reader keeps the top of the last screen the agent
		// drew; the snapshot read that follows replaces the buffer outright a
		// moment later.
		if m.sessionReader == nil {
			// No snapshot reader to fall back to: hand the terminal to the
			// classic attach child (attach.go) instead of pretending a snapshot
			// is coming.
			m.sess = sessionFlow{boxSel: -1, gen: m.sess.gen}
			nm, attachCmd := m.beginAttach(entryRow)
			return nm, attachCmd
		}
		m.sess.phase = sessionFallback
		return m, sessionReadCmd(m.baseCtx(), m.sessionReader, m.sess.target, m.sess.gen)
	}
	if m.sessionReader == nil {
		m.sess = sessionFlow{boxSel: -1, gen: m.sess.gen}
		nm, attachCmd := m.beginAttach(entryRow)
		return nm, attachCmd
	}
	m.sess.phase = sessionIdle
	return m, sessionReadCmd(m.baseCtx(), m.sessionReader, m.sess.target, m.sess.gen)
}

func (m Model) onSessionStreamChunk(msg sessionStreamChunkMsg) (Model, tea.Cmd) {
	if msg.gen != m.sess.gen || m.sess.phase != sessionActive || m.sess.stream != msg.stream {
		return m, nil
	}
	apply := func(chunk streamChunk) error {
		if len(chunk.bytes) > 0 {
			_, _ = msg.stream.buffer.Write(chunk.bytes)
		}
		return chunk.err
	}
	if err := apply(msg.chunk); err != nil {
		return m.beginStreamFallback(err)
	}
	gotBytes := len(msg.chunk.bytes) > 0
	// Drain ready chunks into this one model update. This coalesces redraws,
	// but each chunk is still applied in its original byte order.
	for {
		select {
		case chunk, ok := <-msg.stream.chunks:
			if !ok {
				return m.beginStreamFallback(io.EOF)
			}
			if err := apply(chunk); err != nil {
				if gotBytes {
					msg.stream.deferError(err)
					return m, sessionStreamReadCmd(m.baseCtx(), msg.stream, msg.gen)
				}
				return m.beginStreamFallback(err)
			}
			gotBytes = gotBytes || len(chunk.bytes) > 0
		default:
			return m, sessionStreamReadCmd(m.baseCtx(), msg.stream, msg.gen)
		}
	}
}

func (m Model) onSessionStreamMetadata(msg sessionStreamMetadataMsg) (Model, tea.Cmd) {
	if msg.gen != m.sess.gen || m.sess.phase != sessionActive || m.sess.stream == nil || m.sess.fallback {
		return m, nil
	}
	previousReservedLines := sessionStreamReservedLines(m.sess.snapshot, m.sess.target.Kind, m.w, m.boxAll)
	if msg.err != nil {
		m.sess.snapshot.Runtime = SessionRuntime{Status: query.Unknown, Reason: sessionErrorReason(msg.err)}
		return m, m.resizeStreamForReserve(previousReservedLines, msg.gen)
	}
	// Metadata is deliberately field-wise. In particular, the reader's
	// transcript is never allowed to replace the PTY terminal buffer.
	m.sess.snapshot.RecordedStatus = msg.snapshot.RecordedStatus
	m.sess.snapshot.Runtime = msg.snapshot.Runtime
	m.sess.snapshot.Box = msg.snapshot.Box
	m.sess.snapshot.AsOf = msg.snapshot.AsOf
	return m, m.resizeStreamForReserve(previousReservedLines, msg.gen)
}

// resizeStreamForReserve tells the PTY when the frame's own reserved chrome
// changed height. The banner and, on a narrow Mate frame, the Inbox-dependent
// digest both consume rows above the pane (sessionStreamReservedLines), so a
// metadata poll that adds either one leaves the stream sized for the old
// layout and the renderer then crops the agent's top row - the s5-follow-up
// half of the banner fix, where only the banner was tracked. When the total
// is unchanged there is nothing to resize and no Cmd to issue beyond the next
// metadata tick.
func (m Model) resizeStreamForReserve(previousReservedLines, gen int) tea.Cmd {
	currentReservedLines := sessionStreamReservedLines(m.sess.snapshot, m.sess.target.Kind, m.w, m.boxAll)
	if currentReservedLines == previousReservedLines || m.sess.stream == nil {
		return sessionStreamMetadataTickCmd(sessionMetadataInterval, gen)
	}
	size := streamTerminalSize(m.sess.target.Kind, m.w, m.h, currentReservedLines, m.railWidth)
	m.sess.terminal.Resize(size.Cols, size.Rows)
	return tea.Batch(sessionStreamMetadataTickCmd(sessionMetadataInterval, gen),
		sessionStreamResizeCmd(m.baseCtx(), m.sess.stream, size, gen))
}

// sessionBoxRefreshCmd re-reads the open session's metadata now, without
// waiting for the next tick. A box action is the one thing that changes what
// the box shows as a direct result of a keystroke - a resolve or a reply
// records a line to the crew, and the inbox's rule 2 then drops the item -
// and an answered question left under the reader's cursor for a whole tick
// is a question they can answer twice. Nil when no session is streaming:
// there is then no rail to refresh, and the project frame's own re-read
// (loadCmd) already covers the panel.
func (m Model) sessionBoxRefreshCmd() tea.Cmd {
	if m.sess.phase != sessionActive || m.sess.stream == nil || m.sess.fallback || m.sessionMetadata == nil {
		return nil
	}
	return sessionStreamMetadataCmd(m.baseCtx(), m.sessionMetadata, m.sess.target, m.sess.gen)
}

func (m Model) onSessionStreamMetadataTick(msg sessionStreamMetadataTickMsg) (Model, tea.Cmd) {
	if msg.gen != m.sess.gen || m.sess.phase != sessionActive || m.sess.stream == nil || m.sess.fallback || m.sessionMetadata == nil {
		return m, nil
	}
	return m, sessionStreamMetadataCmd(m.baseCtx(), m.sessionMetadata, m.sess.target, msg.gen)
}

func (m Model) onSessionStreamResized(msg sessionStreamResizedMsg) Model {
	if msg.gen != m.sess.gen || msg.err == nil {
		return m
	}
	m.msg = errMsg("Session resize failed: " + sessionErrorReason(msg.err))
	return m
}

// onSessionStreamClosed retires the stream the reader has left. It is also
// where a box row's "open that crew" is finally started (box_keys.go's
// openBoxEntryFromSession): the pending entry waits until the PTY it was
// asked from reports itself closed, so the Console never holds two streams
// at once. A close that does not belong to the session on screen drops the
// pending open rather than acting on it late - the reader has moved on, and
// opening a pane for a row they left behind would be a frame they did not
// ask for.
func (m Model) onSessionStreamClosed(msg sessionStreamClosedMsg) (Model, tea.Cmd) {
	if msg.gen != m.sess.gen || m.sess.phase != sessionClosing {
		m.pendingBoxOpen = query.BoxEntry{}
		return m, nil
	}
	if msg.err != nil {
		m.msg = errMsg("Session close failed: " + sessionErrorReason(msg.err))
	}
	m.sess = sessionFlow{boxSel: -1, gen: msg.gen}
	return m.startPendingBoxOpen()
}

// onSessionTick starts the next poll read, unless session mode has moved on
// since this tick was scheduled.
func (m Model) onSessionTick(msg sessionTickMsg) (Model, tea.Cmd) {
	if msg.gen != m.sess.gen || m.sess.phase != sessionActive {
		return m, nil
	}
	// Recomputed every tick, not just at beginSession: the window can be
	// resized while session mode is active (view.go feeds RenderSessionFrame
	// the live m.w/m.h on every draw), so a capacity captured once at entry
	// could go stale in either direction.
	target := m.sess.target
	target.TranscriptCapacity = SessionTranscriptCapacity(target.Kind, m.w, m.h)
	return m, sessionReadCmd(m.baseCtx(), m.sessionReader, target, m.sess.gen)
}

// onSessionPromptSent reports a prompt failure with its real taxonomy
// (ADR 0025: session mode calls the runtime in-process, so errors keep
// their taxonomy instead of being compressed through a child exit code). A
// gen mismatch means the reader has already left this target; the message
// line has nothing left to attach the report to, so it is dropped rather
// than surfacing a stale error over whatever the reader is looking at now.
func (m Model) onSessionPromptSent(msg sessionPromptSentMsg) Model {
	if msg.gen != m.sess.gen {
		return m
	}
	if msg.err != nil {
		m.msg = errMsg("Prompt failed: " + sessionErrorReason(msg.err))
	}
	return m
}

// endSession leaves session mode: Esc, from either zone (session_focus.go).
// It restores the navigation stack and the prior selection - untouched
// throughout session mode, since session mode never mutates them - and does
// not stop the agent. SessionClose is given a Cmd to run on rather than invoked
// directly so leaving session mode is not a blocking call.
func (m Model) endSession() (Model, tea.Cmd) {
	target := m.sess.target
	closer := m.sessionClose
	stream := m.sess.stream
	gen := m.sess.gen + 1
	m = m.clearBoxInteraction()
	if stream != nil {
		m.sess = sessionFlow{boxSel: -1, gen: gen, phase: sessionClosing, target: target}
		return m, sessionStreamCloseCmd(stream, gen)
	}
	m.sess = sessionFlow{boxSel: -1, gen: m.sess.gen + 1}
	m.msg, m.boxMsg = footerMsg{}, footerMsg{}
	if closer == nil {
		return m, nil
	}
	ctx := m.baseCtx()
	return m, func() tea.Msg {
		_ = closer(ctx, target)
		return sessionCloseSentMsg{}
	}
}

func streamTerminalSize(kind SessionTargetKind, w, h, reservedLines, railWidth int) TerminalSize {
	cols := w
	if kind == SessionTargetMate {
		// resolveRailWidth, not sessionRailWidth: the splitter is draggable
		// (session_focus.go), so the PTY's width has to follow the column the
		// reader actually put the divider at, not the breakpoint default.
		if rail := resolveRailWidth(kind, w, railWidth); rail > 0 {
			cols = w - rail - 1
		}
	}
	// StreamTranscriptCapacity, not SessionTranscriptCapacity: stream mode
	// draws no composer chrome (the captain's ruling, session_render.go), so
	// the PTY gets the full pane height a Console-drawn composer used to
	// take instead of being sized 4 rows short of what the frame actually
	// shows it. reservedLines is what the frame draws above the pane that
	// StreamTranscriptCapacity is documented to ignore, and each one consumes
	// a real row of the frame the PTY itself is drawn under: the runtime
	// banner, and - on a narrow Mate frame - the Inbox-dependent digest.
	// Failing to reserve either crops the agent's own top row (the s5 banner
	// fix, generalised by the s5 follow-up to cover the digest).
	rows := StreamTranscriptCapacity(kind, w, h)
	rows -= max0(reservedLines)
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return TerminalSize{Cols: cols, Rows: rows}
}

// sessionStreamReservedLines is how many frame rows stream mode's chrome
// takes above the PTY's own screen: the notice/runtime banner, plus the
// Inbox-dependent digest on a narrow Mate frame only (a wide Mate frame draws
// the digest in the rail beside the pane, and a Crew frame draws no digest at
// any width - session_render.go's renderSessionFrame). One definition serves
// both the PTY sizing at open and the resize decision on every metadata poll,
// so the two can never disagree about how much chrome the reader is looking
// at above the agent.
func sessionStreamReservedLines(snapshot SessionSnapshot, kind SessionTargetKind, w int, all bool) int {
	reserved := sessionBannerLineCount(snapshot, true)
	if kind == SessionTargetMate && sessionRailWidth(kind, w) == 0 {
		reserved += sessionDigestHeight(boxList{field: snapshot.Box, all: all})
	}
	return reserved
}

// onSessionKey handles every key while the snapshot-mode composer owns the
// keyboard (sessionActive or sessionFallback with m.sess.stream == nil).
// The zone model is the same one stream mode uses (session_focus.go): with
// the terminal zone focused the keys are the composer's, with the box zone
// focused they are the rail's, and F2 moves between them. Esc leaves the
// view from either zone here - snapshot mode's composer is Console-drawn,
// not a real terminal, so there is nothing under it that wants Esc.
func (m Model) onSessionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if model, cmd, handled := m.onSessionZoneKey(msg); handled {
		return model, cmd
	}
	switch msg.String() {
	case "esc":
		return m.endSession()
	case "enter":
		return m.sendComposer()
	case "backspace":
		m.sess.composer = trimLastCluster(m.sess.composer)
		return m, nil
	}
	if len(msg.Runes) > 0 {
		m.sess.composer += string(msg.Runes)
	}
	return m, nil
}

// onSessionStreamKey handles every key while stream mode owns the terminal
// (m.sess.stream != nil, ADR 0026 step 6). With the terminal zone focused
// there is no prefix and no exception: every key is encoded to the bytes a
// real terminal would have sent and written to the PTY, including q, j, k,
// Enter, Esc and Ctrl+C. That is what the zone model buys - the reader can
// see which zone has focus before pressing anything, so the view no longer
// has to reserve keystrokes out of the agent's own alphabet.
//
// The encoded bytes are handed to streamSession.enqueueWrite rather than
// written via a per-key tea.Cmd: a Cmd runs on its own goroutine with no
// ordering guarantee against any other Cmd's goroutine (session_stream_
// controller.go's own doc comment on streamSession explains why that is
// unsafe for input), so ordering is preserved by enqueueing synchronously
// here, inside Update, and letting the stream's single writeLoop goroutine
// be the only thing that ever calls SessionChannel.Write. A write failure
// is reported back through the existing read-side chunk/fallback path
// (streamSession.writeLoop's own doc comment), so there is no separate
// write-result message to route here.
func (m Model) onSessionStreamKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if model, cmd, handled := m.onSessionZoneKey(msg); handled {
		return model, cmd
	}
	if data, ok := encodeKeyMsg(msg); ok && m.sess.stream != nil {
		m.sess.stream.enqueueWrite(data)
	}
	return m, nil
}

// onSessionZoneKey is everything both modes share: the modal fields that
// own the keyboard outright, the F2 zone switch, and the box zone's own
// keys. The third return says whether the key was consumed; false hands it
// on to the mode's own terminal (the PTY, or the snapshot composer).
func (m Model) onSessionZoneKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	// The Actions menu and its confirmation are Console-drawn and take the
	// whole frame: while one is open every keystroke was aimed at it, not at
	// the harness.
	if m.confirm != nil || m.actions {
		model, cmd := m.onActionOverlayKey(msg.String())
		return model, cmd, true
	}
	if msg.Type == tea.KeyF2 {
		m.sess.zone = m.sess.zone.other()
		m.boxHover = -1
		return m, nil, true
	}
	if m.sess.zone != zoneBox {
		return m, nil, false
	}
	switch msg.String() {
	case "esc":
		model, cmd := m.endSession()
		return model, cmd, true
	case "m":
		model, cmd := m.beginModeToggle(m.sess.target.ProjectID)
		return model, cmd, true
	}
	if model, cmd, handled := m.onSessionBoxKey(msg); handled {
		return model, cmd, true
	}
	// Nothing reaches the PTY while the box has focus. A key the rail does
	// not bind is swallowed rather than forwarded, because a key that
	// sometimes reaches the agent and sometimes does not, depending on a
	// focus the reader may have forgotten, is the exact defect the prefix
	// model was removed for.
	return m, nil, true
}

// trimLastCluster removes the composer's last grapheme cluster rather than
// its last byte, so backspacing a multi-byte character (or one built from
// several code points) removes the whole character the reader sees, not a
// fragment that renders as replacement bytes.
func trimLastCluster(s string) string {
	clusters := graphemeClusters(s)
	if len(clusters) == 0 {
		return s
	}
	return strings.Join(clusters[:len(clusters)-1], "")
}

// sendComposer sends the composer's text through SessionPrompt and clears
// it. It does not wait for or synthesize a transcript update: the ADR is
// explicit that a prompt's effect is observed through the next poll, never
// invented here.
func (m Model) sendComposer() (Model, tea.Cmd) {
	text := strings.TrimSpace(m.sess.composer)
	m.sess.composer = ""
	if text == "" || m.sessionPrompt == nil {
		return m, nil
	}
	prompt := m.sessionPrompt
	target := m.sess.target
	gen := m.sess.gen
	ctx := m.baseCtx()
	return m, func() tea.Msg {
		err := prompt(ctx, target, text)
		return sessionPromptSentMsg{gen: gen, err: err}
	}
}

// sessionErrorReason renders an error's real taxonomy code alongside its
// message where the error carries one, so the message line - and a prompt
// failure - keep the distinction between not_found, runtime_unavailable,
// timeout, etc. rather than a single generic word (ADR 0025: "lỗi có thể
// giữ được taxonomy chi tiết ... thay vì bị nén thành generic failure").
func sessionErrorReason(err error) string {
	if err == nil {
		return ""
	}
	var coded *observability.Error
	if errors.As(err, &coded) {
		return string(coded.Code) + ": " + coded.Message
	}
	return err.Error()
}

// clearBoxInteraction drops every half-finished box interaction: the
// Actions menu the box zone opened, its confirmation, the hover and the
// drag. Leaving the session view has to leave them behind - a confirmation
// still armed when the reader comes back would answer a question they have
// forgotten asking.
func (m Model) clearBoxInteraction() Model {
	m.actions, m.confirm, m.actionChoices, m.actionIndex = false, nil, nil, 0
	m.boxHover, m.draggingSplit = -1, false
	return m
}
