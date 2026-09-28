package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Mate adds a repo to its own project with one mate command (docs/mvp.md
// task 59): by URL, cloned into the workspace, and an empty repo is given
// the first commit a Crew needs to branch from.

func gitIdentity(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "mate test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "mate-test@example.com")
	}
}

// bareRepo is a remote to clone from: empty, or with one commit on main.
func bareRepo(t *testing.T, withCommit bool) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "backend.git")
	runGitOrFatal(t, filepath.Dir(remote), "init", "--bare", "-b", "main", remote)
	if withCommit {
		seed := t.TempDir()
		initGitRepo(t, seed)
		runGitOrFatal(t, seed, "push", remote, "main")
	}
	return remote
}

func commitCount(t *testing.T, dir string) string {
	t.Helper()
	out, err := runGit(dir, "rev-list", "--count", "--all")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRepoAddClonesAnEmptyRemoteAndGivesItAFirstCommit(t *testing.T) {
	gitIdentity(t)
	ws := initProjectWorkspace(t)
	var out, errw bytes.Buffer
	if err := cmdProjectAdd([]string{"--workspace", ws, "shop"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	remote := bareRepo(t, false)

	out.Reset()
	if err := cmdProjectRepo([]string{"add", "--workspace", ws, "shop", "file://" + remote}, &out, &errw); err != nil {
		t.Fatalf("repo add by url: %v\n%s", err, errw.String())
	}
	clone := filepath.Join(ws, "backend")
	if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
		t.Fatalf("no clone at %s: %v", clone, err)
	}
	if got := commitCount(t, clone); got != "1" {
		t.Fatalf("clone has %s commit(s), want the one first commit", got)
	}
	for _, want := range []string{
		"cloned file://" + remote + " into backend",
		"backend had no commit; made an empty first commit on main so crews can branch from it (nothing was pushed)",
		"added repo backend to project shop: path=backend default-branch=main",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output does not say %q:\n%s", want, out.String())
		}
	}
	if head, _ := runGit(remote, "rev-list", "--count", "--all"); head != "0" {
		t.Fatalf("the remote has %s commit(s): repo add pushed", head)
	}
}

func TestRepoAddLeavesARepoWithHistoryAlone(t *testing.T) {
	gitIdentity(t)
	ws := initProjectWorkspace(t)
	var out, errw bytes.Buffer
	if err := cmdProjectAdd([]string{"--workspace", ws, "shop"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	remote := bareRepo(t, true)
	out.Reset()
	if err := cmdProjectRepo([]string{"add", "--workspace", ws, "shop", "file://" + remote, "--name", "api"}, &out, &errw); err != nil {
		t.Fatalf("repo add: %v\n%s", err, errw.String())
	}
	if got := commitCount(t, filepath.Join(ws, "api")); got != "1" {
		t.Fatalf("clone has %s commits, want the remote's one", got)
	}
	if strings.Contains(out.String(), "first commit") {
		t.Fatalf("a repo with history was given a commit:\n%s", out.String())
	}
}

func TestRepoAddRefusesToCloneOverSomething(t *testing.T) {
	ws := initProjectWorkspace(t)
	var out, errw bytes.Buffer
	if err := cmdProjectAdd([]string{"--workspace", ws, "shop"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := cmdProjectRepo([]string{"add", "--workspace", ws, "shop", "file://" + bareRepo(t, false)}, &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want a refusal naming the existing directory", err)
	}
}

func TestGitURLRecognisesRemotesNotPaths(t *testing.T) {
	for in, want := range map[string]bool{
		"git@github.com:nguyenngocanh94/hellovietnambackend.git": true,
		"https://github.com/org/repo.git":                        true,
		"ssh://git@host/org/repo":                                true,
		"file:///tmp/repo.git":                                   true,
		"./services/api":                                         false,
		"/Users/anh/newWorkspace/api":                            false,
		"api":                                                    false,
		"C:dir":                                                  false,
	} {
		if got := isGitURL(in); got != want {
			t.Errorf("isGitURL(%q) = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"git@github.com:nguyenngocanh94/hellovietnambackend.git": "hellovietnambackend",
		"https://github.com/org/repo":                            "repo",
		"file:///tmp/backend.git/":                               "backend",
	} {
		if got := repoNameFromURL(in); got != want {
			t.Errorf("repoNameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
