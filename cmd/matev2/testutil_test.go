package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// initGitRepo turns dir into a git repository with one commit on branch
// "main". The task requires real `git init` repos in tests, so this is not
// skipped when git is missing: it fails loudly instead.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git not found on PATH (required for this test): %v", err)
	}
	runGitOrFatal(t, dir, "init", "-b", "main")
	runGitOrFatal(t, dir, "config", "user.email", "matev2-test@example.com")
	runGitOrFatal(t, dir, "config", "user.name", "matev2 test")
	runGitOrFatal(t, dir, "commit", "--allow-empty", "-m", "init")
}

func runGitOrFatal(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// fakeSpawnDeps wires the fake Herdr adapter with instant sleeps and a
// frozen clock, mirroring internal/spawn's own test fixture, so
// send/peek/state can be driven against a real (fake) live crew without
// ever calling spawn.LiveDeps().
func fakeSpawnDeps(t *testing.T, rt *runtime.Fake) spawn.Deps {
	t.Helper()
	return spawn.Deps{
		Runtime:              rt,
		Names:                rt.Names,
		ConfigHome:           t.TempDir(),
		Binary:               filepath.Join(t.TempDir(), "matev2"),
		ReadinessTimeout:     time.Second,
		StartupPromptTimeout: 50 * time.Millisecond,
		StartTimeout:         10 * time.Second,
		Sleep:                func(context.Context, time.Duration) error { return nil },
		Now:                  func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) },
		NewSessionID:         func() string { return "11111111-2222-3333-4444-555555555555" },
	}
}

// liveCrewWorkspace builds a workspace with one registered git project
// ("shop") ready for spawn.SpawnCrew: a real git repo with a commit on
// "main", the branch spawn's default worktree checkout uses.
func liveCrewWorkspace(t *testing.T, project string) *store.Workspace {
	t.Helper()
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(w.Root(), project)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject(project, store.ProjectConfig{Repo: repo, DefaultBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

// spawnFakeCrew spawns a crew over the fake Herdr adapter, giving a test a
// real live AgentHandle (agent name, pane, session) to script pane output
// for, without any of send/peek/state going near spawn.LiveDeps().
func spawnFakeCrew(t *testing.T, w *store.Workspace, deps spawn.Deps, project, crew string) spawn.CrewResult {
	t.Helper()
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project:   project,
		Crew:      crew,
		BriefText: "do the thing",
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	return res
}

// stopFakeCrew stops a crew over the fake Herdr adapter, the same call
// `matev2 crew stop` makes, so a test can put a crew into the "no agent
// recorded" state send/peek/state all have to handle.
func stopFakeCrew(t *testing.T, w *store.Workspace, deps spawn.Deps, project, crew string) (spawn.StopResult, error) {
	t.Helper()
	return spawn.StopCrew(context.Background(), w, deps, project, crew, true)
}
