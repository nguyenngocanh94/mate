package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/ui/console"
)

// cmdConsole is `matev2 <workspace-dir>` (and `matev2` with no arguments in
// a workspace): the interactive Bubble Tea console.
//
// This is the one place internal/ui/console's boundary (no store, no
// runtime, no Herdr - see internal/ui/console/doc.go) is bridged to real
// state. LoadFunc closes over the opened workspace and calls query.Load;
// everything that would need a live agent is refused with a message naming
// the mvp.md task that wires it, never a fake success.
func cmdConsole(dir string, stdout, stderr io.Writer) error {
	ws, err := store.Open(dir)
	if err != nil {
		return err
	}
	stdinFile, stdoutFile, err := consoleTerminalFiles(stdout)
	if err != nil {
		return err
	}
	if err := (runtime.TTYHandoff{Stdin: stdinFile, Stdout: stdoutFile}).CheckTerminal(); err != nil {
		return consoleTerminalRefusal(err)
	}

	ctx := context.Background()
	load := func(loadCtx context.Context) (query.Snapshot, error) {
		return query.Load(loadCtx, ws)
	}
	model := console.New(load, notWiredAttachCmd, notWiredAction).WithContext(ctx)

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx),
		tea.WithInput(stdinFile), tea.WithOutput(stdoutFile))
	final, err := program.Run()
	if err != nil {
		return fmt.Errorf("console exited with an error: %w", err)
	}
	if cm, ok := final.(console.Model); ok {
		if desc, abandoned := cm.AbandonedAction(); abandoned {
			fmt.Fprintf(stderr, "\nmatev2 console: quit while %s was still running.\n", desc)
		}
	}
	return nil
}

// notWiredAttachCmd is the AttachCmdFunc seam before task 09. It returns nil
// rather than a command that would appear to work: the Console renders a nil
// command as "attach could not be built", which is the truth today.
//
// TODO(task 09): build `matev2 attach <target>` here once the session view
// is wired to a real pane.
func notWiredAttachCmd(string) *exec.Cmd { return nil }

// notWiredAction is the ActionFunc seam before tasks 07 and 09. Every action
// the Console can reach needs a live Herdr agent, and nothing in matev2
// starts one yet, so each one fails with the task that will.
//
// TODO(task 07): start/resume a Mate. TODO(task 09): stop, and the session
// view's own attach.
func notWiredAction(_ context.Context, req console.ActionRequest) (string, error) {
	switch req.Action {
	case console.ActionStart, console.ActionResume:
		return "", errors.New("starting a Mate is not wired until mvp.md task 07")
	case console.ActionStop:
		return "", errors.New("stopping an agent is not wired until mvp.md task 07")
	case console.ActionOnboard:
		return "", errors.New("use `matev2 project add <name> <repo>`; console onboarding is not wired yet")
	default:
		return "", fmt.Errorf("%s is not wired in this build", req.Action)
	}
}

// consoleTerminalFiles requires the caller's own stdout to be a real
// *os.File, which an in-process test harness writing to a bytes.Buffer is
// not. That keeps the dispatch tests from ever reaching tea.NewProgram, and
// it checks the streams the Program is actually given rather than the
// process globals.
func consoleTerminalFiles(stdout io.Writer) (*os.File, *os.File, error) {
	out, ok := stdout.(*os.File)
	if !ok {
		return nil, nil, errNotATerminal
	}
	return os.Stdin, out, nil
}

// errNotATerminal is a usage error, so `matev2 <dir> > file` exits 2 with a
// sentence that says what to do instead of a wall of Bubble Tea noise.
var errNotATerminal = newUsageError(
	"matev2 console needs a real terminal on stdin and stdout; run it from an interactive shell, not a pipe or redirect")

// consoleTerminalRefusal rewords runtime.TTYHandoff's shared terminal check
// for this call site: that check runs the real tty ioctl (reused rather
// than duplicated), but its message is an attach client's and would name
// the wrong command.
func consoleTerminalRefusal(error) error { return errNotATerminal }
