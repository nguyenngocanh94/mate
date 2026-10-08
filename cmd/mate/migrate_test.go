package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/migrate"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestMigrateIsTheCaptains: a Mate or a Crew that runs `mate migrate` is
// refused before the workspace is even opened, as `project mode` was.
func TestMigrateIsTheCaptains(t *testing.T) {
	for _, caller := range []string{"mate", "crew"} {
		t.Setenv("MATE_CALLER", caller)
		var stdout, stderr bytes.Buffer
		code := mainRun([]string{"migrate", t.TempDir(), "--dry-run"}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "the captain runs mate migrate; a "+caller+" cannot") {
			t.Fatalf("MATE_CALLER=%s: exit %d, stderr %q", caller, code, stderr.String())
		}
	}
}

func TestMigrateUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := mainRun([]string{"migrate", "a", "b"}, &stdout, &stderr); code != 2 {
		t.Fatalf("two arguments: exit %d, want 2 (usage)\n%s", code, stderr.String())
	}
}

// TestMigrateCommandMovesAnOldWorkspace drives the command's core over a
// one-repo old workspace: the dry run changes nothing, the run moves.
func TestMigrateCommandMovesAnOldWorkspace(t *testing.T) {
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	root := w.Root()
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, filepath.Join(root, "web"))
	if err := os.MkdirAll(w.CrewsDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectFile("shop"), []byte("repos:\n    - name: web\n      path: web\n      default_branch: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.WorkspaceFile(), []byte("version: 1\nsession: mate-old\nroot: "+root+"\nprojects:\n    - name: shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Every command but mate migrate refuses the old layout.
	var stdout, stderr bytes.Buffer
	if code := mainRun([]string{"tool", "beads", "shop", "--init", "--workspace", root}, &stdout, &stderr); code != 1 || stderr.String() != "mate: this workspace has the old layout (repos beside .mate); run mate migrate first\n" {
		t.Fatalf("another command on the old layout: exit %d, stderr %q", code, stderr.String())
	}
	// mate migrate opens it. No herdr on PATH: the dry run asks no
	// session.
	t.Run("migrate opens it", func(t *testing.T) {
		t.Setenv("MATE_CALLER", "")
		t.Setenv("PATH", t.TempDir())
		var stdout, stderr bytes.Buffer
		if code := mainRun([]string{"migrate", root, "--dry-run"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "would move "+root+"/web -> "+root+"/shop/web\n") {
			t.Fatalf("mate migrate --dry-run on the old layout: exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
		}
	})
	if w, err = store.OpenForMigrate(root); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runMigrate(context.Background(), w, migrate.Deps{}, true, &out); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if want := "would move " + root + "/web -> " + root + "/shop/web\nwould write workspace layout 2\n"; out.String() != want {
		t.Fatalf("dry run printed %q, want %q", out.String(), want)
	}
	if _, err := os.Stat(filepath.Join(root, "web")); err != nil {
		t.Fatalf("the dry run moved web: %v", err)
	}
	out.Reset()
	if err := runMigrate(context.Background(), w, migrate.Deps{}, false, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "moved " + root + "/web -> " + root + "/shop/web\nrepaired 0 worktree(s)\nrewrote 0 brief(s)\nworkspace layout 2\n"; out.String() != want {
		t.Fatalf("run printed %q, want %q", out.String(), want)
	}
}

// TestMigrateRepairsAMovedWorkspaceFirst: an old-layout workspace moved to
// another root, its workspace.yaml, owner marker and a closed crew's brief
// still naming the old one, cannot reach the console's link repair, so
// mate migrate makes it. The dry run says so and changes nothing; the run
// repairs root and owner, rewrites the brief, then moves the repo.
func TestMigrateRepairsAMovedWorkspaceFirst(t *testing.T) {
	w, err := store.Init(filepath.Join(t.TempDir(), "old"), store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	oldRoot := w.Root()
	if err := os.MkdirAll(filepath.Join(oldRoot, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, filepath.Join(oldRoot, "web"))
	if err := os.MkdirAll(w.CrewDir("shop", "k1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectFile("shop"), []byte("repos:\n    - name: web\n      path: web\n      default_branch: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "k1", map[string]string{"task": "an old task", "state": "finished", "repo": "web"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.CrewBrief("shop", "k1"), []byte("Work in "+oldRoot+"/web.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.WorkspaceFile(), []byte("version: 1\nsession: mate-old\nroot: "+oldRoot+"\nprojects:\n    - name: shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configHome := t.TempDir()
	if err := runtime.ReclaimSessionOwner(configHome, "mate-old", store.SessionName(oldRoot)); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(filepath.Dir(oldRoot), "moved")
	if err := os.Rename(oldRoot, root); err != nil {
		t.Fatal(err)
	}
	moved, err := store.OpenForMigrate(root)
	if err != nil {
		t.Fatal(err)
	}
	brief := moved.CrewBrief("shop", "k1")

	// Only git on PATH: no herdr is asked.
	bin := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("MATE_CALLER", "")
	t.Setenv("HERDR_CONFIG_PATH", configHome)

	before, err := os.ReadFile(moved.WorkspaceFile())
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := mainRun([]string{"migrate", root, "--dry-run"}, &stdout, &stderr); code != 0 {
		t.Fatalf("dry run: exit %d, stderr %q", code, stderr.String())
	}
	if want := "recorded root " + oldRoot + " differs from " + root + ": migrate will repair links first\n"; !strings.HasPrefix(stdout.String(), want) {
		t.Fatalf("dry run printed %q, want it to start with %q", stdout.String(), want)
	}
	if after, _ := os.ReadFile(moved.WorkspaceFile()); string(after) != string(before) {
		t.Fatalf("the dry run changed workspace.yaml:\n%s", after)
	}
	if data, _ := os.ReadFile(brief); string(data) != "Work in "+oldRoot+"/web.\n" {
		t.Fatalf("the dry run changed the brief: %q", data)
	}
	if owner, _, _ := runtime.SessionOwner(configHome, "mate-old"); owner != store.SessionName(oldRoot) {
		t.Fatalf("the dry run changed the owner marker to %q", owner)
	}

	stdout.Reset()
	stderr.Reset()
	if code := mainRun([]string{"migrate", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("run: exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"repaired owner: ", "repaired root: brief of crew shop/k1 moved from " + oldRoot + " to " + root, "moved " + root + "/web -> " + root + "/shop/web\n", "rewrote 1 brief(s)\n", "workspace layout 2\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("run printed %q, want %q in it", stdout.String(), want)
		}
	}
	after, err := store.Open(root)
	if err != nil {
		t.Fatalf("store.Open after migrate: %v", err)
	}
	if after.RecordedRoot() != root || after.Session() != "mate-old" {
		t.Fatalf("root %q session %q, want %s and mate-old", after.RecordedRoot(), after.Session(), root)
	}
	if owner, _, _ := runtime.SessionOwner(configHome, "mate-old"); owner != store.SessionName(root) {
		t.Fatalf("owner marker = %q, want this workspace", owner)
	}
	if data, _ := os.ReadFile(brief); string(data) != "Work in "+root+"/shop/web.\n" {
		t.Fatalf("brief = %q", data)
	}
}

// oldShopWorkspace is an old-layout workspace: project shop with repo web
// beside `.mate/`.
func oldShopWorkspace(t *testing.T) (*store.Workspace, string) {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	root := w.Root()
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, filepath.Join(root, "web"))
	if err := os.MkdirAll(w.MateDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectFile("shop"), []byte("repos:\n    - name: web\n      path: web\n      default_branch: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.WorkspaceFile(), []byte("version: 1\nsession: mate-old\nroot: "+root+"\nprojects:\n    - name: shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return w, root
}

// gitOnlyPath puts only git on PATH: no herdr can be asked.
func gitOnlyPath(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("MATE_CALLER", "")
	t.Setenv("HERDR_CONFIG_PATH", t.TempDir())
	return bin
}

// TestCrewStopOpensTheOldLayout: migrate refuses an open crew and names
// mate crew stop; that command opens the old layout, closes the crew, and
// the migrate that follows moves the repo. No herdr is on PATH.
func TestCrewStopOpensTheOldLayout(t *testing.T) {
	w, root := oldShopWorkspace(t)
	if err := os.MkdirAll(w.CrewDir("shop", "k1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "k1", map[string]string{"task": "a task", "state": "spawned", "repo": "web"}); err != nil {
		t.Fatal(err)
	}
	gitOnlyPath(t)

	var stdout, stderr bytes.Buffer
	if code := mainRun([]string{"migrate", root}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "close it with mate crew stop before migrating") {
		t.Fatalf("migrate with an open crew: exit %d, stderr %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := mainRun([]string{"crew", "stop", "shop", "k1", "--workspace", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("crew stop on the old layout: exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := mainRun([]string{"migrate", root}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "moved "+root+"/web -> "+root+"/shop/web\n") {
		t.Fatalf("migrate after crew stop: exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

// TestMateStopOpensTheOldLayout: with no herdr on PATH, a mate.meta that
// records a pane makes migrate refuse; mate mate stop opens the old layout
// (it fails asking Herdr, not on the layout), and the refusal names the way
// out: with herdr on PATH, mate mate stop stops it and migrate moves.
func TestMateStopOpensTheOldLayout(t *testing.T) {
	w, root := oldShopWorkspace(t)
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude", "pane": "w1:p1", "tab": "w1:t1", "agent": "mate-shop"}); err != nil {
		t.Fatal(err)
	}
	bin := gitOnlyPath(t)

	var stdout, stderr bytes.Buffer
	if code := mainRun([]string{"migrate", root, "--dry-run"}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "Herdr could not be asked; with herdr on PATH, run mate migrate again, or stop it with mate mate stop shop") {
		t.Fatalf("migrate with no herdr: exit %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := mainRun([]string{"mate", "stop", "shop", "--no-stow", "--workspace", root}, &stdout, &stderr); code == 0 ||
		strings.Contains(stderr.String(), store.ErrLayoutOld.Error()) || !strings.Contains(stderr.String(), "herdr") {
		t.Fatalf("mate stop with no herdr: exit %d, stderr %q; want a Herdr failure, not the layout", code, stderr.String())
	}

	// herdr on PATH, its server not running: the stop the refusal names
	// works, and so does the migrate.
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte("#!/bin/sh\necho '{\"sessions\":[]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := mainRun([]string{"mate", "stop", "shop", "--no-stow", "--workspace", root}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "shop: already gone") {
		t.Fatalf("mate stop with herdr: exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := mainRun([]string{"migrate", root}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "moved "+root+"/web -> "+root+"/shop/web\n") {
		t.Fatalf("migrate after mate stop: exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}
