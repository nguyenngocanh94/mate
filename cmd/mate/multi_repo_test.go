package main

// The CLI over a project with two repos (docs/mvp.md M9, task 40): every
// crew-scoped command acts on the repo the crew's meta names.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// twoRepoCLIWorkspace registers project "shop" with repos "api" and "web"
// and records crew k3 in api and crew k4 in web, each with one commit on
// its branch. Neither records an agent, so stop and merge never ask Herdr.
func twoRepoCLIWorkspace(t *testing.T) (*store.Workspace, string) {
	t.Helper()
	root := t.TempDir()
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	var repos []store.RepoConfig
	for _, name := range []string{"api", "web"} {
		dir := filepath.Join(w.Root(), "shop-"+name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		initGitRepo(t, dir)
		repos = append(repos, store.RepoConfig{Name: name, Path: dir, DefaultBranch: "main"})
	}
	if err := w.AddProject("shop", store.ProjectConfig{Repos: repos}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	for crew, repo := range map[string]string{"k3": "api", "k4": "web"} {
		worktree := w.WorktreeDir("shop", crew)
		if err := gitx.New().AddWorktree(context.Background(), cliRepo(w, repo), worktree, "mate/"+crew, "main"); err != nil {
			t.Fatalf("AddWorktree: %v", err)
		}
		commitInWorktree(t, worktree, repo+".txt", repo+"\n", "crew commit in "+repo)
		if err := w.WriteCrewMeta("shop", crew, map[string]string{
			spawn.MetaTask:     "change " + repo,
			spawn.MetaHarness:  "codex",
			spawn.MetaBranch:   "mate/" + crew,
			spawn.MetaWorktree: ".worktrees/shop-" + crew,
			store.MetaRepo:     repo,
			spawn.MetaState:    spawn.CrewStateSpawned,
		}); err != nil {
			t.Fatalf("WriteCrewMeta: %v", err)
		}
	}
	return w, root
}

func cliRepo(w *store.Workspace, name string) string {
	return filepath.Join(w.Root(), "shop-"+name)
}

func cliHead(t *testing.T, repo, rev string) string {
	t.Helper()
	head, err := gitx.New().HeadCommit(context.Background(), repo, rev)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

func TestDiffReadsEachCrewsOwnRepo(t *testing.T) {
	w, _ := twoRepoCLIWorkspace(t)
	for crew, c := range map[string]struct{ mine, other string }{"k3": {"api", "web"}, "k4": {"web", "api"}} {
		text, err := crewDiffText(context.Background(), w, gitx.New(), "shop", crew, false)
		if err != nil {
			t.Fatalf("diff %s: %v", crew, err)
		}
		if !strings.Contains(text, "crew commit in "+c.mine) || !strings.Contains(text, c.mine+".txt") {
			t.Errorf("diff %s does not show its commit in %s:\n%s", crew, c.mine, text)
		}
		if strings.Contains(text, c.other+".txt") {
			t.Errorf("diff %s shows the %s repo's change:\n%s", crew, c.other, text)
		}
	}
}

func TestDashboardBranchExistsAsksTheCrewsOwnRepo(t *testing.T) {
	w, _ := twoRepoCLIWorkspace(t)
	deps := dashboardDeps(w)
	for _, c := range []struct {
		crew, branch string
		want         bool
	}{
		{"k3", "mate/k3", true},
		{"k4", "mate/k4", true},
		// k3's branch is not in k4's repo, whatever the name says.
		{"k4", "mate/k3", false},
	} {
		got, err := deps.BranchExists(context.Background(), "shop", c.crew, c.branch)
		if err != nil || got != c.want {
			t.Errorf("BranchExists(%s, %s) = %v, %v; want %v", c.crew, c.branch, got, err, c.want)
		}
	}
}

func TestMergeAndStopActOnEachCrewsOwnRepo(t *testing.T) {
	w, root := twoRepoCLIWorkspace(t)
	t.Setenv("MATE_CALLER", "")
	apiTip := cliHead(t, cliRepo(w, "api"), "mate/k3")
	webMain := cliHead(t, cliRepo(w, "web"), "main")

	var out, errw bytes.Buffer
	if err := cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("merge k3: %v\nstderr: %s", err, errw.String())
	}
	if got := cliHead(t, cliRepo(w, "api"), "main"); got != apiTip {
		t.Fatalf("api main = %s after merging k3, want %s", got, apiTip)
	}
	if got := cliHead(t, cliRepo(w, "web"), "main"); got != webMain {
		t.Fatalf("merging k3 moved web's main to %s", got)
	}

	// k4's commit is ahead of web's main: a plain stop refuses, and a
	// --discard stop throws the branch away in web, not in api.
	out.Reset()
	if err := cmdCrewStop([]string{"--workspace", root, "shop", "k4"}, &out, &errw); !errors.Is(err, spawn.ErrUnlandedWork) {
		t.Fatalf("stop k4 = %v, want ErrUnlandedWork", err)
	}
	if err := cmdCrewStop([]string{"--workspace", root, "--discard", "shop", "k4"}, &out, &errw); err != nil {
		t.Fatalf("stop k4 --discard: %v\nstderr: %s", err, errw.String())
	}
	if ok, err := gitx.New().BranchExists(context.Background(), cliRepo(w, "web"), "mate/k4"); err != nil || ok {
		t.Fatalf("k4's branch survived its discard in web: %v, %v", ok, err)
	}
	if got := cliHead(t, cliRepo(w, "api"), "main"); got != apiTip {
		t.Fatalf("stopping k4 moved api's main to %s", got)
	}
}

func TestCrewSpawnRefusesWhenTheRepoIsNotSettled(t *testing.T) {
	w, root := twoRepoCLIWorkspace(t)
	brief := filepath.Join(root, "brief.md")
	if err := os.WriteFile(brief, []byte(brieftest.Ship("work")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("empty", store.ProjectConfig{}); err != nil {
		t.Fatalf("AddProject with no repo: %v", err)
	}
	// A missing or wrong --repo is the caller's argument to fix: exit 2. A
	// project with no repo is a state only the captain can change, so it is
	// a refusal: exit 1 (the manual's convention, section 4).
	cases := []struct {
		args  []string
		code  int
		wants []string
	}{
		{[]string{"shop", "k5"}, 2, []string{"several repos (api, web)", "--repo <name>"}},
		{[]string{"shop", "k5", "--repo", "mobile"}, 2, []string{`no repo "mobile"`, "api, web"}},
		{[]string{"empty", "k5"}, 1, []string{"has no repo yet", "`mate project repo add empty <repo-path>`"}},
	}
	for _, c := range cases {
		args := append([]string{"crew", "spawn", "--workspace", root, "--brief", brief}, c.args...)
		var out, errw bytes.Buffer
		code := mainRun(args, &out, &errw)
		if code != c.code {
			t.Errorf("%v: exit %d, want %d\nstderr: %s", c.args, code, c.code, errw.String())
		}
		for _, want := range append(c.wants, "nothing was created") {
			if !strings.Contains(errw.String(), want) {
				t.Errorf("%v: stderr %q does not contain %q", c.args, errw.String(), want)
			}
		}
		if _, err := os.Stat(w.WorktreeDir(c.args[0], "k5")); !os.IsNotExist(err) {
			t.Errorf("%v: a refused spawn left a worktree: %v", c.args, err)
		}
	}
}
