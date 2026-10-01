package spawn_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// newWorkspace creates a workspace with one registered git project. It is
// the fixture every test in this package starts from: `store.Init`, a real
// `git init` repository beside `.mate/`, and the registration between them.
func newWorkspace(t *testing.T, project string) *store.Workspace {
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
	gitInit(t, repo)
	if err := w.AddProject(project, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "--quiet")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
}

// fakeDeps wires the fake runtime with instant sleeps and a frozen clock, so
// every unit test runs at memory speed and writes a byte-identical meta.
func fakeDeps(t *testing.T, rt *runtime.Fake) spawn.Deps {
	t.Helper()
	return spawn.Deps{
		Harnesses:            catalog.Default(),
		Runtime:              rt,
		Names:                rt.Names,
		ConfigHome:           t.TempDir(),
		Binary:               filepath.Join(t.TempDir(), "mate"),
		ReadinessTimeout:     time.Second,
		StartupPromptTimeout: 50 * time.Millisecond,
		StartTimeout:         10 * time.Second,
		Sleep:                func(context.Context, time.Duration) error { return nil },
		Now:                  func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) },
		NewSessionID:         func() string { return "11111111-2222-3333-4444-555555555555" },
	}
}

// screen loads one captured startup screen from the harness testdata.
func screen(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "harness", "testdata", "startup", name))
	if err != nil {
		t.Fatalf("read capture %s: %v", name, err)
	}
	return string(data)
}

func readMeta(t *testing.T, w *store.Workspace, project string) map[string]string {
	t.Helper()
	meta, err := w.ReadMateMeta(project)
	if err != nil {
		t.Fatalf("ReadMateMeta: %v", err)
	}
	return meta
}
