package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// MetaTeardown records what a stop did with the worktree and the branch: one
// of TeardownClean, TeardownDiscarded or TeardownRefusedUnlanded. It is
// absent for a crew that has never been stopped, and absent for a Mate,
// which has no worktree of its own.
const MetaTeardown = "teardown"

// Teardown outcomes written to `teardown=`.
const (
	// TeardownClean means the worktree and branch were removed and neither
	// held anything: the branch was already an ancestor of the project's
	// default branch and the worktree was not dirty.
	TeardownClean = "clean"
	// TeardownDiscarded means the worktree and branch were removed with
	// unlanded work, because the caller passed --discard.
	TeardownDiscarded = "discarded"
	// TeardownRefusedUnlanded means the stop refused to remove the
	// worktree and branch because they carried unlanded work and --discard
	// was not given. The worktree and branch are still in place.
	TeardownRefusedUnlanded = "refused_unlanded"
)

// ErrUnlandedWork is returned by StopCrew when the crew's branch is not
// fully contained in the project's default branch, or the worktree has
// uncommitted changes, and the caller did not pass discard=true. By the
// time this error reaches the caller the agent is already stopped and the
// tab already closed - only the worktree and the branch are left standing,
// exactly as they were, so a human can look at them before deciding.
var ErrUnlandedWork = errors.New("crew has unlanded work")

// StopCrew stops one crew's agent, closes its tab, and then tears down its
// worktree and branch unless doing so would silently discard work: docs/mvp.md
// task 16. `crews/<id>/` (brief, report, transcript) is never touched here -
// only a human deleting it by hand removes it.
//
// The teardown decision is made from git facts, never from the status log:
// unlanded means the branch carries a commit the project's default branch
// does not have (`git merge-base --is-ancestor` false), or the worktree has
// uncommitted changes (`git status --porcelain` non-empty). Unlanded work
// without discard=true is refused with ErrUnlandedWork; the meta still
// records the stop (stopped_at=, teardown=refused_unlanded) so a rerun with
// discard=true can finish the job. Otherwise the worktree is force-removed
// and the branch is deleted whenever it is an ancestor of default or discard
// was given, and the meta records teardown=clean or teardown=discarded.
//
// Like StopMate, the proof the agent is gone is Herdr's own answer: absent
// from `agent get` and from the session inventory. A tab Herdr already does
// not know about (`tab_not_found`, ADR 0028 in v1) counts as closed, and an
// agent Herdr already has no record of is not an error - it is the normal
// shape of a crew that crashed, or of a rerun after a previous refusal.
func StopCrew(ctx context.Context, w *store.Workspace, deps Deps, project, crew string, discard bool) (StopResult, error) {
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
	if err := store.ValidateCrewID(crew); err != nil {
		return StopResult{}, err
	}
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return StopResult{}, err
	}
	if len(meta) == 0 {
		return StopResult{}, observability.NewError(observability.CodeNotFound,
			fmt.Sprintf("no crew %s is recorded for project %s; %s is empty", crew, project, w.CrewMeta(project, crew)))
	}

	out := StopResult{
		Project:   project,
		Agent:     meta[MetaAgent],
		Session:   meta[MetaSession],
		SessionID: meta[MetaSessionID],
	}

	// 1 & 2. Stop the agent (if Herdr still has one) and close the tab. A
	// meta with no recorded agent is not an error here: it is either a crew
	// that was already stopped by a previous call (including a previous
	// refusal, which clears agent/pane/tab exactly as a full stop does) or
	// one whose Herdr record never survived a crash.
	stoppedMeta := meta
	if out.Agent == "" {
		out.AlreadyGone, out.TabClosed = true, true
	} else {
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
			Label:       CrewTabLabelPrefix + crew,
		}
		if !running {
			out.AlreadyGone, out.TabClosed = true, true
		} else {
			kind, _ := harness.ParseKind(meta[MetaHarness])
			handle := runtime.AgentHandle{Session: session, Name: out.Agent, RawID: crew, Kind: kind, Tab: tab}
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
			}
			out.TabClosed = true
		}
		stoppedMeta = clearCrewRunMeta(meta)
	}

	// 3. The teardown decision, from git facts.
	cfg, err := w.LoadProject(project)
	if err != nil {
		return StopResult{}, err
	}
	repo := w.RepoDir(cfg.Repo)
	git := deps.git()

	branch := stoppedMeta[MetaBranch]
	worktree := ""
	if rel := stoppedMeta[MetaWorktree]; rel != "" {
		worktree = filepath.Join(w.Root(), filepath.FromSlash(rel))
	}
	out.Branch, out.Worktree = branch, worktree

	branchExists := false
	isAncestor := true
	ahead := 0
	if branch != "" {
		branchExists, err = git.BranchExists(ctx, repo, branch)
		if err != nil {
			return StopResult{}, err
		}
		if branchExists {
			isAncestor, err = git.IsAncestor(ctx, repo, branch, cfg.DefaultBranch)
			if err != nil {
				return StopResult{}, err
			}
			if !isAncestor {
				ahead, err = git.AheadCount(ctx, repo, branch, cfg.DefaultBranch)
				if err != nil {
					return StopResult{}, err
				}
			}
		}
	}
	dirty := 0
	worktreeExists := false
	if worktree != "" {
		if _, statErr := os.Stat(worktree); statErr == nil {
			worktreeExists = true
			dirty, err = git.IsDirty(ctx, worktree)
			if err != nil {
				return StopResult{}, err
			}
		}
	}
	unlanded := !isAncestor || dirty > 0
	out.Unlanded, out.Ahead, out.DirtyFiles = unlanded, ahead, dirty
	now := deps.now()

	if unlanded && !discard {
		out.Teardown = TeardownRefusedUnlanded
		if metaErr := writeCrewTeardownMeta(w, project, crew, stoppedMeta, now, TeardownRefusedUnlanded); metaErr != nil {
			return out, metaErr
		}
		return out, observability.WrapError(observability.CodeStateConflict,
			fmt.Sprintf("branch %s is %d commit(s) ahead of %s and the worktree has %d dirty file(s); rerun with --discard to remove them",
				branch, ahead, cfg.DefaultBranch, dirty), ErrUnlandedWork).
			WithDetails(map[string]any{"branch": branch, "ahead": ahead, "dirty_files": dirty})
	}

	// 4. `git worktree remove --force`, then the branch - only when it is
	// already contained in default or the caller accepted the loss.
	if worktreeExists {
		if err := git.RemoveWorktree(ctx, repo, worktree); err != nil {
			return out, err
		}
		out.WorktreeRemoved = true
	}
	if branchExists && (isAncestor || discard) {
		if err := git.DeleteBranch(ctx, repo, branch); err != nil {
			return out, err
		}
		out.BranchRemoved = true
	}

	teardown := TeardownClean
	if unlanded {
		teardown = TeardownDiscarded
	}
	out.Teardown = teardown
	return out, writeCrewTeardownMeta(w, project, crew, stoppedMeta, now, teardown)
}

// clearCrewRunMeta drops the keys that named a live pane and keeps
// everything a review or a later teardown decision still needs: the task,
// the branch, the worktree and the harness session identity.
func clearCrewRunMeta(meta map[string]string) map[string]string {
	next := map[string]string{}
	for _, key := range []string{MetaTask, MetaHarness, MetaSession, MetaSessionID, MetaTranscript, MetaWorktree, MetaBranch, MetaStartedAt} {
		if v, ok := meta[key]; ok {
			next[key] = v
		}
	}
	return next
}

// writeCrewTeardownMeta writes the final `.meta` of a stop: whatever
// clearCrewRunMeta kept, plus stopped_at= and teardown=. It is the single
// meta write of a StopCrew call, so a crew always shows either its full
// pre-stop meta or its full post-stop meta, never something in between.
func writeCrewTeardownMeta(w *store.Workspace, project, crew string, meta map[string]string, now time.Time, teardown string) error {
	next := make(map[string]string, len(meta)+2)
	for k, v := range meta {
		next[k] = v
	}
	next[MetaStoppedAt] = now.Format(time.RFC3339)
	next[MetaTeardown] = teardown
	return w.WriteCrewMeta(project, crew, next)
}

// CrewSummary is one row of `matev2 crew list`.
type CrewSummary struct {
	Crew    string
	Harness string
	Branch  string
	// Status is what a review needs to see at a glance: `stopped
	// (unlanded)` or `torn down` once a stop has run (from `teardown=`),
	// `stopped` for an older stop that predates teardown, or otherwise the
	// last line of `crews/<id>.status`.
	Status string
	Pane   string
	Task   string
	// Closed is `stopped_at` in the meta: `matev2 crew stop` ran, by the
	// Mate's or the captain's decision. `crew list` hides closed crews
	// unless asked for them; the console never shows them.
	Closed bool
}

// crewListStatus derives the STATUS column from a crew's meta first, since
// the status log is what the crew claims about its own task, not what
// happened to its worktree and branch.
func crewListStatus(meta map[string]string, lastStatusLine string) string {
	switch meta[MetaTeardown] {
	case TeardownRefusedUnlanded:
		return "stopped (unlanded)"
	case TeardownClean, TeardownDiscarded:
		return "torn down"
	}
	if meta[MetaStoppedAt] != "" {
		return "stopped"
	}
	return lastStatusLine
}

// ListCrews reads every `crews/<id>.meta` of a project and the last line of
// each crew's status file. It asks Herdr nothing: a list is a view of what
// was recorded, and `matev2 state <crew>` (task 13) is where a live answer
// comes from.
func ListCrews(w *store.Workspace, project string) ([]CrewSummary, error) {
	if w == nil {
		return nil, errUsage("spawn: a workspace is required")
	}
	if err := store.ValidateProjectName(project); err != nil {
		return nil, err
	}
	if _, ok := w.Project(project); !ok {
		return nil, fmt.Errorf("%w: %s", store.ErrNoProject, project)
	}
	ids, err := crewIDs(w, project)
	if err != nil {
		return nil, err
	}
	out := make([]CrewSummary, 0, len(ids))
	for _, id := range ids {
		meta, err := w.ReadCrewMeta(project, id)
		if err != nil {
			return nil, err
		}
		entries, _, err := w.ReadStatus(project, id, 0)
		if err != nil {
			return nil, err
		}
		last := ""
		if len(entries) > 0 {
			last = entries[len(entries)-1].Line
		}
		out = append(out, CrewSummary{
			Crew:    id,
			Harness: meta[MetaHarness],
			Branch:  meta[MetaBranch],
			Status:  crewListStatus(meta, last),
			Pane:    meta[MetaPane],
			Task:    meta[MetaTask],
			Closed:  meta[MetaStoppedAt] != "",
		})
	}
	return out, nil
}

// readDirNames lists a directory's entries. A missing directory is not an
// error: a project with no crew has nothing under `crews/`.
func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

// crewIDs are the ids with a `.meta` under `crews/`, sorted. A file whose
// name is not a valid crew id is ignored rather than reported: `crews/` is a
// directory the user can also put things in.
func crewIDs(w *store.Workspace, project string) ([]string, error) {
	entries, err := readDirNames(w.CrewsDir(project))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, name := range entries {
		id, ok := strings.CutSuffix(name, ".meta")
		if !ok {
			continue
		}
		if store.ValidateCrewID(id) != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
