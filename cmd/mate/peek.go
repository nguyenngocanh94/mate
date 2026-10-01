package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// defaultPeekLines and maxPeekLines bound `mate peek --lines`
// (docs/mvp.md task 13: "40 dong cuoi pane").
const (
	defaultPeekLines = 40
	maxPeekLines     = 200
)

// cmdPeek implements `mate peek <project> <crew> [--lines 40]`
// (docs/mvp.md task 13, section 4 "Doc crew"): a read-only snapshot of the
// crew's pane. It never types anything and never waits for the agent to do
// anything, so it is always safe to run on a crew that might be busy.
func cmdPeek(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("peek", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate peek <project> <crew> [--lines 40] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	linesFlag := fs.Int("lines", defaultPeekLines, "how many trailing pane lines to read (max 200)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate peek: want exactly 2 arguments: <project> <crew>")
	}
	lines := clampPeekLines(*linesFlag)
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	return peekCrew(context.Background(), w, spawn.LiveDeps(harnesses), fs.Arg(0), fs.Arg(1), lines, stdout)
}

// clampPeekLines applies the 1..200 bound on --lines: zero or negative
// falls back to the default rather than erroring, since "give me the usual
// amount" is the more useful reading of an empty or malformed flag here.
func clampPeekLines(lines int) int {
	if lines <= 0 {
		return defaultPeekLines
	}
	if lines > maxPeekLines {
		return maxPeekLines
	}
	return lines
}

// peekCrew is peek's core, kept separate from flag parsing so a test can
// drive it with a fake runtime instead of spawn.LiveDeps(). When the crew is
// stopped, or Herdr no longer knows its agent, it falls back to the tail of
// crews/<id>.status instead of failing: a peek is a diagnostic, and "the
// agent is gone" is itself the answer worth printing.
func peekCrew(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, crew string, lines int, stdout io.Writer) error {
	resolved, err := resolveCrewHandle(ctx, w, deps, project, crew)
	if err != nil {
		return err
	}
	if resolved.AgentRecorded && resolved.SessionRunning {
		screen, err := deps.Runtime.ReadAgent(ctx, resolved.Handle, resolved.Screen.ReadSource(), lines)
		switch {
		case err == nil:
			fmt.Fprint(stdout, screen)
			return nil
		case runtime.IsAgentNotFound(err):
			// fall through to the status fallback below
		default:
			return err
		}
	}
	fmt.Fprintln(stdout, "agent absent; last status:")
	return printStatusTail(w, project, crew, lines, stdout)
}

// printStatusTail prints the last n lines of crews/<id>.status, or a note
// that the crew has never written one.
func printStatusTail(w *store.Workspace, project, crew string, n int, stdout io.Writer) error {
	entries, _, err := w.ReadStatus(project, crew, 0)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "(no status lines recorded)")
		return nil
	}
	if len(entries) > n {
		entries = entries[len(entries)-n:]
	}
	for _, e := range entries {
		fmt.Fprintln(stdout, e.Line)
	}
	return nil
}
