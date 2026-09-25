package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// noRepoWorkspace is a workspace whose project shop has no repo yet, the
// project the console's New project form makes when the Repo field is empty.
func noRepoWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

// twoRepoWorkspace is liveCrewWorkspace's shop (repo shop, main, one
// commit) plus a second repo api whose default branch develop has no
// commit yet.
func twoRepoWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := liveCrewWorkspace(t, "shop")
	api := filepath.Join(w.Root(), "api")
	runGitOrFatal(t, w.Root(), "init", "-q", "-b", "develop", api)
	if _, err := w.AddRepo("shop", store.RepoConfig{Name: "api", Path: api, DefaultBranch: "develop"}); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}
	return w
}

func projectFactsCLI(t *testing.T, w *store.Workspace) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := mainRun([]string{"project", "facts", "shop", "--workspace", w.Root()}, &out, &errw)
	return code, out.String(), errw.String()
}

// TestProjectFactsNoRepo: a project without a repo is not an error; the
// facts are that it has none, and how one is added.
func TestProjectFactsNoRepo(t *testing.T) {
	w := noRepoWorkspace(t)
	code, out, errw := projectFactsCLI(t, w)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errw)
	}
	want := "repos: none\nhint: project shop has no repo yet, so no crew can be spawned; add one with `mate project repo add shop <repo-path>`\n"
	if out != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", out, want)
	}
}

// TestProjectFactsOneRepo: one block, naming the repo, whose head line
// spells out the anchor a fact recorded now carries.
func TestProjectFactsOneRepo(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	head := strings.TrimSpace(gitOut(t, w.RepoDir("shop"), "rev-parse", "--short", "main"))
	code, out, errw := projectFactsCLI(t, w)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errw)
	}
	want := strings.Join([]string{
		"shop: repo shop at " + w.RepoDir("shop") + ", default branch main",
		"commits: 1 on main",
		"head: " + head + " (anchor shop:main@" + head + ")",
		"tree: empty",
		"note: names and counts from the committed tree of main only; no file was opened, and uncommitted changes in the checkout are not seen",
		"",
	}, "\n")
	if out != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", out, want)
	}
}

// TestProjectFactsTwoRepos: one block per repo, in registration order,
// separated by one blank line, each with its own branch and anchor.
func TestProjectFactsTwoRepos(t *testing.T) {
	w := twoRepoWorkspace(t)
	head := strings.TrimSpace(gitOut(t, w.RepoDir("shop"), "rev-parse", "--short", "main"))
	code, out, errw := projectFactsCLI(t, w)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errw)
	}
	blocks := strings.Split(strings.TrimSuffix(out, "\n"), "\n\n")
	if len(blocks) != 2 {
		t.Fatalf("want two blocks separated by a blank line:\n%s", out)
	}
	if !strings.HasPrefix(blocks[0], "shop: repo shop at "+w.RepoDir("shop")+", default branch main\ncommits: 1 on main\nhead: "+head+" (anchor shop:main@"+head+")\n") {
		t.Errorf("first block is not repo shop's:\n%s", blocks[0])
	}
	wantAPI := strings.Join([]string{
		"shop: repo api at " + w.RepoDir("api") + ", default branch develop",
		"commits: 0 (develop has no commit yet)",
		"head: none (anchor api:develop@none)",
		"tree: empty",
		"note: names and counts from the committed tree of develop only; no file was opened, and uncommitted changes in the checkout are not seen",
	}, "\n")
	if blocks[1] != wantAPI {
		t.Errorf("second block:\n%s\nwant:\n%s", blocks[1], wantAPI)
	}
}
