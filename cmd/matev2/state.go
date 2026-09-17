package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/crewstate"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// cmdState implements `matev2 state <project> <crew>` (docs/mvp.md task 13):
// one deterministic line about a crew, built by internal/crewstate's pure
// decision table over what this command gathers live.
func cmdState(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("state", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: matev2 state <project> <crew> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("matev2 state: want exactly 2 arguments: <project> <crew>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	result, err := stateOfCrew(context.Background(), w, spawn.LiveDeps(), fs.Arg(0), fs.Arg(1))
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, result.Line())
	return nil
}

// stateOfCrew is state's core, kept separate from flag parsing so a test can
// drive it with a fake runtime instead of spawn.LiveDeps(). It gathers the
// four inputs crewstate.Decide needs and hands the verdict entirely to that
// pure function: this is the only place in the CLI allowed to guess.
func stateOfCrew(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, crew string) (crewstate.Result, error) {
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return crewstate.Result{}, err
	}
	in := crewstate.Input{Meta: meta}

	resolved, err := resolveCrewHandle(ctx, w, deps, project, crew)
	if err != nil {
		return crewstate.Result{}, err
	}
	if resolved.AgentRecorded && resolved.SessionRunning {
		screen, err := deps.Runtime.ReadAgent(ctx, resolved.Handle, send.DefaultLines)
		switch {
		case err == nil:
			cls, err := send.ClassifyComposer(resolved.Kind, screen)
			if err != nil {
				return crewstate.Result{}, err
			}
			in.AgentFound = true
			in.Composer = cls
		case runtime.IsAgentNotFound(err):
			in.AgentFound = false
		default:
			return crewstate.Result{}, err
		}
	}

	entries, _, err := w.ReadStatus(project, crew, 0)
	if err != nil {
		return crewstate.Result{}, err
	}
	if len(entries) > 0 {
		parsed := box.ParseStatus(entries[len(entries)-1].Line)
		in.Status = crewstate.Status{Verb: crewstate.StatusVerb(parsed.State), Text: parsed.Text}
	}

	return crewstate.Decide(in), nil
}
