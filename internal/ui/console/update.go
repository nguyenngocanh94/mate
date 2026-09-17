package console

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m = m.relayout()
		if m.sess.stream != nil && m.sess.phase == sessionActive {
			size := streamTerminalSize(m.sess.target.Kind, m.w, m.h,
				sessionStreamReservedLines(m.sess.snapshot, m.sess.target.Kind, m.w))
			m.sess.terminal.Resize(size.Cols, size.Rows)
			return m, sessionStreamResizeCmd(m.baseCtx(), m.sess.stream, size, m.sess.gen)
		}
		return m, nil
	case treeLoadedMsg:
		// attachReadNote runs after onTreeLoaded so that, on the one read
		// that follows a hand-over, what happened to the attach is what the
		// message line says (attach.go).
		return m.onTreeLoaded(msg).attachReadNote(msg.err), nil
	case AttachHandedOverMsg:
		return m.onAttachHandedOver()
	case AttachFinishedMsg:
		return m.onAttachFinished(msg)
	case actionDoneMsg:
		return m.onActionDone(msg)
	case sessionSnapshotMsg:
		return m.onSessionSnapshot(msg)
	case sessionStreamOpenedMsg:
		return m.onSessionStreamOpened(msg)
	case sessionStreamChunkMsg:
		return m.onSessionStreamChunk(msg)
	case sessionStreamMetadataTickMsg:
		return m.onSessionStreamMetadataTick(msg)
	case sessionStreamMetadataMsg:
		return m.onSessionStreamMetadata(msg)
	case sessionStreamResizedMsg:
		return m.onSessionStreamResized(msg), nil
	case sessionStreamClosedMsg:
		return m.onSessionStreamClosed(msg), nil
	case sessionTickMsg:
		return m.onSessionTick(msg)
	case sessionPromptSentMsg:
		return m.onSessionPromptSent(msg), nil
	case sessionCloseSentMsg:
		return m, nil
	case healthResultMsg:
		return m.onHealthResult(msg)
	case healthTickMsg:
		return m.onHealthTick(msg)
	case incidentAckDoneMsg:
		return m.onIncidentAckDone(msg), nil
	case tea.KeyMsg:
		return m.onKey(msg)
	case tea.MouseMsg:
		return m.onMouse(msg)
	default:
		return m, nil
	}
}

// onTreeLoaded applies a fresh read. The first load starts at the top of
// the tree (there is no prior position to keep); every later refresh - only
// 'r' and the return from an attach trigger one - re-anchors the whole
// navigation stack by identity instead of discarding it, then clamps
// whatever the new tree no longer contains (see reconcileSelection).
func (m Model) onTreeLoaded(msg treeLoadedMsg) Model {
	if msg.err != nil {
		if m.hasLoaded {
			if m.actionAfterRead != nil {
				m.msg = *m.actionAfterRead
				m.actionAfterRead = nil
				m.msg.text += " " + m.g.Dot + " snapshot re-read failed; r retries"
				return m
			}
			// A refresh that failed does not throw away the snapshot that is
			// already on screen: the error page says "no earlier snapshot is
			// loaded", which would be false, and the reader would lose a
			// usable picture over a transient read. The header's "As of"
			// already says how old what they are looking at is; the message
			// line says the refresh did not happen.
			m.msg = errMsg("Refresh failed: " + msg.err.Error() + " " + m.g.Dot +
				" still showing the snapshot from " + m.tree.AsOf.Format("15:04:05"))
			return m
		}
		m.phase = phaseFailed
		m.loadErr = msg.err
		m.msg = errMsg("Snapshot read failed")
		return m
	}
	first := !m.hasLoaded
	m.phase = phaseReady
	m.loadErr = nil
	m.tree = msg.tree
	m.hasLoaded = true
	if first {
		m.stack = []frame{{kind: frameWorkspace}}
		m.focus = paneList
		m.detail = false
		m.msg = footerMsg{}
		m = m.relayout()
		return m.applyActionAfterRead()
	}
	m = m.reconcileSelection()
	return m.applyActionAfterRead()
}

func (m Model) applyActionAfterRead() Model {
	if m.actionAfterRead == nil {
		return m
	}
	m.msg = *m.actionAfterRead
	m.actionAfterRead = nil
	return m
}

func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	// Stream mode owns the whole keyboard except its own Ctrl+b q detach
	// (onSessionStreamKey): Esc and Ctrl+C are forwarded to the agent's PTY
	// instead of leaving session mode or quitting the Console - the two
	// keys ADR 0026 deliberately inverts relative to snapshot mode (see
	// session-view-contract.md, "Esc semantics invert" / "Ctrl+C ... second
	// key that inverts"). This check must run before the Ctrl+C-quits
	// branch below, or a real terminal program's own Ctrl+C handling (a
	// shell's job control, an editor's own binding) would never reach it.
	if m.sess.phase == sessionActive && m.sess.stream != nil {
		return m.onSessionStreamKey(msg)
	}
	// q always quits, at every size and in every phase, and never stops an
	// agent by itself - but while an action's ActionFunc is running in the
	// background (actionBusy), Bubble Tea 1.2.4 cannot cancel that goroutine
	// outright (tea.go: "we'll have to leak the goroutine until Cmd
	// returns"), so quitting here can at most signal it and must not do so
	// silently: see onBusyQuit. ctrl+c is the terminal's own interrupt and
	// takes the same path.
	// The one exception is a name input: it is modal, so a printable key
	// typed into it is a character, not a command - otherwise no Project
	// whose name contains a q ("queue-service", "sqlite-tools") can be typed
	// at all, because the Console exits on the first letter. Esc leaves that
	// input, and ctrl+c - the terminal's own interrupt, not a character -
	// still quits from inside it. (A stream-mode Ctrl+C never reaches this
	// branch at all: the check above already routed it to the agent.)
	if key == "ctrl+c" || (key == "q" && !m.actionInputMode &&
		m.sess.phase != sessionActive && m.sess.phase != sessionOpening && m.sess.phase != sessionFallback) {
		if key == "ctrl+c" && (m.sess.phase == sessionActive || m.sess.phase == sessionOpening || m.sess.phase == sessionFallback) {
			// Bubble Tea stops processing after tea.Quit; reap the stream here
			// so Ctrl+C cannot leave an attach child behind.
			if m.sess.openCancel != nil {
				m.sess.openCancel()
			}
			if m.sess.stream != nil {
				_ = m.sess.stream.close(context.Background())
			}
			m.sess = sessionFlow{gen: m.sess.gen + 1}
			m.quitting = true
			return m, tea.Quit
		}
		if m.actionBusy {
			return m.onBusyQuit(), tea.Quit
		}
		m.quitting = true
		return m, tea.Quit
	}
	if m.sess.phase == sessionFallback && m.sess.terminal != nil {
		// A failed stream keeps its last terminal frame on screen while the
		// snapshot fallback's own entry read is in flight (beginStreamFallback:
		// m.sess.stream is already nil, but m.sess.terminal is deliberately
		// kept so the frame does not flash away and back). RenderStreamSessionFrame
		// draws no composer for that frame - stream mode never gets one, per
		// the captain's ruling - so a key typed here has nothing visible to
		// land in; onSessionKey would append it to a composer nothing shows,
		// and Enter would silently submit it once the snapshot lands. Drop
		// every key until the fallback read resolves and the frame becomes a
		// real (composer-bearing) snapshot frame - typically one poll
		// round-trip, not a state a reader lingers in.
		return m, nil
	}
	if m.sess.phase == sessionActive || m.sess.phase == sessionFallback {
		// The composer owns the keyboard: a printable "q" is a character
		// to type, not the Console's quit key (mirroring actionInputMode's
		// own exception above). Esc and "ctrl+b q" are session mode's own
		// way out (session_mode.go); nothing here reaches the navigation
		// keys below until the reader leaves. (m.sess.stream != nil here is
		// unreachable - the check at the top of this function already
		// handled that case - so this is always the snapshot composer, and
		// m.sess.terminal is nil here too - the branch above already
		// handled a still-visible dead stream frame.)
		return m.onSessionKey(msg)
	}
	if m.sess.phase == sessionOpening || m.sess.phase == sessionClosing {
		return m, nil
	}
	if m.attachHoldsTerminal() {
		// The subprocess owns the terminal (attach.go). Anything that
		// reached us was aimed at the agent session, so it moves nothing
		// here; q above is the one exception, because a reader must always
		// be able to leave.
		return m, nil
	}
	l := layout(m.w, m.h)
	if l.TooSmall {
		// Nothing else is drawn, so nothing else responds: a key that moved
		// a selection nobody can see would silently change where the reader
		// lands when the window grows back.
		return m, nil
	}
	if m.failureDetail {
		// The failure detail overlay is modal: only its own scroll/close keys
		// are honoured (q and ctrl+c already ran above), so no key here can
		// move the list behind it.
		return m.onFailureDetailKey(key, l), nil
	}
	if m.actionBusy {
		return m, nil
	}
	if m.actionInputMode {
		return m.onActionInputKey(msg)
	}
	if m.harnessPick {
		return m.onHarnessKey(msg)
	}
	if m.confirm != nil {
		switch key {
		case "esc", "backspace":
			m.confirm = nil
			m.actions = true
			return m, nil
		case "enter":
			choice := m.confirm.choice
			m.confirm = nil
			return m.runAction(choice)
		default:
			return m, nil
		}
	}
	if m.incidents.open {
		return m.onIncidentsKey(msg)
	}
	if m.actions {
		switch key {
		case "esc", "backspace":
			return m.closeActions(), nil
		case "up", "k":
			if m.actionIndex > 0 {
				m.actionIndex--
			}
			return m, nil
		case "down", "j":
			if m.actionIndex+1 < len(m.actionChoices) {
				m.actionIndex++
			}
			return m, nil
		case "enter":
			return m.handleActionEnter()
		default:
			return m, nil
		}
	}
	switch m.phase {
	case phaseLoading:
		return m, nil
	case phaseFailed:
		if key == "r" {
			m.msg = infoMsg("Retrying snapshot read " + m.g.Ellipsis)
			return m, loadCmd(m.load)
		}
		return m, nil
	}

	switch key {
	case "a":
		return m.beginActions(), nil
	case "e":
		// The re-openable failure detail (session_failure.go). Only offered
		// when the chain is non-empty; a key that does nothing is
		// indistinguishable from a lost key, so it is not advertised then
		// either (actionHints).
		if len(m.openFailures) == 0 {
			return m, nil
		}
		m.failureDetail = true
		m.failureTop = 0
		// The summary stays: this key is how a reader sees *more* of a
		// failure, so it must not be what removes the only sign on the frame
		// that anything failed (the counter-review's N2 - pressing 'e' and
		// then Esc left the frame saying nothing happened).
		return m, nil
	case "n":
		return m.beginNewProject(), nil
	case "s":
		return m.beginMateStart()
	case "h":
		return m.beginSwitchHarness(), nil
	case "i":
		return m.beginIncidents(), nil
	case "r":
		m.msg = footerMsg{}
		return m, loadCmd(m.load)
	case "tab":
		return m.onTab(l), nil
	case "esc", "backspace":
		return m.onBack(), nil
	case "up", "k":
		return m.onUp(1), nil
	case "down", "j":
		return m.onDown(1), nil
	case "pgup":
		return m.onUp(l.page()), nil
	case "pgdown":
		return m.onDown(l.page()), nil
	case "enter":
		return m.onEnter()
	default:
		return m, nil
	}
}

// onMouse forwards a mouse event to the live PTY when stream mode is active,
// and drops it otherwise. Mouse reporting is enabled Program-wide
// (tea.WithMouseAllMotion, cmd/mate/console.go), not just while a stream is
// open, so a MouseMsg can reach the Console at any time - this guard is
// what keeps it from doing anything outside an active stream.
func (m Model) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.sess.phase != sessionActive || m.sess.stream == nil {
		return m, nil
	}
	return m.onSessionStreamMouse(msg)
}

// onBusyQuit is what q/ctrl+c do while an action's ActionFunc is still
// running (actionBusy): it cancels the context that action is running under
// - the one signal available, since Bubble Tea leaks the goroutine rather
// than stopping it - and records what was abandoned so cmd/mate can tell the
// operator plainly once the terminal is back (see AbandonedAction). The
// Console has nothing left to draw once tea.Quit takes effect, which is why
// the message cannot just be m.msg: this is the one thing that survives
// past View returning empty.
func (m Model) onBusyQuit() Model {
	if m.actionCancel != nil {
		m.actionCancel()
	}
	desc := m.actionRunningDesc
	if desc == "" {
		desc = "an action"
	}
	m.actionAbandoned = desc
	m.quitting = true
	return m
}

// onTab is the design's one new interaction: with an inspector column it
// moves focus between the panes so a long inspector can be scrolled; below
// 100 columns, where there is no inspector column, it opens and closes
// Detail over the main region instead.
func (m Model) onTab(l frameLayout) Model {
	m.msg = footerMsg{}
	m.inspTop = 0
	if l.Inspector > 0 {
		if m.focus == paneList {
			m.focus = paneInspector
		} else {
			m.focus = paneList
		}
		return m
	}
	m.detail = !m.detail
	return m
}

// onBack unwinds one step at a time, in the order the reader built them:
// close Detail, then leave the inspector, then go up a level. At the root
// it does nothing - Esc is not a quit.
func (m Model) onBack() Model {
	m.msg = footerMsg{}
	m.inspTop = 0
	switch {
	case m.detail:
		m.detail = false
		return m
	case m.focus == paneInspector:
		m.focus = paneList
		return m
	default:
		return m.pop().relayout()
	}
}

// onUp and onDown move the selection in the list, or scroll in the
// inspector. The inspector's own scroll extent is its task's; clamping at
// zero here is what keeps the offset from going negative in the meantime.
func (m Model) onUp(n int) Model {
	if m.detail || m.focus == paneInspector {
		m.inspTop -= n
		if m.inspTop < 0 {
			m.inspTop = 0
		}
		m.msg = footerMsg{}
		return m
	}
	return m.moveSelection(-n)
}

func (m Model) onDown(n int) Model {
	if m.detail || m.focus == paneInspector {
		m.inspTop += n
		m.msg = footerMsg{}
		return m
	}
	return m.moveSelection(n)
}

// onEnter opens the next level for a Project or Task row, and asks to
// attach for a Mate or Crew row. A refusal is decided from the snapshot
// before any subprocess starts (see attachRefusal).
func (m Model) onEnter() (tea.Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	switch r.kind {
	case rowProject:
		return m.open(frame{kind: frameProject, id: r.id}), nil
	case rowTask:
		return m.open(frame{kind: frameTask, id: r.id}), nil
	case rowCompletedGroup:
		parent := m.cur().id
		if m.completedOpen == nil {
			m.completedOpen = map[string]bool{}
		}
		m.completedOpen[parent] = !m.completedOpen[parent]
		m.inspTop = 0
		return m.relayout(), nil
	case rowMate, rowCrew:
		// A deliberate session open starts a new failure chain: the fallback
		// steps inside this open append to it (recordOpenFailure), but an
		// earlier attempt's failures must not be mixed into this one's
		// summary. This is the only place the chain is cleared.
		m = m.clearOpenFailures()
		if target, ok := m.sessionAvailableFor(r); ok {
			return m.beginSession(r, target)
		}
		return m.beginAttach(r)
	default:
		return m, nil
	}
}

// open pushes a new frame and puts the selection on its first row.
func (m Model) open(f frame) Model {
	m.msg = footerMsg{}
	m.focus = paneList
	m.detail = false
	m.failureDetail = false
	m.failureTop = 0
	m.inspTop = 0
	return m.push(f).relayout()
}
