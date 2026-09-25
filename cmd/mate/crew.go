package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdCrew dispatches `mate crew <spawn|list|stop>`.
func cmdCrew(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate crew <spawn|list|stop|dispatch> ...")
	}
	switch args[0] {
	case "spawn":
		return cmdCrewSpawn(args[1:], stdin, stdout, stderr)
	case "dispatch":
		return cmdCrewDispatch(args[1:], stdout, stderr)
	case "list":
		return cmdCrewList(args[1:], stdout, stderr)
	case "stop":
		return cmdCrewStop(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown crew subcommand %q", args[0])
	}
}

// cmdCrewSpawn implements
// `mate crew spawn <project> <id> --brief <file> [--repo <name>] [--scout] [--harness codex|claude] [--model <name>] [--effort <level>] [--task "<one line>"]`.
func cmdCrewSpawn(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew spawn", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: mate crew spawn <project> <id> --brief <file|-> [--repo <name>] [--scout] [--workspace <dir>] [--harness codex|claude] [--model <name>] [--effort low|medium|high|xhigh|max] [--task "<one line>"]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	scoutFlag := fs.Bool("scout", false, "a scout: the brief has ## Deliverable and the crew writes a report instead of committing")
	harnessFlag := fs.String("harness", "", "harness to launch (codex or claude; default: the workspace default)")
	modelFlag := fs.String("model", "", "model the harness runs, as it names it (default: the harness's own)")
	effortFlag := fs.String("effort", "", "reasoning effort: low, medium, high, xhigh or max (default: the harness's own)")
	briefFlag := fs.String("brief", "", "file holding the task text, or - to read it from stdin")
	repoFlag := fs.String("repo", "", "the project repo the crew works in (required when the project has several)")
	taskFlag := fs.String("task", "", "one line recorded as task= (default: the brief's first line)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate crew spawn: want exactly 2 arguments: <project> <id>")
	}
	if *briefFlag == "" {
		fs.Usage()
		return newUsageError("mate crew spawn: --brief is required")
	}
	req := spawn.SpawnCrewRequest{Project: fs.Arg(0), Crew: fs.Arg(1), Task: *taskFlag, Scout: *scoutFlag, Repo: *repoFlag}
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
	model, err := harness.ParseModel(*modelFlag)
	if err != nil {
		return &usageError{err}
	}
	effort, err := harness.ParseEffort(*effortFlag)
	if err != nil {
		return &usageError{err}
	}
	req.Model, req.Effort = model, effort
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if err := checkDispatch(w, req); err != nil {
		return err
	}
	res, err := spawn.SpawnCrew(context.Background(), w, spawn.LiveDeps(), req)
	if errors.Is(err, spawn.ErrRepoRefused) {
		// Which repo a crew works in is the caller's argument to get right,
		// so it exits 2 like any other bad argument.
		return &usageError{err}
	}
	if err != nil {
		return err
	}
	writeCrewSpawnReport(stdout, stderr, w, res)
	return nil
}

// writeCrewSpawnReport prints what a successful spawn prints: notes and
// warnings on stderr, the three lines the manual tells the Mate to keep on
// stdout, and, in auto mode, the line telling it to end its turn now
// (auto_turn.go). Kept apart from flag parsing so a test can drive it with
// a result from a fake-runtime spawn.
func writeCrewSpawnReport(stdout, stderr io.Writer, w *store.Workspace, res spawn.CrewResult) {
	if res.StaleMeta {
		fmt.Fprintf(stderr, "note: crew %s had a recorded agent Herdr no longer knew; the stale record was replaced\n", res.Crew)
	}
	if res.UpdateDialog {
		fmt.Fprintf(stderr, "note: skipped the %s release-update prompt; it returns at the next release\n", res.Harness)
	}
	if res.TrustDialog {
		fmt.Fprintf(stderr, "note: answered the %s directory-trust dialog for %s\n", res.Harness, res.Worktree)
	}
	if res.DeliveryWarning != "" {
		fmt.Fprintf(stderr, "warning: %s\nlast pane lines:\n%s\n", res.DeliveryWarning, res.PaneTail)
	}
	if res.EffortOmitted {
		fmt.Fprintf(stderr, "note: %s does not take effort %s; it was recorded and left out of the launch\n", res.Harness, res.Effort)
	}
	fmt.Fprintf(stdout, "spawned %s/%s: agent %s in pane %s (harness %s%s, repo %s, branch %s, worktree %s)\n",
		res.Project, res.Crew, res.Agent, res.Pane, res.Harness, profileNote(res), res.Repo, res.Branch, res.Worktree)
	fmt.Fprintf(stdout, "brief %s\nstatus %s\n", res.BriefPath, res.StatusPath)
	printAutoTurnEnd(stdout, w, res.Project, autoSpawnLine(res.Crew))
}

// cmdCrewList implements `mate crew list <project>`.
func cmdCrewList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate crew list <project> [--all] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	allFlag := fs.Bool("all", false, "include closed crews (those `crew stop` has run on)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate crew list: want exactly 1 argument: <project>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	all, err := spawn.ListCrews(w, fs.Arg(0))
	if err != nil {
		return err
	}
	crews := all
	closed := 0
	if !*allFlag {
		crews = crews[:0:0]
		for _, c := range all {
			if c.Closed {
				closed++
				continue
			}
			crews = append(crews, c)
		}
	}
	if len(crews) == 0 {
		switch {
		case closed > 0:
			fmt.Fprintf(stdout, "no open crews for %s (%d closed; --all lists them)\n", fs.Arg(0), closed)
		default:
			fmt.Fprintf(stdout, "no crews recorded for %s\n", fs.Arg(0))
		}
		return nil
	}
	// STATE is the app's word for the crew (mvp.md section 4b); NOTE is the
	// crew's own last line. Two columns, because they answer two questions:
	// a `wait-mate` crew with the note "could not build without a db" is not
	// the same row as one that says "ready in branch mate/k3".
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tHARNESS\tBRANCH\tSTATE\tNOTE\tPANE")
	for _, c := range crews {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			c.Crew, harnessCell(c), c.Branch, dashIfEmpty(c.State), dashIfEmpty(c.Note), dashIfEmpty(c.Pane))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if closed > 0 {
		fmt.Fprintf(stdout, "(%d closed crew(s) not shown; --all lists them)\n", closed)
	}
	return nil
}

// dashIfEmpty keeps a table cell from reading as a missing column.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// cmdCrewStop implements `mate crew stop <project> <id> [--discard]`. It
// refuses up front when the branch carries unlanded work and --discard was
// not given, and otherwise stops the agent, closes the tab, tears down the
// worktree and branch, and records the crew as finished or failed
// (task 16, mvp.md section 4b).
func cmdCrewStop(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate crew stop <project> <id> [--discard] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	discardFlag := fs.Bool("discard", false, "remove the worktree and branch even if the branch is unlanded or the worktree is dirty")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate crew stop: want exactly 2 arguments: <project> <id>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	project, crew := fs.Arg(0), fs.Arg(1)
	res, err := spawn.StopCrew(context.Background(), w, spawn.LiveDeps(), project, crew, *discardFlag)
	if err != nil {
		// Including ErrUnlandedWork, which is now a refusal that changed
		// nothing: there is no outcome to report, only the reason.
		return err
	}
	writeCrewStopReport(stdout, project, crew, res, spawn.CallerFromEnv())
	return nil
}

// crewStopReport is the one line `mate crew stop` prints describing
// exactly what happened, what was kept, and the state the crew ends in.
func crewStopReport(project, crew string, res spawn.StopResult) string {
	if res.AlreadyClosed {
		return fmt.Sprintf("%s/%s: already closed, state %s; nothing changed", project, crew, res.State)
	}
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
	case spawn.TeardownClean:
		return fmt.Sprintf("%s/%s: agent %s (%s), tab %s; worktree and branch removed (%s was already landed in default); state %s",
			project, crew, agent, stopped, tab, res.Branch, res.State)
	case spawn.TeardownDiscarded:
		return fmt.Sprintf("%s/%s: agent %s (%s), tab %s; worktree and branch removed with --discard (%d commit(s) ahead, %d dirty file(s) discarded); state %s",
			project, crew, agent, stopped, tab, res.Ahead, res.DirtyFiles, res.State)
	default:
		return fmt.Sprintf("%s/%s: agent %s (%s), tab %s; worktree and branch kept; state %s",
			project, crew, agent, stopped, tab, res.State)
	}
}

// profileNote is ", model m, effort e" for the axes a spawn set.
func profileNote(res spawn.CrewResult) string {
	out := ""
	if res.Model != "" {
		out += ", model " + res.Model
	}
	if res.Effort != "" {
		out += ", effort " + string(res.Effort)
	}
	return out
}

// harnessCell is the HARNESS column: the harness, then the model and
// effort it was spawned with when either was set - "codex gpt-5.5/high",
// "codex gpt-5.5", or "codex default/high" when only the effort was.
func harnessCell(c spawn.CrewSummary) string {
	cell := c.Harness
	switch {
	case c.Model != "" && c.Effort != "":
		cell += " " + c.Model + "/" + c.Effort
	case c.Model != "":
		cell += " " + c.Model
	case c.Effort != "":
		cell += " default/" + c.Effort
	}
	return cell
}
