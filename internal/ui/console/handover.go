package console

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// The hand-over notice: the one thing the reader is guaranteed to see
// before an agent session takes the terminal.
//
// Why this exists rather than a rendered frame. The design requires the
// Console to announce the hand-over before the terminal leaves, and the
// Console does draw that announcement (attachAnnouncing, and the
// attach-attaching golden fixtures). What it cannot do is guarantee the
// reader ever sees it, and in the pinned Bubble Tea 1.2.4 the reader does
// not:
//
//   - the event loop ends every message with renderer.write(model.View()),
//     and standardRenderer.write only replaces a buffer; the flush happens
//     on a 60fps ticker (standard_renderer.go's listen/flush);
//   - tea.Sequence serialises commands, not flushes: its sequenceMsg branch
//     calls p.Send for each command in order (tea.go), so the announcement
//     message and the exec message can - and on an idle machine do - both
//     land inside one 16ms tick, and only the last View written is ever
//     flushed;
//   - and it would not help if it were. Program.exec calls ReleaseTerminal,
//     whose renderer.stop() does flush the buffer - onto the alt screen -
//     and whose restoreTerminalState then writes DisableAltScreenBuffer,
//     which discards exactly the screen that frame was drawn on.
//
// So no frame drawn inside the alt screen can be the announcement. The
// announcement has to be written to the main screen, after Bubble Tea has
// released the terminal and before the child starts - and the only point on
// that path a caller owns is the ExecCommand's Run. Program.exec calls
// ReleaseTerminal, then SetStdin/SetStdout/SetStderr, then Run (exec.go), so
// a Run that writes before delegating writes onto the restored main screen,
// above the session - a real line of terminal output rather than a frame in a
// buffer about to be discarded.
//
// What that establishes and no more: the bytes reached the terminal, in that
// order, before the child's first byte. Whether the line is still on screen
// after the child has had the terminal is the child's business - `herdr
// terminal attach` may take the screen however it likes - so nothing here
// claims it persists.
//
// The in-grid announcement frame is kept: it is what the Console shows if a
// flush does land, it is the state that ignores stray keys while the
// terminal is leaving, and it is a gallery state. It is simply not the
// guarantee.

// handoverNotice is the tea.ExecCommand the Console hands to tea.Exec. It
// writes one line to the terminal Bubble Tea has just released, then runs
// `mate attach`.
//
// The setters mirror Bubble Tea's own osExecCommand (exec.go): each only
// fills a stream the caller left nil, so a command that was built with its
// own stdio keeps it.
type handoverNotice struct {
	// notice is already sanitised and already bounded - see
	// handoverNoticeText. It is written raw to the terminal, so it must
	// never be assembled here from recorded state.
	notice string
	cmd    *exec.Cmd
	out    io.Writer
}

func newHandoverNotice(notice string, cmd *exec.Cmd) *handoverNotice {
	return &handoverNotice{notice: notice, cmd: cmd}
}

func (h *handoverNotice) SetStdin(r io.Reader) {
	if h.cmd.Stdin == nil {
		h.cmd.Stdin = r
	}
}

// SetStdout records the terminal as well as giving it to the child: the
// notice and the child write to the same stream, which is what makes "the
// notice came first" an ordering the reader actually experiences.
func (h *handoverNotice) SetStdout(w io.Writer) {
	h.out = w
	if h.cmd.Stdout == nil {
		h.cmd.Stdout = w
	}
}

func (h *handoverNotice) SetStderr(w io.Writer) {
	if h.cmd.Stderr == nil {
		// Assign the writer directly. os/exec passes an *os.File through as fd
		// 2, while wrapping it in a MultiWriter replaces fd 2 with a pipe.
		// That would both remove the child's terminal and create a second
		// copier when stdout and stderr are the same writer.
		h.cmd.Stderr = w
	}
}

// Run writes the notice, then runs the child.
//
// A notice that cannot be written aborts the hand-over instead of attaching
// silently. That is not tidiness: the notice is the reader's only guaranteed
// sighting of which session is taking their terminal and how to get it back,
// and a write failure on a just-released terminal means the terminal is
// already gone. The error is wrapped so the failure chain classifies it as a
// subprocess that did not run (session_failure.go's classifyOpenFailure) -
// which is what happened.
//
// The line opens with CRLF rather than LF and closes the same way: the main
// screen has just been restored with the cursor wherever it was when the
// Console started, which is usually mid-prompt, and the terminal may still
// be in raw mode on some paths. CR is harmless when it is not needed.
func (h *handoverNotice) Run() error {
	result, err := os.CreateTemp("", "mate-attach-result-")
	if err != nil {
		return fmt.Errorf("creating the attach result channel: %w", err)
	}
	resultPath := result.Name()
	if err := result.Close(); err != nil {
		_ = os.Remove(resultPath)
		return fmt.Errorf("closing the attach result channel: %w", err)
	}
	defer func() { _ = os.Remove(resultPath) }()
	h.cmd.Env = withEnv(h.cmd.Env, observability.EnvAttachResultFile, resultPath)
	if h.out != nil {
		if _, err := io.WriteString(h.out, "\r\n"+h.notice+"\r\n"); err != nil {
			return fmt.Errorf("writing the hand-over notice to the terminal: %w", err)
		}
	}
	err = h.cmd.Run()
	if err == nil {
		return nil
	}
	if report, readErr := readAttachResult(resultPath); readErr == nil && report.Error != nil {
		return &attachReportError{Cause: err, Code: report.Error.Code, Message: report.Error.Message, Details: report.Error.Details}
	}
	return err
}

func withEnv(env []string, key, value string) []string {
	prefix := key + "="
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, prefix+value)
}

func readAttachResult(path string) (observability.Envelope, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return observability.Envelope{}, err
	}
	var envelope observability.Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return observability.Envelope{}, err
	}
	return envelope, nil
}

// handoverNoticeText is the line itself.
//
// Unlike every other string in this package it does not go through
// cells.go, because it is not part of the grid: it is written to a terminal
// the Console no longer owns and cannot measure. Two consequences are
// handled here and nowhere else:
//
//   - the label is recorded state - a Herdr agent name or an abbreviated id -
//     so it is sanitised (sanitizeText: ANSI stripped, C0/DEL/C1 mapped to a
//     space) before it can reach a terminal that would execute it;
//   - and it is bounded, so a pathological recorded name cannot turn one
//     line into a screenful.
//
// The sentence says the three things the reader needs and nothing the
// Console has not established: which session is taking the terminal, that
// `mate attach` is what is running, and that the way back is Ctrl+b then q
// and does not stop the agent (ADR 0010). It does not say the agent is
// alive: the recorded binding said active, which is not liveness.
func handoverNoticeText(label string, g glyphSet) string {
	dot := " " + g.Dot + " "
	return "mate console: attaching to " + truncateEnd(sanitizeText(label), handoverLabelCells, g) +
		" via mate attach" + dot +
		"detach with Ctrl+b then q" + dot +
		"detaching does not stop the agent"
}

// handoverLabelCells bounds the recorded name in the notice. 48 is chosen
// against the same yardstick as the inspector's value column: `prefix_` plus
// 16 hex characters is 21 cells for a Crew id (internal/domain/id.go), and a
// Herdr agent name allocated by runtime.AllocateAgentName is shorter than
// this, so a real name is never cut - the bound is a ceiling on recorded
// state this package did not generate, not a layout.
const handoverLabelCells = 48
