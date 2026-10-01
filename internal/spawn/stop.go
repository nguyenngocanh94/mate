package spawn

import (
	"context"
	"fmt"
	"path/filepath"
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
	if !running {
		// Herdr does not report the session running, so there is no server
		// to ask about the agent or the tab. The record is treated as stale
		// and cleared, keeping a Codex session the rollouts can still name,
		// but the absence is inferred, not confirmed: Confirmed stays false
		// and nothing may treat the transcript as at rest.
		out.AlreadyGone, out.TabClosed = true, true
		if meta[MetaHarness] == string(harness.KindCodex) {
			if id := codexSessionAtStop(deps, w.MateDir(project), meta, ""); id != "" {
				meta[MetaSessionID] = id
				out.SessionID = id
			}
		}
		return out, clearRunMeta(w, project, meta, deps.now())
	}

	kind, _ := harness.ParseKind(meta[MetaHarness])
	handle := runtime.AgentHandle{Session: session, Name: out.Agent, RawID: project, Kind: kind, Tab: tab}
	observed, live, err := inspectLive(ctx, deps, handle)
	if err != nil {
		return StopResult{}, err
	}
	out.AlreadyGone = !live
	if kind == harness.KindCodex {
		// Read before the agent is gone: Herdr forgets agent_session with
		// the agent, and a Codex session id exists nowhere mate writes.
		if id := codexSessionAtStop(deps, w.MateDir(project), meta, observed.SessionRef); id != "" {
			meta[MetaSessionID] = id
			out.SessionID = id
		}
	}
	if live {
		if err := stopLiveAgent(ctx, deps, handle); err != nil {
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

// codexSessionAtStop is the Codex session a stopping Mate was in, for the
// next start to resume (task 35, B11). Codex has no launch-time session id:
// it exists once the first prompt opens the rollout, and after a `/clear` it
// is a new one, so it is read at the one moment that knows the answer.
//
// Herdr's agent_session.value is the first rule. It is exact, and on Herdr
// 0.8.2 it is filled by the Herdr integration's SessionStart hook in the
// operator's ~/.codex/hooks.json, which codex-cli 0.154.0 runs at the first
// prompt of a session, not at launch (measured 2026-09-24). When Herdr has
// none - the agent already gone, or no integration - the rollout is adopted
// by the timeline's own rule (harness.AdoptCodexRollout: this cwd, a
// session_meta at or after launched_at, and exactly one of them). Empty
// means neither answered, and the caller keeps whatever session_id the meta
// already had: a resumed session's rollout is older than its launch, so the
// adoption rule rightly finds nothing new for it.
func codexSessionAtStop(deps Deps, mateDir string, meta map[string]string, herdrRef string) string {
	if ref := strings.TrimSpace(herdrRef); ref != "" {
		return ref
	}
	launched, err := time.Parse(time.RFC3339, strings.TrimSpace(meta[MetaLaunchedAt]))
	if err != nil {
		return ""
	}
	dir, err := deps.codexSessionsDir()
	if err != nil {
		return ""
	}
	candidates, err := harness.CodexRolloutCandidatesSince(dir, launched)
	if err != nil {
		return ""
	}
	cwd := mateDir
	if resolved, err := filepath.EvalSymlinks(mateDir); err == nil {
		cwd = resolved
	}
	adopted := harness.AdoptCodexRollout(candidates, cwd, launched, "")
	if adopted.Status != harness.CodexAdoptionKnown {
		return ""
	}
	return adopted.Candidate.Meta.SessionID
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
