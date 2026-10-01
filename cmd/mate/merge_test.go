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
	repo := filepath.Join(w.Root(), "shop")
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
// into the Mate's pane, and which no flag can override.
func TestCmdMergeReadsTheCallerFromThePaneEnvironment(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	t.Setenv("MATE_CALLER", "mate")

	var out, errw bytes.Buffer
	err := cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw)
	if err == nil {
		t.Fatal("a Mate must be refused while yolo is off")
	}
	if !strings.Contains(err.Error(), "merge refused: yolo is off for shop; the captain merges") {
		t.Fatalf("err = %v, want the yolo refusal", err)
	}
	if out.String() != "" {
		t.Fatalf("a refused merge printed %q", out.String())
	}

	// `project yolo shop on` is the switch, and it is enough on its own.
	out.Reset()
	if err := cmdProjectYolo([]string{"--workspace", root, "shop", "on"}, &out, &errw); err != nil {
		t.Fatalf("project yolo on: %v", err)
	}
	out.Reset()
	if err := cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("merge under yolo: %v", err)
	}
	if !strings.Contains(out.String(), "crew finished") {
		t.Fatalf("merge under yolo printed %q", out.String())
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

func TestCmdProjectYoloFlipsTheFlagAndSaysWhenAMateLearnsIt(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	w, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if err := cmdProjectYolo([]string{"--workspace", root, "shop", "on"}, &out, &errw); err != nil {
		t.Fatalf("yolo on: %v", err)
	}
	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Yolo {
		t.Fatal("project.yaml still has yolo off after `project yolo shop on`")
	}
	// The manual is rendered at `mate start`, so a running Mate is still
	// quoting the old value. The output has to say so, or the captain flips
	// the flag and wonders why the Mate keeps refusing.
	if !strings.Contains(out.String(), "next restart") {
		t.Fatalf("yolo on printed %q, want it to say a running Mate learns the new value at its next restart", out.String())
	}

	// The flag is visible where a reader looks for it.
	out.Reset()
	if err := cmdProjectList([]string{"--workspace", root}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "YOLO") || !strings.Contains(out.String(), "true") {
		t.Fatalf("project list = %q, want a YOLO column reading true", out.String())
	}

	// Off again, and a second `off` is a no-op that says so rather than
	// claiming a change.
	out.Reset()
	if err := cmdProjectYolo([]string{"--workspace", root, "shop", "off"}, &out, &errw); err != nil {
		t.Fatalf("yolo off: %v", err)
	}
	cfg, err = w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Yolo {
		t.Fatal("project.yaml still has yolo on after `project yolo shop off`")
	}
	out.Reset()
	if err := cmdProjectYolo([]string{"--workspace", root, "shop", "off"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already false") {
		t.Fatalf("a repeated `off` printed %q, want it to say nothing changed", out.String())
	}

	// Other fields of project.yaml survive the rewrite.
	if len(cfg.Repos) != 1 || cfg.Repos[0].Path != "shop" || cfg.Repos[0].DefaultBranch != "main" || cfg.Mode != store.ModeLocalOnly {
		t.Fatalf("project.yaml after two flips = %+v", cfg)
	}
}

func TestCmdProjectYoloRefusesAnythingButOnAndOff(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	var out, errw bytes.Buffer
	var ue *usageError
	if err := cmdProjectYolo([]string{"--workspace", root, "shop", "yes"}, &out, &errw); err == nil || !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a usage error for `yes`", err)
	}
	if err := cmdProjectYolo([]string{"--workspace", root, "nope", "on"}, &out, &errw); err == nil {
		t.Fatal("an unregistered project must be refused")
	}
}
