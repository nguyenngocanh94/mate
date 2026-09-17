package console

import (
	"errors"
	"fmt"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The attach lifecycle: handing the terminal to an agent session, taking it
// back, and every way that can be refused or fail.
//
// Two facts this file keeps apart at every step, because folding them
// together is the whole reason this surface is its own file:
//
//	refused  the Console read the snapshot, decided the attach cannot
//	         happen, and started nothing. The message reads "Attach
//	         refused: <what the snapshot says>, nothing was started".
//	failed   the Console started `matev2 attach` and it came back non-zero,
//	         or could not be started at all. The message reads "Attach
//	         failed: <cause> <evidence>" - the evidence parenthetical
//	         (session_failure.go) carries what the child itself reported.
//
// The two never render alike, and the difference is words rather than
// colour: both are red, and a reader with a monochrome terminal still has
// "refused ... nothing was started" against "failed ... matev2 attach exit".
//
// Neither ever claims the agent is alive or dead. A refusal reports what
// the recorded snapshot says (ADR 0027: a stale binding means mate could
// not confirm the agent stopped - that is an unconfirmed stop, not a dead
// agent and not a live one); a failure reports what the subprocess did.
// Liveness would need the crew health observer of ADR 0019, which does not
// exist.
//
// The Console never talks to Herdr. The design notes' sample code calls
// `herdr agent attach` directly; in this codebase attach is this same
// binary's own `matev2 attach <target>` use case (ADR 0010), built by the
// AttachCmdFunc the CLI layer supplies and run through tea.Exec so Bubble
// Tea releases and restores the terminal. boundary_test.go enforces that
// rather than trusting this paragraph.
//
// The hand-over announcement lives in handover.go, not here: the frame this
// file draws is a state of the model, and in Bubble Tea 1.2.4 that frame is
// not what reaches the reader.

// attachPhase is where the terminal is. It is deliberately not part of
// Model.phase: the snapshot read and the terminal handover are different
// axes, and a handover neither invalidates the snapshot nor is invalidated
// by one.
type attachPhase int

const (
	// attachIdle: the Console owns the terminal.
	attachIdle attachPhase = iota
	// attachAnnouncing: the announcement is written and the exec command is
	// queued behind it. It is a state of its own rather than a message set
	// on the way out because it is what ignores stray keys while the
	// terminal is leaving - and because it is the frame the reader gets if
	// the renderer does flush in time. It is not the guarantee that they
	// were told: handover.go is.
	attachAnnouncing
	// attachHeld: the `matev2 attach` subprocess owns the terminal. The
	// Console is not drawing and not reading state; the frame it would draw
	// says exactly that, because it is what the terminal shows for the
	// moment between the child exiting and AttachFinishedMsg arriving.
	attachHeld
)

// attachReturn is what the terminal came back from, kept until the single
// re-read that follows completes so the message can name both the session
// and the age of the new snapshot.
type attachReturn int

const (
	returnNone attachReturn = iota
	returnDetached
	returnFailed
)

// attachTargetRef is what the Console recorded about the session it handed
// the terminal to. label is what to call it on the message line: the
// recorded Herdr agent name when that field read Known, the abbreviated
// target id otherwise - never a blank, and never an agent name the snapshot
// did not actually carry.
type attachTargetRef struct {
	kind  rowKind
	id    string
	label string
}

// attachFlow is the whole attach lifecycle state. One field on Model.
type attachFlow struct {
	phase  attachPhase
	target attachTargetRef

	// ret and retFrom describe the return: what happened, and which
	// session it was. The failure's own text lives in the failure chain
	// (session_failure.go), which is the one renderer for it.
	ret     attachReturn
	retFrom string
}

// attachReportError is the child's coded error plus the original process
// result. The child writes its normal JSON envelope to the terminal, but the
// handover's private result channel lets the Console recover that taxonomy
// after the terminal is returned without replacing stderr with a pipe.
type attachReportError struct {
	Cause   error
	Code    observability.Code
	Message string
	Details map[string]any
}

func (e *attachReportError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Code)
}

func (e *attachReportError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// AttachHandedOverMsg is sent between the announcement and the subprocess.
// tea.Sequence runs the two in order, which orders the *messages* - it does
// not force a renderer flush, so it does not by itself put the announcing
// frame in front of the reader (handover.go explains why nothing can, and
// what carries that guarantee instead). What the ordering buys is that the
// flow is in attachHeld before the child can hand the terminal back.
// Exported for the same reason AttachFinishedMsg is - cmd/matev2's wiring and
// the tests recognize it without reaching into package internals.
type AttachHandedOverMsg struct{}

// ---------- Enter: refuse, or announce and hand over ----------

// beginAttach is Enter on a Mate or Crew row.
//
// The refusal is decided from the snapshot alone, before any subprocess
// exists (attachRefusal). Only when the snapshot says the attach can be
// tried does the Console build the command, announce the hand-over, and
// queue the exec behind that announcement.
func (m Model) beginAttach(r row) (Model, tea.Cmd) {
	if reason, refused := m.attachRefusal(r); refused {
		m.msg = m.withEarlierMarker(m.refusalMsg(reason))
		return m, nil
	}
	target, ok := m.attachTargetFor(r)
	if !ok {
		// attachRefusal already established that this row names an
		// attachable entity, so this is unreachable through the keyboard.
		// It is a refusal rather than a silent no-op because a keystroke
		// that does nothing at all is indistinguishable from a lost key.
		m.msg = m.withEarlierMarker(m.refusalMsg("this row does not name an attach target"))
		return m, nil
	}
	cmd, err := m.buildAttachCmd(target.id)
	if err != nil {
		// The Console tried to start `matev2 attach` and could not. That is a
		// failure, not a refusal - and it must not leave the flow in
		// attachAnnouncing, which ignores keys while it waits for a
		// subprocess that will never run. It is recorded in the failure chain
		// like every other step, so the message is cause-first and the same
		// 'e' detail view carries it with any earlier failure.
		m.att = attachFlow{}
		m = m.recordOpenFailure(stepAttach, target.label, target.id, err)
		m.msg = m.openFailureMsg()
		return m, nil
	}
	m.att = attachFlow{phase: attachAnnouncing, target: target}
	m.msg = infoMsg(fmt.Sprintf("Attaching to %s via matev2 attach %s detach: Ctrl+b then q",
		target.label, m.g.Dot))
	// The announcement the reader is *guaranteed* to see is not this frame:
	// it is the line handoverNotice writes to the terminal Bubble Tea has
	// just released, immediately before the child starts. handover.go
	// carries the reason - in Bubble Tea 1.2.4 no frame drawn inside the
	// alt screen can be that guarantee, because ReleaseTerminal discards
	// the alt screen. tea.Exec rather than tea.ExecProcess is how the
	// notice gets onto that path.
	//
	// Sequence, not Batch, is still the right ordering for the *model*: the
	// hand-over message has to land before the exec message, or the flow
	// can be told it holds the terminal after the child has already given
	// it back. Batch would race the two.
	return m, tea.Sequence(
		func() tea.Msg { return AttachHandedOverMsg{} },
		tea.Exec(
			newHandoverNotice(handoverNoticeText(target.label, m.g), cmd),
			func(err error) tea.Msg { return AttachFinishedMsg{Err: err} },
		),
	)
}

// refusalMsg is every refusal's message: the prefix, the snapshot's own
// reason, and the clause that says no subprocess ran. One place, so a
// refusal can never be worded as a failure by accident.
func (m Model) refusalMsg(reason string) footerMsg {
	return errMsg(attachRefusedPrefix + reason + " " + m.g.Dot + " " + attachNotAttempted)
}

// buildAttachCmd asks the CLI layer for the subprocess. A missing closure
// or a nil command is reported rather than swallowed: without it the flow
// would announce a hand-over that never happens.
func (m Model) buildAttachCmd(target string) (*exec.Cmd, error) {
	if m.attachCmd == nil {
		return nil, errors.New("this Console was built without an attach command")
	}
	cmd := m.attachCmd(target)
	if cmd == nil {
		return nil, errors.New("matev2 attach could not be built for " + target)
	}
	return cmd, nil
}

// attachTargetFor resolves the row to the `matev2 attach <target>` argument
// and the label the messages use.
//
// The target is always an id from the snapshot - a Mate id or a Crew id -
// never a Herdr pane or tab id and never the agent name:
// application.ResolveAttachTarget resolves ids and the reserved
// `herdr_agent` name only (ADR 0010), and the agent name here is recorded
// state, used for the reader and nothing else.
func (m Model) attachTargetFor(r row) (attachTargetRef, bool) {
	switch r.kind {
	case rowMate:
		mate := m.currentProject().Mate
		if !mate.Designated.IsKnown() || mate.Designated.Value.MateID == "" {
			return attachTargetRef{}, false
		}
		id := mate.Designated.Value.MateID
		return attachTargetRef{kind: rowMate, id: id, label: m.attachLabel(id, mate.AgentName)}, true
	case rowCrew:
		c, ok := m.crewByID(r.id)
		if !ok || c.CrewID == "" {
			return attachTargetRef{}, false
		}
		return attachTargetRef{kind: rowCrew, id: c.CrewID, label: m.attachLabel(c.CrewID, c.AgentName)}, true
	default:
		return attachTargetRef{}, false
	}
}

// attachLabel names the session on the message line: the recorded agent
// name when that field is Known and non-empty, the abbreviated id
// otherwise. An Absent or Unknown agent name is not rendered as a blank and
// not invented - the id it falls back to is a fact the snapshot does carry.
func (m Model) attachLabel(id string, agent query.Field[string]) string {
	if agent.IsKnown() && agent.Value != "" {
		return agent.Value
	}
	return shortID(id, m.g)
}

// ---------- the refusal, decided from the snapshot ----------

// Message prefixes. The two prefixes are the one thing that separates "we
// did not try" from "we tried and it broke", so they are words, not tones:
// both messages render red, and a reader with a monochrome terminal has to
// be able to tell them apart.
const (
	attachRefusedPrefix = "Attach refused: "
	attachFailedPrefix  = "Attach failed: "
	// attachNotAttempted closes every refusal. "refused" already means the
	// Console decided this on its own, but the clause says the part that
	// matters out loud: no subprocess ran, so nothing about the agent
	// changed and nothing about the agent was observed.
	attachNotAttempted = "nothing started"
)

// attachRefusal decides, from the snapshot alone, whether Enter can attach
// to this row - and says why not. A binding that is stale, absent or
// unreadable is refused here rather than by Herdr: a stale binding means
// mate could not confirm the agent stopped (ADR 0027), and `matev2 attach`
// refuses it outright, so announcing an attach that cannot happen would be a
// lie the Console tells before the subprocess gets a chance to tell the
// truth. A *held but not active* (reserved) binding is deliberately not a
// refusal - see the reserved case below.
//
// The Mate branch reads MateNode.Binding from the snapshot exactly like the
// Crew branch reads CrewNode.Binding - both are the query layer's own
// runtime_binding read - so a stale or absent Mate binding is refused here
// the same way a stale or absent Crew one is, without deferring the
// decision to the attach subprocess.
//
// Every reason is short enough that the whole message fits the design's
// narrowest gallery frame, 80 columns
// (TestEveryAttachMessageFitsAnEightyColumnFrame). The message line
// truncates without a marker, so a long reason would silently lose the half
// that says why; below 80 columns - a size the design gallery does not
// cover - a long recorded name can still be cut at the pane edge, like
// every other value on the frame.
func (m Model) attachRefusal(r row) (string, bool) {
	var binding query.Field[query.BindingValue]
	switch r.kind {
	case rowMate:
		mate := m.currentProject().Mate
		switch {
		case mate.Designated.State == query.Unknown:
			return "the Mate could not be read; r re-reads", true
		case !mate.Designated.IsKnown() || mate.Designated.Value.MateID == "":
			return "this Project has no Mate", true
		case !mate.Designated.Value.Status.OccupiesActiveSlot():
			// created, stopped: there is no session to attach to, and the
			// snapshot is enough to know it. OccupiesActiveSlot is the
			// domain's own predicate for "this Mate holds the Project's one
			// active slot" (internal/domain/status.go), which is exactly the
			// set of statuses a session can exist for.
			return "Mate recorded " + string(mate.Designated.Value.Status) + ", no session to attach", true
		}
		binding = mate.Binding
	case rowCrew:
		c, ok := m.crewByID(r.id)
		if !ok {
			return "this attempt is no longer in the snapshot", true
		}
		binding = c.Binding
	default:
		return "this row cannot be attached to", true
	}
	switch binding.State {
	case query.Absent:
		return "no runtime binding recorded", true
	case query.Unknown:
		return "the binding could not be read; r re-reads", true
	}
	switch binding.Value.Status {
	case query.BindingActive, query.BindingReserved:
		// reserved is not a refusal. application.ResolveAttachTarget (the
		// CLI's own resolution, ADR 0010) accepts any held binding that is
		// not stale - reserved included - and a reserved binding is
		// frequently a live agent: a SIGKILL mid-spawn leaves `preparing` +
		// reserved binding + live agent (ADR 0012). Refusing it here would
		// refuse an attach the CLI allows, and would hide the one session a
		// crash recovery most needs to reach. The Agent View is enterable
		// for the same reason (sessionAvailableFor, session_mode.go).
		return "", false
	case query.BindingStale:
		// Its own sentence, not "not active": stale is the one status that
		// means mate could not confirm the agent stopped, and a reader who
		// sees it lumped in with a generic "not active" would read it as
		// "not running".
		return "binding recorded stale, stop unconfirmed", true
	case "":
		// A Known binding whose status is the empty string: the read
		// succeeded and recorded nothing to compare against active, so it
		// is refused as unreadable rather than printed as "binding recorded
		// , not active".
		return "the binding records no status", true
	default:
		return fmt.Sprintf("binding recorded %s, not active", binding.Value.Status), true
	}
}

func unavailableSuffix(_ string, refused bool) string {
	if refused {
		return " (unavailable)"
	}
	return ""
}

// ---------- the hand-over and the return ----------

// onAttachHandedOver records that the subprocess now owns the terminal.
// Nothing else changes: the navigation stack, the selection and the
// snapshot are exactly what they were, so taking the terminal back lands
// the reader where they left.
func (m Model) onAttachHandedOver() (tea.Model, tea.Cmd) {
	if m.att.phase != attachAnnouncing {
		// Not ours: a hand-over with no announcement behind it would leave
		// the Console ignoring keys for a subprocess it never started.
		return m, nil
	}
	m.att.phase = attachHeld
	m.msg = infoMsg(fmt.Sprintf("%s owns this terminal %s Console idle until it returns",
		m.att.target.label, m.g.Dot))
	return m, nil
}

// onAttachFinished takes the terminal back and re-reads once: the Console
// was not drawing and not reading while the child owned the screen, so
// whatever is on screen is now of unknown age. Exactly one re-read, on both
// the clean and the failed path - the terminal came back either way.
//
// The message names the outcome now and is completed when the re-read
// lands (attachReadNote), so the reader is never told a snapshot time that
// has not happened yet.
//
// Unlike the hand-over, this is honoured whatever phase the flow is in: the
// terminal is back either way, and the cost of a message that never
// followed an announcement of ours is one read and a line that names no
// session. Refusing it could leave the flow holding a terminal nothing is
// going to return.
func (m Model) onAttachFinished(msg AttachFinishedMsg) (tea.Model, tea.Cmd) {
	from := m.att.target.label
	targetID := m.att.target.id
	m.att = attachFlow{retFrom: from}
	if msg.Err != nil {
		m.att.ret = returnFailed
		// Record the attach step in the same chain the fallbacks use, so the
		// classified reason is the one the summary and the 'e' detail view
		// read (and so an earlier stream/snapshot failure is not lost behind
		// it). The error's own message is part of that record
		// (openFailureEvidence), which is what keeps the child's report on the
		// one-line summary.
		m = m.recordOpenFailure(stepAttach, from, targetID, msg.Err)
	} else {
		m.att.ret = returnDetached
	}
	m.msg = m.attachReturnMessage("", false)
	return m, loadCmd(m.load)
}

// attachReadNote completes the return message once the single re-read that
// follows a hand-over has landed. It is called after onTreeLoaded, so it
// deliberately overwrites whatever that set: on this path the fact the
// reader needs first is what happened to the attach, and the standard
// refresh wording ("Refresh failed: ...") would drop it.
func (m Model) attachReadNote(readErr error) Model {
	if m.att.ret == returnNone {
		return m
	}
	m.msg = m.attachReturnMessage(m.tree.AsOf.Format("15:04:05"), readErr != nil)
	m.att = attachFlow{}
	return m
}

// attachReturnMessage is the message for a terminal that has just come
// back. readAt is the new snapshot's time, empty while the re-read is still
// in flight; readFailed says the re-read did not happen.
//
// The detach line always says the agent was not stopped, because detaching
// does not stop it (Ctrl+b q is Herdr's detach, ADR 0010) - and it says
// nothing else about the agent, because nothing else was established.
//
// Neither branch ever writes Mate/Crew lifecycle status: attach is a TTY
// hand-off, successful or not, and never issues a start/stop. So a Project
// screen that still reads the Mate as running right after a failed attach
// (issue #60) is rendering the same recorded status the row already had
// before the attempt, correctly - a failed attach had no chance to change
// it, and the one re-read this flow triggers (TestAttachReturnRendersThe...
// in model_test.go) proves that read is fresh, not stale. Making the row
// itself distrust "running" here would require inferring liveness from the
// attach outcome, which ADR 0025 forbids; closing that gap for real is the
// G7-04 health observer (ADR 0019), not this hand-off.
func (m Model) attachReturnMessage(readAt string, readFailed bool) footerMsg {
	dot := " " + m.g.Dot + " "
	switch m.att.ret {
	case returnDetached:
		text := "Detached"
		if m.att.retFrom != "" {
			text += " from " + m.att.retFrom
		}
		text += dot + "agent not stopped"
		switch {
		case readFailed:
			text += dot + "re-read failed"
		case readAt != "":
			text += dot + "re-read " + readAt
		default:
			text += dot + "re-reading" + m.g.Ellipsis
		}
		return okMsg(text)
	case returnFailed:
		// The chain's own summary leads with the cause and carries the
		// evidence the child reported (its message and the exit), plus a
		// "+N earlier" marker when the stream or snapshot view failed before
		// this (session_failure.go). The fallback below is unreachable -
		// every returnFailed path records its failure first
		// (onAttachFinished) - and exists so a missed one says less rather
		// than nothing.
		text := m.openFailureLine(false)
		if text == "" {
			text = attachFailedPrefix + "no reason was recorded"
		}
		switch {
		case readFailed:
			text += dot + "the re-read failed too"
		case readAt != "":
			// The failure stands, and the picture below it is fresh: the one
			// re-read runs on this path too, because the Console was blind
			// while the subprocess had the terminal.
			text += dot + "snapshot re-read"
		}
		return errMsg(text)
	default:
		return m.msg
	}
}

// ---------- the key line during a hand-over ----------

// attachKeyHints is the key line while the terminal is leaving or gone. It
// names the child's detach keystroke, because the child owns the keyboard:
// the Console's own keys do not reach this process while the subprocess
// runs, so advertising them would be an offer it cannot keep.
//
// q is deliberately not offered here either, even though onKey still
// answers it if a key ever does arrive - the Console must not tell a reader
// whose keyboard belongs to an agent session that q will quit.
func (m Model) attachKeyHints() ([]keyHint, bool) {
	switch m.att.phase {
	case attachAnnouncing, attachHeld:
		return []keyHint{{key: "Ctrl+b q", desc: "Detach; does not stop the agent", sacrifice: keyAction}}, true
	default:
		return nil, false
	}
}

// attachHoldsTerminal reports whether the Console is between announcing a
// hand-over and getting the terminal back. Keys other than the ones onKey
// answers everywhere are ignored while it is true: a selection moved by a
// key the reader aimed at the agent session would silently change where
// they land on return.
func (m Model) attachHoldsTerminal() bool { return m.att.phase != attachIdle }
