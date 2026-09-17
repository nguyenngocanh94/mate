package spawn_test

import (
	"context"
	"os"
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

// TestLiveSpawnMateResumeRemembers is the task 10 proof: a stopped Mate
// restarted with StartMate's default (Resume: true) actually resumes the
// same Claude conversation, not merely the same `mate.meta` bookkeeping.
// The word ZEBRA is planted in one session and recalled after a real stop
// and restart, through --resume rather than --session-id.
func TestLiveSpawnMateResumeRemembers(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	// 1. First start: a fresh session, nothing to resume yet.
	started, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude, Resume: true})
	if err != nil {
		t.Fatalf("StartMate (first): %v", err)
	}
	if started.Resumed {
		t.Fatal("the first start of this project has nothing to resume")
	}
	t.Logf("first start: agent %s pane %s session_id %s", started.Agent, started.Pane, started.SessionID)
	firstHandle := func() runtime.AgentHandle {
		return runtime.AgentHandle{
			Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
			Name:    started.Agent,
			RawID:   "shop",
			Kind:    harness.KindClaude,
		}
	}()

	if err := rt.PromptAgent(ctx, firstHandle, "remember the word ZEBRA and reply OK"); err != nil {
		t.Fatalf("PromptAgent (plant): %v", err)
	}

	// Wait for the Stop hook's own entry (Source: mate), not merely the
	// UserPromptSubmit one: stopping before Claude has actually answered
	// would race the turn this test is trying to plant.
	plantCursor := waitForMateEntry(t, w, 0, 90*time.Second, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate
	})
	t.Logf("sent.log after planting ZEBRA, cursor=%d", plantCursor)

	// 2. Stop. mate.meta must keep session_id for the resume.
	stopped, err := spawn.StopMate(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if stopped.SessionID != started.SessionID || stopped.SessionID == "" {
		t.Fatalf("stopped session_id = %q, want %q kept for resume", stopped.SessionID, started.SessionID)
	}

	// 3. Restart: must resume, not start fresh.
	resumed, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude, Resume: true})
	if err != nil {
		t.Fatalf("StartMate (resume): %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})
	t.Logf("resumed start: agent %s pane %s session_id %s resumed=%v resumed_from=%s",
		resumed.Agent, resumed.Pane, resumed.SessionID, resumed.Resumed, resumed.ResumedFrom)
	if !resumed.Resumed {
		t.Fatalf("second start did not resume; ResumeNote=%q", resumed.ResumeNote)
	}
	if resumed.SessionID != started.SessionID {
		t.Fatalf("resumed session_id = %q, want the original %q", resumed.SessionID, started.SessionID)
	}

	resumedHandle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    resumed.Agent,
		RawID:   "shop",
		Kind:    harness.KindClaude,
	}
	pane, err := rt.ReadAgent(ctx, resumedHandle, 60)
	if err != nil {
		t.Fatalf("ReadAgent after resume: %v", err)
	}
	t.Logf("pane after resume:\n%s", pane)
	class, err := harness.ClassifyStartupScreen(harness.KindClaude, pane)
	if err != nil {
		t.Fatalf("ClassifyStartupScreen after resume: %v (pane: %s)", err, pane)
	}
	if class != harness.StartupScreenReady {
		t.Fatalf("resumed pane classifies as %q (not the composer); pane:\n%s", class, pane)
	}

	// 4. Ask the resumed conversation what it was told to remember.
	if err := rt.PromptAgent(ctx, resumedHandle, "what word did I ask you to remember? answer with the word only"); err != nil {
		t.Fatalf("PromptAgent (recall): %v", err)
	}

	var recallText string
	waitForMateEntry(t, w, plantCursor, 90*time.Second, func(e store.SentEntry) bool {
		if e.Source != store.SourceMate {
			return false
		}
		recallText = e.Text
		return true
	})
	if !strings.Contains(strings.ToUpper(recallText), "ZEBRA") {
		pane, readErr := rt.ReadAgent(ctx, resumedHandle, 60)
		if readErr != nil {
			pane = "(ReadAgent failed: " + readErr.Error() + ")"
		}
		t.Fatalf("resumed Mate did not recall ZEBRA; last mate sent.log entry: %q; pane:\n%s", recallText, pane)
	}
	t.Logf("resumed Mate recalled: %q", recallText)

	stoppedFinal, err := spawn.StopMate(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("final StopMate: %v", err)
	}
	if stoppedFinal.Agent != resumed.Agent {
		t.Fatalf("stopped %q, want %q", stoppedFinal.Agent, resumed.Agent)
	}
}

// waitForMateEntry polls sent.log from offset `from` until a new entry
// satisfies `match`, returning the cursor just past the entry it accepted.
// It fails the test on timeout, printing every entry seen meanwhile.
func waitForMateEntry(t *testing.T, w *store.Workspace, from int64, timeout time.Duration, match func(store.SentEntry) bool) int64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var seen []store.SentEntry
	for time.Now().Before(deadline) {
		entries, next, err := w.ReadSent("shop", from)
		if err != nil {
			t.Fatalf("ReadSent: %v", err)
		}
		for _, e := range entries {
			seen = append(seen, e)
			if match(e) {
				return next
			}
		}
		if len(entries) > 0 {
			from = next
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("timed out waiting for a matching sent.log entry; saw: %+v", seen)
	return from
}
