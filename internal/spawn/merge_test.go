package spawn_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The merge of docs/mvp.md task 22, on real temporary repositories. Every
// refusal below is written from the rule it enforces, and each one asserts
// the same two things: the message a reader gets, and that nothing changed -
// the default branch still points where it did, the branch and worktree are
// still there, and the crew is still open. A refusal that quietly did half
// the work would pass a test that only read the error.

// mergeFixture spawns one crew over the fake runtime and returns the
// workspace, the deps and the spawn result.
func mergeFixture(t *testing.T, yolo bool) (*store.Workspace, spawn.Deps, spawn.CrewResult) {
	t.Helper()
	w := crewWorkspace(t, "shop")
	if yolo {
		cfg, err := w.LoadProject("shop")
		if err != nil {
			t.Fatal(err)
		}
		cfg.Yolo = true
		if err := w.SaveProject("shop", cfg); err != nil {
			t.Fatal(err)
		}
	}
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	return w, deps, res
}

// assertNothingChanged is the second half of every refusal test: the crew is
// still open, its branch and worktree are still on disk, and the default
// branch is exactly where it was.
func assertNothingChanged(t *testing.T, w *store.Workspace, res spawn.CrewResult, headBefore string) {
	t.Helper()
	ctx := context.Background()
	repo := w.RepoDir("shop")
	head, err := gitx.New().HeadCommit(ctx, repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if head != headBefore {
		t.Fatalf("a refused merge moved main from %s to %s", headBefore, head)
	}
	if res.Branch != "" {
		exists, err := gitx.New().BranchExists(ctx, repo, res.Branch)
		if err != nil || !exists {
			t.Fatalf("a refused merge removed the branch: %v, %v", exists, err)
		}
	}
	if res.Worktree != "" {
		if _, err := os.Stat(res.Worktree); err != nil {
			t.Fatalf("a refused merge removed the worktree: %v", err)
		}
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if state := meta[spawn.MetaState]; state != spawn.CrewStateSpawned {
		t.Fatalf("a refused merge left the crew at state %q, want %q", state, spawn.CrewStateSpawned)
	}
	if meta[spawn.MetaStoppedAt] != "" {
		t.Fatalf("a refused merge recorded stopped_at=%q", meta[spawn.MetaStoppedAt])
	}
}

func headOf(t *testing.T, w *store.Workspace, rev string) string {
	t.Helper()
	head, err := gitx.New().HeadCommit(context.Background(), w.RepoDir("shop"), rev)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

func TestMergeCrewLandsTheBranchAndFinishesTheCrew(t *testing.T) {
	w, deps, res := mergeFixture(t, false)
	commitInWorktree(t, res.Worktree, "feature.txt", "feature\n")
	before := headOf(t, w, "main")
	tip := headOf(t, w, res.Branch)

	out, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser)
	if err != nil {
		t.Fatalf("MergeCrew: %v", err)
	}

	// The merge itself: main is the branch tip, and no merge commit was
	// made, because the whole command is a fast-forward.
	if got := headOf(t, w, "main"); got != tip {
		t.Fatalf("main = %s after the merge, want the branch tip %s", got, tip)
	}
	if out.Commits != 1 || out.Before != before || out.After != tip {
		t.Fatalf("result = %+v, want 1 commit from %s to %s", out, before, tip)
	}

	// The teardown that mvp.md's M4 decision attaches to it: the crew is
	// finished, and its branch and worktree are gone.
	if out.Stop.State != spawn.CrewStateFinished || out.Stop.Teardown != spawn.TeardownClean {
		t.Fatalf("stop = %+v, want a clean finish", out.Stop)
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaState] != spawn.CrewStateFinished {
		t.Fatalf("crew meta state = %q, want finished", meta[spawn.MetaState])
	}
	if _, err := os.Stat(res.Worktree); !os.IsNotExist(err) {
		t.Fatalf("the worktree survived a merge: %v", err)
	}
	exists, err := gitx.New().BranchExists(context.Background(), w.RepoDir("shop"), res.Branch)
	if err != nil || exists {
		t.Fatalf("the branch survived a merge: %v, %v", exists, err)
	}
	// crews/<id>/ is history and is kept, exactly as `crew stop` keeps it.
	if _, err := os.Stat(w.CrewBrief("shop", "k3")); err != nil {
		t.Fatalf("brief.md did not survive the merge: %v", err)
	}

	want := "shop/k3: merged 1 commit(s) into main (" + before[:7] + ".." + tip[:7] + "); crew finished, worktree and branch removed"
	if out.Line() != want {
		t.Fatalf("line = %q, want %q", out.Line(), want)
	}
}

func TestMergeCrewRefusesAnUnknownCrew(t *testing.T) {
	w, deps, _ := mergeFixture(t, false)
	_, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k9", spawn.CallerUser)
	if err == nil || !strings.Contains(err.Error(), "no crew k9 is recorded") {
		t.Fatalf("err = %v, want a refusal naming the unknown crew", err)
	}
}

func TestMergeCrewRefusesAClosedCrew(t *testing.T) {
	w, deps, res := mergeFixture(t, false)
	if _, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	_ = res

	_, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser)
	if err == nil || !strings.Contains(err.Error(), "is closed (state=finished)") {
		t.Fatalf("err = %v, want a refusal naming the closed crew", err)
	}
}

func TestMergeCrewRefusesTheMateWhileYoloIsOff(t *testing.T) {
	w, deps, res := mergeFixture(t, false)
	commitInWorktree(t, res.Worktree, "feature.txt", "feature\n")
	before := headOf(t, w, "main")

	_, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerMate)
	if err == nil || !strings.Contains(err.Error(), "merge refused: yolo is off for shop; the captain merges") {
		t.Fatalf("err = %v, want the yolo refusal in the manual's own words", err)
	}
	assertNothingChanged(t, w, res, before)

	// The same call from the captain is allowed: the refusal is about who
	// is typing, not about the branch.
	if _, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser); err != nil {
		t.Fatalf("the captain's own merge was refused: %v", err)
	}
}

func TestMergeCrewLetsTheMateMergeWhenYoloIsOn(t *testing.T) {
	w, deps, res := mergeFixture(t, true)
	commitInWorktree(t, res.Worktree, "feature.txt", "feature\n")
	tip := headOf(t, w, res.Branch)

	out, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerMate)
	if err != nil {
		t.Fatalf("MergeCrew as the Mate under yolo: %v", err)
	}
	if got := headOf(t, w, "main"); got != tip {
		t.Fatalf("main = %s, want the branch tip %s", got, tip)
	}
	if out.Stop.State != spawn.CrewStateFinished {
		t.Fatalf("stop state = %q, want finished", out.Stop.State)
	}
}

func TestMergeCrewAlwaysRefusesACrew(t *testing.T) {
	// A crew may never land its own branch, whatever yolo says: yolo is
	// the captain delegating to the Mate, not to the crew.
	w, deps, res := mergeFixture(t, true)
	commitInWorktree(t, res.Worktree, "feature.txt", "feature\n")
	before := headOf(t, w, "main")

	_, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerCrew)
	if err == nil || !strings.Contains(err.Error(), "a crew cannot merge its own branch") {
		t.Fatalf("err = %v, want the crew refusal", err)
	}
	assertNothingChanged(t, w, res, before)
}

func TestMergeCrewRefusesADirtyCrewWorktreeNamingTheCount(t *testing.T) {
	w, deps, res := mergeFixture(t, false)
	commitInWorktree(t, res.Worktree, "feature.txt", "feature\n")
	if err := os.WriteFile(filepath.Join(res.Worktree, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := headOf(t, w, "main")

	_, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser)
	if err == nil || !strings.Contains(err.Error(), "has 1 uncommitted file(s)") {
		t.Fatalf("err = %v, want a refusal naming the dirty count", err)
	}
	assertNothingChanged(t, w, res, before)
}

func TestMergeCrewRefusesABranchWithNoCommits(t *testing.T) {
	w, deps, res := mergeFixture(t, false)
	before := headOf(t, w, "main")

	_, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser)
	if err == nil || !strings.Contains(err.Error(), "nothing to merge") {
		t.Fatalf("err = %v, want the nothing-to-merge refusal", err)
	}
	assertNothingChanged(t, w, res, before)
}

func TestMergeCrewRefusesADivergedBranchWithNeedsRebase(t *testing.T) {
	w, deps, res := mergeFixture(t, false)
	commitInWorktree(t, res.Worktree, "feature.txt", "feature\n")
	// The default branch moves on: the crew's branch is no longer a
	// fast-forward. mvp.md's M4 decisions call this needs-rebase and are
	// explicit that it is a command result, never a crew state.
	repo := w.RepoDir("shop")
	if err := os.WriteFile(filepath.Join(repo, "captain.txt"), []byte("captain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "captain.txt")
	git(t, repo, "commit", "-m", "captain's own commit")
	before := headOf(t, w, "main")

	_, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser)
	if err == nil {
		t.Fatal("a diverged branch must be refused")
	}
	if !strings.Contains(err.Error(), "needs-rebase") {
		t.Fatalf("err = %v, want the word needs-rebase", err)
	}
	if !strings.Contains(err.Error(), "rebase onto main") {
		t.Fatalf("err = %v, want the instruction to have the crew rebase", err)
	}
	assertNothingChanged(t, w, res, before)
}

func TestMergeCrewRefusesWhenThePrimaryRepoIsNotOnTheDefaultBranch(t *testing.T) {
	w, deps, res := mergeFixture(t, false)
	commitInWorktree(t, res.Worktree, "feature.txt", "feature\n")
	repo := w.RepoDir("shop")
	git(t, repo, "checkout", "-q", "-b", "captain-scratch")
	before := headOf(t, w, "main")

	_, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser)
	if err == nil || !strings.Contains(err.Error(), "has captain-scratch checked out, not main") {
		t.Fatalf("err = %v, want a refusal naming the checked-out branch", err)
	}
	assertNothingChanged(t, w, res, before)
}

func TestMergeCrewRefusesADirtyPrimaryRepo(t *testing.T) {
	w, deps, res := mergeFixture(t, false)
	commitInWorktree(t, res.Worktree, "feature.txt", "feature\n")
	repo := w.RepoDir("shop")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("captain was here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := headOf(t, w, "main")

	_, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser)
	if err == nil || !strings.Contains(err.Error(), "uncommitted file(s); a fast-forward needs a clean primary repo") {
		t.Fatalf("err = %v, want a refusal naming the dirty primary repo", err)
	}
	assertNothingChanged(t, w, res, before)
}

func TestCallerFromEnvDefaultsToTheCaptain(t *testing.T) {
	// The default matters: a missing MATE_CALLER is somebody's own shell,
	// and the yolo rule must never be skipped because a Mate's pane lost a
	// variable. A Mate's pane always has it, because spawn injects it.
	for _, tc := range []struct{ raw, want string }{
		{"", spawn.CallerUser},
		{"  ", spawn.CallerUser},
		{"captain", spawn.CallerUser},
		{"mate", spawn.CallerMate},
		{"MATE", spawn.CallerMate},
		{"crew", spawn.CallerCrew},
	} {
		if got := spawn.NormaliseCaller(tc.raw); got != tc.want {
			t.Errorf("NormaliseCaller(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	t.Setenv("MATE_CALLER", "mate")
	if got := spawn.CallerFromEnv(); got != spawn.CallerMate {
		t.Fatalf("CallerFromEnv = %q, want mate", got)
	}
	t.Setenv("MATE_CALLER", "")
	if got := spawn.CallerFromEnv(); got != spawn.CallerUser {
		t.Fatalf("CallerFromEnv with no value = %q, want user", got)
	}
}
