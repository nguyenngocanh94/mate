package spawn_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// orphanWorktree reproduces a workspace copied to another machine: the
// worktree directory is still there, but its `.git` file names a gitdir on
// the old machine and the repository no longer registers it. git refuses
// every command inside it ("not a git repository").
func orphanWorktree(t *testing.T, repo, worktree string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(worktree, ".git"),
		[]byte("gitdir: /Users/old-machine/ws/shop/.git/worktrees/shop-k3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repo, ".git", "worktrees")); err != nil {
		t.Fatal(err)
	}
}

func spawnOrphanCrew(t *testing.T) (deps spawn.Deps, worktree, repo string, stop func(discard bool) (spawn.StopResult, error)) {
	t.Helper()
	w := crewWorkspace(t, "shop")
	deps = fakeDeps(t, runtime.NewFake())
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	repo = w.RepoDir("shop")
	return deps, res.Worktree, repo, func(discard bool) (spawn.StopResult, error) {
		return spawn.StopCrew(context.Background(), w, deps, "shop", "k3", discard)
	}
}

// A worktree git no longer knows is still closed by a plain stop when its
// files are exactly a commit git already holds: nothing in it would be lost.
func TestStopCrewClosesAnOrphanedWorktreeWhoseFilesGitAlreadyHas(t *testing.T) {
	_, worktree, repo, stop := spawnOrphanCrew(t)
	// The crew sat on a commit that is already in main, then the machine moved.
	commitInWorktree(t, worktree, "review.txt", "reviewed\n")
	git(t, repo, "merge", "--ff-only", "mate/k3")
	orphanWorktree(t, repo, worktree)

	stopped, err := stop(false)
	if err != nil {
		t.Fatalf("StopCrew on an orphaned, landed worktree: %v", err)
	}
	if stopped.Unlanded || stopped.Teardown != spawn.TeardownClean || stopped.State != spawn.CrewStateFinished {
		t.Fatalf("stop = %+v, want a clean, finished teardown", stopped)
	}
	if !stopped.WorktreeRemoved || !stopped.BranchRemoved {
		t.Fatalf("stop = %+v, want worktree and branch removed", stopped)
	}
	if _, statErr := os.Stat(worktree); !os.IsNotExist(statErr) {
		t.Fatalf("the orphaned worktree directory survived the stop: %v", statErr)
	}
}

// Files git does not hold anywhere are unlanded work, orphan or not: a plain
// stop refuses and leaves the directory alone, --discard removes it.
func TestStopCrewRefusesAnOrphanedWorktreeWithFilesGitDoesNotHave(t *testing.T) {
	_, worktree, repo, stop := spawnOrphanCrew(t)
	if err := os.WriteFile(filepath.Join(worktree, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orphanWorktree(t, repo, worktree)

	stopped, err := stop(false)
	if !errors.Is(err, spawn.ErrUnlandedWork) {
		t.Fatalf("err = %v, want ErrUnlandedWork", err)
	}
	if !strings.Contains(err.Error(), "no longer registered") {
		t.Fatalf("the refusal must say git no longer knows the worktree: %v", err)
	}
	if !stopped.Unlanded || stopped.DirtyFiles != 1 {
		t.Fatalf("stop = %+v, want unlanded with one differing file", stopped)
	}
	if _, statErr := os.Stat(filepath.Join(worktree, "scratch.txt")); statErr != nil {
		t.Fatalf("a refused stop must keep the orphaned worktree: %v", statErr)
	}

	discarded, err := stop(true)
	if err != nil {
		t.Fatalf("StopCrew --discard on an orphaned worktree: %v", err)
	}
	if discarded.Teardown != spawn.TeardownDiscarded || !discarded.WorktreeRemoved {
		t.Fatalf("stop = %+v, want the orphan discarded", discarded)
	}
	if _, statErr := os.Stat(worktree); !os.IsNotExist(statErr) {
		t.Fatal("--discard must remove the orphaned worktree directory")
	}
	if exists, err := gitx.New().BranchExists(context.Background(), repo, "mate/k3"); err != nil || exists {
		t.Fatalf("--discard must delete the branch: %v, %v", exists, err)
	}
}
