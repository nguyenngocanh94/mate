package spawn_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// oldLayoutWorkspace is a workspace written before layout 2: a committed
// repo beside `.mate/` registered to project shop, and a workspace.yaml with
// no `layout:`. It is built by hand because store refuses to make one now.
func oldLayoutWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initCommittedRepo(t, repo, "shop")
	if err := os.MkdirAll(w.CrewsDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectFile("shop"), []byte("repos:\n    - name: shop\n      path: shop\n      default_branch: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := "version: 1\nsession: " + w.Session() + "\nroot: " + w.Root() + "\ndefaults:\n    mate_harness: claude\n    crew_harness: codex\nprojects:\n    - name: shop\n"
	if err := os.WriteFile(w.WorkspaceFile(), []byte(ws), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := store.Open(w.Root())
	if err != nil {
		t.Fatalf("Open on the old layout: %v", err)
	}
	if !old.LayoutOld() {
		t.Fatal("the hand-built workspace does not read as the old layout")
	}
	return old
}

// TestSpawnCrewRefusesTheOldLayoutBeforeCreatingAnything is the spawn half
// of plan test 11: on a workspace still on layout 1 a crew spawn names
// `mate migrate` and leaves no worktree, branch, crew dir, meta or pane,
// while the project's Mate still starts.
func TestSpawnCrewRefusesTheOldLayoutBeforeCreatingAnything(t *testing.T) {
	w := oldLayoutWorkspace(t)
	rt := runtime.NewFake()
	_, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, rt), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("fix the cart total"),
	})
	const msg = "this workspace has the old layout (repos beside .mate); run mate migrate first"
	if !errors.Is(err, store.ErrLayoutOld) || !strings.Contains(err.Error(), msg) {
		t.Fatalf("SpawnCrew on the old layout = %v, want ErrLayoutOld %q", err, msg)
	}
	for _, path := range []string{w.WorktreeDir("shop", "k3"), w.CrewDir("shop", "k3"), w.CrewMeta("shop", "k3"), w.CrewStatus("shop", "k3")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Errorf("%s exists after a refused spawn (%v)", path, statErr)
		}
	}
	if exists, _ := gitx.New().BranchExists(context.Background(), w.RepoDir("shop"), "mate/k3"); exists {
		t.Error("branch mate/k3 exists after a refused spawn")
	}
	if len(rt.Tabs) != 0 {
		t.Errorf("a pane was opened for a refused spawn: %v", rt.Tabs)
	}

	if _, err := spawn.StartMate(context.Background(), w, fakeDeps(t, rt), spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatalf("StartMate on the old layout: %v", err)
	}
}
