package main

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

// mergeCLIWorkspace builds a workspace with one project and one crew that
// Herdr has no record of: `agent=` is absent, which is the state a crew
// reaches after a crash and the state StopCrew handles without asking Herdr
// anything. That is what lets `mate merge` - which wires spawn.LiveDeps()
// on purpose, so the CLI path under test is the real one - run in a unit
// test without a live session.
func mergeCLIWorkspace(t *testing.T) (*store.Workspace, string) {
	t.Helper()
	root := t.TempDir()
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	worktree := filepath.Join(w.Root(), ".worktrees", "shop-k3")
	if err := gitx.New().AddWorktree(context.Background(), repo, worktree, "mate/k3", "main"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, worktree, "add", "feature.txt")
	runGitOrFatal(t, worktree, "commit", "-m", "crew commit")

	if err := w.WriteCrewMeta("shop", "k3", map[string]string{
		spawn.MetaTask:     "add a feature",
		spawn.MetaHarness:  "codex",
		spawn.MetaBranch:   "mate/k3",
		spawn.MetaWorktree: ".worktrees/shop-k3",
		spawn.MetaState:    spawn.CrewStateSpawned,
	}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	return w, root
}

func TestCmdMergePrintsOneLineAndFinishesTheCrew(t *testing.T) {
	w, root := mergeCLIWorkspace(t)
	t.Setenv("MATE_CALLER", "")

	var out, errw bytes.Buffer
	if err := cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("merge: %v\nstderr: %s", err, errw.String())
	}
	line := strings.TrimSpace(out.String())
	for _, want := range []string{
		"shop/k3: merged 1 commit(s) into main (",
		"); crew finished, worktree and branch removed",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("merge printed %q, want it to contain %q", line, want)
		}
	}
	if n := len(strings.Split(line, "\n")); n != 1 {
		t.Fatalf("merge printed %d lines, want exactly one:\n%s", n, line)
	}

	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaState] != spawn.CrewStateFinished {
		t.Fatalf("crew state = %q, want finished", meta[spawn.MetaState])
	}
}

// TestCmdMergeReadsTheCallerFromThePaneEnvironment is the whole of how a
// Mate is told apart from the captain: MATE_CALLER, which spawn injects
// into the Mate's pane, and which no flag can override. A Mate merges only
// reviewed work (docs/mvp.md M19); the captain is never gated.
func TestCmdMergeReadsTheCallerFromThePaneEnvironment(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	t.Setenv("MATE_CALLER", "mate")

	var out, errw bytes.Buffer
	err := cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw)
	if err == nil {
		t.Fatal("a Mate must be refused without --review")
	}
	if !strings.Contains(err.Error(), "merge refused: the Mate merges only reviewed work") {
		t.Fatalf("err = %v, want the review refusal", err)
	}
	if out.String() != "" {
		t.Fatalf("a refused merge printed %q", out.String())
	}

	t.Setenv("MATE_CALLER", "crew")
	if err := cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err == nil || !strings.Contains(err.Error(), "a crew cannot merge its own branch") {
		t.Fatalf("a crew's merge: err = %v, want the crew refusal", err)
	}

	t.Setenv("MATE_CALLER", "")
	out.Reset()
	if err := cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("the captain's merge: %v", err)
	}
	if !strings.Contains(out.String(), "crew finished") {
		t.Fatalf("the captain's merge printed %q", out.String())
	}
}

func TestCmdMergeWantsTwoArguments(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	var out, errw bytes.Buffer
	err := cmdMerge([]string{"--workspace", root, "shop"}, &out, &errw)
	var ue *usageError
	if err == nil || !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestRunDispatchesMerge(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	t.Setenv("MATE_CALLER", "")
	var out, errw bytes.Buffer
	if err := run([]string{"merge", "shop", "k3", "--workspace", root}, &out, &errw); err != nil {
		t.Fatalf("run merge: %v\nstderr: %s", err, errw.String())
	}
	if !strings.Contains(out.String(), "shop/k3: merged") {
		t.Fatalf("run merge printed %q", out.String())
	}
}

// `project yolo` and `project mode` were removed by docs/mvp.md M19; a Mate
// quoting an old manual is told what replaced them.
func TestCmdProjectYoloAndModeAreGone(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	for _, sub := range []string{"yolo", "mode"} {
		var out, errw bytes.Buffer
		err := cmdProject([]string{sub, "--workspace", root, "shop", "on"}, &out, &errw)
		var ue *usageError
		if err == nil || !errors.As(err, &ue) || !strings.Contains(err.Error(), "--deliver local|pr") || !strings.Contains(err.Error(), "--review") {
			t.Fatalf("project %s: err = %v, want a usage error naming --deliver and --review", sub, err)
		}
	}
}
