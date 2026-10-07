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

// The Project row's Remove entry is `mate project remove`: the crew is
// stopped before the Project leaves workspace.yaml, and its files stay.
func TestConsoleRemoveProjectStopsAgentsThenUnregisters(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	crew := spawnFakeCrew(t, w, deps, "shop", "k3")

	line, err := consoleAction(w, deps)(context.Background(), console.ActionRequest{
		Action: console.ActionRemoveProject, Target: "shop", TargetKind: "project",
	})
	if err != nil {
		t.Fatalf("remove project action: %v", err)
	}
	if !strings.Contains(line, "Project shop removed") || !strings.Contains(line, "1 agent(s) stopped") {
		t.Fatalf("line = %q", line)
	}
	fresh, err := store.Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fresh.Project("shop"); ok {
		t.Fatal("project is still registered")
	}
	if _, err := os.Stat(filepath.Join(fresh.ProjectDir("shop"), "crews")); err != nil {
		t.Fatalf("crew records not kept: %v", err)
	}
	if _, err := os.Stat(crew.Worktree); !os.IsNotExist(err) {
		t.Fatalf("a landed crew's worktree should go with its stop: %v", err)
	}
}

func TestConsoleRemoveProjectKeepsTheProjectWhenACrewRefuses(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	crew := spawnFakeCrew(t, w, deps, "shop", "k3")
	if err := os.WriteFile(filepath.Join(crew.Worktree, "work.txt"), []byte("unlanded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, crew.Worktree, "add", "work.txt")
	runGitOrFatal(t, crew.Worktree, "commit", "-q", "-m", "work")

	_, err := consoleAction(w, deps)(context.Background(), console.ActionRequest{
		Action: console.ActionRemoveProject, Target: "shop", TargetKind: "project",
	})
	if err == nil || !strings.Contains(err.Error(), "crew k3") {
		t.Fatalf("err = %v, want it to name crew k3", err)
	}
	fresh, _ := store.Open(w.Root())
	if _, ok := fresh.Project("shop"); !ok {
		t.Fatal("project was unregistered although a crew is still running")
	}
}
