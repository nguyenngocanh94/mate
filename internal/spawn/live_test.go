package spawn_test

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/process"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// requireLive gates every TestLive* proof in this package, the same way
// internal/runtime does: a live Herdr lab session and the harness binaries
// are machine facts, so MATEV2_LIVE=1 is the single opt-in that says this
// machine has them. scripts/gotestreport allows TestLive* and nothing else
// to skip.
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("MATEV2_LIVE") != "1" {
		t.Skip("set MATEV2_LIVE=1 to run live Herdr proofs")
	}
}

// liveLabSession is the provisioned lab the runner owns. This test never
// starts a Herdr server and refuses the shared sessions.
func liveLabSession(t *testing.T) (session, configHome string) {
	t.Helper()
	session = strings.TrimSpace(os.Getenv("MATEV2_HERDR_LIVE_SESSION"))
	if session == "" {
		t.Skip("set MATEV2_HERDR_LIVE_SESSION to a provisioned fm-lab-* Herdr session")
	}
	if session == "default" || session == "firstmate" {
		t.Fatal("refusing to run a live spawn against the default or firstmate session")
	}
	if !strings.HasPrefix(session, "fm-lab-") {
		t.Fatalf("live test requires an fm-lab- session, got %q", session)
	}
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		t.Fatal("HOME is required to resolve the Herdr socket")
	}
	return session, filepath.Join(home, ".config")
}

// TestLiveSpawnStartMateClaude is the task 07 proof: a real Claude Code Mate
// in a real Herdr pane, with its composer on screen, stopped and confirmed
// gone afterwards.
func TestLiveSpawnStartMateClaude(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

	// TMPDIR must not go through a symlink: Herdr reports a pane cwd with
	// symlinks resolved, and the launch guard compares the two (docs/mvp.md
	// section 7). The runner sets TMPDIR; t.TempDir honours it.
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	useLabSession(t, w, session)
	w, err = store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if w.Session() != session {
		t.Fatalf("workspace session = %q, want the lab session %q", w.Session(), session)
	}

	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	liveGit(t, repo, "init", "-b", "main")
	liveGit(t, repo, "config", "user.email", "matev2-test@example.com")
	liveGit(t, repo, "config", "user.name", "matev2 test")
	liveGit(t, repo, "commit", "--allow-empty", "-m", "init")
	if err := w.AddProject("shop", store.ProjectConfig{Repo: repo, DefaultBranch: "main"}); err != nil {
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

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	started, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	t.Cleanup(func() {
		// Whatever this test asserts, the lab must not be left with a Mate.
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})
	t.Logf("started agent %s in pane %s (session %s, session_id %s)", started.Agent, started.Pane, started.Session, started.SessionID)
	if started.Agent != "mate-shop" {
		t.Fatalf("agent = %q, want mate-shop", started.Agent)
	}

	handle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    started.Agent,
		RawID:   "shop",
		Kind:    harness.KindClaude,
	}
	pane, err := rt.ReadAgent(ctx, handle, 40)
	if err != nil {
		t.Fatalf("ReadAgent: %v", err)
	}
	t.Logf("pane after start:\n%s", harness.StartupScreenTail(pane, 12))
	class, err := harness.ClassifyStartupScreen(harness.KindClaude, pane)
	if err != nil {
		t.Fatal(err)
	}
	if class != harness.StartupScreenReady {
		t.Fatalf("pane classifies as %q, want the Claude composer", class)
	}

	status, err := spawn.MateStatus(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus: %v", err)
	}
	if status.State != spawn.StateRunning {
		t.Fatalf("status = %q, want running", status.Line())
	}
	t.Logf("status: %s", status.Line())

	stopped, err := spawn.StopMate(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if stopped.Agent != started.Agent {
		t.Fatalf("stopped %q, want %q", stopped.Agent, started.Agent)
	}

	// The proof of the stop is Herdr's own inventory, not the stop's answer.
	listed, err := rt.ListAgents(ctx, runtime.SessionHandle{Name: session, ConfigHome: configHome})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	for _, obs := range listed {
		if obs.Handle.Name == started.Agent {
			t.Fatalf("agent %s is still listed after StopMate", started.Agent)
		}
	}
	if _, err := rt.InspectAgent(ctx, handle); !runtime.IsAgentNotFound(err) {
		t.Fatalf("InspectAgent after stop: %v, want agent_not_found", err)
	}
	after, err := spawn.MateStatus(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus after stop: %v", err)
	}
	if after.State != spawn.StateStopped {
		t.Fatalf("status after stop = %q, want stopped", after.Line())
	}
	meta := readMeta(t, w, "shop")
	if meta[spawn.MetaSessionID] != started.SessionID || started.SessionID == "" {
		t.Fatalf("meta session_id = %q, want %q kept for resume", meta[spawn.MetaSessionID], started.SessionID)
	}
	t.Logf("stopped; meta kept session_id=%s", meta[spawn.MetaSessionID])
}

// useLabSession rewrites workspace.yaml's session name to the provisioned
// lab. store.Init derives the name from the workspace path, which a lab run
// must not use: the session already exists and the runner owns it.
func useLabSession(t *testing.T, w *store.Workspace, session string) {
	t.Helper()
	path := w.WorkspaceFile()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	scanner := bufio.NewScanner(f)
	replaced := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "session:") {
			line = "session: " + session
			replaced = true
		}
		out = append(out, line)
	}
	f.Close()
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !replaced {
		t.Fatalf("%s carries no session key", path)
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// binaryPath builds the matev2 binary the rendered manual points at, so the
// live Mate's AGENTS.md names a real executable rather than the test binary.
func binaryPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "matev2")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/nguyenngocanh94/matev2/cmd/matev2")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build matev2: %v\n%s", err, out)
	}
	return bin
}

func liveGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
