package spawn_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestLiveSpawnCrewCodex is the task 11 proof: a real Codex crew in a real
// Herdr pane, in its own git worktree, that reads the brief mate pointed it
// at, commits in its branch, and reports `wait-mate:` through $MATE_STATUS.
//
// It asserts the work, not the screen: a new commit on the crew branch that
// touches README.md, and a status file the crew itself appended to.
func TestLiveSpawnCrewCodex(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

	// TMPDIR must not go through a symlink: Herdr reports a pane cwd with
	// symlinks resolved and the launch guard compares the two.
	root := t.TempDir()
	w, err := store.Init(root, store.Defaults{})
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
	base := liveGitOut(t, repo, "rev-parse", "main")

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
		Crew:    "k3",
		Harness: harness.KindCodex,
		BriefText: brieftest.Ship(`Append the line "hello from crew" to README.md, commit it, ` +
			`then append wait-mate: ready in branch to the status file`),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Cleanup(func() {
		// Whatever this test asserts, the lab must not be left with a crew,
		// nor with its worktree or branch: this is task 11's live test, not
		// task 16's, so any unlanded work here is discarded on purpose.
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
	})
	t.Logf("spawned crew %s in pane %s (session %s, branch %s)", res.Agent, res.Pane, res.Session, res.Branch)
	t.Logf("worktree %s\nbrief %s\nstatus %s", res.Worktree, res.BriefPath, res.StatusPath)
	if res.TrustDialog {
		// ADR 0028: a linked worktree inherits the primary repo's trust
		// decision, so seeing one here is news worth recording.
		t.Logf("note: the startup settle answered a directory-trust dialog for the worktree")
	}
	if res.DeliveryWarning != "" {
		t.Logf("delivery warning: %s\n%s", res.DeliveryWarning, res.PaneTail)
	}

	handle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    res.Agent,
		RawID:   "k3",
		Kind:    harness.KindCodex,
	}
	status := waitForStatus(t, ctx, w, "k3", "wait-mate:", 120*time.Second, func() string {
		screen, readErr := rt.ReadAgent(ctx, handle, harness.ReadRecentUnwrapped, 40)
		if readErr != nil {
			return "(pane not readable: " + readErr.Error() + ")"
		}
		return harness.StartupScreenTail(screen, 20)
	})
	t.Logf("status file:\n%s", status)

	head := liveGitOut(t, repo, "rev-parse", res.Branch)
	if head == base {
		t.Fatalf("branch %s is still at the base commit %s; the crew committed nothing", res.Branch, base)
	}
	touched := liveGitOut(t, repo, "diff", "--name-only", base, res.Branch)
	if !strings.Contains(touched, "README.md") {
		t.Fatalf("the crew's commit does not touch README.md; changed files:\n%s", touched)
	}
	readme, err := os.ReadFile(filepath.Join(res.Worktree, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("README.md in the worktree:\n%s", readme)

	// The crew committed, so its branch is ahead of main: a stop without
	// --discard checks the branch first and refuses before killing anything
	// (docs/mvp.md section 4b, task 18b), so the agent is still listed.
	stopped, err := spawn.StopCrew(ctx, w, deps, "shop", "k3", false)
	if !errors.Is(err, spawn.ErrUnlandedWork) {
		t.Fatalf("StopCrew: err = %v, want ErrUnlandedWork", err)
	}
	if stopped.TabClosed {
		t.Fatal("a refused StopCrew closed the crew tab")
	}
	if stopped.Ahead < 1 {
		t.Fatalf("stopped.Ahead = %d, want at least 1 commit", stopped.Ahead)
	}
	listed, err := rt.ListAgents(ctx, runtime.SessionHandle{Name: session, ConfigHome: configHome})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	stillLive := false
	for _, obs := range listed {
		if obs.Handle.Name == res.Agent {
			stillLive = true
		}
	}
	if !stillLive {
		t.Fatalf("agent %s is gone after a refused StopCrew; the refusal must change nothing", res.Agent)
	}
	// The unlanded work survives the refusal; the deferred cleanup discards
	// it once this test is done looking at it.
	if _, err := os.Stat(res.Worktree); err != nil {
		t.Fatalf("a refused StopCrew removed the worktree: %v", err)
	}
}

// waitForStatus polls crew's status file until it contains want. The
// failure carries both the status file and the pane, because a crew that
// never reported is a question about what its screen is showing.
func waitForStatus(t *testing.T, ctx context.Context, w *store.Workspace, crew, want string, budget time.Duration, pane func() string) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	var text string
	for {
		entries, _, err := w.ReadStatus("shop", crew, 0)
		if err != nil {
			t.Fatalf("ReadStatus: %v", err)
		}
		lines := make([]string, 0, len(entries))
		for _, e := range entries {
			lines = append(lines, e.Line)
		}
		text = strings.Join(lines, "\n")
		if strings.Contains(text, want) {
			return text
		}
		if time.Now().After(deadline) {
			t.Fatalf("crew %s did not report %q within %s\nstatus file:\n%s\npane:\n%s", crew, want, budget, text, pane())
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled while waiting for %q; status so far:\n%s", want, text)
		case <-time.After(2 * time.Second):
		}
	}
}

func liveGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
