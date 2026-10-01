package spawn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nguyenngocanh94/mate/internal/gitx"
)

// WorktreeLease names one crew's working copy: the primary checkout of the
// repo the crew works in, the directory it works from, the new branch that
// directory is on, and the branch it starts from and merges back into.
type WorktreeLease struct {
	Repo   string
	Path   string
	Branch string
	Base   string
}

// ReleaseParts says which half of a lease a Release gives back. They are
// separate because a stop reports them separately (a removed worktree with
// a surviving branch is a real outcome) and a compensation keeps going
// after either fails.
type ReleaseParts struct {
	Worktree bool
	Branch   bool
}

// Worktrees hands crews their working copies and takes them back
// (docs/mvp.md M9). It is the one seam between the crew saga and how a
// working copy is made, so a later backend replaces plain git worktrees
// without touching spawn, stop or merge.
type Worktrees interface {
	// Acquire makes lease.Path a working copy of lease.Repo on the new
	// branch lease.Branch, started from lease.Base. A refusal creates
	// nothing. Once it returns nil the copy is isolated: its own top level,
	// never the primary checkout. A failure after the copy existed gives it
	// back before returning.
	Acquire(ctx context.Context, lease WorktreeLease) error
	// Release gives back the parts of a lease that parts names, worktree
	// first: a branch still checked out somewhere cannot be deleted.
	Release(ctx context.Context, lease WorktreeLease, parts ReleaseParts) error
}

// GitWorktrees is the default backend: linked git worktrees under
// `.worktrees/`, one branch each.
type GitWorktrees struct {
	Git gitx.Git
}

// Acquire checks every precondition first, then runs `git worktree add`,
// then the tangle guard.
func (g GitWorktrees) Acquire(ctx context.Context, lease WorktreeLease) error {
	if err := checkWorktreePreconditions(ctx, g.Git, lease.Repo, lease.Path, lease.Branch, lease.Base); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(lease.Path), 0o755); err != nil {
		return err
	}
	if err := g.Git.AddWorktree(ctx, lease.Repo, lease.Path, lease.Branch, lease.Base); err != nil {
		return err
	}
	// The guard runs before a single byte is written into the worktree: a
	// `worktree add` that silently landed in the primary repo would let a
	// crew commit on the user's own branch.
	if err := assertIsolatedWorktree(ctx, g.Git, lease.Repo, lease.Path); err != nil {
		g.discard(ctx, lease)
		return err
	}
	return nil
}

// Release removes the worktree (`worktree remove --force` and a prune)
// and deletes the branch, as parts asks. The first failure is returned.
//
// A directory git no longer treats as a worktree (its `.git` link names a
// gitdir that is gone, as after the workspace moved machines) cannot be
// removed by `git worktree remove`; it is deleted as a plain directory and
// the repo's stale entries are pruned. Callers decide beforehand whether
// its files may go - StopCrew compares them with history first.
func (g GitWorktrees) Release(ctx context.Context, lease WorktreeLease, parts ReleaseParts) error {
	if parts.Worktree {
		if err := g.releaseWorktree(ctx, lease); err != nil {
			return err
		}
	}
	if parts.Branch {
		if err := g.Git.DeleteBranch(ctx, lease.Repo, lease.Branch); err != nil {
			return err
		}
	}
	return nil
}

func (g GitWorktrees) releaseWorktree(ctx context.Context, lease WorktreeLease) error {
	attached, err := g.Git.WorktreeAttached(ctx, lease.Path)
	if err != nil {
		return err
	}
	if attached {
		return g.Git.RemoveWorktree(ctx, lease.Repo, lease.Path)
	}
	if lease.Path == "" || gitx.SamePath(lease.Path, lease.Repo) {
		return fmt.Errorf("spawn: refusing to delete %q as a detached worktree of %q", lease.Path, lease.Repo)
	}
	if err := os.RemoveAll(lease.Path); err != nil {
		return err
	}
	return g.Git.PruneWorktrees(ctx, lease.Repo)
}

// discard is the best-effort undo of an Acquire, used when a guard fails
// after the worktree exists. Its errors are swallowed: the caller must see
// why the acquire was refused.
func (g GitWorktrees) discard(ctx context.Context, lease WorktreeLease) {
	_ = g.Git.RemoveWorktree(ctx, lease.Repo, lease.Path)
	_ = os.RemoveAll(lease.Path)
	_ = g.Git.DeleteBranch(ctx, lease.Repo, lease.Branch)
}
