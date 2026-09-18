package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/crewstate"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// cmdState implements `matev2 state <project> <crew>` (docs/mvp.md task 13,
// vocabulary of section 4b): one deterministic line about a crew, two
// columns wide -
//
//	state: <the crew's declared state> · health: <what the runtime looks like>
//
// The state comes from the record and the health from a live look, and they
// are kept apart on purpose: a crew is `wait-mate` because it said so, not
// because its pane went quiet, and its pane is busy or idle whatever the
// record says. internal/crewstate decides both from what this command
// gathers.
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
// inputs crewstate.Decide needs - the crew's meta, the observer's open
// incidents, the last status verb, and what the pane looks like - and hands
// the verdict entirely to that pure function: this is the only place in the
// CLI allowed to guess.
//
// A crew with no record at all is an error rather than a state: `unknown`
// is not one of the seven (mvp.md section 4b), and "there is no such crew"
// is a different answer from any state a real crew could be in.
func stateOfCrew(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, crew string) (crewstate.Result, error) {
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return crewstate.Result{}, err
	}
	if len(meta) == 0 {
		return crewstate.Result{}, observability.NewError(observability.CodeNotFound,
			fmt.Sprintf("no crew %s is recorded for project %s; %s is empty", crew, project, w.CrewMeta(project, crew)))
	}
	in := crewstate.Input{
		Declaration: crewstate.Declaration{
			Meta:         meta,
			OpenIncident: crewHasOpenIncident(w, project, crew),
			LastVerb:     crewstate.StatusVerb(lastCrewVerb(w, project, crew)),
		},
		Observation: crewstate.Observation{AgentRecorded: meta[spawn.MetaAgent] != ""},
	}

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
			in.Composer = composerReading(cls.State)
			in.Evidence = cls.Evidence
		case runtime.IsAgentNotFound(err):
			in.AgentFound = false
		default:
			return crewstate.Result{}, err
		}
	}

	return crewstate.Decide(in), nil
}

// composerReading translates internal/send's composer vocabulary into
// crewstate's. crewstate is a leaf package on purpose (it imports nothing
// from matev2), so the translation lives at the caller, where both
// vocabularies are already in scope.
func composerReading(state send.ComposerState) crewstate.Composer {
	switch state {
	case send.StateBusy:
		return crewstate.ComposerBusy
	case send.StateEmpty:
		return crewstate.ComposerEmpty
	case send.StatePending:
		return crewstate.ComposerPending
	default:
		return crewstate.ComposerUnknown
	}
}

// crewHasOpenIncident asks the merged box view whether the observer has an
// unresolved finding for this crew - the one thing that makes a crew
// `blocked` (mvp.md section 4b). A box that cannot be read is reported as
// no incident: `blocked` is a claim, and a failed read has not established
// it.
func crewHasOpenIncident(w *store.Workspace, project, crew string) bool {
	view, err := box.Load(w, project, nil)
	if err != nil {
		return false
	}
	return len(box.OpenIncidents(view, crew)) > 0
}

// lastCrewVerb is the verb of the crew's last recognised status line, ""
// when it has written none. box owns the parsing, including the pre-4b
// verbs an older status file may still carry.
func lastCrewVerb(w *store.Workspace, project, crew string) string {
	entries, _, err := w.ReadStatus(project, crew, 0)
	if err != nil {
		return ""
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, e.Line)
	}
	if verb := box.LastVerb(lines); verb != box.StateUnknown {
		return string(verb)
	}
	return ""
}
