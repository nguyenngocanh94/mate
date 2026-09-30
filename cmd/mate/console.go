package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/nguyenngocanh94/mate/internal/host"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// cmdConsole is `mate <workspace-dir>` (and `mate` with no arguments in
// a workspace): the interactive Bubble Tea console.
//
// This is the one place internal/ui/console's boundary (no store, no
// runtime, no Herdr - see internal/ui/console/doc.go) is bridged to real
// state. LoadFunc closes over the opened workspace and calls query.Load;
// everything that would need a live agent is refused with a message naming
// the mvp.md task that wires it, never a fake success.
func cmdConsole(dir string, stdout, stderr io.Writer) error {
	return runConsole(dir, stdout, stderr, false)
}

// cmdConsoleLaunch is `mate console [<workspace-dir>]`: the interactive
// console, and on a known host (WezTerm, Ghostty) it lays out its columns -
// the agent stage and the file review - before the TUI starts, so Enter
// fills them.
func cmdConsoleLaunch(args []string, stdout, stderr io.Writer) error {
	if len(args) > 1 {
		return newUsageError("usage: mate console [<workspace-dir>]")
	}
	dir := ""
	if len(args) == 1 {
		dir = args[0]
	}
	resolved, err := findWorkspaceDir(dir)
	if err != nil {
		return err
	}
	return runConsole(resolved, stdout, stderr, true)
}

func runConsole(dir string, stdout, stderr io.Writer, split bool) error {
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
	// One set of live dependencies for the whole Console run: the Herdr
	// adapter and the agent-name registry it shares, so a Mate started from
	// the action menu and the stream opened on it a keystroke later agree
	// about which names are reserved.
	deps := spawn.LiveDeps()

	// The observer of mvp.md section 4b runs for as long as the workspace is
	// open, and only then: it lives in this process, so quitting the console
	// means nobody is watching the crews and no new incident is opened. What
	// it already wrote stays in `incidents.log`.
	//
	// It is also the single writer of `.mate/mate.db` (mvp.md M5): the
	// timeline is recorded at the end of each of its polls, and the database
	// handle's advisory lock is released when the console quits.
	watcher, timelineDB, err := consoleWatcherWithTimeline(dir, deps)
	if err != nil {
		return err
	}
	if timelineDB != nil {
		defer timelineDB.Close()
	}
	watcher.Start(ctx)
	defer watcher.Stop()

	// The auto daemon of mvp.md section 5 runs beside it, and like it only
	// while the workspace is open: a closed console sends nothing, which is
	// the honest shape of a feature whose whole point is that it types into
	// somebody's composer. It sends only for the projects whose `mate/.auto`
	// exists, and it re-reads that flag every tick.
	pilot, err := consolePilot(dir, deps)
	if err != nil {
		return err
	}
	pilot.Start(ctx)
	defer pilot.Stop()

	load := func(loadCtx context.Context) (query.Snapshot, error) {
		snap, err := query.Load(loadCtx, ws)
		if err != nil {
			return snap, err
		}
		// query.Load reads files; the health column is an observation. The
		// snapshot picks up whatever the observer has seen by now, and the
		// crews it has not seen keep their Absent health. The daemon's own
		// state - when it last sent, why it last could not - rides along the
		// same way, and so does the observer's standing word about the
		// terminal runtime it could not reach.
		snap = withCrewHealth(snap, watcher.Snapshot())
		snap = withRuntimeNotice(snap, watcher)
		snap = withAutoStatus(snap, pilot.Snapshot())
		return withTokens(snap, ws), nil
	}
	h := host.Open(host.Detect(os.Getenv), host.Options{Env: os.Getenv, SelfCols: func() int {
		cols, _, err := term.GetSize(stdoutFile.Fd())
		if err != nil {
			return 0
		}
		return cols
	}})
	notice := ""
	var columns *consoleColumns
	if h != nil {
		if columns, err = newConsoleColumns(h, os.Getenv); err != nil {
			notice = "no next pane: " + err.Error()
		} else {
			defer columns.close()
			// `mate console` lays the columns out now; `mate <dir>` at the
			// first Enter, which finds them gone and makes them.
			if split {
				if err := columns.layout(ctx); err != nil {
					// Said on the status line: stderr is under the alt
					// screen by the time anyone could read it.
					notice = "no next pane: " + err.Error()
				}
			}
			if notice == "" && columns.review == "" {
				notice = "a crew's file changes need the Fresh editor: brew install fresh-editor"
			}
		}
	}
	// A Jev configuration problem is said on the same status line, after
	// any pane message; the console stays usable either way.
	noticeClient, noticeErr := consoleNoticeClient(ws)
	if noticeErr != nil {
		if notice != "" {
			notice += "; "
		}
		notice += noticeErr.Error()
	}
	action := consoleAction(ws, deps)
	if noticeClient != nil {
		action = consoleNoticeAction(ws, deps, noticeClient, action)
	}
	model := console.New(load, action).
		WithNoticeClassifier(noticeClient != nil).
		WithContext(ctx).
		WithStage(consoleStage(ws, deps, columns)).
		WithKindGlyphs(probeKindGlyphs(os.Getenv)).
		WithHarnessIcons(probeNerdIcons(os.Getenv, execOutput)).
		WithNotice(notice).
		WithClipboard(func(seq []byte) {
			// One write per sequence: the renderer also writes this file
			// from its own goroutine, one frame per write, so a single
			// write lands between two frames rather than inside one.
			_, _ = stdoutFile.Write(seq)
		})

	// Cell motion, not all motion: the Console answers presses and the
	// wheel, and nothing in it follows a bare pointer.
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx),
		tea.WithInput(stdinFile), tea.WithOutput(stdoutFile), tea.WithMouseCellMotion())
	final, err := program.Run()
	if err != nil {
		return fmt.Errorf("console exited with an error: %w", err)
	}
	if cm, ok := final.(console.Model); ok {
		if desc, abandoned := cm.AbandonedAction(); abandoned {
			fmt.Fprintf(stderr, "\nmate console: quit while %s was still running; waiting for it to undo what it started...\n", desc)
			waitAbandoned(stderr, desc, cm.AbandonedActionDone(), abandonedWait)
		}
	}
	return nil
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

// errNotATerminal is a usage error, so `mate <dir> > file` exits 2 with a
// sentence that says what to do instead of a wall of Bubble Tea noise.
var errNotATerminal = newUsageError(
	"mate console needs a real terminal on stdin and stdout; run it from an interactive shell, not a pipe or redirect")

// consoleTerminalRefusal rewords runtime.TTYHandoff's shared terminal check
// for this call site: that check runs the real tty ioctl (reused rather
// than duplicated), but its message is an attach client's and would name
// the wrong command.
func consoleTerminalRefusal(error) error { return errNotATerminal }

// abandonedWait bounds how long the Console waits, after the terminal is
// restored, for an action it quit in the middle of. It is longer than the
// spawn package's own 30s cleanup bound, so the action's compensation gets
// the chance to finish.
const abandonedWait = 45 * time.Second

// waitAbandoned blocks until the abandoned action has returned, or the
// bound passes. Exiting earlier would kill the goroutine mid-cleanup and
// leave a half-started agent behind (a Mate running with no mate.meta).
func waitAbandoned(stderr io.Writer, desc string, done <-chan struct{}, bound time.Duration) {
	if done == nil {
		return
	}
	select {
	case <-done:
		fmt.Fprintf(stderr, "mate console: %s has stopped.\n", desc)
	case <-time.After(bound):
		fmt.Fprintf(stderr, "mate console: %s had not stopped after %s; if it left an agent running, starting again adopts or refuses it by name.\n", desc, bound)
	}
}
