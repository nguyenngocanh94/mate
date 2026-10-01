package spawn_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLiveHookMateRoundTrip is the task 08 proof: a real Claude Code Mate,
// started with the hooks StartMate now wires into `.claude/settings.json`,
// records a real prompt/answer turn in sent.log and keeps mate.meta's
// session_id/transcript current, without mate ever reading Herdr's pane
// text to find out.
func TestLiveHookMateRoundTrip(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

	// TMPDIR must not go through a symlink (docs/mvp.md section 7).
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

	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	liveGit(t, repo, "init", "-b", "main")
	liveGit(t, repo, "config", "user.email", "mate-test@example.com")
	liveGit(t, repo, "config", "user.name", "mate test")
	liveGit(t, repo, "commit", "--allow-empty", "-m", "init")
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

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	started, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})
	t.Logf("started agent %s in pane %s (session_id %s)", started.Agent, started.Pane, started.SessionID)

	// The settings file StartMate wrote must be the one this task generates,
	// wired to the same mate binary the Mate's manual names - otherwise a
	// silent fallback to an empty {} would make the rest of this test prove
	// nothing about the real hooks.
	settingsPath := filepath.Join(started.MateDir, spawn.ClaudeSettingsDir, spawn.ClaudeSettingsFile)
	settings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read %s: %v", settingsPath, err)
	}
	if !strings.Contains(string(settings), "hook mate-prompt") || !strings.Contains(string(settings), "hook mate-stop") {
		t.Fatalf("settings.json %s does not wire both hooks", settings)
	}

	handle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    started.Agent,
		RawID:   "shop",
		Kind:    harness.KindClaude,
	}
	if err := rt.PromptAgent(ctx, handle, "say PONG"); err != nil {
		t.Fatalf("PromptAgent: %v", err)
	}

	deadline := time.Now().Add(60 * time.Second)
	var hasUser, hasPong bool
	for time.Now().Before(deadline) {
		entries, _, readErr := w.ReadSent("shop", 0)
		if readErr != nil {
			t.Fatalf("ReadSent: %v", readErr)
		}
		hasUser, hasPong = false, false
		for _, e := range entries {
			if e.Source == store.SourceUser && e.Target == store.TargetMate {
				hasUser = true
			}
			if e.Source == store.SourceMate && strings.Contains(strings.ToUpper(e.Text), "PONG") {
				hasPong = true
			}
		}
		if hasUser && hasPong {
			break
		}
		time.Sleep(time.Second)
	}
	if !hasUser {
		t.Fatal("sent.log never recorded a user entry; the UserPromptSubmit hook did not fire (or mate hook mate-prompt failed)")
	}
	if !hasPong {
		t.Fatal("sent.log never recorded a mate entry with PONG; the Stop hook did not fire (or mate hook mate-stop failed)")
	}
	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("sent.log after the round trip: %+v", entries)

	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatalf("ReadMateMeta: %v", err)
	}
	if meta[spawn.MetaSessionID] != started.SessionID {
		t.Fatalf("meta session_id = %q, want the launched %q", meta[spawn.MetaSessionID], started.SessionID)
	}
	if strings.TrimSpace(meta[spawn.MetaTranscript]) == "" {
		t.Fatal("meta transcript is empty; the Stop hook did not record a transcript path")
	}
	if _, statErr := os.Stat(meta[spawn.MetaTranscript]); statErr != nil {
		t.Fatalf("meta transcript %q is not readable: %v", meta[spawn.MetaTranscript], statErr)
	}
	t.Logf("mate.meta session_id=%s transcript=%s", meta[spawn.MetaSessionID], meta[spawn.MetaTranscript])

	stopped, err := spawn.StopMate(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if stopped.Agent != started.Agent {
		t.Fatalf("stopped %q, want %q", stopped.Agent, started.Agent)
	}
}
