package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/tool"
)

// toolCommandTimeout bounds one tool command run for an agent or a script.
const toolCommandTimeout = 30 * time.Second

const toolUsage = "usage: mate tool <name> <project> [--workspace <dir>] -- <arguments>"

// cmdTool implements `mate tool <name> <project> [--workspace <dir>] --
// <arguments>`: the tool's own command (tool.Command) on the project, with
// every argument after -- passed whole. The -- separates mate's flags from
// the tool's; with no --, the flags are all mate's (cmdToolHere).
func cmdTool(args []string, stdout, stderr io.Writer) error {
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return cmdToolHere(args, stdout, stderr)
	}
	fs := flag.NewFlagSet("tool", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args[:sep])); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 || len(args[sep+1:]) == 0 {
		return newUsageError(toolUsage)
	}
	name, err := tools.Parse(fs.Arg(0))
	if err != nil {
		return &usageError{err}
	}
	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	p, err := toolOnProject(w, name, fs.Arg(1))
	if err != nil {
		return err
	}
	cmd := p.Capabilities().Command
	if !cmd.Verified() {
		return fmt.Errorf("%s has no command: %s", p.Info().Title, cmd.Reason)
	}
	if err := toolDataRefusal(w, fs.Arg(1), p, false); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), toolCommandTimeout)
	defer cancel()
	return cmd.Impl.Run(ctx, toolEnv(w, fs.Arg(1), p), args[sep+1:], os.Stdin, stdout, stderr)
}

// toolOnProject is the profile of the tool named, once the project is
// known.
func toolOnProject(w *store.Workspace, name tool.Name, project string) (tool.Profile, error) {
	p, err := tools.Lookup(name)
	if err != nil {
		return nil, err
	}
	if err := requireProject(w, project); err != nil {
		return nil, err
	}
	return p, nil
}

// toolEnv is what a tool runs in on project: the project directory, its
// data there, the tool's lock under `.mate/projects/<p>/locks/`, and real
// processes.
func toolEnv(w *store.Workspace, project string, p tool.Profile) tool.CommandEnv {
	home := w.ProjectHome(project)
	env := tool.CommandEnv{
		ProjectDir: home,
		Lock: func(ctx context.Context) (func(), error) {
			return w.LockTool(ctx, project, string(p.Name()))
		},
		Run: runTool,
	}
	if data := p.Capabilities().Data; data.Verified() {
		env.DataDir = data.Impl.Dir(home)
	}
	return env
}

// runTool is the tool.Runner that starts real processes: no shell, the
// caller's streams attached, inv.Env set over this process's environment.
// A binary that is not installed is exec's own error, for the tool to say
// how to install it.
func runTool(ctx context.Context, inv tool.Invocation, in io.Reader, out, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, inv.Name, inv.Args...)
	cmd.Dir = inv.Dir
	if len(inv.Env) > 0 {
		cmd.Env = append(os.Environ(), inv.Env...)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return err
		}
		return fmt.Errorf("%s: %w", inv.Name, err)
	}
	return nil
}

// toolDataRefusal refuses to run a tool on a project whose data for that
// tool is not in the project directory yet while something older is where
// it should not be overwritten or hidden:
//
//   - data mate kept under `.mate/projects/<p>/` before layout 2, under the
//     same name: the captain moves it by hand, or starts empty with
//     `mate tool <name> <p> --init` (starting is true then, and only then
//     is the old copy no obstacle). mate never moves it: it may be all
//     there is.
//   - the task plan of mate's own task manager, `tasks.yaml`, replaced
//     before release (docs/beads.md): the tool's data is not made empty
//     over it, even to start.
func toolDataRefusal(w *store.Workspace, project string, p tool.Profile, starting bool) error {
	data := p.Capabilities().Data
	if !data.Verified() {
		return nil
	}
	home := w.ProjectHome(project)
	exists, err := data.Impl.Exists(home)
	if err != nil || exists {
		return err
	}
	info := p.Info()
	legacy := filepath.Join(w.ProjectDir(project), "tasks.yaml")
	if _, err := os.Stat(legacy); err == nil {
		return fmt.Errorf("legacy plan exists at %s; import its tasks into %s first (%s)", legacy, info.Title, info.Docs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if starting {
		return nil
	}
	dir := data.Impl.Dir(home)
	old := filepath.Join(w.ProjectDir(project), filepath.Base(dir))
	if st, err := os.Stat(old); err == nil && st.IsDir() {
		return fmt.Errorf("%s data from before layout 2 sits at %s; move it to %s by hand (mv), or run mate tool %s %s --init to start empty", info.Title, old, dir, p.Name(), project)
	}
	return nil
}
