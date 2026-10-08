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

// taskTool is the one tool whose `mate tool` takes mate's own flags beyond
// --init: --list, --json and --triage, and its viewer in this terminal
// with none, are bd's and bv's arguments chosen by mate.
const taskTool tool.Name = "beads"

const toolHereUsage = "usage: mate tool <name> <project> --init [--workspace <dir>] | mate tool beads <project> [--list|--json|--triage] [--workspace <dir>]"

// cmdToolHere implements `mate tool <name> <project>` with mate's own flags
// and no --: --init makes the tool's data (Data.Init) and prints where it
// is; for the task tool, --list and --json list it (its Command), --triage
// is its viewer's robot triage, which never waits for a person, and no
// flag opens its viewer in this terminal.
func cmdToolHere(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tool", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	list := fs.Bool("list", false, "list Beads issues")
	jsonFlag := fs.Bool("json", false, "list Beads issues as JSON")
	triage := fs.Bool("triage", false, "Beads Viewer's robot triage")
	init := fs.Bool("init", false, "initialize the tool's data and refresh its viewer export")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		return newUsageError(toolHereUsage)
	}
	name, err := tools.Parse(fs.Arg(0))
	if err != nil {
		return &usageError{err}
	}
	if !*init && name != taskTool {
		return newUsageError(toolHereUsage)
	}
	if !*list && !*jsonFlag && !*init && !*triage && !term.IsTerminal(os.Stdin.Fd()) {
		return newUsageError("mate tool beads needs a terminal; use --list or --json")
	}
	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	project := fs.Arg(1)
	p, err := toolHereOn(w, name, project, *init)
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
	if *triage {
		return runViewerHere(ctx, p, env, []string{"--robot-triage", "--brief"}, nil, stdout, stderr)
	}
	// The captain decides the interactive process lifetime. Initialization is
	// bounded; the viewer stays open until they quit it.
	return runViewerHere(context.Background(), p, env, nil, os.Stdin, stdout, stderr)
}

// toolHereOn is the tool on project, refused while its data is not where
// it should be (toolDataRefusal). starting is --init, which needs only the
// tool's data; the task tool's other flags need its command and viewer
// too.
func toolHereOn(w *store.Workspace, name tool.Name, project string, starting bool) (tool.Profile, error) {
	p, err := toolOnProject(w, name, project)
	if err != nil {
		return nil, err
	}
	caps := p.Capabilities()
	if !caps.Data.Verified() {
		return nil, fmt.Errorf("%s keeps no data to initialize", p.Info().Title)
	}
	if !starting && (!caps.Command.Verified() || !caps.Viewer.Verified()) {
		return nil, fmt.Errorf("%s has no command and viewer to run mate tool %s with", p.Info().Title, name)
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
