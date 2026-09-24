package main

// `mate diff <project> <crew>` over real git repositories (mvp.md task
// 21). Every case here is a state a crew is genuinely found in - ahead,
// untouched, dirty, torn down with its branch kept, torn down with its
// branch gone - and the assertions are on the text a reader sees, because
// that text is also what the Console's overlay shows.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// diffFixture is a workspace with project "shop" on branch main and a crew
// "k3" in a real linked worktree on branch mate/k3, recorded in
// crews/k3.meta exactly the way spawn records it.
type diffFixture struct {
	w        *store.Workspace
	repo     string
	worktree string
}

func newDiffFixture(t *testing.T) diffFixture {
	t.Helper()
	w := liveCrewWorkspace(t, "shop")
	repo := filepath.Join(w.Root(), "shop")
	// A file to change, so a diff has something to show that is not the
	// empty initial commit.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, repo, "add", "README.md")
	runGitOrFatal(t, repo, "commit", "-m", "add README")

	worktree := w.WorktreeDir("shop", "k3")
	runGitOrFatal(t, repo, "worktree", "add", "-b", "mate/k3", worktree, "main")
	writeCrewMetaForDiff(t, w, worktree)
	return diffFixture{w: w, repo: repo, worktree: worktree}
}

func writeCrewMetaForDiff(t *testing.T, w *store.Workspace, worktree string) {
	t.Helper()
	rel, err := filepath.Rel(w.Root(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{
		spawn.MetaTask:     "add a line",
		spawn.MetaHarness:  "codex",
		spawn.MetaBranch:   "mate/k3",
		spawn.MetaWorktree: filepath.ToSlash(rel),
		spawn.MetaState:    spawn.CrewStateSpawned,
	}
	if err := w.WriteCrewMeta("shop", "k3", meta); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
}

// commitInWorktree makes one commit on the crew's branch.
func commitInWorktree(t *testing.T, worktree, name, body, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(worktree, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, worktree, "add", name)
	runGitOrFatal(t, worktree, "commit", "-m", subject)
}

func diffText(t *testing.T, f diffFixture, stat bool) string {
	t.Helper()
	text, err := crewDiffText(context.Background(), f.w, gitx.New(), "shop", "k3", stat)
	if err != nil {
		t.Fatalf("crewDiffText: %v", err)
	}
	return text
}

// TestDiffPrintsTheCommitListThenThePatch is the ordinary case: a branch two
// commits ahead. The order is the assertion - a reader scanning a review
// wants the subjects before the hunks, and the command promises that order
// rather than whatever git happened to print first.
func TestDiffPrintsTheCommitListThenThePatch(t *testing.T) {
	f := newDiffFixture(t)
	commitInWorktree(t, f.worktree, "README.md", "shop\nfirst\n", "add first line")
	commitInWorktree(t, f.worktree, "README.md", "shop\nfirst\nsecond\n", "add second line")

	text := diffText(t, f, false)
	for _, want := range []string{"add first line", "add second line", "diff --git", "+first", "+second"} {
		if !strings.Contains(text, want) {
			t.Fatalf("diff output does not carry %q:\n%s", want, text)
		}
	}
	if i, j := strings.Index(text, "add second line"), strings.Index(text, "diff --git"); i > j {
		t.Fatalf("the commit list must come before the patch:\n%s", text)
	}
	if strings.Contains(text, "uncommitted") {
		t.Fatalf("a clean worktree must not report uncommitted files:\n%s", text)
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatalf("output must end in a newline:\n%q", text)
	}
}

// TestDiffStatPrintsTheSummaryInsteadOfThePatch: --stat changes the second
// section and nothing else, so the commit list is still there.
func TestDiffStatPrintsTheSummaryInsteadOfThePatch(t *testing.T) {
	f := newDiffFixture(t)
	commitInWorktree(t, f.worktree, "README.md", "shop\nfirst\n", "add first line")

	text := diffText(t, f, true)
	if !strings.Contains(text, "add first line") {
		t.Fatalf("--stat dropped the commit list:\n%s", text)
	}
	if !strings.Contains(text, "1 file changed") {
		t.Fatalf("--stat did not print git's summary:\n%s", text)
	}
	if strings.Contains(text, "diff --git") {
		t.Fatalf("--stat printed the patch:\n%s", text)
	}
}

// TestDiffOnABranchWithNoCommitsSaysSo is the scout crew's normal state, and
// the line it gets must name both branches: "nothing" without saying
// nothing-compared-to-what is the kind of answer a reader re-runs the
// command to understand.
func TestDiffOnABranchWithNoCommitsSaysSo(t *testing.T) {
	f := newDiffFixture(t)

	text := diffText(t, f, false)
	if text != "no commits on mate/k3 beyond main\n" {
		t.Fatalf("diff output = %q, want the no-commits line", text)
	}
}

// TestDiffReportsUncommittedFilesInOneLeadingLine: the patch is the
// committed work only, so a dirty worktree changes how everything under the
// line must be read and the line therefore leads. It is never hidden - that
// is the whole rule of this case.
func TestDiffReportsUncommittedFilesInOneLeadingLine(t *testing.T) {
	f := newDiffFixture(t)
	commitInWorktree(t, f.worktree, "README.md", "shop\nfirst\n", "add first line")
	if err := os.WriteFile(filepath.Join(f.worktree, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.worktree, "README.md"), []byte("shop\nfirst\nnot committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	text := diffText(t, f, false)
	first, _, _ := strings.Cut(text, "\n")
	if first != "2 uncommitted file(s) not shown" {
		t.Fatalf("first line = %q, want the uncommitted count:\n%s", first, text)
	}
	if strings.Contains(text, "not committed") {
		t.Fatalf("the patch must be the committed work only:\n%s", text)
	}
}

// TestDiffOnACleanBranchWithNoCommitsStillReportsDirt pins the two signals
// apart: a crew can have written plenty and committed none of it, and the
// answer has to say both things at once rather than only "no commits".
func TestDiffOnACleanBranchWithNoCommitsStillReportsDirt(t *testing.T) {
	f := newDiffFixture(t)
	if err := os.WriteFile(filepath.Join(f.worktree, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	text := diffText(t, f, false)
	if text != "1 uncommitted file(s) not shown\nno commits on mate/k3 beyond main\n" {
		t.Fatalf("diff output = %q, want both lines", text)
	}
}

// TestDiffReadsATornDownCrewsBranchFromThePrimaryRepo: `crew stop` removes
// the worktree and keeps a branch that still carries work, so this is the
// state a crew waiting to be merged is actually in. The answer must come
// from the primary checkout rather than from a directory that is gone.
func TestDiffReadsATornDownCrewsBranchFromThePrimaryRepo(t *testing.T) {
	f := newDiffFixture(t)
	commitInWorktree(t, f.worktree, "README.md", "shop\nfirst\n", "add first line")
	runGitOrFatal(t, f.repo, "worktree", "remove", "--force", f.worktree)
	if _, err := os.Stat(f.worktree); !os.IsNotExist(err) {
		t.Fatalf("the worktree survived removal: %v", err)
	}

	text := diffText(t, f, false)
	if !strings.Contains(text, "add first line") || !strings.Contains(text, "+first") {
		t.Fatalf("a torn-down crew's branch did not diff from the primary repo:\n%s", text)
	}
	if strings.Contains(text, "uncommitted") {
		t.Fatalf("a crew with no worktree has nothing to be dirty:\n%s", text)
	}
}

// TestDiffSaysSoWhenTheBranchIsGone: the branch deleted means the work
// landed or was discarded. That is an answer, not a failure, so it is a line
// on stdout and the command still exits 0.
func TestDiffSaysSoWhenTheBranchIsGone(t *testing.T) {
	f := newDiffFixture(t)
	runGitOrFatal(t, f.repo, "worktree", "remove", "--force", f.worktree)
	runGitOrFatal(t, f.repo, "branch", "-D", "mate/k3")

	text := diffText(t, f, false)
	if !strings.Contains(text, "mate/k3 no longer exists") || !strings.Contains(text, "torn down") {
		t.Fatalf("diff output = %q, want the branch-is-gone line", text)
	}
}

func TestDiffRefusesACrewItHasNoRecordOf(t *testing.T) {
	f := newDiffFixture(t)
	_, err := crewDiffText(context.Background(), f.w, gitx.New(), "shop", "k9", false)
	if err == nil {
		t.Fatal("a crew with no meta must not diff")
	}
	if !strings.Contains(err.Error(), "no crew k9") {
		t.Fatalf("error = %v, want it to name the missing crew", err)
	}
}

func TestDiffRefusesACrewThatRecordsNoBranch(t *testing.T) {
	f := newDiffFixture(t)
	if err := f.w.WriteCrewMeta("shop", "k3", map[string]string{spawn.MetaTask: "no branch"}); err != nil {
		t.Fatal(err)
	}
	_, err := crewDiffText(context.Background(), f.w, gitx.New(), "shop", "k3", false)
	if err == nil {
		t.Fatal("a crew with no recorded branch must not diff")
	}
	if !strings.Contains(err.Error(), "records no branch") {
		t.Fatalf("error = %v, want it to say the branch is missing", err)
	}
}

// TestDiffCommandParsesItsFlagsInAnyPosition is the CLI seam itself: the
// workspace flag after the positional arguments is the case reorderArgs
// exists for, and --stat is a boolean that must not swallow the next token.
func TestDiffCommandParsesItsFlagsInAnyPosition(t *testing.T) {
	f := newDiffFixture(t)
	commitInWorktree(t, f.worktree, "README.md", "shop\nfirst\n", "add first line")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"diff", "shop", "k3", "--stat", "--workspace", f.w.Root()}, &stdout, &stderr); err != nil {
		t.Fatalf("mate diff: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 file changed") {
		t.Fatalf("stdout = %q, want the --stat summary", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err := run([]string{"diff", "shop", "--workspace", f.w.Root()}, &stdout, &stderr)
	if err == nil {
		t.Fatal("diff with one argument must be a usage error")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("error = %v (%T), want a usage error", err, err)
	}
}
