package console

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m.relayout(), nil
	case treeLoadedMsg:
		m = m.onTreeLoaded(msg)
		// The auto-refresh chain starts here, once, on the first
		// treeLoadedMsg the Console ever sees - see Init's own comment for
		// why not there directly. Every later treeLoadedMsg (a manual 'r',
		// an action's own re-read, or the chain's own
		// tick) finds treeTickStarted already true and this is a no-op.
		if !m.treeTickStarted {
			m.treeTickStarted = true
			return m, treeTickCmd(m.treeTickInterval(), m.treeGen)
		}
		return m, nil
	case treeTickMsg:
		return m.onTreeTick(msg)
	case actionDoneMsg:
		return m.onActionDone(msg)
	case stageDoneMsg:
		return m.onStageDone(msg), nil
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
	m.treeLoadInFlight = false
	if msg.err != nil {
		if m.hasLoaded {
			// The header's "stale" wording (frame.go) reads this rather than
			// m.msg: the footer line below is cleared by the reader's very
			// next keystroke, but the tree on screen stays exactly what the
			// last successful load said until another one succeeds, so the
			// header keeps saying so for exactly as long as that is true.
			m.lastLoadErr = msg.err
			if m.actionAfterRead != nil {
				m.msg = *m.actionAfterRead
				m.actionAfterRead = nil
				m.msg.text += " " + m.g.Dot + " snapshot re-read failed; r retries"
				return m
			}
			// A refresh that failed does not throw away the snapshot that is
			// already on screen: the error page says "no earlier snapshot is
			// loaded", which would be false, and the reader would lose a
			// usable picture over a transient read. The header already says
			// "stale" and how old what they are looking at is; the message
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
	m.lastLoadErr = nil
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

// startLoad issues a tree load and marks it in flight, whether it was asked
// for by the reader ('r'), the failed-phase retry, or a background tick
// (onTreeTick). Every call site that can issue a loadCmd goes through this
// rather than calling loadCmd directly, so treeLoadInFlight always reflects
// whether a load is actually outstanding.
func (m Model) startLoad() (Model, tea.Cmd) {
	m.treeLoadInFlight = true
	return m, loadCmd(m.load)
}

// onTreeTick is the auto-refresh chain (Init, treeTickCmd): every
// treeTickInterval it starts a background load unless one is already in
// flight, then always reschedules itself so the chain never stops on its
// own. A tick whose gen no longer matches m.treeGen is a leftover from a
// chain Init did not start (never happens in production - see Init's own
// comment - but the same defence session_mode.go's ticks carry) and is
// dropped without rescheduling, since the chain that owns treeGen is
// already rescheduling itself.
func (m Model) onTreeTick(msg treeTickMsg) (Model, tea.Cmd) {
	if msg.gen != m.treeGen {
		return m, nil
	}
	if m.actionBusy && !m.actionStartedAt.IsZero() && !msg.at.IsZero() {
		prev := m.msg
		m.msg = m.runningLine(msg.at.Sub(m.actionStartedAt))
		// A box action mirrors its running line into the box zone
		// (runAction); keep the two saying the same thing.
		if m.boxMsg == prev {
			m.boxMsg = m.msg
		}
	}
	next := treeTickCmd(m.treeTickInterval(), m.treeGen)
	if m.treeLoadInFlight {
		return m, next
	}
	m, load := m.startLoad()
	return m, tea.Batch(load, next)
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
	// still quits from inside it.
	// The diff overlay is the third exception, for the same reason a pager
	// is: it is a full-region reader, and q is how every pager closes one.
	// Ctrl+C still quits from inside it, and the key line says so (seams.go).
	if key == "ctrl+c" || (key == "q" && !m.actionInputMode && !m.diff.open) {
		if m.actionBusy {
			return m.onBusyQuit(), tea.Quit
		}
		m.quitting = true
		return m, tea.Quit
	}
	l := m.listLayout()
	if l.TooSmall {
		// Nothing else is drawn, so nothing else responds: a key that moved
		// a selection nobody can see would silently change where the reader
		// lands when the window grows back.
		return m, nil
	}
	if m.diff.open {
		// The diff overlay is modal for the same reason (diff.go): a key
		// that moved the selection behind a full-region patch would be
		// invisible. Here q reaches this branch and closes, because the
		// branch above lets it through while the overlay is open.
		return m.onDiffKey(key, l), nil
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
	if key == "f2" {
		// The same key that moves focus in the session view moves it here, so
		// one key means "the other zone" everywhere in the Console. Tab still
		// cycles list -> inspector -> box; F2 goes straight to the box and
		// back.
		if _, panel := m.boxRegion(layout(m.w, m.h)); panel {
			if m.focus == paneBox {
				m.focus = paneList
			} else {
				m.focus = paneBox
			}
			m.msg = footerMsg{}
		}
		return m, nil
	}
	// The Actions menu and its confirmation are modal, and they are checked
	// before the box panel: the box zone opens the menu itself (`o`), and a
	// j or an Enter with the menu open belongs to the menu, not to the rows
	// behind it.
	if m.actions || m.confirm != nil {
		return m.onActionOverlayKey(key)
	}
	if m.focus == paneBox {
		// The box panel owns the keyboard while focus is on it (box_keys.go):
		// Enter, a, l and o act on the box instead of on the list row, and
		// Esc, Tab or F2 hands focus back.
		model, cmd := m.onProjectBoxKey(msg)
		return model, cmd
	}
	switch m.phase {
	case phaseLoading:
		return m, nil
	case phaseFailed:
		if key == "r" {
			m.msg = infoMsg("Retrying snapshot read " + m.g.Ellipsis)
			return m.startLoad()
		}
		return m, nil
	}

	switch key {
	case "a":
		return m.beginActions(), nil
	case "n":
		return m.beginNewProject(), nil
	case "s":
		return m.beginMateStart()
	case "m":
		// The communication mode is a Project setting, so the key is offered
		// on the Project frame only - there is nothing for it to act on at
		// the Workspace level, where the selection is a Project row but the
		// frame lists several.
		if m.cur().kind != frameProject {
			return m, nil
		}
		return m.beginModeToggle(m.currentProject().ProjectID)
	case "r":
		m.msg = footerMsg{}
		return m.startLoad()
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

// onMouse routes a mouse event to the surface it landed on
// (box_mouse.go). Mouse reporting is enabled Program-wide
// (tea.WithMouseAllMotion, cmd/mate/console.go), so an event can reach the
// Console at any time; every branch below resolves it against the geometry
// of the frame that is actually drawn, and an event on a frame with nothing
// clickable on it does nothing at all.
func (m Model) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.phase != phaseReady {
		return m, nil
	}
	if l := m.listLayout(); l.TooSmall {
		return m, nil
	}
	return m.onFrameMouse(msg)
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
	// The box panel joins the cycle when it is drawn (mvp.md task 15): its
	// three keys are bare, so they need a focus of their own or they would
	// have to take Enter and `r` away from the list.
	_, panel := m.boxRegion(layout(m.w, m.h))
	if l.Inspector > 0 {
		switch {
		case m.focus == paneList:
			m.focus = paneInspector
		case m.focus == paneInspector && panel:
			m.focus = paneBox
		default:
			m.focus = paneList
		}
		return m
	}
	if panel && !m.detail {
		if m.focus == paneBox {
			m.focus = paneList
			return m
		}
		m.focus = paneBox
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

// onEnter opens a Project row, toggles the Completed group, and shows a
// Mate or Crew row in the next pane (stageRow).
func (m Model) onEnter() (tea.Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	switch r.kind {
	case rowProject:
		return m.open(frame{kind: frameProject, id: r.id}), nil
	case rowCompletedGroup:
		parent := m.cur().id
		if m.completedOpen == nil {
			m.completedOpen = map[string]bool{}
		}
		m.completedOpen[parent] = !m.completedOpen[parent]
		m.inspTop = 0
		return m.relayout(), nil
	case rowMate, rowCrew:
		return m.stageRow(r)
	default:
		return m, nil
	}
}

// open pushes a new frame and puts the selection on its first row.
func (m Model) open(f frame) Model {
	m.msg = footerMsg{}
	m.focus = paneList
	m.detail = false
	m.diff = diffFlow{}
	m.inspTop = 0
	return m.push(f).relayout()
}
