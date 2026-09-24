package main

import (
	"context"
	"fmt"

	"github.com/nguyenngocanh94/matev2/internal/autopilot"
	"github.com/nguyenngocanh94/matev2/internal/gitx"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/ui/console"
)

// consoleAction is the Console's ActionFunc: the only bridge from the
// action menu and the Mate keys to internal/spawn and internal/store.
//
// Every branch returns the one line the Console shows after the action, and
// every failure is propagated verbatim - a spawn error already says which
// step refused and what it observed, and rewording it here would lose the
// Herdr detail the operator needs. Nothing returns a success it did not
// establish: an action mvp.md has not reached yet refuses by name.
func consoleAction(ws *store.Workspace, deps spawn.Deps) console.ActionFunc {
	holds := newRestartHolds()
	return func(ctx context.Context, req console.ActionRequest) (string, error) {
		switch req.Action {
		case console.ActionStart, console.ActionResume:
			return startMateAction(ctx, ws, deps, req)
		case console.ActionOnboard:
			if req.TargetKind == "workspace" {
				return "", observability.NewError(observability.CodeUsage,
					"use `matev2 project add <name> <repo>`; the Console cannot register a repo it has not been given")
			}
			return startMateAction(ctx, ws, deps, req)
		case console.ActionStop:
			if req.TargetKind != "mate" && req.TargetKind != "project-mate" {
				return "", observability.NewError(observability.CodeUsage,
					"stopping a Crew is not wired until mvp.md task 16")
			}
			res, err := spawn.StopMate(ctx, ws, deps, req.Target)
			if err != nil {
				return "", err
			}
			if res.AlreadyGone {
				return fmt.Sprintf("Mate %s was already gone; mate.meta cleared", res.Agent), nil
			}
			return fmt.Sprintf("Mate %s stop confirmed in session %s", res.Agent, res.Session), nil
		case console.ActionRepair:
			// `mate status` is the repair: it re-asks Herdr about the agent
			// mate.meta names and reports what it found. It never rewrites
			// the meta, so the one line it returns is an observation, not a
			// claim that anything was fixed.
			if req.TargetKind != "mate" && req.TargetKind != "project-mate" && req.TargetKind != "project" {
				return "", observability.NewError(observability.CodeUsage,
					"repairing a Crew is not wired until mvp.md task 16")
			}
			status, err := spawn.MateStatus(ctx, ws, deps, req.Target)
			if err != nil {
				return "", err
			}
			return status.Line(), nil
		case console.ActionMode:
			return toggleModeAction(ws, req.Target)
		case console.ActionResolve:
			return boxResolveAction(ctx, ws, deps, req)
		case console.ActionReply:
			return boxReplyAction(ctx, ws, deps, req)
		case console.ActionPeek:
			return boxPeekAction(ctx, ws, deps, req)
		case console.ActionDiff:
			return crewDiffAction(ctx, ws, req)
		case console.ActionRestartMate:
			return restartMateAction(ctx, ws, deps, req, holds)
		case console.ActionClearComposer:
			return clearComposerAction(ctx, ws, deps, req)
		case console.ActionMerge:
			return mergeCrewAction(ctx, ws, deps, req)
		default:
			return "", observability.NewError(observability.CodeUsage,
				fmt.Sprintf("%s is not wired in this build", req.Action))
		}
	}
}

// crewDiffAction is the Console's `diff` entry (mvp.md task 21). It calls
// the same function `matev2 diff <project> <crew>` calls, so the overlay
// and the terminal show one text and cannot drift: a reader who runs the
// command after reading the overlay sees what they already saw.
//
// It takes no spawn.Deps because it asks Herdr nothing. A diff is git and
// the workspace's own files, and a crew whose agent is gone still has a
// branch worth reading.
func crewDiffAction(ctx context.Context, ws *store.Workspace, req console.ActionRequest) (string, error) {
	if req.Target == "" || req.Crew == "" {
		return "", observability.NewError(observability.CodeUsage,
			"diff needs both a Project and a Crew; the Console sent "+
				fmt.Sprintf("target=%q crew=%q", req.Target, req.Crew))
	}
	return crewDiffText(ctx, ws, gitx.New(), req.Target, req.Crew, false)
}

// startMateAction starts the Mate of one Project. The harness is whatever
// the Console's picker chose; an empty one is not an error but "no choice
// was made", which StartMate reads as the workspace default.
func startMateAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req console.ActionRequest) (string, error) {
	if req.Target == "" {
		return "", observability.NewError(observability.CodeUsage, "no Project was named for "+string(req.Action))
	}
	kind, err := consoleHarness(req.Harness)
	if err != nil {
		return "", err
	}
	res, err := spawn.StartMate(ctx, ws, deps, spawn.StartRequest{Project: req.Target, Harness: kind})
	if err != nil {
		return "", err
	}
	line := fmt.Sprintf("Mate %s is running on %s in pane %s", res.Agent, res.Harness, res.Pane)
	if res.StaleMeta {
		line += "; the previous record was stale and has been replaced"
	}
	if res.UpdateDialog {
		line += "; a Codex update prompt was skipped"
	}
	if res.TrustDialog {
		line += "; a trust dialog was answered"
	}
	return line, nil
}

// mergeCrewAction is the Console's `merge` entry on a `wait-mate` Crew row
// (mvp.md task 22). The caller is CallerUser and is passed as a literal,
// never read from the environment: the console is the captain's own
// program, and a console that inherited MATEV2_CALLER=mate - which it does
// whenever the captain opens it from inside a Mate's pane - would otherwise
// refuse the captain's own keystroke because the project's yolo is off.
//
// The line it returns is the command's own one line, so the console and
// `matev2 merge` report the same event in the same words, and a refusal is
// propagated verbatim for the reason every other branch here does.
func mergeCrewAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req console.ActionRequest) (string, error) {
	if req.Target == "" || req.Crew == "" {
		return "", observability.NewError(observability.CodeUsage,
			"merge needs both a Project and one of its Crews; the request named "+req.Target+"/"+req.Crew)
	}
	res, err := spawn.MergeCrew(ctx, ws, deps, req.Target, req.Crew, spawn.CallerUser)
	if err != nil {
		return "", err
	}
	return res.Line(), nil
}

// toggleModeAction flips `mate/.auto` (mvp.md section 5). The flag is the
// whole of the mode: the daemon re-reads it every tick and at the moment it
// types, so the keystroke takes effect within one window in both directions
// without the Console telling the daemon anything.
//
// The line it returns names what the flag now permits, and the digest window
// with it, because "auto" on its own does not tell a reader when the first
// line might land in the Mate's pane.
func toggleModeAction(ws *store.Workspace, project string) (string, error) {
	if project == "" {
		return "", observability.NewError(observability.CodeUsage, "no Project was named for mode")
	}
	if err := store.ValidateProjectName(project); err != nil {
		return "", err
	}
	if _, ok := ws.Project(project); !ok {
		return "", fmt.Errorf("%w: %s", store.ErrNoProject, project)
	}
	next := !ws.Auto(project)
	if err := ws.SetAuto(project, next); err != nil {
		return "", err
	}
	mode := query.ModeFor(next)
	if next {
		return fmt.Sprintf("%s is now %s; the daemon digests the inbox into the Mate's pane every %s",
			project, mode, autopilot.DefaultInterval), nil
	}
	return fmt.Sprintf("%s is now %s; nothing is sent to the Mate without a keystroke", project, mode), nil
}

// consoleHarness parses the harness the Console picked. It is parsed here
// rather than trusted: the Console sends a string across a seam, and an
// unknown kind must be a refusal naming the field.
func consoleHarness(kind query.HarnessKind) (harness.Kind, error) {
	if kind == "" {
		return "", nil
	}
	parsed, err := harness.ParseKind(string(kind))
	if err != nil {
		return "", observability.WrapError(observability.CodeUsage, "harness", err)
	}
	return parsed, nil
}
