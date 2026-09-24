package gitx_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/gitx"
)

// The merge half of the package (docs/mvp.md task 22). Like the rest of this
// file's neighbours these run the real git: what `mate merge` depends on is
// that `--ff-only` really refuses a diverged branch, which no fake can show.
// The one fake-runner test at the bottom pins the argv and the order of the
// two commands, which is the part a real repository cannot observe.

// branchWithCommit adds a linked worktree on branch, commits one file in it,
// and returns the worktree path.
func branchWithCommit(t *testing.T, repo, branch, file, text string) string {
	t.Helper()
	g := gitx.New()
	wt := filepath.Join(t.TempDir(), strings.ReplaceAll(branch, "/", "-"))
	if err := g.AddWorktree(context.Background(), repo, wt, branch, "main"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	writeFile(t, filepath.Join(wt, file), text)
	run(t, wt, "add", file)
	run(t, wt, "commit", "-m", "work on "+branch)
	return wt
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentBranchNamesTheCheckedOutBranchAndSaysHEADWhenDetached(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()

	got, err := g.CurrentBranch(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != "main" {
		t.Fatalf("CurrentBranch = %q, want main", got)
	}

	// A detached checkout is the shape the merge guard has to recognise:
	// git names no branch, and moving "the checked-out branch" forward
	// would move nothing a reader could find again.
	run(t, repo, "checkout", "--detach", "HEAD")
	got, err = g.CurrentBranch(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != gitx.DetachedHead {
		t.Fatalf("CurrentBranch on a detached HEAD = %q, want %q", got, gitx.DetachedHead)
	}
}

func TestMergeFFOnlyAdvancesTheCheckedOutBranchToTheCrewBranch(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()
	branchWithCommit(t, repo, "mate/k3", "feature.txt", "feature\n")

	before, err := g.HeadCommit(ctx, repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	tip, err := g.HeadCommit(ctx, repo, "mate/k3")
	if err != nil {
		t.Fatal(err)
	}
	if before == tip {
		t.Fatal("the branch must be ahead before the merge, or this test proves nothing")
	}

	if err := g.MergeFFOnly(ctx, repo, "mate/k3"); err != nil {
		t.Fatalf("MergeFFOnly: %v", err)
	}

	after, err := g.HeadCommit(ctx, repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if after != tip {
		t.Fatalf("main is at %s after the merge, want the branch tip %s", after, tip)
	}
	// A fast-forward creates no commit: main's new tip IS the branch tip,
	// which is what lets `crew stop` afterwards find the branch landed.
	if n := strings.TrimSpace(run(t, repo, "rev-list", "--count", "--merges", before+"..main")); n != "0" {
		t.Fatalf("a --ff-only merge created %s merge commit(s)", n)
	}
}

func TestMergeFFOnlyRefusesABranchThatIsNotADescendant(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()
	branchWithCommit(t, repo, "mate/k3", "feature.txt", "feature\n")

	// main moves on: the crew branch is no longer a fast-forward. This is
	// the `needs-rebase` situation, and git must refuse it rather than
	// creating a merge commit.
	writeFile(t, filepath.Join(repo, "other.txt"), "captain\n")
	run(t, repo, "add", "other.txt")
	run(t, repo, "commit", "-m", "captain's own commit")
	head, err := g.HeadCommit(ctx, repo, "main")
	if err != nil {
		t.Fatal(err)
	}

	if err := g.MergeFFOnly(ctx, repo, "mate/k3"); err == nil {
		t.Fatal("MergeFFOnly must refuse a diverged branch")
	}
	after, err := g.HeadCommit(ctx, repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if after != head {
		t.Fatalf("a refused merge moved main from %s to %s", head, after)
	}
}

func TestMergeFFOnlyRefusesADetachedHeadAndTheBranchItself(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()
	branchWithCommit(t, repo, "mate/k3", "feature.txt", "feature\n")

	run(t, repo, "checkout", "--detach", "HEAD")
	err := g.MergeFFOnly(ctx, repo, "mate/k3")
	if err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("MergeFFOnly on a detached HEAD = %v, want a refusal naming the detached HEAD", err)
	}

	run(t, repo, "checkout", "main")
	// The primary repo cannot check out mate/k3 (the linked worktree
	// holds it), so the same-branch guard is exercised by asking to merge
	// main into main.
	err = g.MergeFFOnly(ctx, repo, "main")
	if err == nil || !strings.Contains(err.Error(), "into itself") {
		t.Fatalf("MergeFFOnly of the checked-out branch = %v, want a refusal", err)
	}
}

func TestMergeFFOnlyReadsHEADBeforeItRunsAnyMerge(t *testing.T) {
	// The fake runner is where the order is observable: a merge that ran
	// before the HEAD check would still pass every real-git test above,
	// because git would refuse it for its own reasons - but the message a
	// caller got would be git's, not this package's.
	f := &fakeRunner{
		results: map[string]gitx.Result{
			"rev-parse --abbrev-ref HEAD": {Stdout: "main\n"},
		},
	}
	g := gitx.Git{Runner: f}

	if err := g.MergeFFOnly(context.Background(), "/ws/shop", "mate/k3"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("MergeFFOnly issued %d git commands, want 2", len(f.calls))
	}
	want := [][]string{
		{"rev-parse", "--abbrev-ref", "HEAD"},
		{"merge", "--ff-only", "mate/k3"},
	}
	for i, w := range want {
		if strings.Join(f.calls[i].Args, "\x00") != strings.Join(w, "\x00") {
			t.Fatalf("command %d argv = %q, want %q", i, f.calls[i].Args, w)
		}
		if f.calls[i].Dir != "/ws/shop" {
			t.Fatalf("command %d ran in %q, want the primary repo", i, f.calls[i].Dir)
		}
	}
}

func TestMergeFFOnlyDoesNotRunMergeWhenHEADIsDetached(t *testing.T) {
	f := &fakeRunner{
		results: map[string]gitx.Result{
			"rev-parse --abbrev-ref HEAD": {Stdout: "HEAD\n"},
		},
	}
	g := gitx.Git{Runner: f}

	if err := g.MergeFFOnly(context.Background(), "/ws/shop", "mate/k3"); err == nil {
		t.Fatal("a detached HEAD must be refused")
	}
	if len(f.calls) != 1 {
		t.Fatalf("a refused merge issued %d git commands, want only the HEAD read", len(f.calls))
	}
}
