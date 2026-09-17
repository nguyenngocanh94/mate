package gitx_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/gitx"
)

// The tests in this file run the real git against temporary repositories.
// A fake git would only prove that gitx builds the argv it was written to
// build; what the crew saga depends on is what git actually does with it.

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "init", "-b", "main")
	run(t, repo, "config", "user.email", "gitx-test@example.com")
	run(t, repo, "config", "user.name", "gitx test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", "README.md")
	run(t, repo, "commit", "-m", "init")
	return repo
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func TestAddWorktreeCreatesALinkedWorktreeOnANewBranch(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "shop-k3")

	if err := g.AddWorktree(ctx, repo, wt, "matev2/k3", "main"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	// The worktree is its own top level, and it is not the primary checkout:
	// this pair is the tangle guard the crew saga makes after every add.
	top, err := g.Toplevel(ctx, wt)
	if err != nil {
		t.Fatalf("Toplevel: %v", err)
	}
	if !gitx.SamePath(top, wt) {
		t.Fatalf("worktree top level = %q, want %q", top, wt)
	}
	primary, err := g.Toplevel(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if gitx.SamePath(top, primary) {
		t.Fatalf("worktree top level %q is the primary checkout %q", top, primary)
	}
	if got := strings.TrimSpace(run(t, wt, "rev-parse", "--abbrev-ref", "HEAD")); got != "matev2/k3" {
		t.Fatalf("worktree branch = %q, want matev2/k3", got)
	}
	exists, err := g.BranchExists(ctx, repo, "matev2/k3")
	if err != nil || !exists {
		t.Fatalf("BranchExists(matev2/k3) = %v, %v; want true", exists, err)
	}
}

func TestAddWorktreeRefusesABranchThatExists(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()
	run(t, repo, "branch", "matev2/k3")

	err := g.AddWorktree(ctx, repo, filepath.Join(t.TempDir(), "shop-k3"), "matev2/k3", "main")
	if err == nil {
		t.Fatal("git worktree add -b must fail on an existing branch")
	}
	if !strings.Contains(err.Error(), "worktree add") {
		t.Fatalf("error = %v, want it to name the failed command", err)
	}
}

func TestBranchAndRevisionProbesAnswerWithoutFailing(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()

	for _, tc := range []struct {
		branch string
		want   bool
	}{{"main", true}, {"matev2/nope", false}} {
		got, err := g.BranchExists(ctx, repo, tc.branch)
		if err != nil {
			t.Fatalf("BranchExists(%s): %v", tc.branch, err)
		}
		if got != tc.want {
			t.Errorf("BranchExists(%s) = %v, want %v", tc.branch, got, tc.want)
		}
	}
	ok, err := g.RevisionExists(ctx, repo, "main")
	if err != nil || !ok {
		t.Fatalf("RevisionExists(main) = %v, %v; want true", ok, err)
	}
	ok, err = g.RevisionExists(ctx, repo, "no-such-branch")
	if err != nil {
		t.Fatalf("RevisionExists(no-such-branch): %v", err)
	}
	if ok {
		t.Fatal("RevisionExists(no-such-branch) = true")
	}
}

func TestRevisionExistsIsFalseInAnEmptyRepository(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-b", "main")
	g := gitx.New()
	ok, err := g.RevisionExists(context.Background(), dir, "main")
	if err != nil {
		t.Fatalf("RevisionExists: %v", err)
	}
	if ok {
		t.Fatal("an unborn branch must not report a commit")
	}
}

func TestRemoveWorktreeAndDeleteBranchUndoAnAdd(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "shop-k3")
	if err := g.AddWorktree(ctx, repo, wt, "matev2/k3", "main"); err != nil {
		t.Fatal(err)
	}
	// A crew that has already written something must not stop the removal:
	// compensation is force.
	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := g.RemoveWorktree(ctx, repo, wt); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree directory survived removal: %v", err)
	}
	if err := g.DeleteBranch(ctx, repo, "matev2/k3"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	exists, err := g.BranchExists(ctx, repo, "matev2/k3")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("branch matev2/k3 survived the delete")
	}
	if listed := run(t, repo, "worktree", "list"); strings.Contains(listed, "shop-k3") {
		t.Fatalf("worktree list still names the removed worktree:\n%s", listed)
	}
}

func TestHeadCommitReadsTheWorktreeBranch(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "shop-k3")
	if err := g.AddWorktree(ctx, repo, wt, "matev2/k3", "main"); err != nil {
		t.Fatal(err)
	}
	before, err := g.HeadCommit(ctx, repo, "matev2/k3")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("shop\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, wt, "commit", "-am", "hello")
	after, err := g.HeadCommit(ctx, repo, "matev2/k3")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("the commit in the worktree did not move the branch")
	}
}

// fakeRunner is the seam the crew saga's compensation tests use: git is not
// run at all, the scripted answer is.
type fakeRunner struct {
	calls   []gitx.Command
	results map[string]gitx.Result
	errs    map[string]error
}

func (f *fakeRunner) Run(_ context.Context, cmd gitx.Command) (gitx.Result, error) {
	f.calls = append(f.calls, cmd)
	key := strings.Join(cmd.Args, " ")
	if err, ok := f.errs[key]; ok {
		return gitx.Result{}, err
	}
	if res, ok := f.results[key]; ok {
		return res, nil
	}
	return gitx.Result{}, nil
}

func TestRunnerIsAPortAndNoShellIsInvolved(t *testing.T) {
	f := &fakeRunner{
		results: map[string]gitx.Result{
			"rev-parse --show-toplevel": {Stdout: "/tmp/ws/.worktrees/shop-k3\n"},
		},
		errs: map[string]error{
			"branch -D matev2/k3": errors.New("git is gone"),
		},
	}
	g := gitx.Git{Runner: f}
	ctx := context.Background()

	top, err := g.Toplevel(ctx, "/tmp/ws/.worktrees/shop-k3")
	if err != nil {
		t.Fatal(err)
	}
	if top != "/tmp/ws/.worktrees/shop-k3" {
		t.Fatalf("Toplevel = %q", top)
	}
	if err := g.AddWorktree(ctx, "/tmp/ws/shop", "/tmp/ws/.worktrees/shop-k3", "matev2/k3", "main"); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteBranch(ctx, "/tmp/ws/shop", "matev2/k3"); err == nil {
		t.Fatal("a runner error must reach the caller")
	}

	// Every argument is its own argv element: nothing is ever concatenated
	// into a string a shell would re-split.
	add := f.calls[1]
	want := []string{"worktree", "add", "-b", "matev2/k3", "/tmp/ws/.worktrees/shop-k3", "main"}
	if strings.Join(add.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("add argv = %q, want %q", add.Args, want)
	}
	if add.Dir != "/tmp/ws/shop" {
		t.Fatalf("add dir = %q", add.Dir)
	}
}

func TestExecRunnerReportsANonZeroExitAsAResult(t *testing.T) {
	repo := newRepo(t)
	res, err := gitx.ExecRunner{}.Run(context.Background(), gitx.Command{
		Dir:  repo,
		Args: []string{"rev-parse", "--verify", "--quiet", "refs/heads/nope"},
	})
	if err != nil {
		t.Fatalf("a non-zero exit must not be an error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("exit code = 0, want non-zero for a missing ref")
	}
}
