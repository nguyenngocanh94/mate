package spawn_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/brief/brieftest"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/process"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// TestLiveSpawnCrewCodexSkipsUpdateDialog is the proof for the 2026-09-18
// production failure: with codex-cli 0.154.0 installed and 0.155.0 published,
// every Codex crew spawn died on `target_blocked: codex startup screen not
// recognised`, because codex draws a three-option release-update prompt ahead
// of the directory-trust dialog and matev2 could not name it.
//
// The launch now passes `-c check_for_update_on_startup=false`, so on a
// healthy machine the prompt is never drawn and UpdateDialog is false. The
// test asserts what it observed rather than a fixed answer: what it proves
// either way is that the crew reached its composer, did the work and reported
// `wait-mate:`, which is exactly what the failure prevented. The recogniser itself
// is covered by the captured screens in internal/harness/testdata/startup.
func TestLiveSpawnCrewCodexSkipsUpdateDialog(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

	// TMPDIR must not go through a symlink: Herdr reports a pane cwd with
	// symlinks resolved and the launch guard compares the two.
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
	liveGit(t, repo, "config", "user.email", "matev2-test@example.com")
	liveGit(t, repo, "config", "user.name", "matev2 test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	liveGit(t, repo, "add", "README.md")
	liveGit(t, repo, "commit", "-m", "init")
	if err := w.AddProject("shop", store.ProjectConfig{Repo: repo, DefaultBranch: "main"}); err != nil {
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
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               binaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 90 * time.Second,
		StartTimeout:         60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	res, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop",
		Crew:    "u1",
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
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "u1", true)
	})
	t.Logf("spawned crew %s in pane %s (session %s, branch %s)", res.Agent, res.Pane, res.Session, res.Branch)
	t.Logf("startup settle observed: update dialog answered = %v, trust dialog answered = %v", res.UpdateDialog, res.TrustDialog)
	if res.UpdateDialog {
		t.Log("note: codex drew its release-update prompt despite -c check_for_update_on_startup=false; the settle skipped it until the next version")
	}
	if res.DeliveryWarning != "" {
		t.Logf("delivery warning: %s\n%s", res.DeliveryWarning, res.PaneTail)
	}

	handle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    res.Agent,
		RawID:   "u1",
		Kind:    harness.KindCodex,
	}
	status := waitForStatus(t, ctx, w, "u1", "wait-mate:", 150*time.Second, func() string {
		screen, readErr := rt.ReadAgent(ctx, handle, 40)
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
}
