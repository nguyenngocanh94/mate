package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
)

// cmdCrew dispatches `matev2 crew <spawn|list|stop>`.
func cmdCrew(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: matev2 crew <spawn|list|stop> ...")
	}
	switch args[0] {
	case "spawn":
		return cmdCrewSpawn(args[1:], stdin, stdout, stderr)
	case "list":
		return cmdCrewList(args[1:], stdout, stderr)
	case "stop":
		return cmdCrewStop(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown crew subcommand %q", args[0])
	}
}

// cmdCrewSpawn implements
// `matev2 crew spawn <project> <id> --brief <file> [--harness codex|claude] [--task "<one line>"]`.
func cmdCrewSpawn(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew spawn", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: matev2 crew spawn <project> <id> --brief <file|-> [--workspace <dir>] [--harness codex|claude] [--task "<one line>"]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	harnessFlag := fs.String("harness", "", "harness to launch (codex or claude; default: the workspace default)")
	briefFlag := fs.String("brief", "", "file holding the task text, or - to read it from stdin")
	taskFlag := fs.String("task", "", "one line recorded as task= (default: the brief's first line)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("matev2 crew spawn: want exactly 2 arguments: <project> <id>")
	}
	if *briefFlag == "" {
		fs.Usage()
		return newUsageError("matev2 crew spawn: --brief is required")
	}
	req := spawn.SpawnCrewRequest{Project: fs.Arg(0), Crew: fs.Arg(1), Task: *taskFlag}
	if *briefFlag == "-" {
		text, err := spawn.ReadBriefStdin(stdin)
		if err != nil {
			return err
		}
		req.BriefText = text
	} else {
		req.BriefFile = *briefFlag
	}
	if *harnessFlag != "" {
		kind, err := harness.ParseKind(*harnessFlag)
		if err != nil {
			return &usageError{err}
		}
		req.Harness = kind
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	res, err := spawn.SpawnCrew(context.Background(), w, spawn.LiveDeps(), req)
	if err != nil {
		return err
	}
	if res.StaleMeta {
		fmt.Fprintf(stderr, "note: crew %s had a recorded agent Herdr no longer knew; the stale record was replaced\n", res.Crew)
	}
	if res.TrustDialog {
		fmt.Fprintf(stderr, "note: answered the %s directory-trust dialog for %s\n", res.Harness, res.Worktree)
	}
	if res.DeliveryWarning != "" {
		fmt.Fprintf(stderr, "warning: %s\nlast pane lines:\n%s\n", res.DeliveryWarning, res.PaneTail)
	}
	fmt.Fprintf(stdout, "spawned %s/%s: agent %s in pane %s (harness %s, branch %s, worktree %s)\n",
		res.Project, res.Crew, res.Agent, res.Pane, res.Harness, res.Branch, res.Worktree)
	fmt.Fprintf(stdout, "brief %s\nstatus %s\n", res.BriefPath, res.StatusPath)
	return nil
}

// cmdCrewList implements `matev2 crew list <project>`.
func cmdCrewList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: matev2 crew list <project> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("matev2 crew list: want exactly 1 argument: <project>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	crews, err := spawn.ListCrews(w, fs.Arg(0))
	if err != nil {
		return err
	}
	if len(crews) == 0 {
		fmt.Fprintf(stdout, "no crews recorded for %s\n", fs.Arg(0))
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tHARNESS\tBRANCH\tSTATUS\tPANE")
	for _, c := range crews {
		status := c.Status
		if status == "" {
			status = "-"
		}
		pane := c.Pane
		if pane == "" {
			pane = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.Crew, c.Harness, c.Branch, status, pane)
	}
	return tw.Flush()
}

// cmdCrewStop implements `matev2 crew stop <project> <id> [--discard]`. It
// stops the agent, closes the tab, and then tears down the worktree and
// branch unless they carry unlanded work and --discard was not given
// (task 16).
func cmdCrewStop(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: matev2 crew stop <project> <id> [--discard] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	discardFlag := fs.Bool("discard", false, "remove the worktree and branch even if the branch is unlanded or the worktree is dirty")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("matev2 crew stop: want exactly 2 arguments: <project> <id>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	project, crew := fs.Arg(0), fs.Arg(1)
	res, err := spawn.StopCrew(context.Background(), w, spawn.LiveDeps(), project, crew, *discardFlag)
	if err != nil && !errors.Is(err, spawn.ErrUnlandedWork) {
		return err
	}
	fmt.Fprintln(stdout, crewStopReport(project, crew, res))
	return err
}

// crewStopReport is the one line `matev2 crew stop` prints describing
// exactly what happened and what was kept, whether the stop succeeded,
// tore down cleanly, discarded unlanded work, or refused to.
func crewStopReport(project, crew string, res spawn.StopResult) string {
	agent := res.Agent
	if agent == "" {
		agent = "(none recorded)"
	}
	stopped := "stopped"
	if res.AlreadyGone {
		stopped = "already gone"
	}
	tab := "closed"
	if !res.TabClosed {
		tab = "not confirmed closed"
	}
	switch res.Teardown {
	case spawn.TeardownRefusedUnlanded:
		return fmt.Sprintf("%s/%s: agent %s (%s), tab %s; worktree and branch KEPT: branch %s is %d commit(s) ahead of default and the worktree has %d dirty file(s); rerun with --discard to remove them",
			project, crew, agent, stopped, tab, res.Branch, res.Ahead, res.DirtyFiles)
	case spawn.TeardownClean:
		return fmt.Sprintf("%s/%s: agent %s (%s), tab %s; worktree and branch removed (%s was already landed in default)",
			project, crew, agent, stopped, tab, res.Branch)
	case spawn.TeardownDiscarded:
		return fmt.Sprintf("%s/%s: agent %s (%s), tab %s; worktree and branch removed with --discard (%d commit(s) ahead, %d dirty file(s) discarded)",
			project, crew, agent, stopped, tab, res.Ahead, res.DirtyFiles)
	default:
		return fmt.Sprintf("%s/%s: agent %s (%s), tab %s; worktree and branch kept",
			project, crew, agent, stopped, tab)
	}
}
