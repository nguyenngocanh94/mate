package spawn

import (
	"context"
	"fmt"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// StopResult is what a stop established.
type StopResult struct {
	Project string
	// Agent is the name that was stopped, as `mate.meta` recorded it. It is
	// empty when the meta named none.
	Agent string
	// Session is the Herdr session the agent lived in.
	Session string
	// SessionID is the harness session uuid kept for a later resume.
	SessionID string
	// AlreadyGone is true when Herdr had no such agent before the stop: the
	// meta was stale and nothing had to be killed.
	AlreadyGone bool
	// TabClosed is true when the tab was removed (or was already).
	TabClosed bool

	// The fields below are set only by StopCrew; a Mate has no worktree or
	// branch of its own.

	// Branch and Worktree are the crew's, as recorded in its meta before
	// this stop's teardown decision.
	Branch   string
	Worktree string
	// Unlanded is true when the branch was not fully contained in the
	// project's default branch, or the worktree had uncommitted changes.
	Unlanded bool
	// Ahead is the commit count the branch had over the default branch.
	// Only meaningful when Unlanded is true because of the branch.
	Ahead int
	// DirtyFiles is the `git status --porcelain` line count of the
	// worktree. Only meaningful when Unlanded is true because of the
	// worktree.
	DirtyFiles int
	// Teardown is TeardownClean or TeardownDiscarded once StopCrew has torn
	// the crew down. It is empty on a refusal, which changes nothing.
	Teardown string
	// State is the terminal state written to `crews/<id>.meta`:
	// CrewStateFinished for a clean stop, CrewStateFailed for a --discard.
	// Empty on a refusal, for the same reason.
	State string
	// WorktreeRemoved and BranchRemoved report what a teardown actually did,
	// as distinct from what it decided: a crew whose worktree or branch had
	// already been removed by an earlier call leaves these false even on a
	// successful (idempotent) teardown.
	WorktreeRemoved bool
	BranchRemoved   bool
}

// StopMate stops the Mate of one project and proves it is gone.
//
// `herdr agent stop` reporting success is not the proof: the agent must be
// absent from the session inventory *and* unknown to `agent get`, which is
// the pair v1 required after observing the two disagree for a window. The
// tab is then closed, and `mate.meta` keeps `session_id=` - task 10 resumes
// the conversation from it - while `agent=` and `pane=` are dropped, because
// nothing owns them any more.
func StopMate(ctx context.Context, w *store.Workspace, deps Deps, project string) (StopResult, error) {
	if w == nil {
		return StopResult{}, errUsage("spawn: a workspace is required")
	}
	if deps.Runtime == nil {
		return StopResult{}, errUsage("spawn: a runtime adapter is required")
	}
	if deps.Names == nil {
		deps.Names = runtime.NewMemoryNameRegistry()
	}
	if err := store.ValidateProjectName(project); err != nil {
		return StopResult{}, err
	}
	meta, err := w.ReadMateMeta(project)
	if err != nil {
		return StopResult{}, err
	}
	out := StopResult{
		Project:   project,
		Agent:     meta[MetaAgent],
		Session:   meta[MetaSession],
		SessionID: meta[MetaSessionID],
	}
	if out.Agent == "" {
		return out, observability.NewError(observability.CodeNotFound,
			fmt.Sprintf("no Mate is recorded for project %s; %s names no agent", project, w.MateMeta(project)))
	}

	spec, err := deps.sessionSpec(w)
	if err != nil {
		return StopResult{}, err
	}
	session, running, err := deps.Runtime.LookupSession(ctx, spec)
	if err != nil {
		return StopResult{}, err
	}
	tab := runtime.TabHandle{
		Session:     session,
		WorkspaceID: meta[MetaWorkspace],
		TabID:       meta[MetaTab],
		PaneID:      meta[MetaPane],
		Cwd:         w.MateDir(project),
		Label:       MateTabLabel,
	}
	if !running {
		// No server, no agent, no tab. The record is stale; clear it.
		out.AlreadyGone, out.TabClosed = true, true
		return out, clearRunMeta(w, project, meta, deps.now())
	}

	kind, _ := harness.ParseKind(meta[MetaHarness])
	handle := runtime.AgentHandle{Session: session, Name: out.Agent, RawID: project, Kind: kind, Tab: tab}
	live, err := agentLive(ctx, deps, handle)
	if err != nil {
		return StopResult{}, err
	}
	out.AlreadyGone = !live
	if live {
		if err := stopLiveAgent(ctx, deps, handle); err != nil {
			return StopResult{}, err
		}
	}
	if err := confirmGone(ctx, deps, handle); err != nil {
		return StopResult{}, err
	}
	deps.Names.Release(session.Name, handle.Name)

	if tab.PaneID != "" || tab.TabID != "" {
		if err := deps.Runtime.RemoveTab(ctx, tab); err != nil && !runtime.IsTabGone(err) {
			return StopResult{}, err
		}
		out.TabClosed = true
	}
	return out, clearRunMeta(w, project, meta, deps.now())
}

// stopLiveAgent asks for a graceful stop where the harness has one, then
// forces. A name Herdr already does not know is success, not a failure.
func stopLiveAgent(ctx context.Context, deps Deps, handle runtime.AgentHandle) error {
	if handle.Kind == harness.KindClaude {
		if err := deps.Runtime.StopAgent(ctx, handle, runtime.StopGraceful); err == nil || runtime.IsAgentNotFound(err) {
			if confirmGone(ctx, deps, handle) == nil {
				return nil
			}
		}
	}
	if err := deps.Runtime.StopAgent(ctx, handle, runtime.StopForce); err != nil && !runtime.IsAgentNotFound(err) {
		if confirmGone(ctx, deps, handle) == nil {
			return nil
		}
		return err
	}
	return confirmGone(ctx, deps, handle)
}

// confirmGone is the only success path for a stop: `agent get` must not find
// the name and the session inventory must not list it. Either one alone has
// been observed lagging the other.
func confirmGone(ctx context.Context, deps Deps, handle runtime.AgentHandle) error {
	if _, err := deps.Runtime.InspectAgent(ctx, handle); err == nil {
		return observability.NewError(observability.CodeUnknown,
			fmt.Sprintf("agent %q is still live; stop is not confirmed", handle.Name)).
			WithDetails(map[string]any{"agent_name": handle.Name})
	} else if !runtime.IsAgentNotFound(err) {
		return observability.WrapError(observability.CodeUnknown,
			"stop could not be confirmed; the agent was not inspectable", err)
	}
	listed, err := deps.Runtime.ListAgents(ctx, handle.Session)
	if err != nil {
		return observability.WrapError(observability.CodeUnknown,
			"stop could not be confirmed; the live inventory was not readable", err)
	}
	for _, obs := range listed {
		if obs.Handle.Name == handle.Name {
			return observability.NewError(observability.CodeUnknown,
				fmt.Sprintf("agent %q is gone from `agent get` but still in the session inventory; stop is not confirmed", handle.Name)).
				WithDetails(map[string]any{"agent_name": handle.Name})
		}
	}
	return nil
}

// agentLive reports whether Herdr still knows the recorded name.
func agentLive(ctx context.Context, deps Deps, handle runtime.AgentHandle) (bool, error) {
	if _, err := deps.Runtime.InspectAgent(ctx, handle); err != nil {
		if runtime.IsAgentNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// clearRunMeta drops everything that named a live pane and keeps what a
// restart needs: the harness, the Herdr session and the harness session id.
func clearRunMeta(w *store.Workspace, project string, meta map[string]string, now time.Time) error {
	next := map[string]string{}
	for _, key := range []string{MetaHarness, MetaSession, MetaSessionID, MetaStartedAt} {
		if v := meta[key]; v != "" {
			next[key] = v
		}
	}
	next[MetaStoppedAt] = now.Format(time.RFC3339)
	return w.WriteMateMeta(project, next)
}
