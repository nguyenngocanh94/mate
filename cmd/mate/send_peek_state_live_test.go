package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLiveSendPeekStateCrew is the docs/mvp.md task 13 live proof: a real
// Codex crew in a real Herdr pane, driven end to end through the actual
// `mate send`, `mate peek` and `mate state` commands (cmdSend,
// cmdPeek, cmdState - the same functions main() dispatches to, over
// spawn.LiveDeps()).
//
// The crew's brief asks it to report needs-decision, then wait for an
// answer typed into its pane, then report done. That is exactly the
// firstmate protocol docs/mvp.md section 4 describes and the one this
// task's three commands exist to drive: `mate state` must read the
// crew's own needs-decision as parked, `mate peek` must show the crew's
// pane without waking it, and `mate send "A"` must be the thing that
// unblocks it.
func TestLiveSendPeekStateCrew(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	// TMPDIR must not go through a symlink (docs/mvp.md section 7).
	root := t.TempDir()
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
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
		Harnesses:            harnesses,
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               consoleBinaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	res, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop",
		Crew:    "k3",
		Harness: codex.KindCodex,
		BriefText: brieftest.Ship(`Append needs-decision: pick A or B to the status file and stop. ` +
			`When the Mate answers, append wait-mate: chose <answer> and stop.`),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
	})
	t.Logf("spawned crew %s in pane %s (branch %s)", res.Agent, res.Pane, res.Branch)
	if res.DeliveryWarning != "" {
		t.Logf("delivery warning: %s\n%s", res.DeliveryWarning, res.PaneTail)
	}

	handle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    res.Agent,
		RawID:   "k3",
		Kind:    codex.KindCodex,
	}
	needsDecision := waitForStatusLine(t, ctx, w, "needs-decision:", 120*time.Second, func() string {
		screen, readErr := rt.ReadAgent(ctx, handle, harness.ReadRecentUnwrapped, 40)
		if readErr != nil {
			return "(pane not readable: " + readErr.Error() + ")"
		}
		return harness.StartupScreenTail(screen, 20)
	})
	t.Logf("crew reported:\n%s", needsDecision)

	// `mate state` must read the crew's own needs-decision as parked -
	// but only once the crew has actually stopped its turn. A busy pane
	// outranks a needs-decision status line by design (crewstate.Decide),
	// so right after the append the crew may still be mid-turn finishing
	// up; poll until the pane goes idle before asserting parked.
	line := waitForStateLine(t, root, "state: parked", 60*time.Second)
	t.Logf("mate state: %s", line)

	// `mate peek` must show the crew's pane, without waking it.
	var peekOut, peekErr bytes.Buffer
	if err := cmdPeek([]string{"shop", "k3", "--workspace", root}, &peekOut, &peekErr); err != nil {
		t.Fatalf("mate peek: %v (stderr: %s)", err, peekErr.String())
	}
	t.Logf("mate peek:\n%s", harness.StartupScreenTail(peekOut.String(), 20))
	if strings.TrimSpace(peekOut.String()) == "" {
		t.Fatal("mate peek printed nothing")
	}

	// `mate send "A"` must be the thing that unblocks the crew.
	var sendOut, sendErr bytes.Buffer
	if err := cmdSend([]string{"shop", "k3", "A", "--workspace", root, "--from", "mate"}, &sendOut, &sendErr); err != nil {
		t.Fatalf("mate send: %v (stderr: %s)", err, sendErr.String())
	}
	t.Logf("mate send: %s", strings.TrimSpace(sendOut.String()))

	sent, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatalf("ReadSent: %v", err)
	}
	if len(sent) != 1 || sent[0].Source != store.SourceMate || sent[0].Target != store.CrewTarget("k3") || sent[0].Text != "A" {
		t.Fatalf("sent.log = %+v, want one mate->crew:k3 \"A\" entry", sent)
	}

	done := waitForStatusLine(t, ctx, w, "wait-mate: chose A", 120*time.Second, func() string {
		screen, readErr := rt.ReadAgent(ctx, handle, harness.ReadRecentUnwrapped, 40)
		if readErr != nil {
			return "(pane not readable: " + readErr.Error() + ")"
		}
		return harness.StartupScreenTail(screen, 20)
	})
	t.Logf("crew reported:\n%s", done)

	// `mate state` must now say done, again allowing the pane a beat to
	// leave its busy state before the done status line wins.
	line = waitForStateLine(t, root, "state: done", 60*time.Second)
	t.Logf("mate state (after done): %s", line)

	stopped, err := spawn.StopCrew(ctx, w, deps, "shop", "k3", true)
	if err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if !stopped.TabClosed {
		t.Fatal("StopCrew did not close the crew tab")
	}
}

// waitForStateLine polls `mate state` until its output starts with want,
// failing the test with the last line seen if the budget runs out.
func waitForStateLine(t *testing.T, root, want string, budget time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	var last string
	for {
		var out, errBuf bytes.Buffer
		if err := cmdState([]string{"shop", "k3", "--workspace", root}, &out, &errBuf); err != nil {
			t.Fatalf("mate state: %v (stderr: %s)", err, errBuf.String())
		}
		last = strings.TrimSpace(out.String())
		if strings.HasPrefix(last, want) {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("mate state never said %q within %s; last: %q", want, budget, last)
		}
		time.Sleep(2 * time.Second)
	}
}

// waitForStatusLine polls the crew's status file until it contains want.
func waitForStatusLine(t *testing.T, ctx context.Context, w *store.Workspace, want string, budget time.Duration, pane func() string) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	var text string
	for {
		entries, _, err := w.ReadStatus("shop", "k3", 0)
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
			t.Fatalf("crew k3 did not report %q within %s\nstatus file:\n%s\npane:\n%s", want, budget, text, pane())
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled while waiting for %q; status so far:\n%s", want, text)
		case <-time.After(2 * time.Second):
		}
	}
}
