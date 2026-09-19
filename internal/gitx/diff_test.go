package gitx_test

// The review pair of mvp.md task 21: LogOneline lists what a crew branch
// carries, Diff shows it. Both run the real git against temp repositories
// here, because what the `matev2 diff` command depends on is what git
// actually does with the ranges - the argv-level test at the bottom is the
// fake-runner half, and it exists to pin the two dot counts apart.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/gitx"
)

func TestLogOnelineListsTheBranchsOwnCommitsNewestFirst(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "shop-k3")
	if err := g.AddWorktree(ctx, repo, wt, "matev2/k3", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("shop\nfirst\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, wt, "commit", "-am", "add first line")
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("shop\nfirst\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, wt, "commit", "-am", "add second line")

	out, err := g.LogOneline(ctx, wt, "main", "matev2/k3")
	if err != nil {
		t.Fatalf("LogOneline: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("LogOneline listed %d commits, want the branch's own two:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "add second line") || !strings.Contains(lines[1], "add first line") {
		t.Fatalf("LogOneline is not newest-first:\n%s", out)
	}
	// The base's own commit is somebody else's work and must not be listed.
	if strings.Contains(out, "init") {
		t.Fatalf("LogOneline listed a commit base already has:\n%s", out)
	}
}

func TestLogOnelineAndDiffAreEmptyForABranchWithNoCommits(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "shop-k3")
	if err := g.AddWorktree(ctx, repo, wt, "matev2/k3", "main"); err != nil {
		t.Fatal(err)
	}

	log, err := g.LogOneline(ctx, wt, "main", "matev2/k3")
	if err != nil {
		t.Fatalf("LogOneline: %v", err)
	}
	if strings.TrimSpace(log) != "" {
		t.Fatalf("LogOneline on a branch with no commits = %q, want empty", log)
	}
	diff, err := g.Diff(ctx, wt, "main", "matev2/k3", false)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if strings.TrimSpace(diff) != "" {
		t.Fatalf("Diff on a branch with no commits = %q, want empty", diff)
	}
}

// TestDiffIgnoresCommitsTheBaseGainedAfterTheBranchStarted is the whole
// reason Diff spells three dots. The crew branched from main, main moved on
// underneath it, and the crew's diff must still be only the crew's work:
// with two dots, main's own new file would show up in it as a deletion.
func TestDiffIgnoresCommitsTheBaseGainedAfterTheBranchStarted(t *testing.T) {
	repo := newRepo(t)
	g := gitx.New()
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "shop-k3")
	if err := g.AddWorktree(ctx, repo, wt, "matev2/k3", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "crew.txt"), []byte("crew work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, wt, "add", "crew.txt")
	run(t, wt, "commit", "-m", "crew adds a file")

	// main moves on, in the primary checkout, after the branch was cut.
	if err := os.WriteFile(filepath.Join(repo, "captain.txt"), []byte("captain work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", "captain.txt")
	run(t, repo, "commit", "-m", "captain adds a file")

	diff, err := g.Diff(ctx, wt, "main", "matev2/k3", false)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "crew.txt") || !strings.Contains(diff, "+crew work") {
		t.Fatalf("Diff does not carry the crew's own change:\n%s", diff)
	}
	if strings.Contains(diff, "captain.txt") {
		t.Fatalf("Diff reported base's own commit as the crew's change (two-dot range):\n%s", diff)
	}

	stat, err := g.Diff(ctx, wt, "main", "matev2/k3", true)
	if err != nil {
		t.Fatalf("Diff --stat: %v", err)
	}
	if !strings.Contains(stat, "crew.txt") || !strings.Contains(stat, "1 file changed") {
		t.Fatalf("Diff --stat is not a summary of the crew's change:\n%s", stat)
	}
	if strings.Contains(stat, "diff --git") {
		t.Fatalf("Diff --stat printed the patch:\n%s", stat)
	}
}

// TestDiffAndLogArgvAreExactlyWhatGitIsGiven pins the two ranges apart at
// the argv level, where the dots live: a change to either one fails here
// rather than in a diff nobody reads twice.
func TestDiffAndLogArgvAreExactlyWhatGitIsGiven(t *testing.T) {
	f := &fakeRunner{}
	g := gitx.Git{Runner: f}
	ctx := context.Background()

	if _, err := g.LogOneline(ctx, "/w/.worktrees/shop-k3", "main", "matev2/k3"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Diff(ctx, "/w/.worktrees/shop-k3", "main", "matev2/k3", false); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Diff(ctx, "/w/.worktrees/shop-k3", "main", "matev2/k3", true); err != nil {
		t.Fatal(err)
	}

	want := [][]string{
		{"log", "--oneline", "main..matev2/k3"},
		{"diff", "main...matev2/k3"},
		{"diff", "--stat", "main...matev2/k3"},
	}
	if len(f.calls) != len(want) {
		t.Fatalf("calls = %d, want %d", len(f.calls), len(want))
	}
	for i, w := range want {
		if strings.Join(f.calls[i].Args, "\x00") != strings.Join(w, "\x00") {
			t.Errorf("call %d argv = %q, want %q", i, f.calls[i].Args, w)
		}
		if f.calls[i].Dir != "/w/.worktrees/shop-k3" {
			t.Errorf("call %d dir = %q", i, f.calls[i].Dir)
		}
	}
}
