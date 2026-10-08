package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/x/term"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/tool"
)

// taskTool is the tool `mate tasks`, `mate beads` and `mate task-triage`
// stand for: they are kept for one release while manuals still name them,
// then `mate tool` alone remains
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, PR 6).
const taskTool tool.Name = "beads"

// cmdTasks implements `mate tasks <project> [--list|--json|--init]`: the
// task tool's data made (Data.Init), listed (its Command), or open in its
// viewer in this terminal.
func cmdTasks(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tasks", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	list := fs.Bool("list", false, "list Beads issues")
	jsonFlag := fs.Bool("json", false, "list Beads issues as JSON")
	init := fs.Bool("init", false, "initialize Beads and refresh its viewer export")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		return newUsageError("usage: mate tasks <project> [--list|--json|--init] [--workspace <dir>]")
	}
	if !*list && !*jsonFlag && !*init && !term.IsTerminal(os.Stdin.Fd()) {
		return newUsageError("mate tasks needs a terminal; use --list or --json")
	}
	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	project := fs.Arg(0)
	p, err := taskToolOn(w, project, *init)
	if err != nil {
		return err
	}
	env := toolEnv(w, project, p)
	caps := p.Capabilities()
	ctx, cancel := context.WithTimeout(context.Background(), toolCommandTimeout)
	defer cancel()
	switch {
	case *init:
		if err := caps.Data.Impl.Init(ctx, env, stderr); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, env.DataDir)
		return err
	case *list || *jsonFlag:
		bdArgs := []string{"list", "--all", "--limit", "0", "--no-pager"}
		if *jsonFlag {
			bdArgs = append(bdArgs, "--json")
		}
		return caps.Command.Impl.Run(ctx, env, bdArgs, nil, stdout, stderr)
	}
	if err := caps.Data.Impl.Init(ctx, env, stderr); err != nil {
		return err
	}
	// The captain decides the interactive process lifetime. Initialization is
	// bounded; the viewer stays open until they quit it.
	return runViewerHere(context.Background(), p, env, nil, os.Stdin, stdout, stderr)
}

// cmdBeads is `mate beads <project> -- <bd arguments>`, which `mate tool
// beads` replaced: it says so and runs that.
func cmdBeads(args []string, stdout, stderr io.Writer) error {
	fmt.Fprintln(stderr, "note: mate beads is now mate tool beads")
	return cmdTool(append([]string{string(taskTool)}, args...), stdout, stderr)
}

// cmdTaskTriage implements `mate task-triage <project>`: the task tool's
// data made, then its viewer's robot triage, which never waits for a
// person.
func cmdTaskTriage(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("task-triage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		return newUsageError("usage: mate task-triage <project> [--workspace <dir>]")
	}
	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	project := fs.Arg(0)
	p, err := taskToolOn(w, project, false)
	if err != nil {
		return err
	}
	env := toolEnv(w, project, p)
	ctx, cancel := context.WithTimeout(context.Background(), toolCommandTimeout)
	defer cancel()
	if err := p.Capabilities().Data.Impl.Init(ctx, env, stderr); err != nil {
		return err
	}
	return runViewerHere(ctx, p, env, []string{"--robot-triage", "--brief"}, nil, stdout, stderr)
}

// taskToolOn is the task tool on project, refused while its data is not
// where it should be (toolDataRefusal). starting is `--init`.
func taskToolOn(w *store.Workspace, project string, starting bool) (tool.Profile, error) {
	p, err := toolOnProject(w, taskTool, project)
	if err != nil {
		return nil, err
	}
	caps := p.Capabilities()
	if !caps.Data.Verified() || !caps.Command.Verified() || !caps.Viewer.Verified() {
		return nil, fmt.Errorf("%s has no data, command and viewer to run mate tasks with", p.Info().Title)
	}
	if err := toolDataRefusal(w, project, p, starting); err != nil {
		return nil, err
	}
	return p, nil
}

// runViewerHere runs p's viewer on the project in this terminal rather than
// in a Console tab, with extra arguments after its own.
func runViewerHere(ctx context.Context, p tool.Profile, env tool.CommandEnv, extra []string, in io.Reader, out, stderr io.Writer) error {
	inv, err := p.Capabilities().Viewer.Impl.Argv(tool.ViewerContext{ProjectDir: env.ProjectDir}, func(name string) string {
		if path := findTool(os.Getenv, name); filepath.IsAbs(path) {
			return path
		}
		return ""
	})
	if err != nil {
		return err
	}
	inv.Args = append(inv.Args, extra...)
	if inv.Dir == "" {
		inv.Dir = env.ProjectDir
	}
	return runTool(ctx, inv, in, out, stderr)
}
