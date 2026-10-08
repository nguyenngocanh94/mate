package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"

	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/screen/fixture"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// cmdState implements `mate state <project> <crew>` (docs/mvp.md task 13,
// vocabulary of section 4b): one deterministic line about a crew, two
// columns wide -
//
//	state: <the crew's declared state> · health: <what the runtime looks like> · via <observer>
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
		fmt.Fprintln(stderr, "usage: mate state <project> <crew> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate state: want exactly 2 arguments: <project> <crew>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	result, err := stateOfCrew(context.Background(), w, liveDeps(w, stderr), fs.Arg(0), fs.Arg(1))
	if err != nil {
		return err
	}
	writeStateReport(stdout, w, fs.Arg(0), fs.Arg(1), result)
	return nil
}

// writeStateReport prints the one state line and then the line
// telling the Mate not to poll (auto_turn.go). The state line stays the
// first line, so anything that reads it keeps working.
func writeStateReport(stdout io.Writer, w *store.Workspace, project, crew string, result crewstate.Result) {
	fmt.Fprint(stdout, result.Line())
	fmt.Fprint(stdout, tokensSuffix(w, project, crew))
	fmt.Fprintln(stdout)
	printTurnEnd(stdout, turnStateLine)
}

// tokensSuffix is `mate state`'s own addition to crewstate.Result.Line()
// (mvp.md M5 task 27): " · tokens: 96k" and, once the crew's most recent
// turn has a priced context window, " · ctx: 62%". It lives here rather
// than in internal/crewstate because that package is a deliberate leaf that
// imports nothing else in this module (internal/crewstate/state.go's own
// doc comment) - the ledger is this command's business, not the state
// machine's.
//
// No database yet, or no ledger row for this crew, renders as nothing at
// all: `mate state` printed a state before M5 existed, and a workspace
// that has not opened a console yet must keep printing exactly that.
func tokensSuffix(w *store.Workspace, project, crew string) string {
	handle, err := db.OpenRead(w)
	if err != nil {
		return ""
	}
	defer handle.Close()

	var total int64
	var contextPct sql.NullFloat64
	err = handle.SQL().QueryRow(`
		SELECT COALESCE(input_tokens,0)+COALESCE(cache_read_tokens,0)+COALESCE(cache_write_tokens,0)+COALESCE(output_tokens,0),
		       context_pct
		  FROM v_task_ledger WHERE crew_actor_id = ?`, timeline.CrewActorID(project, crew)).
		Scan(&total, &contextPct)
	if err != nil {
		return ""
	}
	out := " · tokens: " + query.HumanizeTokens(total)
	if contextPct.Valid {
		out += fmt.Sprintf(" · ctx: %.0f%%", contextPct.Float64)
	}
	return out
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
		// Styled, so a faint suggestion in the composer reads as the idle
		// pane it is rather than as somebody's half-typed line.
		screen, err := deps.Runtime.ReadAgentStyled(ctx, resolved.Handle, resolved.Screen.ReadSource(), send.DefaultLines)
		switch {
		case err == nil:
			observer := deps.Observer
			if observer == nil {
				observer = fixture.New()
			}
			observed, err := observer.Observe(ctx, resolved.Screen, screen)
			if err != nil {
				return crewstate.Result{}, err
			}
			in.AgentFound = true
			in.Composer = composerReading(observed.Composer)
			in.Evidence = observed.Evidence
			in.Source = observed.Source
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
// from mate), so the translation lives at the caller, where both
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
// unresolved *blocking* finding for this crew - `stale` or `runtime_lost`,
// the two kinds that make a crew `blocked` (mvp.md section 4b; `budget` is
// excluded by decision 2026-09-20, box.BlockingIncidents). A box that cannot
// be read is reported as no incident: `blocked` is a claim, and a failed
// read has not established it.
func crewHasOpenIncident(w *store.Workspace, project, crew string) bool {
	view, err := box.Load(w, project)
	if err != nil {
		return false
	}
	return len(box.BlockingIncidents(view, crew)) > 0
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
