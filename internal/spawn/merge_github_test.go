package spawn_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/github"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The merge of docs/mvp.md M18 for a crew that delivers a pull request
// (M19): `gh pr merge` under the same caller rules as the fast-forward. gh is a script here; nothing reaches
// GitHub.

const mergePR = "https://github.com/acme/shop/pull/7"

// ghScript answers gh by its first two arguments and records every call. A
// `pr view` takes the next answer of its queue (the last repeats).
type ghScript struct {
	mu    sync.Mutex
	views []github.Result
	calls []string
	// mergeFails makes `pr merge` exit non-zero.
	mergeFails bool
}

func (g *ghScript) Run(_ context.Context, cmd github.Command) (github.Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, strings.Join(cmd.Args, " "))
	switch cmd.Args[1] {
	case "view":
		i := 0
		for _, c := range g.calls {
			if strings.HasPrefix(c, "pr view") {
				i++
			}
		}
		i--
		if i >= len(g.views) {
			i = len(g.views) - 1
		}
		return g.views[i], nil
	case "merge":
		if g.mergeFails {
			return github.Result{ExitCode: 1, Stderr: "Pull request is not mergeable\n"}, nil
		}
	}
	return github.Result{}, nil
}

func (g *ghScript) merged() bool {
	for _, c := range g.calls {
		if strings.HasPrefix(c, "pr merge") {
			return true
		}
	}
	return false
}

func prView(state, head, merge string) github.Result {
	m := "null"
	if merge != "" {
		m = `{"oid":"` + merge + `"}`
	}
	return github.Result{Stdout: `{"state":"` + state + `","mergeCommit":` + m + `,"baseRefName":"main","headRefOid":"` + head + `"}`}
}

// githubMergeFixture is mergeFixture for a crew that delivers a pull
// request, committed, with the pull request recorded in its meta.
func githubMergeFixture(t *testing.T, gh *ghScript) (*store.Workspace, spawn.Deps, spawn.CrewResult) {
	t.Helper()
	w, deps, res := mergeFixture(t)
	commitInWorktree(t, res.Worktree, "feature.txt", "feature\n")
	if err := w.UpdateCrewMeta("shop", "k3", map[string]string{
		crewstate.MetaDelivery: crewstate.DeliveryPR,
		crewstate.MetaPRURL:    mergePR,
		crewstate.MetaPRState:  crewstate.PRStateOpen,
	}); err != nil {
		t.Fatal(err)
	}
	deps.GitHub = github.Client{Runner: gh}
	return w, deps, res
}

func TestGitHubMergeRefusesAnUnreviewedMate(t *testing.T) {
	gh := &ghScript{views: []github.Result{prView("OPEN", "aaa", "")}}
	w, deps, res := githubMergeFixture(t, gh)
	before := headOf(t, w, "main")

	_, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerMate)
	if err == nil || !strings.Contains(err.Error(), "merge refused: the Mate merges only reviewed work") {
		t.Fatalf("err = %v, want the review refusal", err)
	}
	if len(gh.calls) != 0 {
		t.Fatalf("gh was called %q for a refused merge", gh.calls)
	}
	assertNothingChanged(t, w, res, before)

	_, err = spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerCrew)
	if err == nil || !strings.Contains(err.Error(), "a crew cannot merge its own branch") || len(gh.calls) != 0 {
		t.Fatalf("crew: err = %v, gh calls %q", err, gh.calls)
	}
}

func TestGitHubMergeMergesThePullRequestAndFinishesTheCrew(t *testing.T) {
	for name, caller := range map[string]struct {
		who      string
		reviewed []spawn.ReviewedCommit
		merge    string
	}{
		"the captain":            {spawn.CallerUser, nil, "pr merge " + mergePR + " --merge"},
		"the Mate with a review": {spawn.CallerMate, []spawn.ReviewedCommit{{Head: "aaa"}}, "pr merge " + mergePR + " --merge --match-head-commit aaa"},
	} {
		t.Run(name, func(t *testing.T) {
			gh := &ghScript{views: []github.Result{prView("OPEN", "aaa", ""), prView("MERGED", "aaa", "feedbeef1234")}}
			w, deps, res := githubMergeFixture(t, gh)

			out, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", caller.who, caller.reviewed...)
			if err != nil {
				t.Fatalf("MergeCrew: %v", err)
			}
			if !gh.merged() || !contains(gh.calls, caller.merge) {
				t.Fatalf("gh calls = %q, want gh pr merge", gh.calls)
			}
			if !strings.Contains(out.Line(), "merged pull request "+mergePR+" as feedbee") {
				t.Fatalf("line = %q", out.Line())
			}
			// The branch is not an ancestor of main - the merge happened on
			// GitHub - yet the crew closes clean and the branch goes.
			if out.Stop.State != spawn.CrewStateFinished || out.Stop.Teardown != spawn.TeardownClean || !out.Stop.BranchRemoved {
				t.Fatalf("stop = %+v", out.Stop)
			}
			meta, err := w.ReadCrewMeta("shop", "k3")
			if err != nil {
				t.Fatal(err)
			}
			if meta[crewstate.MetaPRState] != crewstate.PRStateMerged || meta[crewstate.MetaMergeCommit] != "feedbeef1234" || meta[spawn.MetaState] != spawn.CrewStateFinished {
				t.Fatalf("meta = %v", meta)
			}
			if !strings.HasPrefix(meta[crewstate.MetaPRSync], "skipped: ") {
				t.Fatalf("pr_sync = %q: the fixture has no origin, so the checkout must be left alone with a reason", meta[crewstate.MetaPRSync])
			}
			if exists, err := gitx.New().BranchExists(context.Background(), w.RepoDir("shop/shop"), res.Branch); err != nil || exists {
				t.Fatalf("the branch survived: %v, %v", exists, err)
			}
		})
	}
}

func TestGitHubMergeRefusalsChangeNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		gh       *ghScript
		noPR     bool
		reviewed []spawn.ReviewedCommit
		want     string
	}{
		"no pull request recorded": {gh: &ghScript{views: []github.Result{prView("OPEN", "aaa", "")}}, noPR: true, want: "has no pull request recorded"},
		"pull request closed":      {gh: &ghScript{views: []github.Result{prView("CLOSED", "aaa", "")}}, want: "is closed, not open"},
		"already merged":           {gh: &ghScript{views: []github.Result{prView("MERGED", "aaa", "x")}}, want: "is merged, not open"},
		"stale review":             {gh: &ghScript{views: []github.Result{prView("OPEN", "bbb", "")}}, reviewed: []spawn.ReviewedCommit{{Head: "aaa"}}, want: "review is stale"},
		"gh refuses":               {gh: &ghScript{views: []github.Result{prView("OPEN", "aaa", "")}, mergeFails: true}, want: "gh could not merge"},
	} {
		t.Run(name, func(t *testing.T) {
			w, deps, res := githubMergeFixture(t, tc.gh)
			if tc.noPR {
				if err := w.WriteCrewMeta("shop", "k3", withoutPR(t, w)); err != nil {
					t.Fatal(err)
				}
			}
			before := headOf(t, w, "main")
			_, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser, tc.reviewed...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			assertNothingChanged(t, w, res, before)
			if meta, _ := w.ReadCrewMeta("shop", "k3"); meta[crewstate.MetaPRState] == crewstate.PRStateMerged {
				t.Fatalf("a refused merge recorded pr_state=merged")
			}
		})
	}
}

func TestGitHubMergePinsTheReviewedHead(t *testing.T) {
	gh := &ghScript{views: []github.Result{prView("OPEN", "aaa", ""), prView("MERGED", "aaa", "feedbeef1234")}}
	w, deps, _ := githubMergeFixture(t, gh)
	if _, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser, spawn.ReviewedCommit{Head: "aaa"}); err != nil {
		t.Fatalf("MergeCrew: %v", err)
	}
	if !contains(gh.calls, "pr merge "+mergePR+" --merge --match-head-commit aaa") {
		t.Fatalf("gh calls = %q, want the merge pinned to the reviewed head", gh.calls)
	}
}

func withoutPR(t *testing.T, w *store.Workspace) map[string]string {
	t.Helper()
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	delete(meta, crewstate.MetaPRURL)
	delete(meta, crewstate.MetaPRState)
	return meta
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
