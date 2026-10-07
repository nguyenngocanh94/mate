package github_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/github"
)

// fakeRunner scripts gh: the answer is looked up by the joined arguments.
type fakeRunner struct {
	calls   []github.Command
	results map[string]github.Result
	err     error
}

func (f *fakeRunner) Run(_ context.Context, cmd github.Command) (github.Result, error) {
	f.calls = append(f.calls, cmd)
	if f.err != nil {
		return github.Result{}, f.err
	}
	return f.results[strings.Join(cmd.Args, " ")], nil
}

type fakeRemotes map[string]string

func (f fakeRemotes) RemoteURL(_ context.Context, repo, _ string) (string, error) {
	return f[repo], nil
}

func TestIsGitHubURL(t *testing.T) {
	for url, want := range map[string]bool{
		"git@github.com:acme/shop.git":          true,
		"https://github.com/acme/shop":          true,
		"ssh://git@github.com/acme/shop.git":    true,
		"https://GitHub.com/acme/shop.git":      true,
		"git@gitlab.com:acme/shop.git":          false,
		"https://example.com/github.com/x":      false,
		"/srv/git/shop.git":                     false,
		"../shop":                               false,
		"git@github.com.evil.example:acme/shop": false,
		"":                                      false,
	} {
		if got := github.IsGitHubURL(url); got != want {
			t.Errorf("IsGitHubURL(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestCheckProjectAcceptsAReadyProject(t *testing.T) {
	gh := github.Client{Runner: &fakeRunner{}}
	err := github.CheckProject(context.Background(), gh,
		fakeRemotes{"/ws/shop": "git@github.com:acme/shop.git", "/ws/api": "https://github.com/acme/api"},
		[]github.Repo{{Name: "shop", Path: "/ws/shop"}, {Name: "api", Path: "/ws/api"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCheckProjectSaysWhatToFixForEveryProblem(t *testing.T) {
	gh := github.Client{Runner: &fakeRunner{results: map[string]github.Result{
		"auth status": {ExitCode: 1, Stderr: "You are not logged into any GitHub hosts.\n"},
	}}}
	err := github.CheckProject(context.Background(), gh,
		fakeRemotes{"/ws/api": "git@gitlab.com:acme/api.git"},
		[]github.Repo{{Name: "shop", Path: "/ws/shop"}, {Name: "api", Path: "/ws/api"}})
	if err == nil {
		t.Fatal("a project with no login and two bad origins was accepted")
	}
	for _, want := range []string{
		"gh auth login",
		"repo shop has no origin remote",
		"remote add origin",
		"repo api: origin git@gitlab.com:acme/api.git does not point to GitHub",
		"remote set-url origin",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

func TestAuthStatusNamesAMissingBinary(t *testing.T) {
	gh := github.Client{Runner: &fakeRunner{err: github.ErrNotInstalled}}
	err := gh.AuthStatus(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("AuthStatus = %v, want a not-installed message", err)
	}
	gh = github.Client{Runner: &fakeRunner{err: errors.New("boom")}}
	if err := gh.AuthStatus(context.Background()); err == nil || err.Error() != "boom" {
		t.Fatalf("AuthStatus = %v, want the runner's own error", err)
	}
}

func TestPRViewReadsStateAndMergeCommit(t *testing.T) {
	const view = "pr view https://github.com/acme/shop/pull/7 --json state,mergeCommit,baseRefName,headRefOid"
	f := &fakeRunner{results: map[string]github.Result{
		view: {Stdout: `{"state":"MERGED","mergeCommit":{"oid":"abc123"},"baseRefName":"main","headRefOid":"def456"}`},
	}}
	pr, err := github.Client{Runner: f}.PRView(context.Background(), "/ws/shop", "https://github.com/acme/shop/pull/7")
	if err != nil {
		t.Fatal(err)
	}
	if pr.State != github.StateMerged || pr.MergeCommit != "abc123" || pr.Base != "main" || pr.HeadSHA != "def456" {
		t.Fatalf("PR = %+v", pr)
	}
	if f.calls[0].Dir != "/ws/shop" {
		t.Fatalf("gh ran in %q, want the repo", f.calls[0].Dir)
	}

	f.results[view] = github.Result{Stdout: `{"state":"OPEN","mergeCommit":null,"baseRefName":"main"}`}
	pr, err = github.Client{Runner: f}.PRView(context.Background(), "", "https://github.com/acme/shop/pull/7")
	if err != nil || pr.State != github.StateOpen || pr.MergeCommit != "" {
		t.Fatalf("PR = %+v, err %v", pr, err)
	}

	f.results[view] = github.Result{ExitCode: 1, Stderr: "no pull requests found\n"}
	if _, err := (github.Client{Runner: f}).PRView(context.Background(), "", "https://github.com/acme/shop/pull/7"); err == nil || !strings.Contains(err.Error(), "no pull requests found") {
		t.Fatalf("err = %v, want gh's own words", err)
	}
}

func TestPRMergeNamesTheHeadItWasReviewedAt(t *testing.T) {
	f := &fakeRunner{}
	gh := github.Client{Runner: f}
	if err := gh.PRMerge(context.Background(), "/ws/shop", "https://github.com/acme/shop/pull/7", ""); err != nil {
		t.Fatal(err)
	}
	if err := gh.PRMerge(context.Background(), "/ws/shop", "https://github.com/acme/shop/pull/7", "abc"); err != nil {
		t.Fatal(err)
	}
	got := []string{strings.Join(f.calls[0].Args, " "), strings.Join(f.calls[1].Args, " ")}
	want := []string{"pr merge https://github.com/acme/shop/pull/7 --merge", "pr merge https://github.com/acme/shop/pull/7 --merge --match-head-commit abc"}
	if got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}
