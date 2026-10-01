package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// MetaTeardown records what a stop did with the worktree and the branch:
// TeardownClean or TeardownDiscarded. It is absent for a crew that has
// never been stopped, and absent for a Mate, which has no worktree of its
// own.
const MetaTeardown = "teardown"

// Teardown outcomes written to `teardown=`. There are only two, because a
// stop now has only two outcomes: it tears the crew down, or it refuses and
// changes nothing (mvp.md section 4b).
const (
	// TeardownClean means the worktree and branch were removed and neither
	// held anything: the branch was already an ancestor of the project's
	// default branch and the worktree was not dirty. The crew is finished.
	TeardownClean = "clean"
	// TeardownDiscarded means the worktree and branch were removed with
	// unlanded work, because the caller passed --discard. The crew failed:
	// the work was thrown away.
	TeardownDiscarded = "discarded"
)

// ErrUnlandedWork is returned by StopCrew when the crew's branch is not
// fully contained in the project's default branch, or the worktree has
// uncommitted changes, and the caller did not pass discard=true. It is a
// refusal, checked before anything is touched: the agent is still running,
// the tab still open, the worktree and branch exactly as they were, and the
// meta unchanged. The previous shape of this - kill the agent, close the
// tab, then refuse the cleanup - left a third outcome nobody could name,
// where the crew was dead but the task was not closed (mvp.md section 4b
// removes it).
var ErrUnlandedWork = errors.New("crew has unlanded work")

// StopCrew closes one crew's task: docs/mvp.md task 16 and the state
// machine of section 4b. `crews/<id>/` (brief, report, transcript) is never
// touched here - only a human deleting it by hand removes it.
//
// The order is the point. The teardown decision is made first, from git
// facts and never from the status log: unlanded means the branch carries a
// commit the project's default branch does not have (`git merge-base
// --is-ancestor` false), or the worktree has uncommitted changes (`git
// status --porcelain` non-empty). Unlanded work without discard=true is
// refused with ErrUnlandedWork before a single byte changes, so the answer
// to a refusal is to look at the branch - with the crew still alive to ask -
// and then either land it or rerun with --discard.
//
// Only once the teardown is allowed does the agent get stopped, the tab
// closed, the worktree removed and the branch deleted. The meta is written
// last, in one write: stopped_at=, teardown=, and the state that ends the
// task - `finished` for a clean stop, `failed` for a --discard, because
// discarding is deciding the work will not land.
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

	// 0. A closed task stays closed. `finished` and `failed` are final
	// (mvp.md section 4b), so a second stop - after `mate merge` closed
	// the crew, or a --discard sweep over every crew - reports the outcome
	// already recorded and changes nothing; it never turns a merged crew
	// into a failed one.
	if state := meta[MetaState]; state == CrewStateFinished || state == CrewStateFailed {
		out.AlreadyClosed, out.AlreadyGone, out.TabClosed = true, true, true
		out.Branch = meta[MetaBranch]
		if rel := meta[MetaWorktree]; rel != "" {
			out.Worktree = filepath.Join(w.Root(), filepath.FromSlash(rel))
		}
		out.Teardown, out.State = meta[MetaTeardown], state
		return out, nil
	}

	// 1. The teardown decision, from git facts, before anything is touched.
	cfg, err := w.LoadProject(project)
	if err != nil {
		return StopResult{}, err
	}
	repoCfg, err := cfg.CrewRepo(meta)
	if err != nil {
		return StopResult{}, observability.WrapError(observability.CodeStateConflict,
			fmt.Sprintf("crew stop %s/%s refused, nothing was stopped (meta %s)", project, crew, w.CrewMeta(project, crew)), err)
	}
	repo := w.RepoDir(repoCfg.Path)
	git := deps.git()

	branch := meta[MetaBranch]
	worktree := ""
	if rel := meta[MetaWorktree]; rel != "" {
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
			isAncestor, err = git.IsAncestor(ctx, repo, branch, repoCfg.DefaultBranch)
			if err != nil {
				return StopResult{}, err
			}
			if !isAncestor {
				ahead, err = git.AheadCount(ctx, repo, branch, repoCfg.DefaultBranch)
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

	if unlanded && !discard {
		// Nothing has been changed and nothing will be: the crew is still
		// running, its pane is still open, and the reader can go and look
		// at the branch before deciding.
		return out, observability.WrapError(observability.CodeStateConflict,
			fmt.Sprintf("branch %s is %d commit(s) ahead of %s and the worktree has %d dirty file(s); nothing was stopped - land the branch, or rerun with --discard to throw the work away",
				branch, ahead, repoCfg.DefaultBranch, dirty), ErrUnlandedWork).
			WithDetails(map[string]any{"branch": branch, "ahead": ahead, "dirty_files": dirty})
	}

	// 2 & 3. Stop the agent (if Herdr still has one) and close the tab. A
	// meta with no recorded agent is not an error here: it is either a crew
	// that was already stopped by a previous call or one whose Herdr record
	// never survived a crash.
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
			kind, _ := deps.Harnesses.Parse(meta[MetaHarness])
			handle := runtime.AgentHandle{Session: session, Name: out.Agent, RawID: crew, Kind: kind, Tab: tab}
			live, err := agentLive(ctx, deps, handle)
			if err != nil {
				return StopResult{}, err
			}
			out.AlreadyGone = !live
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
			}
			out.TabClosed = true
		}
		stoppedMeta = clearCrewRunMeta(meta)
	}

	// 4. The worktree is given back, then the branch - only when it is
	// already contained in default or the caller accepted the loss. Two
	// releases, not one, because the result reports each half.
	worktrees := deps.worktrees()
	lease := WorktreeLease{Repo: repo, Path: worktree, Branch: branch, Base: repoCfg.DefaultBranch}
	if worktreeExists {
		if err := worktrees.Release(ctx, lease, ReleaseParts{Worktree: true}); err != nil {
			return out, err
		}
		out.WorktreeRemoved = true
	}
	if branchExists && (isAncestor || discard) {
		if err := worktrees.Release(ctx, lease, ReleaseParts{Branch: true}); err != nil {
			return out, err
		}
		out.BranchRemoved = true
	}

	// 5. The one meta write: the task is over, and which way.
	//
	// The two words answer two different questions. `teardown=` is what
	// happened to the worktree and branch, so it says `discarded` only when
	// something was actually thrown away. `state=` is the caller's verdict
	// on the task, and mvp.md section 4b assigns it by the flag: a stop is
	// `finished`, a `--discard` is `failed`. Reaching for --discard is
	// saying the work will not land, and that is true whether or not the
	// branch happened to be clean when it was said.
	teardown, state := TeardownClean, CrewStateFinished
	if unlanded {
		teardown = TeardownDiscarded
	}
	if discard {
		state = CrewStateFailed
	}
	out.Teardown, out.State = teardown, state
	return out, writeCrewTeardownMeta(w, project, crew, stoppedMeta, deps.now(), teardown, state)
}

// clearCrewRunMeta drops the keys that named a live pane and keeps
// everything a review or a later teardown decision still needs: the task,
// the branch, the worktree and the harness session identity.
func clearCrewRunMeta(meta map[string]string) map[string]string {
	next := map[string]string{}
	for _, key := range []string{MetaTask, MetaHarness, MetaSession, MetaSessionID, MetaTranscript, MetaWorktree, MetaBranch, store.MetaRepo, MetaStartedAt, MetaLaunchedAt} {
		if v, ok := meta[key]; ok {
			next[key] = v
		}
	}
	return next
}

// writeCrewTeardownMeta writes the final `.meta` of a stop: whatever
// clearCrewRunMeta kept, plus stopped_at=, teardown= and state=. It is the
// single meta write of a StopCrew call, so a crew always shows either its
// full pre-stop meta or its full post-stop meta, never something in
// between - and by the time it runs the teardown has already happened, so
// the state it records is a fact rather than an intention.
func writeCrewTeardownMeta(w *store.Workspace, project, crew string, meta map[string]string, now time.Time, teardown, state string) error {
	next := make(map[string]string, len(meta)+3)
	for k, v := range meta {
		next[k] = v
	}
	next[MetaStoppedAt] = now.Format(time.RFC3339)
	next[MetaTeardown] = teardown
	next[MetaState] = state
	return w.WriteCrewMeta(project, crew, next)
}

// CrewSummary is one row of `mate crew list`: the crew's declared state,
// and beside it the crew's own last word. They are two different things and
// the table shows both - the state is who the crew is to the app, the note
// is what it said about its work.
type CrewSummary struct {
	Crew    string
	Harness string
	// Model and Effort are the launch profile (`model=`, `effort=`), ""
	// for the harness's own default.
	Model  string
	Effort string
	Branch string
	// State is the declared state of mvp.md section 4b, resolved in the
	// fixed order: `.meta` state=, then an open incident, then the last
	// status verb, then `spawned`.
	State string
	// Note is the text of the crew's last status line, verb stripped -
	// empty for a crew that has written nothing. It is the NOTE column and
	// never a state: a crew's own words are not the app's vocabulary.
	Note string
	Pane string
	Task string
	// Closed is whether State is terminal (`finished` or `failed`).
	// `crew list` hides closed crews unless asked for them; the console
	// never shows them.
	Closed bool
}

// ListCrews reads every `crews/<id>.meta` of a project, the last line of
// each crew's status file, and the observer's open incidents from the
// project's box. It asks Herdr nothing: a list is a view of what was
// recorded, and `mate state <crew>` (task 13) is where a live answer -
// the health column - comes from.
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
	// One box read for the whole list: `blocked` is an open incident and
	// nothing else, and reading the file once per crew could report two
	// different answers inside one table. A box that will not read leaves
	// every crew without incidents, which is the honest degradation -
	// `blocked` is a claim a failed read has not established.
	view, viewOK := box.View{}, false
	if v, err := box.Load(w, project); err == nil {
		view, viewOK = v, true
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
		lines := make([]string, 0, len(entries))
		for _, e := range entries {
			lines = append(lines, e.Line)
		}
		verb := box.LastVerb(lines)
		note := ""
		if len(entries) > 0 {
			note = box.ParseStatus(entries[len(entries)-1].Line).Text
		}
		if verb == box.StateUnknown {
			verb = ""
		}
		state := crewstate.Declare(crewstate.Declaration{
			Meta:         meta,
			OpenIncident: viewOK && len(box.BlockingIncidents(view, id)) > 0,
			LastVerb:     crewstate.StatusVerb(verb),
		})
		out = append(out, CrewSummary{
			Crew:    id,
			Harness: meta[MetaHarness],
			Model:   meta[MetaModel],
			Effort:  meta[MetaEffort],
			Branch:  meta[MetaBranch],
			State:   string(state),
			Note:    note,
			Pane:    meta[MetaPane],
			Task:    meta[MetaTask],
			Closed:  state.Closed(),
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

// crewIDs are the ids with a `.meta` under `crews/`, sorted (store.CrewIDs).
func crewIDs(w *store.Workspace, project string) ([]string, error) {
	return w.CrewIDs(project)
}
