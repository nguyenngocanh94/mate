package console

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// The hand-over notice's own tests.
//
// The Console's in-grid announcement frame is a state of the model, and
// model_test-style assertions prove it exists. They cannot prove the reader
// ever saw it, and in Bubble Tea 1.2.4 the reader does not: tea.Sequence
// orders messages, not renderer flushes, and the alt screen the frame was
// drawn on is thrown away by ReleaseTerminal before the child starts. The
// guarantee this file is evidence for is therefore a different one - the
// notice `handoverNotice` writes to the terminal Bubble Tea has just
// released, on the main screen, where it survives the hand-over.
//
//	unit         the ExecCommand writes the notice, then runs the child,
//	             in that order, with a real child process
//	production   Enter's own exec message carries a *handoverNotice, so the
//	             unit test above is testing the path the keyboard takes
//	integration  a real tea.Program, with the real standardRenderer and a
//	             real alt screen, puts the notice on the terminal after the
//	             alt screen is released and before the child's first byte

// ---------- unit: the notice precedes the child ----------

// TestTheHandOverNoticeIsWrittenBeforeTheChildRuns drives the ExecCommand
// exactly as Bubble Tea's Program.exec does - SetStdin, SetStdout,
// SetStderr, Run - and asserts the byte order on the writer both the notice
// and the child were given. The child is a real process, so "before" is
// process ordering rather than a fake's bookkeeping.
func TestTheHandOverNoticeIsWrittenBeforeTheChildRuns(t *testing.T) {
	const childMark = "CHILD-HAS-THE-TERMINAL"
	child := exec.Command("sh", "-c", "printf %s "+childMark)

	notice := newHandoverNotice("attaching to crew-payments-api-2", child)

	var out bytes.Buffer
	notice.SetStdin(strings.NewReader(""))
	notice.SetStdout(&out)
	notice.SetStderr(io.Discard)
	if err := notice.Run(); err != nil {
		t.Fatalf("running the hand-over notice: %v", err)
	}

	got := out.String()
	iNotice := strings.Index(got, "attaching to crew-payments-api-2")
	iChild := strings.Index(got, childMark)
	switch {
	case iNotice < 0:
		t.Fatalf("the notice never reached the terminal: %q", got)
	case iChild < 0:
		t.Fatalf("the child never ran: %q", got)
	case iNotice > iChild:
		t.Fatalf("the notice landed after the child (%d > %d): %q", iNotice, iChild, got)
	}
	if !strings.HasPrefix(got, "\r\n") {
		t.Fatalf("the notice must open a fresh line rather than append to the shell prompt: %q", got)
	}
	if !strings.Contains(got[:iChild], "\r\n"+"attaching to crew-payments-api-2") {
		t.Fatalf("the notice is not on a line of its own: %q", got[:iChild])
	}
}

// TestTheHandOverNoticeReportsAWriteFailureRatherThanAttachingSilently: the
// notice is the reader's one guaranteed sighting of the hand-over and its
// detach keystroke. If it cannot be written the terminal is already gone,
// and attaching anyway would hand the session to a reader who was told
// nothing - which is the defect the notice exists to close.
func TestTheHandOverNoticeReportsAWriteFailureRatherThanAttachingSilently(t *testing.T) {
	ran := t.TempDir() + "/ran"
	child := exec.Command("sh", "-c", "touch "+ran)
	notice := newHandoverNotice("attaching to crew-payments-api-2", child)
	notice.SetStdin(strings.NewReader(""))
	notice.SetStdout(refusingWriter{})
	notice.SetStderr(io.Discard)

	err := notice.Run()
	if err == nil {
		t.Fatalf("a terminal that refused the notice produced no error")
	}
	if !strings.Contains(err.Error(), "hand-over notice") {
		t.Fatalf("error = %v, want it to name the notice rather than look like a child failure", err)
	}
	if _, statErr := exec.Command("test", "-e", ran).Output(); statErr == nil {
		t.Fatalf("the child ran even though the reader was never told about the hand-over")
	}
	// And the classification must not dress this up as an exit code: the
	// Console's own notice failure is the cause (so the reader can see the
	// hand-over, not the child, is what failed), and the line says the
	// subprocess never ran rather than reporting an exit it does not have.
	line := openFailureShortLine(classified(t, stepAttach, err))
	if !strings.Contains(line, "hand-over notice") || !strings.Contains(line, "did not run") {
		t.Fatalf("classified notice failure = %q, want the notice named and the child not run", line)
	}
	if strings.Contains(line, "exit") {
		t.Fatalf("classified notice failure = %q, must not report an exit code", line)
	}
}

// TestTheHandOverCarriesTheChildErrorCodeBackToTheConsole proves the
// fallback's machine-readable side channel: the child writes its envelope
// before returning, while stderr remains the real terminal for the attach
// hand-off. Exit 1 alone is deliberately insufficient; this fixture is the
// captain's unknown attach failure and must not render as generic failure.
func TestTheHandOverCarriesTheChildErrorCodeBackToTheConsole(t *testing.T) {
	child := exec.Command("sh", "-c", `printf '%s' '{"ok":false,"schema_version":1,"command":"attach","error":{"code":"unknown","message":"terminal already has an attached client","details":{"exit":1}}}' > "$MATE_ATTACH_RESULT_FILE"; exit 1`)
	var terminal bytes.Buffer
	notice := newHandoverNotice("attaching", child)
	notice.SetStdout(&terminal)
	notice.SetStderr(io.Discard)
	err := notice.Run()
	if err == nil {
		t.Fatal("failed child returned nil")
	}
	var report *attachReportError
	if !errors.As(err, &report) {
		t.Fatalf("error = %T %v, want attach report", err, err)
	}
	if report.Code != observability.CodeUnknown || report.Message != "terminal already has an attached client" {
		t.Fatalf("report = %+v, want the child's unknown code and message", report)
	}
	classifiedErr := classified(t, stepAttach, err)
	if classifiedErr.Code != string(observability.CodeUnknown) {
		t.Fatalf("classified report code = %q, want the child's unknown code", classifiedErr.Code)
	}
	// The message must be on the line itself, not only in the detail view:
	// the code alone cannot say which refusal the terminal gave.
	line := openFailureShortLine(classifiedErr)
	if !strings.Contains(line, "unknown") || !strings.Contains(line, "terminal already has an attached client") {
		t.Fatalf("failure line = %q, want the reported code and message", line)
	}
	if strings.Contains(line, "generic failure") {
		t.Fatalf("failure line = %q, must not fall back to a generic phrase", line)
	}
}

type refusingWriter struct{}

func (refusingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// TestTheNoticeCarriesTheDetachKeystrokeAndSaysItDoesNotStopTheAgent pins
// the text itself, independent of any golden frame: a regenerated fixture
// must not be able to drop the one sentence the reader keeps.
func TestTheNoticeCarriesTheDetachKeystrokeAndSaysItDoesNotStopTheAgent(t *testing.T) {
	for _, g := range []glyphSet{unicodeGlyphs, asciiGlyphs} {
		text := handoverNoticeText("crew-payments-api-2", g)
		for _, want := range []string{
			"mate attach",
			"crew-payments-api-2",
			"Ctrl+b then q",
			"does not stop the agent",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("handoverNoticeText with %v glyphs = %q, want it to say %q", g.Dot, text, want)
			}
		}
	}
}

// TestTheNoticeNeverCarriesAControlSequenceFromRecordedState: the notice is
// the one string in this package written straight to the terminal instead of
// through cells.go, so a recorded Herdr agent name carrying an escape
// sequence would otherwise be executed by the reader's terminal.
func TestTheNoticeNeverCarriesAControlSequenceFromRecordedState(t *testing.T) {
	hostile := "crew\x1b[31m-red\nsecond line\ttab\x9bmore"
	text := handoverNoticeText(hostile, unicodeGlyphs)
	for _, banned := range []string{"\x1b", "\n", "\r", "\t", "\x9b"} {
		if strings.Contains(text, banned) {
			t.Fatalf("handoverNoticeText passed through %q: %q", banned, text)
		}
	}
	if !strings.Contains(text, "crew") {
		t.Fatalf("sanitising the label removed the label: %q", text)
	}
}

// TestTheNoticeBoundsRecordedStateSoOneLineStaysOneLine: the notice is
// written to a terminal the Console no longer owns and cannot measure, so
// the only defence against a pathological recorded name is the bound in
// handoverNoticeText. Asserted in display cells, like every other width in
// this package.
func TestTheNoticeBoundsRecordedStateSoOneLineStaysOneLine(t *testing.T) {
	for _, g := range []glyphSet{unicodeGlyphs, asciiGlyphs} {
		long := strings.Repeat("crew-", 400)
		text := handoverNoticeText(long, g)
		// The fixed sentence plus the bounded label, and nothing more. The
		// generous ceiling is deliberate: this is a bound, not a layout -
		// the Console cannot know the width of a terminal it has released.
		if got := lipgloss.Width(text); got > handoverLabelCells+120 {
			t.Fatalf("a %d-cell recorded name produced a %d-cell notice with %v glyphs; the bound is not applied",
				lipgloss.Width(long), got, g.Name)
		}
		if !strings.Contains(text, "does not stop the agent") {
			t.Fatalf("bounding the label truncated the sentence the reader needs: %q", text)
		}
	}
	// A name a real allocator produces is not cut: a Crew id is `crew_` plus
	// 16 hex characters (internal/domain/id.go), well inside the bound.
	full := "crew_0123456789abcdef"
	if text := handoverNoticeText(full, unicodeGlyphs); !strings.Contains(text, full) {
		t.Fatalf("notice = %q, want the whole of a real id (%q) - the bound is a ceiling on hostile state, not a layout", text, full)
	}
}

// ---------- production: Enter's exec really carries the notice ----------

// TestEntersExecCarriesTheHandOverNotice binds the unit tests above to the
// keyboard: the second element of Enter's sequence is Bubble Tea's exec
// message, and the ExecCommand inside it is this package's notice rather
// than a bare *exec.Cmd. Without this, beginAttach could go back to
// tea.ExecProcess and every test in this file would still pass.
func TestEntersExecCarriesTheHandOverNotice(t *testing.T) {
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := toRunningAttempt(t, attachFixture(t, spy, 120, 36, unicodeGlyphs))
	_, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatalf("Enter returned no Cmd")
	}
	seq := reflect.ValueOf(cmd())
	if seq.Kind() != reflect.Slice || seq.Len() != 2 {
		t.Fatalf("Enter produced %T, want a two-element tea.Sequence", cmd())
	}
	second, ok := seq.Index(1).Interface().(tea.Cmd)
	if !ok {
		t.Fatalf("sequence element 1 is %T, want tea.Cmd", seq.Index(1).Interface())
	}
	// tea.execMsg and its fields are unexported, so the assertion is on the
	// dynamic type of the ExecCommand it holds - readable through reflection
	// without unsafe, and enough to name the implementation.
	msg := reflect.ValueOf(second())
	field := msg.FieldByName("cmd")
	if !field.IsValid() {
		t.Fatalf("Bubble Tea's exec message %T has no cmd field", second())
	}
	if field.IsZero() {
		t.Fatalf("Bubble Tea's exec message carries no ExecCommand")
	}
	if got := field.Elem().Type().String(); got != "*console.handoverNotice" {
		t.Fatalf("the exec message carries %s, want *console.handoverNotice - the notice is what puts the hand-over on the reader's terminal", got)
	}
}

// ---------- integration: a real Program, a real renderer, a real alt screen ----------

// orderedWriter is the terminal for the integration test. The renderer runs
// on its own goroutine and the notice is written from Program.exec's, so the
// writer has to serialise them to be evidence of ordering at all.
type orderedWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *orderedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *orderedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// waitFor polls the terminal stream for a substring. Condition-based rather
// than timed: the renderer's own flush cadence is 60fps and the child is a
// real process, so any fixed sleep would either be flaky or slow.
func (w *orderedWriter) waitFor(t *testing.T, what, substr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.String(), substr) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (%q) in the terminal stream:\n%q", what, substr, w.String())
}

// TestARealProgramPutsTheNoticeOnTheTerminalAfterReleasingTheAltScreen is
// the evidence B1 asked for: not the shape of a Cmd, but the bytes a real
// tea.Program with the real standardRenderer and a real alt screen puts on
// the terminal, in order.
//
// Why this is faithful without a PTY: the only thing a PTY would add is
// termios. ReleaseTerminal's flush, the DisableAltScreenBuffer that discards
// the frame the announcement was drawn on, Program.exec's SetStdout and the
// child's own writes all go to p.output, which is exactly the writer
// tea.WithOutput is given - a *os.File in production, this buffer here.
// Bubble Tea's own screen_test.go asserts alt-screen sequences the same way.
//
// The ordering asserted is the one that matters: the notice is on the main
// screen, after the alt screen carrying the in-grid announcement is gone and
// before the child has written anything. Recording the announcement's text
// somewhere in the stream would not do - under tea.ExecProcess the
// announcement appears only inside alt-screen frames, which the very next
// sequence throws away.
func TestARealProgramPutsTheNoticeOnTheTerminalAfterReleasingTheAltScreen(t *testing.T) {
	const childMark = "CHILD-HAS-THE-TERMINAL"
	spy := &attachSpy{
		tree: sameTree(sampleTree()),
		cmd: func(string) *exec.Cmd {
			return exec.Command("sh", "-c", "printf %s "+childMark)
		},
	}
	// The model is navigated to the one attachable row before the Program
	// takes it, so the test drives a single keystroke rather than decoding
	// arrow keys off a pipe. Init re-reads, which is why loads is not
	// asserted here - the counting evidence lives in attach_test.go.
	m := toRunningAttempt(t, attachFixture(t, spy, 120, 36, unicodeGlyphs))

	out := &orderedWriter{}
	// An input reader that is already at EOF, the way Bubble Tea's own
	// tests drive a Program without a tty. It matters for more than the
	// read loop: Program.exec hands p.input to the child as its stdin, and
	// an exec.Cmd whose Stdin is not an *os.File spawns a copy goroutine
	// that Cmd.Wait blocks on - so a still-open pipe here would wedge the
	// hand-over rather than exercise it.
	prog := tea.NewProgram(m, tea.WithAltScreen(),
		tea.WithInput(bytes.NewReader(nil)), tea.WithOutput(out))
	done := make(chan error, 1)
	go func() {
		_, err := prog.Run()
		done <- err
	}()

	// The alt screen is up: the Program is running and its renderer owns
	// the terminal.
	out.waitFor(t, "the alt screen", ansi.EnableAltScreenBuffer)
	prog.Send(tea.WindowSizeMsg{Width: 120, Height: 36})
	out.waitFor(t, "the first rendered frame", "payments-api")

	prog.Send(tea.KeyMsg{Type: tea.KeyEnter})
	// The whole round trip, on the real terminal stream: the child writes,
	// the Console takes the terminal back, and the return message lands on
	// a repainted alt screen.
	out.waitFor(t, "the child's output", childMark)
	out.waitFor(t, "the Console taking the terminal back", "Detached from")
	prog.Quit()
	if err := <-done; err != nil {
		t.Fatalf("the Program exited with %v", err)
	}

	stream := out.String()
	altGone := strings.Index(stream, ansi.DisableAltScreenBuffer)
	childAt := strings.Index(stream, childMark)
	if altGone < 0 {
		t.Fatalf("the alt screen was never released:\n%q", stream)
	}
	if childAt < 0 || childAt < altGone {
		t.Fatalf("the child did not write after the alt screen was released (alt=%d child=%d):\n%q", altGone, childAt, stream)
	}
	handover := stream[altGone:childAt]
	for _, want := range []string{"mate attach", "Ctrl+b then q", "does not stop the agent"} {
		if !strings.Contains(handover, want) {
			t.Fatalf("the terminal never showed %q between releasing the alt screen and the child's first byte; what it got was:\n%q", want, handover)
		}
	}
}

func TestHandoverKeepsChildOutputWritersIdentical(t *testing.T) {
	var terminal bytes.Buffer
	h := newHandoverNotice("notice", exec.Command("true"))
	h.SetStdout(&terminal)
	h.SetStderr(&terminal)

	if h.cmd.Stdout != h.cmd.Stderr {
		t.Fatalf("child stdout and stderr writers differ: stdout=%T stderr=%T", h.cmd.Stdout, h.cmd.Stderr)
	}
	// os/exec uses equality of these interface values to share one copier;
	// preserving it prevents concurrent writes when both streams target one
	// test terminal, and direct *os.File assignment is what preserves a real
	// terminal fd for the child in production.
}
