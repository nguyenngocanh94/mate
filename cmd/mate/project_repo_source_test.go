package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// A Mate adds a repo to its own project with one mate command (docs/mvp.md
// task 59): by URL, cloned into the project's directory, and an empty repo is given
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
	clone := filepath.Join(ws, "shop", "backend")
	if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
		t.Fatalf("no clone at %s: %v", clone, err)
	}
	if got := commitCount(t, clone); got != "1" {
		t.Fatalf("clone has %s commit(s), want the one first commit", got)
	}
	for _, want := range []string{
		"cloned file://" + remote + " into backend",
		"backend had no commit; made an empty first commit on main so crews can branch from it (nothing was pushed)",
		"added repo backend to project shop: path=shop/backend default-branch=main",
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
	if got := commitCount(t, filepath.Join(ws, "shop", "api")); got != "1" {
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
	if err := os.MkdirAll(filepath.Join(ws, "shop", "backend"), 0o755); err != nil {
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

// TestProjectAddMakesTheDirectoryAndRepoAddFillsIt is plan test 7 and the
// tree of docs/mvp.md section 3: `project add` makes `<root>/<project>/`,
// with or without a repo, a URL is cloned into it, and nothing else appears
// at the workspace root.
func TestProjectAddMakesTheDirectoryAndRepoAddFillsIt(t *testing.T) {
	gitIdentity(t)
	ws := initProjectWorkspace(t)
	var out, errw bytes.Buffer
	for _, name := range []string{"shop", "notes"} {
		if err := cmdProjectAdd([]string{"--workspace", ws, name}, &out, &errw); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Stat(filepath.Join(ws, name)); err != nil || !fi.IsDir() {
			t.Fatalf("project add %s did not make %s: %v", name, filepath.Join(ws, name), err)
		}
	}
	if err := cmdProjectRepo([]string{"add", "--workspace", ws, "shop", "file://" + bareRepo(t, true)}, &out, &errw); err != nil {
		t.Fatalf("repo add by url: %v\n%s", err, errw.String())
	}
	if _, err := os.Stat(filepath.Join(ws, "shop", "backend", ".git")); err != nil {
		t.Fatalf("the clone is not under the project directory: %v", err)
	}
	entries, err := os.ReadDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	var top []string
	for _, e := range entries {
		top = append(top, e.Name())
	}
	if strings.Join(top, " ") != ".mate notes shop" {
		t.Fatalf("workspace root holds %v, want .mate, notes and shop", top)
	}
}

// TestRepoAddRefusesAPathOutsideTheProject: a repo by path must already be
// under the project's directory, and the refusal says where to move it.
func TestRepoAddRefusesAPathOutsideTheProject(t *testing.T) {
	ws := initProjectWorkspace(t, "loose", "blog/web")
	var out, errw bytes.Buffer
	if err := cmdProjectAdd([]string{"--workspace", ws, "shop"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	w, err := store.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"loose", "blog/web"} {
		err := cmdProjectRepo([]string{"add", "--workspace", ws, "shop", filepath.Join(ws, rel)}, &out, &errw)
		want := "repo " + rel + " must live under " + w.ProjectHome("shop") + "; move it there, or run mate migrate on an old workspace"
		if !errors.Is(err, store.ErrRepoOutsideProject) || !strings.Contains(err.Error(), want) {
			t.Fatalf("repo add %s = %v, want ErrRepoOutsideProject %q", rel, err, want)
		}
	}
	if err := cmdProjectAdd([]string{"--workspace", ws, "blog", filepath.Join(ws, "loose")}, &out, &errw); !errors.Is(err, store.ErrRepoOutsideProject) {
		t.Fatalf("project add blog with a repo outside blog/ = %v, want ErrRepoOutsideProject", err)
	}
}

// TestRepoAddOnTheOldLayoutClonesNothing: a workspace not yet migrated does
// not open, so no clone is made and no directory is left behind.
func TestRepoAddOnTheOldLayoutClonesNothing(t *testing.T) {
	ws := initProjectWorkspace(t)
	var out, errw bytes.Buffer
	if err := cmdProjectAdd([]string{"--workspace", ws, "shop"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	w, err := store.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(w.WorkspaceFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.WorkspaceFile(), []byte(strings.Replace(string(raw), "layout: 2\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	err = cmdProjectRepo([]string{"add", "--workspace", ws, "shop", "file://" + bareRepo(t, true)}, &out, &errw)
	if !errors.Is(err, store.ErrLayoutOld) || err.Error() != "this workspace has the old layout (repos beside .mate); run mate migrate first" {
		t.Fatalf("repo add on the old layout = %v, want ErrLayoutOld", err)
	}
	for _, dir := range []string{filepath.Join(ws, "shop", "backend"), filepath.Join(ws, "backend")} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("a refused repo add left a clone at %s: %v", dir, err)
		}
	}
}
