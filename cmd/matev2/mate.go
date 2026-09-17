package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
)

// cmdMate dispatches `matev2 mate <start|stop|status>`.
func cmdMate(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: matev2 mate <start|stop|status> <project> ...")
	}
	switch args[0] {
	case "start":
		return cmdMateStart(args[1:], stdout, stderr)
	case "stop":
		return cmdMateStop(args[1:], stdout, stderr)
	case "status":
		return cmdMateStatus(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown mate subcommand %q", args[0])
	}
}

// cmdMateStart implements `matev2 mate start <project> [--harness claude|codex]`.
func cmdMateStart(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mate start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: matev2 mate start <project> [--workspace <dir>] [--harness claude|codex]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	harnessFlag := fs.String("harness", "", "harness to launch (claude or codex; default: the workspace default)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("matev2 mate start: want exactly 1 argument: <project>")
	}
	req := spawn.StartRequest{Project: fs.Arg(0)}
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
	res, err := spawn.StartMate(context.Background(), w, spawn.LiveDeps(), req)
	if err != nil {
		return err
	}
	if res.StaleMeta {
		fmt.Fprintf(stderr, "note: %s recorded a Mate that Herdr no longer had; the stale record was replaced\n", res.Project)
	}
	if res.TrustDialog {
		fmt.Fprintf(stderr, "note: answered the %s directory-trust dialog for %s\n", res.Harness, res.MateDir)
	}
	fmt.Fprintf(stdout, "started %s: agent %s in pane %s (session %s, harness %s)\n",
		res.Project, res.Agent, res.Pane, res.Session, res.Harness)
	return nil
}

// cmdMateStop implements `matev2 mate stop <project>`.
func cmdMateStop(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mate stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: matev2 mate stop <project> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("matev2 mate stop: want exactly 1 argument: <project>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	res, err := spawn.StopMate(context.Background(), w, spawn.LiveDeps(), fs.Arg(0))
	if err != nil {
		return err
	}
	what := "stopped"
	if res.AlreadyGone {
		what = "already gone"
	}
	fmt.Fprintf(stdout, "%s: %s (agent %s, session_id kept for resume)\n", res.Project, what, res.Agent)
	return nil
}

// cmdMateStatus implements `matev2 mate status <project>`. It prints one
// line: running, stopped or stale, with what Herdr was observed to say.
func cmdMateStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mate status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: matev2 mate status <project> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("matev2 mate status: want exactly 1 argument: <project>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	status, err := spawn.MateStatus(context.Background(), w, spawn.LiveDeps(), fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, status.Line())
	return nil
}
