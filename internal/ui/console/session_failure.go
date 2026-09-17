package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/nguyenngocanh94/matev2/internal/observability"
)

// The session-open failure chain.
//
// Opening an embedded Agent View is a chain of steps - the live PTY stream,
// then the snapshot controller, then the classic `mate attach` hand-off -
// and each step can fail for a reason the next step knows nothing about. The
// pre-fix Console reported only the taxonomy *code* of the step that failed
// most recently (sessionErrorCode), because the message line is one line and
// a coded message is arbitrary text. Two information losses followed from
// that one trade:
//
//   - the code is a bounded vocabulary but not self-explanatory: a reader who
//     saw `runtime_unavailable` could not tell a Herdr server that had shut
//     down from a binary that was never on PATH, and got no next step;
//   - each fallback reset sessionFlow, so by the time the last step failed
//     the first failure's cause was already gone - three failures in a row
//     read as one (the last), not as three.
//
// This file keeps a small, bounded record of every step that failed opening
// the last session (oldest first) on the Model *outside* sessionFlow, which
// is exactly the structure each fallback boundary throws away. From it the
// Console builds both a cause-first one-line summary and a re-openable detail
// view (frame.go's failure overlay) that carries the whole chain. The line
// generalises the failure taxonomy the attach fallback already had: cause
// first, then the technical evidence the step's error actually carried - the
// code, the error's own message, and the exit.
//
// Carrying the message is the point, not a decoration: the code alone was
// the loss this file repairs, and a phrase substituted for the message
// cannot distinguish a runtime that shut down from a binary never on PATH.
// The message is the step's own words, so it is evidence, not authorship;
// where the Console does not have enough to name a cause it says so and
// still shows the message (openFailureCause, openFailureEvidence).
//
// What it deliberately does not do: infer the agent's lifecycle from a
// failed open. A failed attach establishes nothing about whether the agent is
// alive (ADR 0010, and the issue #60 rule); the next-step text is derived
// from the error's own code, never from the outcome. A `not_found` says the
// recorded name may be out of date, not that the Crew is dead.

// sessionFailureStep is the step of the open chain that failed. The order of
// the consts is the chain's own order and the order the detail view lists
// them in.
type sessionFailureStep int

const (
	stepLiveStream sessionFailureStep = iota
	stepSnapshotView
	stepAttach
)

// label is the step's name in both the summary line and the detail view.
// "Attach" matches the established `Attach failed:` prefix the fallback
// already used (attach.go), so the two paths read the same.
func (s sessionFailureStep) label() string {
	switch s {
	case stepLiveStream:
		return "Live stream"
	case stepSnapshotView:
		return "Snapshot view"
	case stepAttach:
		return "Attach"
	default:
		return "Session"
	}
}

// sessionFailure is one failed step. Every field is derived from the error
// the step returned (or from the recorded target), never from the agent's
// lifecycle - so nothing here can overclaim what a failed open observed.
type sessionFailure struct {
	Step     sessionFailureStep
	Target   string // the agent name, or the abbreviated id when there is none
	TargetID string // the Mate/Crew id the attach would have used
	// Cause is plain-language and bounded (openFailureCauses): what happened,
	// before any code.
	Cause string
	// Code is the taxonomy code the error itself carried. When the error was
	// only an exit code, Code is derived from the exit when that exit names
	// exactly one code, and CodeFromExit says so - the code is then evidence
	// the Console read off the exit, not one the child reported.
	Code         string
	CodeFromExit bool
	// ExitCode is the process exit code, or -1 when the error was not a
	// process result. ExitNote names the two results that have no code: a
	// signalled child and one that never ran.
	ExitCode int
	ExitNote string
	// Err is the error's own message, empty when it carried none. It is
	// sanitised where it is drawn (line.add), never here.
	Err string
	// Next is the next step, derived from the code alone (openFailureNext),
	// or a statement that the evidence does not determine one.
	Next string
}

const (
	exitNoteSignalled = "mate attach was signalled"
	exitNoteNotRun    = "mate attach did not run"
)

// recordOpenFailure appends one failed step to the chain. It is a Model
// method because the chain lives on the Model (model.go), and it returns the
// Model so a caller in the middle of building one can thread it through.
func (m Model) recordOpenFailure(step sessionFailureStep, target, targetID string, err error) Model {
	m.openFailures = append(m.openFailures, classifyOpenFailure(step, target, targetID, err))
	return m
}

// clearOpenFailures starts a new chain. It is called when the reader
// deliberately opens a session (Enter on a Mate/Crew row) and nowhere else:
// the fallback steps inside one open must append to the chain they started,
// which is the whole reason it does not live in sessionFlow.
func (m Model) clearOpenFailures() Model {
	m.openFailures = nil
	m.failureDetail = false
	m.failureTop = 0
	return m
}

// classifyOpenFailure turns one step's error into the record the summary and
// the detail view are built from. It is the failure taxonomy for the whole
// open chain; the attach fallback reads its own exit codes through the same
// table (attachExits) so the two cannot disagree about what exit 20 was.
func classifyOpenFailure(step sessionFailureStep, target, targetID string, err error) sessionFailure {
	f := sessionFailure{Step: step, Target: target, TargetID: targetID, ExitCode: -1}

	// A coded error, in either wrapper: the Console's own *observability.Error
	// from a session port, or the attach result channel's attachReportError
	// around the child's envelope. Either one is a report from a step that
	// actually ran.
	ran := false
	var coded *observability.Error
	if errors.As(err, &coded) {
		ran = true
		f.Code = string(coded.Code)
		f.Err = coded.Message
	}
	var report *attachReportError
	if errors.As(err, &report) {
		ran = true
		if report.Code != "" {
			f.Code = string(report.Code)
		}
		if report.Message != "" {
			f.Err = report.Message
		}
	}

	// The process result, when there is one. errors.As walks the report's
	// Unwrap, so this sees the exit under both shapes.
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		ran = true
		if code := exit.ExitCode(); code >= 0 {
			f.ExitCode = code
			if f.Code == "" {
				if derived := attachExits[code].Code; derived != "" {
					f.Code = string(derived)
					f.CodeFromExit = true
				}
			}
		} else {
			// Killed by a signal: ExitCode is -1 and the error string names
			// the signal. There is no code to report, so the signal is said
			// instead.
			f.ExitNote = exitNoteSignalled
			f.Err = errorText(err)
		}
	case step == stepAttach && err != nil && !ran:
		// No process result and no report on the attach path: the child never
		// ran (a build failure, or an exec failure). Say that rather than
		// report a code that does not exist.
		f.ExitNote = exitNoteNotRun
		if f.Err == "" {
			f.Err = errorText(err)
		}
	}

	if f.Err == "" && err != nil {
		// No code, no report message, no signal: whatever the step's own error
		// says is still evidence, and it is kept as evidence rather than
		// promoted to a cause the Console cannot vouch for
		// (openFailureEvidence). A bare exit result is the one exception: its
		// whole text is "exit status N", which the exit evidence already
		// carries as a number, so treating it as a message would print the
		// number twice and push the exit's own phrase out of the cause.
		if text := errorText(err); !isBareExitStatus(text) {
			f.Err = text
		}
	}

	f.Cause = openFailureCause(f, err)
	f.Next = openFailureNext(f, err)
	return f
}

// openFailureCause is the plain-language cause, chosen before the code. The
// code map is authoring, not machine text: each phrase must add something the
// code does not already say - a phrase that only de-underscores the code is
// printed next to that code and costs cells the evidence needs - and each is
// bounded so the cause survives the one-line summary at 80 columns
// (TestEveryAttachMessageFitsAnEightyColumnFrame).
//
// A code with no entry here is not dressed up as one: the exit its error
// carried, then the error's own message, and only then is the cause called
// undetermined.
func openFailureCause(f sessionFailure, err error) string {
	if c, ok := openFailureCauses[observability.Code(f.Code)]; ok {
		return c
	}
	switch {
	case isDeadline(err):
		return causeDeadline
	case errors.Is(err, io.EOF):
		return causeStreamEnded
	case f.Step == stepAttach && f.ExitNote == exitNoteNotRun:
		// The child never ran (a build failure, a failed hand-over notice
		// write, an exec failure): that is the cause, and the error's own
		// words - why it could not be started - are its evidence, so they
		// appear after this phrase rather than in front of it.
		return exitNoteNotRun
	case f.Step == stepAttach && f.ExitNote == exitNoteSignalled:
		return exitNoteSignalled
	case f.ExitCode >= 0 && f.Err == "":
		// An exit result and nothing else: what that exit means is the cause.
		// The message gate matters - a report that carries a message has not
		// said "nothing to report" just because its exit code is a shared one
		// (the counter-review's N4), so the message is used below instead.
		if facts, ok := attachExits[f.ExitCode]; ok {
			return facts.Cause
		}
		return fmt.Sprintf("mate attach exited %d", f.ExitCode)
	}
	// The last resort is the step's own message. It is arbitrary text, which
	// is why it is bounded nowhere and the one-line summary may have to cut
	// it (frame.go), but it is also the most concrete fact available: saying
	// "cause undetermined" next to the error's own words would read worse and
	// cost the line the words. The evidence parenthetical then drops it as
	// redundant (openFailureEvidence), so it appears exactly once.
	if f.Err != "" {
		return sanitizeText(f.Err)
	}
	return causeUndetermined
}

// The Console's own cause phrases, in one place so the summary, the detail
// view and the tests that measure them cannot drift apart. Each is short by
// construction: unlike a step's own message, these are what the one-line
// summary promises to keep whole at 80 columns.
const (
	causeUndetermined = "cause undetermined"
	causeDeadline     = "the runtime did not answer in time"
	causeStreamEnded  = "the session stream ended"
)

// isBareExitStatus reports whether an error's text is nothing but a child's
// exit number ("exit status 3", what a bare *exec.ExitError says). That text
// names what the exit evidence already names, in more cells, so it is not the
// step's message - the difference between a subprocess that said something and
// one that only exited.
func isBareExitStatus(text string) bool {
	digits, ok := strings.CutPrefix(text, "exit status ")
	if !ok || digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// openFailureCauses is the one plain-language phrase per taxonomy code. Every
// entry deliberately says something the code does not: the code is a bounded
// vocabulary, but a reader meeting `state_conflict` still needs to be told
// that the recorded state moved, not that there was "conflicting state".
var openFailureCauses = map[observability.Code]string{
	observability.CodeUsage:              "the request was rejected",
	observability.CodeStateConflict:      "the recorded state changed",
	observability.CodeRuntimeUnavailable: "the runtime is not reachable",
	observability.CodeTargetBlocked:      "another agent holds the pane",
	observability.CodeInteractionTimeout: "the interaction did not resolve",
	observability.CodeInteractionExpired: "the interaction expired",
	observability.CodeNeedsRepair:        "the workspace needs repair",
	observability.CodeNotFound:           "no agent was found",
	observability.CodeAlreadyExists:      "that name is already in use",
	observability.CodePermission:         "the runtime refused it",
	observability.CodeTimeout:            "the runtime did not answer in time",
}

// attachExitFacts is what the Console may claim for one process exit the CLI
// publishes (docs/phase1/agent.md section 4, the constants in
// internal/observability). This is the one exit-keyed table on the failure
// path; the keys come from the constants rather than from literals so the two
// cannot drift.
//
// Code is set only for the exits that name exactly one taxonomy code (2, 20,
// 30, 40) - that is what lets an exit-only failure carry a real code into the
// detail view, marked as derived. The exits several codes share (1, 10, 31)
// have no Code, because guessing which of them it was is the same overclaim
// as guessing the agent's life from the outcome. Cause is the phrase used
// when the error carried no message of its own, and every entry must be
// distinct from its code's own phrase for the same reason openFailureCauses'
// entries must be.
type attachExitFacts struct {
	Code  observability.Code
	Cause string
}

var attachExits = map[int]attachExitFacts{
	observability.ExitGeneric:            {Cause: "no reason was reported"},
	observability.ExitUsage:              {Code: observability.CodeUsage, Cause: "the request was rejected"},
	observability.ExitStateConflict:      {Cause: "the recorded state changed"},
	observability.ExitRuntimeUnavailable: {Code: observability.CodeRuntimeUnavailable, Cause: "the runtime is not reachable"},
	observability.ExitTargetBlocked:      {Code: observability.CodeTargetBlocked, Cause: "another agent holds the pane"},
	observability.ExitInteractionWait:    {Cause: "the interaction did not resolve"},
	observability.ExitNeedsRepair:        {Code: observability.CodeNeedsRepair, Cause: "the workspace needs repair"},
}

// openFailureNext is the suggested next step, from the evidence alone. A
// timeout is retryable, a held pane is a conflict to resolve, and anything
// the code does not determine says so - the reader is never told to retry a
// conflict, or handed a lifecycle claim the error did not make.
func openFailureNext(f sessionFailure, err error) string {
	switch {
	case isDeadline(err) || f.Code == string(observability.CodeTimeout):
		return "retryable: open the session again"
	case f.Code == string(observability.CodeRuntimeUnavailable):
		return "check the runtime is running, then retry"
	case f.Code == string(observability.CodeTargetBlocked):
		return "conflict: another agent holds the pane; stop or detach it, then retry"
	case f.Code == string(observability.CodeStateConflict), f.Code == string(observability.CodeAlreadyExists):
		return "another change is in flight; re-read (r), then retry"
	case f.Code == string(observability.CodeNeedsRepair):
		return "run `mate doctor`, then retry"
	case f.Code == string(observability.CodeNotFound):
		return "re-read (r): the recorded name may be out of date"
	case f.Code == string(observability.CodeInteractionTimeout), f.Code == string(observability.CodeInteractionExpired):
		return "retryable: the pending interaction did not resolve"
	case f.Code == string(observability.CodeUsage), f.Code == string(observability.CodePermission):
		return "the request was refused; check the recorded session, then retry"
	case f.Step == stepAttach && f.ExitNote == exitNoteNotRun:
		return "check `mate` is installed and on PATH, then retry"
	}
	return "cause undetermined from this evidence; re-read (r)"
}

// isDeadline reports an error the runtime documents as a read deadline: a
// read result, not a request to close the session. It is a timeout for the
// purpose of the next-step suggestion, and it is what makes the stream
// fallback retryable rather than unknown.
func isDeadline(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ---------- the one-line summary ----------

// openFailureMsg is the message line while the chain is non-empty: the cause
// of the step that failed most recently, cause first and technical evidence
// after, plus the mode now in use. It replaces each open step's old code-only
// line (sessionErrorCode).
func (m Model) openFailureMsg() footerMsg {
	if text := m.openFailureLine(true); text != "" {
		return errMsg(text)
	}
	return footerMsg{}
}

// openFailureLine is the one-line summary of the chain's most recent failure,
// built for the fallbacks (stream -> snapshot, snapshot -> attach) whose
// earlier cause sessionFlow's reset would otherwise discard.
//
// withMode appends the mode the Console is in now, read live from
// sessionModeLabel rather than stored on the failure - the mode is a fact
// about right now, not about the failure. The attach return path passes false:
// there the reader already knows the terminal came back, and the cells are
// better spent on the re-read status that follows.
//
// When more than one step failed the line also says how many earlier ones are
// behind it ("+N earlier"), so three failures read as three rather than as
// the last one - the detail view (openFailureDetail) is where all of them are
// readable. The detail key is advertised on the key line, not repeated here,
// because the message line is one line at 80 columns and the cause is what
// must survive.
func (m Model) openFailureLine(withMode bool) string {
	if len(m.openFailures) == 0 {
		return ""
	}
	last := m.openFailures[len(m.openFailures)-1]
	text := openFailureShortLine(last)
	if withMode {
		if mode := m.sessionModeLabel(); mode != "" {
			text += " " + m.g.Dot + " " + mode
		}
	}
	return text + m.earlierFailureMarker()
}

// earlierFailureMarker says how many earlier steps are behind the one on the
// line, so a reader who sees the last failure of a chain knows the earlier
// causes exist and that 'e' is where they are. The detail key itself is
// advertised on the key line, not here: the message line is one line, and the
// cause is what must survive it.
func (m Model) earlierFailureMarker() string {
	if earlier := len(m.openFailures) - 1; earlier > 0 {
		return fmt.Sprintf(" %s +%d earlier", m.g.Dot, earlier)
	}
	return ""
}

// withEarlierMarker appends the marker to a message the chain did not build
// (an attach refusal, which is a refusal and not a failed step). Every
// recorded failure is earlier than the refusal, so the count is the whole
// chain. The marker yields when it would push the line past the frame: a
// refusal's own reason and its "nothing started" clause have to survive at 80
// columns (the PR #72 finding), and the marker is the cheaper fact to lose.
func (m Model) withEarlierMarker(msg footerMsg) footerMsg {
	if len(m.openFailures) == 0 || msg.text == "" {
		return msg
	}
	marker := fmt.Sprintf(" %s +%d earlier", m.g.Dot, len(m.openFailures))
	if cells(" "+msg.text+marker) <= m.w {
		msg.text += marker
	}
	return msg
}

// openFailureShortLine is one failure's summary: the step, the cause, then
// the evidence the step's error actually carried, in one parenthetical
// (openFailureEvidence). The evidence is what makes the line worth reading -
// a reader who saw `runtime_unavailable` could not tell a Herdr server that
// shut down from a binary never on PATH, and that message is exactly the
// evidence that distinguishes them. Where there is no evidence at all the
// line names the cause and stops rather than inventing a parenthetical.
func openFailureShortLine(f sessionFailure) string {
	text := f.Step.label() + " failed: " + f.Cause
	if evidence := openFailureEvidence(f, f.Cause); evidence != "" {
		text += " (" + evidence + ")"
	}
	return text
}

// openFailureEvidence is the parenthetical: the code the error carried (or
// the one the Console derived from an exit that names exactly one), the
// error's own message when it says something the cause does not already say,
// and the exit - full text, because "Attach failed:" has already named the
// command, so a bare "exit 1" cannot be read as some other process.
//
// The message is dropped only when it would restate the cause, which is the
// case when the cause *is* that message (an uncoded error), and an exit whose
// number the cause already names ("mate attach exited 7") is not repeated
// either. The pair is not bounded: the error's message is arbitrary text, and
// messageLine cuts an over-long line with a visible marker (frame.go) - the
// safety net that makes carrying the message affordable. The cause itself is
// bounded (openFailureCauses, attachExits), so the cut cannot land inside it.
func openFailureEvidence(f sessionFailure, cause string) string {
	parts := make([]string, 0, 2)
	if f.Code != "" {
		parts = append(parts, f.Code)
	}
	if message := sanitizeText(f.Err); message != "" && message != cause {
		if len(parts) > 0 {
			parts[len(parts)-1] += ": " + message
		} else {
			parts = append(parts, message)
		}
	}
	switch {
	case f.ExitCode >= 0 && cause != fmt.Sprintf("mate attach exited %d", f.ExitCode):
		parts = append(parts, fmt.Sprintf("exit %d", f.ExitCode))
	case f.ExitNote != "" && cause != f.ExitNote:
		parts = append(parts, f.ExitNote)
	}
	return strings.Join(parts, ", ")
}

// sessionModeLabel is the mode the Console is in right now, as the detail
// view's "Now" field. It is derived from the live flow state, never stored,
// so it cannot go stale.
func (m Model) sessionModeLabel() string {
	switch {
	case m.sess.stream != nil && m.sess.phase == sessionActive:
		return "live stream"
	case m.sess.phase == sessionActive || m.sess.phase == sessionFallback:
		return "snapshot view"
	case m.attachHoldsTerminal():
		return "mate attach"
	}
	return "no session open"
}

// ---------- the re-openable detail view ----------

// openFailureDetail is the full failure chain in the main region: every step
// that failed, oldest first, with the target, the plain-language cause, the
// code (and its provenance), the exit, the error's own message and the
// suggested next step. This is where the content the one-line summary has to
// abbreviate lives in full, which is what lets the summary lead with the
// cause instead of the code.
func (m Model) openFailureDetail(l frameLayout) []*line {
	return windowContent(m.openFailureDetailContent(l.Cols), m.failureTop, l.Body, m.g, m.p)
}

func (m Model) openFailureDetailContent(w int) []*line {
	valueWidth := w - labelWidth - 3
	if valueWidth < 8 {
		valueWidth = 8
	}
	content := []*line{
		newLine().pad(1).add("Session open failed", m.p.Bold),
		newLine(),
	}
	if n := len(m.openFailures); n > 0 {
		last := m.openFailures[n-1]
		if last.Target != "" {
			content = append(content, m.textField("Target", last.Target, m.p.Fg, valueWidth)...)
		}
		if last.TargetID != "" && last.TargetID != last.Target {
			content = append(content, m.textField("Reference", last.TargetID, m.p.Dim, valueWidth)...)
		}
	}
	content = append(content, m.textField("Now", m.sessionModeLabel(), m.p.Fg, valueWidth)...)
	for i, f := range m.openFailures {
		content = append(content, newLine())
		content = append(content, newLine().pad(1).add(
			fmt.Sprintf("%d. %s failed", i+1, f.Step.label()), m.p.Red))
		content = append(content, m.textField("Cause", f.Cause, m.p.Fg, valueWidth)...)
		content = append(content, m.textField("Code", codeFieldValue(f), m.p.Fg, valueWidth)...)
		if exit := exitFieldValue(f); exit != "" {
			content = append(content, m.textField("Exit", exit, m.p.Fg, valueWidth)...)
		}
		message := f.Err
		if message == "" {
			message = "(no message recorded)"
		}
		content = append(content, m.textField("Message", message, m.p.Dim, valueWidth)...)
		content = append(content, m.textField("Next", f.Next, m.p.Dim, valueWidth)...)
	}
	content = append(content, newLine())
	content = append(content, newLine().pad(1).add(
		"Esc closes this view. Nothing here changes the session.", m.p.Dim))
	return content
}

// codeFieldValue renders the code with where it came from: the error's own
// taxonomy, or an exit the Console mapped. "derived" matters - it is the
// difference between what the child reported and what the Console read off
// an exit code it was told is a coarse signal (docs/phase1/agent.md).
func codeFieldValue(f sessionFailure) string {
	switch {
	case f.Code == "":
		return "none recorded"
	case f.CodeFromExit:
		return fmt.Sprintf("%s (derived from exit %d)", f.Code, f.ExitCode)
	default:
		return f.Code
	}
}

func exitFieldValue(f sessionFailure) string {
	switch {
	case f.ExitCode >= 0:
		return fmt.Sprintf("mate attach exit %d", f.ExitCode)
	case f.ExitNote != "":
		return f.ExitNote
	}
	return ""
}

// onFailureDetailKey is the detail view's keys. q and ctrl+c still quit
// (they are handled before this is reached, in onKey); everything else is
// scroll or close, so no key here can move the list behind the overlay.
func (m Model) onFailureDetailKey(key string, l frameLayout) Model {
	switch key {
	case "esc", "backspace", "e":
		m.failureDetail = false
		m.failureTop = 0
		return m
	case "up", "k":
		return m.scrollFailureDetail(-1, l)
	case "down", "j":
		return m.scrollFailureDetail(1, l)
	case "pgup":
		return m.scrollFailureDetail(-l.page(), l)
	case "pgdown":
		return m.scrollFailureDetail(l.page(), l)
	}
	return m
}

func (m Model) scrollFailureDetail(delta int, l frameLayout) Model {
	total := len(m.openFailureDetailContent(l.Cols))
	m.failureTop = clampTop(m.failureTop+delta, 0, total, l.Body)
	return m
}
