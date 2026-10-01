package spawn_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLiveCrewTeardownRefusesThenDiscards is task 16's live proof: a real
// Codex crew that commits and reports `wait-mate:` leaves its branch ahead of
// main, so a stop without --discard must refuse (ErrUnlandedWork) and leave
// the worktree and branch standing; a second stop with --discard must then
// remove both, while `crews/<id>/brief.md` survives every step.
func TestLiveCrewTeardownRefusesThenDiscards(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	useLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	liveGit(t, repo, "init", "-b", "main")
	liveGit(t, repo, "config", "user.email", "mate-test@example.com")
	liveGit(t, repo, "config", "user.name", "mate test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	liveGit(t, repo, "add", "README.md")
	liveGit(t, repo, "commit", "-m", "init")
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = names
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("the lab session is provisioned by the runner; this test must not start a Herdr server")
		return nil
	}
	deps := spawn.Deps{
		Harnesses:            catalog.Default(),
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               binaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	res, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop",
		Crew:    "k9",
		Harness: harness.KindCodex,
		BriefText: brieftest.Ship(`Append the line "hello from crew" to README.md, commit it, ` +
			`then append wait-mate: ready in branch to the status file`),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k9", true)
	})
	t.Logf("spawned crew %s in pane %s (branch %s, worktree %s)", res.Agent, res.Pane, res.Branch, res.Worktree)

	handle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    res.Agent,
		RawID:   "k9",
		Kind:    harness.KindCodex,
	}
	status := waitForStatus(t, ctx, w, "k9", "wait-mate:", 120*time.Second, func() string {
		screen, readErr := rt.ReadAgent(ctx, handle, 40)
		if readErr != nil {
			return "(pane not readable: " + readErr.Error() + ")"
		}
		return harness.StartupScreenTail(screen, 20)
	})
	t.Logf("status file:\n%s", status)

	// First stop: refused. The branch is ahead of main, so a stop without
	// --discard checks the branch first and changes nothing at all - the
	// agent keeps running and its tab stays open (docs/mvp.md section 4b,
	// task 18b: no more "agent dead, worktree kept" outcome).
	refused, err := spawn.StopCrew(ctx, w, deps, "shop", "k9", false)
	if !errors.Is(err, spawn.ErrUnlandedWork) {
		t.Fatalf("first StopCrew: err = %v, want ErrUnlandedWork", err)
	}
	t.Logf("first stop (refused): %+v", refused)
	if refused.TabClosed || refused.AlreadyGone {
		t.Fatal("a refused teardown must leave the crew running with its tab open")
	}
	if _, err := rt.InspectAgent(ctx, runtime.AgentHandle{Session: runtime.SessionHandle{Name: session, ConfigHome: configHome}, Name: res.Agent}); err != nil {
		t.Fatalf("the crew agent must still be live after a refused stop: %v", err)
	}
	if refused.Ahead < 1 {
		t.Fatalf("refused.Ahead = %d, want at least 1", refused.Ahead)
	}
	if _, statErr := os.Stat(res.Worktree); statErr != nil {
		t.Fatalf("a refused stop must keep the worktree: %v", statErr)
	}
	if exists := liveBranchExists(t, repo, res.Branch); !exists {
		t.Fatal("a refused stop must keep the branch")
	}
	if _, statErr := os.Stat(w.CrewBrief("shop", "k9")); statErr != nil {
		t.Fatalf("brief.md must survive a refused stop: %v", statErr)
	}

	// Second stop: --discard finishes the job.
	discarded, err := spawn.StopCrew(ctx, w, deps, "shop", "k9", true)
	if err != nil {
		t.Fatalf("second StopCrew (--discard): %v", err)
	}
	t.Logf("second stop (discarded): %+v", discarded)
	if discarded.Teardown != spawn.TeardownDiscarded {
		t.Fatalf("teardown = %q, want %q", discarded.Teardown, spawn.TeardownDiscarded)
	}
	if _, statErr := os.Stat(res.Worktree); !os.IsNotExist(statErr) {
		t.Fatalf("--discard must remove the worktree: %v", statErr)
	}
	if exists := liveBranchExists(t, repo, res.Branch); exists {
		t.Fatal("--discard must remove the branch")
	}
	if _, statErr := os.Stat(w.CrewBrief("shop", "k9")); statErr != nil {
		t.Fatalf("brief.md must survive a discarded teardown: %v", statErr)
	}
}

func liveBranchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repo
	return cmd.Run() == nil
}
