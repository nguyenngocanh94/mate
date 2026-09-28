package spawn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The three callers a `mate` command can have, as the pane environment
// spells them. A Mate's pane carries CallerMate and a Crew's pane carries
// CallerCrew because spawn injects them when the pane is made; the captain's
// own shell carries nothing, which is CallerUser.
//
// The default is deliberately the captain: an environment mate did not
// create is somebody typing, and the rule the variable exists for - a Mate
// may not merge while `yolo` is off - must never be skipped because a
// variable was missing. Missing means "not a Mate", and a Mate always has
// the variable because spawn put it there.
const (
	CallerUser = "user"
	CallerMate = "mate"
	CallerCrew = "crew"
)

// CallerFromEnv reads MATE_CALLER out of the process environment and
// normalises it. An unset, empty or unrecognised value is CallerUser: see
// the constants above for why that is the safe default rather than a
// refusal.
func CallerFromEnv() string {
	return NormaliseCaller(os.Getenv(config.EnvCaller))
}

// NormaliseCaller maps one raw MATE_CALLER value onto the three callers.
func NormaliseCaller(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case CallerMate:
		return CallerMate
	case CallerCrew:
		return CallerCrew
	default:
		return CallerUser
	}
}

// MergeResult is one successful `mate merge`: what was landed, and the
// teardown that closed the crew behind it.
type MergeResult struct {
	Project string
	Crew    string
	Branch  string
	// DefaultBranch is the project's default branch, which is both the
	// branch the primary repository had checked out and the branch that
	// moved.
	DefaultBranch string
	// Commits is how many commits the branch carried that the default
	// branch did not. It is read before the merge, because afterwards the
	// answer is always zero.
	Commits int
	// Before and After are the default branch's commit either side of the
	// fast-forward. After is the crew branch's tip by definition of
	// --ff-only; printing both is what lets a reader undo it.
	Before string
	After  string
	// Merged is true once the fast-forward has actually happened. It stays
	// true when the teardown afterwards fails, because the merge is not
	// undone by a failure to close the crew.
	Merged bool
	// Stop is the teardown StopCrew performed. Its State is `finished`.
	Stop StopResult
}

// Line is the one line a successful merge prints, in the console and in the
// CLI alike: both surfaces report the same event, so they report it in the
// same words.
func (r MergeResult) Line() string {
	return fmt.Sprintf("%s/%s: merged %d commit(s) into %s (%s..%s); crew finished, worktree and branch removed",
		r.Project, r.Crew, r.Commits, r.DefaultBranch, shortCommit(r.Before), shortCommit(r.After))
}

// shortCommit is the seven-character prefix git itself prints by default.
func shortCommit(sha string) string {
	s := strings.TrimSpace(sha)
	if len(s) > 7 {
		return s[:7]
	}
	if s == "" {
		return "?"
	}
	return s
}

// mergeRefusal is every refusal of this command: a coded error whose message
// starts with the same three words, because a reader scanning a Mate's pane
// needs to see at a glance that nothing happened.
func mergeRefusal(code observability.Code, msg string) error {
	return observability.NewError(code, "merge refused: "+msg)
}

// MergeCrew lands one crew's branch and closes the crew: docs/mvp.md task 22
// and the M4 decision that "merge là hành động kết thúc một ship", the one
// place a crew reaches a terminal state without `crew stop` being typed.
//
// Every check below is made before anything is touched, in this order, and
// each is a refusal that changes nothing:
//
//  1. the crew is unknown, or already closed (`finished`/`failed`);
//  2. a Crew is typing - a crew never merges its own branch;
//  3. a Mate is typing and the project's `yolo` is off - the captain merges;
//  4. the crew's worktree is dirty - uncommitted work is not in the branch,
//     and merging would land a different change than the one reviewed;
//  5. the branch carries no commit the default branch does not have;
//  6. the branch is not fast-forwardable because the default branch moved
//     on. This is `needs-rebase`, and the M4 decisions are explicit that it
//     is a command result and never a crew state: the crew stays exactly as
//     it was and somebody tells it to rebase;
//  7. the primary repository does not have the default branch checked out,
//     or has uncommitted changes of its own.
//
// Only then does the fast-forward run, and only then StopCrew with
// discard=false - which re-derives the same git facts, now finds the branch
// landed, and writes `state=finished`. A teardown that fails after the merge
// succeeded is reported as exactly that: the merge is not undone, and the
// caller is told the crew is still open.
// ReviewedCommit optionally pins a merge to the independently reviewed revision.
type ReviewedCommit struct{ Head, Base string }

func MergeCrew(ctx context.Context, w *store.Workspace, deps Deps, project, crew, caller string, reviewed ...ReviewedCommit) (MergeResult, error) {
	if w == nil {
		return MergeResult{}, errUsage("spawn: a workspace is required")
	}
	if err := store.ValidateProjectName(project); err != nil {
		return MergeResult{}, err
	}
	if err := store.ValidateCrewID(crew); err != nil {
		return MergeResult{}, err
	}
	if _, ok := w.Project(project); !ok {
		return MergeResult{}, fmt.Errorf("%w: %s", store.ErrNoProject, project)
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return MergeResult{}, err
	}
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return MergeResult{}, err
	}

	out := MergeResult{Project: project, Crew: crew, Branch: meta[MetaBranch]}

	// 1. The crew itself.
	if len(meta) == 0 {
		return MergeResult{}, mergeRefusal(observability.CodeNotFound,
			fmt.Sprintf("no crew %s is recorded for project %s; %s is empty", crew, project, w.CrewMeta(project, crew)))
	}
	if state := crewstate.State(meta[MetaState]); state.Closed() {
		return MergeResult{}, mergeRefusal(observability.CodeStateConflict,
			fmt.Sprintf("crew %s/%s is closed (state=%s); its branch and worktree were removed when it closed", project, crew, state))
	}
	if out.Branch == "" {
		return MergeResult{}, mergeRefusal(observability.CodeStateConflict,
			fmt.Sprintf("crew %s/%s records no branch; there is nothing to merge", project, crew))
	}
	repoCfg, err := cfg.CrewRepo(meta)
	if err != nil {
		return MergeResult{}, mergeRefusal(observability.CodeStateConflict,
			fmt.Sprintf("crew %s/%s (meta %s): %v", project, crew, w.CrewMeta(project, crew), err))
	}
	out.DefaultBranch = repoCfg.DefaultBranch

	// 2 & 3. Who is typing. A crew is refused outright; a Mate is refused
	// while `yolo` is off, in the wording the manual quotes back.
	switch NormaliseCaller(caller) {
	case CallerCrew:
		return MergeResult{}, mergeRefusal(observability.CodePermission,
			"a crew cannot merge its own branch; the captain merges, or the Mate does when yolo is on")
	case CallerMate:
		if !cfg.Yolo {
			return MergeResult{}, mergeRefusal(observability.CodePermission,
				fmt.Sprintf("yolo is off for %s; the captain merges", project))
		}
	}

	repo := w.RepoDir(repoCfg.Path)
	git := deps.git()

	// 4. The crew's worktree. Uncommitted work is not on the branch, so a
	// merge would land something other than what was reviewed.
	if rel := meta[MetaWorktree]; rel != "" {
		worktree := filepath.Join(w.Root(), filepath.FromSlash(rel))
		if _, statErr := os.Stat(worktree); statErr == nil {
			dirty, err := git.IsDirty(ctx, worktree)
			if err != nil {
				return MergeResult{}, err
			}
			if dirty > 0 {
				return MergeResult{}, mergeRefusal(observability.CodeStateConflict,
					fmt.Sprintf("crew %s/%s has %d uncommitted file(s) in %s; have the crew commit or drop them, then merge",
						project, crew, dirty, worktree))
			}
		}
	}

	exists, err := git.BranchExists(ctx, repo, out.Branch)
	if err != nil {
		return MergeResult{}, err
	}
	if !exists {
		return MergeResult{}, mergeRefusal(observability.CodeNotFound,
			fmt.Sprintf("branch %s does not exist in %s", out.Branch, repo))
	}

	// 5. Something to merge at all.
	ahead, err := git.AheadCount(ctx, repo, out.Branch, repoCfg.DefaultBranch)
	if err != nil {
		return MergeResult{}, err
	}
	if ahead == 0 {
		return MergeResult{}, mergeRefusal(observability.CodeStateConflict,
			fmt.Sprintf("branch %s is not ahead of %s: nothing to merge", out.Branch, repoCfg.DefaultBranch))
	}
	out.Commits = ahead

	// 6. Fast-forwardable. `needs-rebase` is a command result, not a state:
	// the crew is untouched and stays exactly where it was.
	ff, err := git.IsAncestor(ctx, repo, repoCfg.DefaultBranch, out.Branch)
	if err != nil {
		return MergeResult{}, err
	}
	if !ff {
		return MergeResult{}, mergeRefusal(observability.CodeStateConflict,
			fmt.Sprintf("needs-rebase: %s has moved on since %s branched, so %s cannot fast-forward; send the crew one line telling it to rebase onto %s and hand back with wait-mate",
				repoCfg.DefaultBranch, out.Branch, out.Branch, repoCfg.DefaultBranch))
	}

	// 7. The primary repository: the branch it has checked out is the one
	// --ff-only would move, and a dirty checkout makes git refuse halfway.
	head, err := git.CurrentBranch(ctx, repo)
	if err != nil {
		return MergeResult{}, err
	}
	if head != repoCfg.DefaultBranch {
		where := head
		if head == "" || head == "HEAD" {
			where = "a detached HEAD"
		}
		return MergeResult{}, mergeRefusal(observability.CodeStateConflict,
			fmt.Sprintf("%s has %s checked out, not %s; check out %s in the primary repo and merge again",
				repo, where, repoCfg.DefaultBranch, repoCfg.DefaultBranch))
	}
	repoDirty, err := git.IsDirty(ctx, repo)
	if err != nil {
		return MergeResult{}, err
	}
	if repoDirty > 0 {
		return MergeResult{}, mergeRefusal(observability.CodeStateConflict,
			fmt.Sprintf("%s has %d uncommitted file(s); a fast-forward needs a clean primary repo", repo, repoDirty))
	}

	// The merge.
	before, err := git.HeadCommit(ctx, repo, repoCfg.DefaultBranch)
	if err != nil {
		return MergeResult{}, err
	}
	out.Before = before
	mergeTarget := out.Branch
	if len(reviewed) > 0 {
		tip, err := git.HeadCommit(ctx, repo, out.Branch)
		if err != nil {
			return MergeResult{}, err
		}
		if reviewed[0].Head == "" || tip != reviewed[0].Head || before != reviewed[0].Base {
			return MergeResult{}, mergeRefusal(observability.CodeStateConflict, "review is stale; branch or base changed")
		}
		// Merge the object reviewed, even if the branch advances during git.
		mergeTarget = reviewed[0].Head
	}
	if err := git.MergeFFOnly(ctx, repo, mergeTarget); err != nil {
		return MergeResult{}, err
	}
	after, err := git.HeadCommit(ctx, repo, repoCfg.DefaultBranch)
	if err != nil {
		return MergeResult{}, err
	}
	out.After, out.Merged = after, true

	// The teardown. discard=false on purpose: StopCrew re-derives the git
	// facts itself and must find the branch landed, which is the proof the
	// merge did what this function says it did.
	stop, err := StopCrew(ctx, w, deps, project, crew, false)
	out.Stop = stop
	if err != nil {
		return out, observability.WrapError(observability.CodeStateConflict,
			fmt.Sprintf("%s/%s: merged %d commit(s) into %s (%s..%s), but the crew was not torn down and is still open; rerun `mate crew stop %s %s` - the merge is done and must not be repeated",
				project, crew, out.Commits, repoCfg.DefaultBranch, shortCommit(out.Before), shortCommit(out.After), project, crew), err)
	}
	return out, nil
}
