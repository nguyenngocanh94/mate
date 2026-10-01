package spawn

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
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
	// Confirmed is true when the stop proved the agent absent through
	// confirmGone: `agent get` reported it not found and the session
	// inventory did not list it. It is false when the absence was only
	// inferred - Herdr's session list did not report the session running,
	// so no agent could be asked about - and false on a stop that changed
	// nothing because the record was already closed. Only a confirmed stop
	// may be taken to mean the harness has stopped writing its transcript.
	Confirmed bool
	// TabClosed is true when the tab was removed (or was already).
	TabClosed bool
	// Forced says why the stop closed the agent's pane instead of letting
	// the harness exit on its own: no verified graceful stop, or one that
	// did not end the agent (plan section 3.7). Empty when the harness
	// exited, or when there was no live agent to stop.
	Forced string

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
	// worktree. For an OrphanedWorktree it is the number of paths that
	// differ from the branch, and 0 when the files are exactly a commit
	// git already holds.
	DirtyFiles int
	// OrphanedWorktree is true when the worktree directory was there but
	// git no longer treated it as one (its `.git` named a gitdir that is
	// gone, as after the workspace moved machines). It is then judged by
	// its files and removed as a plain directory.
	OrphanedWorktree bool
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
	// AlreadyClosed is true when the crew's meta already recorded a final
	// state: the stop changed nothing, and Teardown and State are the ones
	// recorded by the stop (or merge) that closed it.
	AlreadyClosed bool
}

// StopMate stops the Mate of one project and proves it is gone.
//
// `herdr agent stop` reporting success is not the proof: the agent must be
// absent from the session inventory *and* unknown to `agent get`, which is
// the pair v1 required after observing the two disagree for a window. When
// Herdr's session list does not report the session running there is nothing
// to ask; the stale record is cleared and the result says AlreadyGone with
// Confirmed false. The
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
	kind, _ := deps.Harnesses.Parse(meta[MetaHarness])
	if !running {
		// Herdr does not report the session running, so there is no server
		// to ask about the agent or the tab. The record is treated as stale
		// and cleared, keeping a session the harness can still name from
		// disk, but the absence is inferred, not confirmed: Confirmed stays
		// false and nothing may treat the transcript as at rest.
		out.AlreadyGone, out.TabClosed = true, true
		if id := sessionAtStop(deps, kind, w.MateDir(project), meta, ""); id != "" {
			meta[MetaSessionID] = id
			out.SessionID = id
		}
		return out, clearRunMeta(w, project, meta, deps.now())
	}

	handle := runtime.AgentHandle{Session: session, Name: out.Agent, RawID: project, Kind: kind, Tab: tab}
	observed, live, err := inspectLive(ctx, deps, handle)
	if err != nil {
		return StopResult{}, err
	}
	out.AlreadyGone = !live
	// Read before the agent is gone: Herdr forgets agent_session with the
	// agent, and a harness that names its session only after the first
	// prompt (Codex) has it nowhere mate writes.
	if id := sessionAtStop(deps, kind, w.MateDir(project), meta, observed.SessionRef); id != "" {
		meta[MetaSessionID] = id
		out.SessionID = id
	}
	if live {
		if out.Forced, err = stopLiveAgent(ctx, deps, handle); err != nil {
			return StopResult{}, err
		}
	}
	if err := confirmGone(ctx, deps, handle); err != nil {
		return StopResult{}, err
	}
	out.Confirmed = true
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
// forced says why the pane was closed instead, empty when the harness
// exited on its own.
func stopLiveAgent(ctx context.Context, deps Deps, handle runtime.AgentHandle) (string, error) {
	exit, forced := gracefulStop(deps, handle.Kind)
	if exit != nil {
		failed := deps.Runtime.StopAgent(ctx, handle, runtime.StopGraceful(exit))
		if failed == nil || runtime.IsAgentNotFound(failed) {
			if failed = confirmGone(ctx, deps, handle); failed == nil {
				return "", nil
			}
		}
		forced = fmt.Sprintf("the %s exit prompt %q did not end the agent (%s)", handle.Kind, exit.ExitPrompt(), oneLineErr(failed))
	}
	if err := deps.Runtime.StopAgent(ctx, handle, runtime.StopForce); err != nil && !runtime.IsAgentNotFound(err) {
		if confirmGone(ctx, deps, handle) == nil {
			return forced, nil
		}
		return forced, err
	}
	return forced, confirmGone(ctx, deps, handle)
}

// gracefulStop is the harness's verified graceful stop, or nil and why
// there is none: unsupported, unknown, or a kind no profile is registered
// for all go straight to the force stop (plan section 3.7).
func gracefulStop(deps Deps, kind harness.Kind) (harness.GracefulStopper, string) {
	profile, err := deps.Harnesses.Lookup(kind)
	if err != nil {
		return nil, oneLineErr(err)
	}
	c := profile.Capabilities().GracefulStop
	if !c.Verified() {
		return nil, fmt.Sprintf("the %s harness declares no verified graceful stop (%s)", kind, capReason(c.Status, c.Reason))
	}
	return c.Impl, ""
}

// capReason is why a capability is not verified, for a note: its declared
// reason, or its status when it gave none.
func capReason(status harness.CapStatus, reason string) string {
	if strings.TrimSpace(reason) != "" {
		return reason
	}
	if status == harness.CapUndeclared {
		return "undeclared"
	}
	return string(status)
}

// confirmGone is the only way a stop is confirmed: `agent get` must not find
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
	_, live, err := inspectLive(ctx, deps, handle)
	return live, err
}

// inspectLive is agentLive that keeps what Herdr said about the agent.
func inspectLive(ctx context.Context, deps Deps, handle runtime.AgentHandle) (runtime.ObservedAgent, bool, error) {
	observed, err := deps.Runtime.InspectAgent(ctx, handle)
	if err != nil {
		if runtime.IsAgentNotFound(err) {
			return runtime.ObservedAgent{}, false, nil
		}
		return runtime.ObservedAgent{}, false, err
	}
	return observed, true, nil
}

// sessionAtStop is the session a stopping Mate was in, for the next start to
// resume (task 35, B11), as its harness names it
// (harness.SessionIdentity.AtStop) from Herdr's agent_session.value
// (herdrRef) or from disk. Empty means the harness could not say - or has
// no verified session identity, so there is nothing to resume anyway - and
// the caller keeps whatever session_id the meta already had.
func sessionAtStop(deps Deps, kind harness.Kind, mateDir string, meta map[string]string, herdrRef string) string {
	profile, err := deps.Harnesses.Lookup(kind)
	if err != nil {
		return ""
	}
	session := profile.Capabilities().Session
	if !session.Verified() {
		return ""
	}
	// An unreadable launched_at is the zero time: the harness then has only
	// herdrRef to go on.
	launched, _ := time.Parse(time.RFC3339, strings.TrimSpace(meta[MetaLaunchedAt]))
	return session.Impl.AtStop(mateDir, launched, herdrRef)
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
