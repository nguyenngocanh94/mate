package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// The Crew row's Stop entry closes the task the way `mate crew stop` does:
// the same spawn.StopCrew call, reported in the same words.
func TestConsoleStopCrewClosesTheTaskLikeTheCLI(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	crew := spawnFakeCrew(t, w, deps, "shop", "k3")

	out, err := consoleAction(w, deps)(context.Background(), console.ActionRequest{
		Action: console.ActionStop, Target: "shop", TargetKind: "crew", Crew: "k3",
	})
	if err != nil {
		t.Fatalf("stop crew action: %v", err)
	}
	if !strings.HasPrefix(out, "shop/k3: agent "+crew.Agent+" (stopped)") || !strings.Contains(out, "worktree and branch removed") {
		t.Fatalf("stop crew line = %q, want the CLI's report of a clean teardown", out)
	}
	if _, err := deps.Runtime.InspectAgent(context.Background(), runtime.AgentHandle{Name: crew.Agent, Session: runtime.SessionHandle{Name: crew.Session}}); !runtime.IsAgentNotFound(err) {
		t.Fatalf("agent %s still inspectable after the stop: %v", crew.Agent, err)
	}
	if _, err := os.Stat(crew.Worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree %s still there after a clean stop: %v", crew.Worktree, err)
	}
}

// Unlanded work is refused before anything changes, exactly as on the
// command line; the Console has no discard, so the work stays put.
func TestConsoleStopCrewRefusesUnlandedWork(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	crew := spawnFakeCrew(t, w, deps, "shop", "k3")
	if err := os.WriteFile(filepath.Join(crew.Worktree, "work.txt"), []byte("unlanded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, crew.Worktree, "add", "work.txt")
	runGitOrFatal(t, crew.Worktree, "commit", "-q", "-m", "work")

	_, err := consoleAction(w, deps)(context.Background(), console.ActionRequest{
		Action: console.ActionStop, Target: "shop", TargetKind: "crew", Crew: "k3",
	})
	if err == nil || !strings.Contains(err.Error(), "nothing was stopped") {
		t.Fatalf("stop crew with unlanded work: err = %v, want the unlanded-work refusal", err)
	}
	if _, err := os.Stat(crew.Worktree); err != nil {
		t.Fatalf("worktree touched by a refused stop: %v", err)
	}
	if _, err := deps.Runtime.InspectAgent(context.Background(), runtime.AgentHandle{Name: crew.Agent, Session: runtime.SessionHandle{Name: crew.Session}}); err != nil {
		t.Fatalf("agent stopped by a refused stop: %v", err)
	}
}
