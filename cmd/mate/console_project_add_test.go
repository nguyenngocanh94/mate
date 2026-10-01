package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// TestConsoleNewProjectRegistersTheRepoItWasGiven is the Console's 'n' key
// end to end at the bridge: a name and a repo path relative to the workspace
// root register the same Project `mate project add` would, PROJECT.md
// included. Before this the bridge refused every workspace onboard, so the
// key the footer advertised could only ever fail.
func TestConsoleNewProjectRegistersTheRepoItWasGiven(t *testing.T) {
	w, deps := consoleFixture(t, "alpha")
	repo := filepath.Join(w.Root(), "services", "beta")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)

	line, err := consoleAction(w, deps)(context.Background(), console.ActionRequest{
		Action: console.ActionOnboard, TargetKind: "workspace", Input: "beta", Repo: "services/beta",
	})
	if err != nil {
		t.Fatalf("workspace onboard: %v", err)
	}
	if !strings.Contains(line, "beta") || !strings.Contains(line, "services/beta") {
		t.Fatalf("result line = %q, want it to name the Project and its repo", line)
	}

	fresh, err := store.Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := fresh.LoadProject("beta")
	if err != nil {
		t.Fatalf("beta not registered on disk: %v", err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0] != (store.RepoConfig{Name: "beta", Path: "services/beta", DefaultBranch: "main"}) {
		t.Fatalf("beta config = %+v, want repo services/beta on main", cfg)
	}
	if _, err := os.Stat(fresh.ProjectDoc("beta")); err != nil {
		t.Fatalf("PROJECT.md not seeded: %v", err)
	}
}

// TestConsoleNewProjectRefusalsMatchTheCLI: the Console runs the same checks
// as `mate project add`, so a path that is not a git work tree root, or
// does not exist, is refused and nothing is registered.
func TestConsoleNewProjectRefusalsMatchTheCLI(t *testing.T) {
	w, deps := consoleFixture(t, "alpha")
	plain := filepath.Join(w.Root(), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideAnyCheckout(t, w.Root())
	for _, tc := range []struct{ repo, want string }{
		{"plain", "not a git repository"},
		{"missing", "no such file"},
	} {
		_, err := consoleAction(w, deps)(context.Background(), console.ActionRequest{
			Action: console.ActionOnboard, TargetKind: "workspace", Input: "gamma", Repo: tc.repo,
		})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("repo %q: err = %v, want %q", tc.repo, err, tc.want)
		}
	}
	fresh, err := store.Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fresh.Project("gamma"); ok {
		t.Fatal("a refused onboard still registered gamma")
	}
}

// TestConsoleNewProjectWithoutARepo: the form's repo is optional (docs/mvp.md
// M9). An empty one registers a Project with no repo, PROJECT.md included,
// and the result line says how to add one.
func TestConsoleNewProjectWithoutARepo(t *testing.T) {
	w, deps := consoleFixture(t, "alpha")
	line, err := consoleAction(w, deps)(context.Background(), console.ActionRequest{
		Action: console.ActionOnboard, TargetKind: "workspace", Input: "beta", Repo: "  ",
	})
	if err != nil {
		t.Fatalf("workspace onboard without a repo: %v", err)
	}
	for _, want := range []string{"beta", "no repo yet", "mate project repo add beta"} {
		if !strings.Contains(line, want) {
			t.Fatalf("result line = %q, want it to contain %q", line, want)
		}
	}
	fresh, err := store.Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := fresh.LoadProject("beta")
	if err != nil {
		t.Fatalf("beta not registered on disk: %v", err)
	}
	if len(cfg.Repos) != 0 {
		t.Fatalf("beta repos = %+v, want none", cfg.Repos)
	}
	if _, err := os.Stat(fresh.ProjectDoc("beta")); err != nil {
		t.Fatalf("PROJECT.md not seeded: %v", err)
	}
}
