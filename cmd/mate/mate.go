package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdMate dispatches `mate mate <start|stop|status|refresh>`.
func cmdMate(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate mate <start|stop|status|refresh> <project> ...")
	}
	switch args[0] {
	case "start":
		return cmdMateStart(args[1:], stdout, stderr)
	case "stop":
		return cmdMateStop(args[1:], stdout, stderr)
	case "refresh":
		return cmdMateRefresh(args[1:], stdout, stderr)
	case "status":
		return cmdMateStatus(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown mate subcommand %q", args[0])
	}
}

// cmdMateStart implements `mate mate start <project> [--harness claude|codex] [--fresh]`.
func cmdMateStart(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mate start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate mate start <project> [--workspace <dir>] [--harness "+harnessChoices(harness.RoleMate, "|")+"] [--fresh]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	harnessFlag := fs.String("harness", "", "harness to launch ("+harnessChoices(harness.RoleMate, " or ")+"; default: the workspace default)")
	freshFlag := fs.Bool("fresh", false, "start a brand new harness session instead of resuming mate.meta's session_id")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate mate start: want exactly 1 argument: <project>")
	}
	req := spawn.StartRequest{Project: fs.Arg(0), Resume: true, Fresh: *freshFlag}
	if *harnessFlag != "" {
		kind, err := harnesses.Parse(*harnessFlag)
		if err != nil {
			return &usageError{err}
		}
		if !runsMate(kind) {
			// spawn.StartMate refuses it too, through the harness's own
			// launcher; this says so before a workspace is looked for.
			return newUsageErrorf("mate mate start: the %s harness cannot run a Mate: it has no verified Hooks capability (choose %s)",
				kind, harnessChoices(harness.RoleMate, " or "))
		}
		req.Harness = kind
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	res, err := spawn.StartMate(context.Background(), w, liveDeps(w, stderr), req)
	if err != nil {
		return err
	}
	if res.StaleMeta {
		fmt.Fprintf(stderr, "note: %s recorded a Mate that Herdr no longer had; the stale record was replaced\n", res.Project)
	}
	if res.UpdateDialog {
		fmt.Fprintf(stderr, "note: skipped the %s release-update prompt; it returns at the next release\n", res.Harness)
	}
	if res.TrustDialog {
		fmt.Fprintf(stderr, "note: answered the %s directory-trust dialog for %s\n", res.Harness, res.MateDir)
	}
	if res.ResumeNote != "" {
		fmt.Fprintf(stderr, "note: %s\n", res.ResumeNote)
	}
	resumed := "fresh session"
	if res.Resumed {
		resumed = fmt.Sprintf("resumed session %s", res.ResumedFrom)
	}
	fmt.Fprintf(stdout, "started %s: agent %s in pane %s (session %s, harness %s, %s)\n",
		res.Project, res.Agent, res.Pane, res.Session, res.Harness, resumed)
	return nil
}

// cmdMateStop implements `mate mate stop <project> [--no-stow]`.
//
// Stopped by the captain with its composer empty, the Mate is first asked
// to stow (docs/mvp.md task 37, B7), exactly as the console's restart asks
// it, and the stop waits for that turn up to outbox.DefaultStowCeiling. A
// Mate mid-turn or with unsent text in its composer is stopped at once: a
// captain who types `mate stop` wants it stopped, and only the console's
// restart has a second press to ask with. A Mate stopping itself is never
// asked - it would be waiting on its own turn.
func cmdMateStop(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mate stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate mate stop <project> [--no-stow] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	noStowFlag := fs.Bool("no-stow", false, "stop at once, without asking the Mate to stow what exists only in its conversation")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate mate stop: want exactly 1 argument: <project>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	return mateStop(context.Background(), w, liveDeps(w, stderr), fs.Arg(0), spawn.CallerFromEnv(), *noStowFlag, stdout, stderr)
}

// mateStop is cmdMateStop's core, over any deps, for tests.
func mateStop(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, caller string, noStow bool, stdout, stderr io.Writer) error {
	if err := requireProject(w, project); err != nil {
		return err
	}
	stowed, err := stowBeforeStop(ctx, w, deps, project, caller, noStow, stderr)
	if err != nil {
		return err
	}
	res, err := spawn.StopMate(ctx, w, deps, project)
	if err != nil {
		return err
	}
	what := "stopped"
	if res.AlreadyGone {
		what = "already gone"
	}
	fmt.Fprintf(stdout, "%s: %s (agent %s, session_id kept for resume); %s\n", res.Project, what, res.Agent, stowed)
	return nil
}

// stowBeforeStop is the stow half of `mate mate stop`, also run by `mate
// project remove` before it stops the Mate. It returns the words the stop
// reports for it: what the stow did, or why none was asked for.
func stowBeforeStop(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, caller string, noStow bool, stderr io.Writer) (string, error) {
	switch {
	case noStow:
		return "not stowed: --no-stow", nil
	case caller == spawn.CallerMate:
		return "not stowed: the Mate stopped itself", nil
	case caller != spawn.CallerUser:
		return "not stowed: only the captain's stop asks the Mate to stow", nil
	}
	fmt.Fprintf(stderr, "stowing: asking the Mate to record what exists only in its conversation, waiting up to %s for its turn (--no-stow skips this)\n",
		outbox.Span(outbox.DefaultStowCeiling))
	res, err := consoleOutbox(w, deps).Stow(ctx, project, outbox.StowOptions{RequireEmpty: true})
	if err != nil {
		return "", err
	}
	return res.Outcome(), nil
}

// cmdMateStatus implements `mate mate status <project>`. It prints one
// line: running, stopped or stale, with what Herdr was observed to say.
func cmdMateStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mate status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate mate status <project> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate mate status: want exactly 1 argument: <project>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	status, err := spawn.MateStatus(context.Background(), w, spawn.LiveDeps(harnesses), fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, status.Line())
	return nil
}

// cmdMateRefresh safely replaces the transcript; ordinary start still resumes.
func cmdMateRefresh(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mate refresh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return newUsageError("usage: mate mate refresh <project> [--workspace <dir>]")
	}
	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	changed, err := contextRefresh(context.Background(), w, liveDeps(w, stderr), fs.Arg(0), false)
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("refresh postponed: Mate has no current session or another refresh is running")
	}
	fmt.Fprintln(stdout, "Mate refreshed: checkpoint saved, fresh session started with recall")
	return nil
}
