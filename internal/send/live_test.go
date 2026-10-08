package send_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex/codexlab"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// requireLive gates this package's live proof the same way internal/spawn
// and internal/runtime do. scripts/gotestreport allows TestLive* and nothing
// else to skip.
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("MATE_LIVE") != "1" {
		t.Skip("set MATE_LIVE=1 to run live Herdr proofs")
	}
}

func liveLabSession(t *testing.T) (session, configHome string) {
	t.Helper()
	session = strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_SESSION"))
	if session == "" {
		t.Skip("set MATE_HERDR_LIVE_SESSION to a provisioned fm-lab-* Herdr session")
	}
	if session == "default" || session == "firstmate" {
		t.Fatal("refusing to run a live send against the default or firstmate session")
	}
	if !strings.HasPrefix(session, "fm-lab-") {
		t.Fatalf("live test requires an fm-lab- session, got %q", session)
	}
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		t.Fatal("HOME is required to resolve the Herdr socket")
	}
	// Every live test that reaches a lab session runs Codex in a lab
	// CODEX_HOME, never the operator's ~/.codex (internal/harness/codex/codexlab).
	codexlab.Home(t)
	return session, filepath.Join(home, ".config")
}

// TestLiveSendToClaudeThreeCases is the task 12 proof, against a real Claude
// Code agent in a real Herdr pane:
//
//	(a) an empty composer takes the line and the model answers it - proved
//	    from sent.log, written by the Mate's own Stop hook, never from pane
//	    text;
//	(b) a composer holding a human's half-typed line is refused, and the
//	    half-typed line is still there afterwards, character for character;
//	(c) a slash command, whose completion popup eats an enter sent too soon,
//	    lands and produces its output.
func TestLiveSendToClaudeThreeCases(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

	// TMPDIR must not go through a symlink (docs/mvp.md section 7).
	root := t.TempDir()
	w, err := store.Init(root, store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	useLabSession(t, w, session)
	w, err = store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	repo := filepath.Join(w.ProjectHome("shop"), "shop")
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
	spawnDeps := spawn.Deps{
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

	started, err := spawn.StartMate(ctx, w, spawnDeps, spawn.StartRequest{Project: "shop", Harness: claude.KindClaude})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopMate(stopCtx, w, spawnDeps, "shop")
	})
	t.Logf("started agent %s in pane %s (session_id %s)", started.Agent, started.Pane, started.SessionID)

	handle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    started.Agent,
		RawID:   "shop",
		Kind:    claude.KindClaude,
	}
	deps := send.Deps{Harnesses: catalog.Default(), Runtime: rt}

	// (a) An empty composer takes the line.
	report, err := send.Send(ctx, deps, handle, claude.KindClaude, "say PONG", send.Options{WaitForWorking: true})
	logReport(t, "case a", report)
	if err != nil {
		t.Fatalf("Send into an empty composer: %v", err)
	}
	if report.Before.State != send.StateEmpty {
		t.Fatalf("classification before = %q, want empty", report.Before.State)
	}
	if !report.Delivered() {
		t.Fatal("the send did not confirm the composer cleared")
	}
	waitForPong(t, w)

	// (b) A half-typed human line is never typed over. The text goes in with
	// send-text alone, which is what a person typing looks like: characters
	// in the composer and no enter.
	waitForComposer(ctx, t, rt, handle, send.StateEmpty)
	const halfTyped = "half typed"
	if err := rt.SendText(ctx, handle, halfTyped); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	time.Sleep(time.Second)
	refused, err := send.Send(ctx, deps, handle, claude.KindClaude, "say PONG AGAIN", send.Options{})
	logReport(t, "case b", refused)
	if !errors.Is(err, send.ErrComposerPending) {
		t.Fatalf("Send over a half-typed line: err = %v, want ErrComposerPending", err)
	}
	if refused.Before.Pending != halfTyped {
		t.Fatalf("refusal reported pending %q, want %q", refused.Before.Pending, halfTyped)
	}
	if refused.Typed {
		t.Fatal("the refused send typed into the pane anyway")
	}
	screen, err := rt.ReadAgent(ctx, handle, harness.ReadRecentUnwrapped, 40)
	if err != nil {
		t.Fatal(err)
	}
	still := send.ClassifyComposer(claude.Claude{}.Screen(), screen)
	if still.State != send.StatePending || still.Pending != halfTyped {
		t.Fatalf("after the refusal the composer holds %q (%s), want exactly %q untouched", still.Pending, still.State, halfTyped)
	}
	t.Logf("case b: composer still holds %q after the refusal", still.Pending)

	// Clear the human's line with Claude's own clear-line key, then prove
	// the composer really is empty again before case (c) types into it.
	if err := rt.SendKeys(ctx, handle, []string{"ctrl+u"}); err != nil {
		t.Fatalf("clear line: %v", err)
	}
	waitForComposer(ctx, t, rt, handle, send.StateEmpty)

	// (c) A slash command: the completion popup swallows an enter sent too
	// soon, which is what Options.Settle's 1200ms for a `/` line is for.
	slash, err := send.Send(ctx, deps, handle, claude.KindClaude, "/help", send.Options{})
	logReport(t, "case c", slash)
	if err != nil {
		t.Fatalf("Send of a slash command: %v", err)
	}
	if slash.Settled != send.SlashSettle {
		t.Fatalf("slash settle = %s, want %s", slash.Settled, send.SlashSettle)
	}
	deadline := time.Now().Add(60 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		last, err = rt.ReadAgent(ctx, handle, harness.ReadRecentUnwrapped, 40)
		if err != nil {
			t.Fatal(err)
		}
		if helpOnScreen(last) {
			break
		}
		time.Sleep(time.Second)
	}
	if !helpOnScreen(last) {
		t.Fatalf("/help produced no help output; pane tail:\n%s", send.ScreenTail(last, 20))
	}
	t.Logf("case c: /help output on screen:\n%s", send.ScreenTail(last, 14))
}

// helpOnScreen recognises Claude Code's /help panel. The markers are
// measured from the panel this test drew on 2026-09-17 (Claude Code
// 2.1.274); several are accepted because the wording is the harness's, not
// mate's, and one line moving must not fail the send this test is proving.
func helpOnScreen(screen string) bool {
	lower := strings.ToLower(screen)
	for _, marker := range []string{
		"help  general",
		"for more help:",
		"shortcuts",
		"for commands",
		"custom commands",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// waitForComposer blocks until the pane classifies as want, so the next step
// starts from a state this test proved rather than assumed.
func waitForComposer(ctx context.Context, t *testing.T, rt *runtime.Herdr, handle runtime.AgentHandle, want send.ComposerState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	var last send.Classification
	for time.Now().Before(deadline) {
		screen, err := rt.ReadAgent(ctx, handle, harness.ReadRecentUnwrapped, 40)
		if err != nil {
			t.Fatalf("ReadAgent: %v", err)
		}
		last = send.ClassifyComposer(screenOf(handle.Kind), screen)
		if last.State == want {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("pane never reached %q; last was %q (%s)", want, last.State, last.Evidence)
}

// waitForPong reads the Mate's own sent.log, written by its Stop hook. The
// pane is never consulted: pane text is what this whole package exists to
// avoid trusting for delivery.
func waitForPong(t *testing.T, w *store.Workspace) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		entries, _, err := w.ReadSent("shop", 0)
		if err != nil {
			t.Fatalf("ReadSent: %v", err)
		}
		for _, e := range entries {
			if e.Source == store.SourceMate && strings.Contains(strings.ToUpper(e.Text), "PONG") {
				t.Logf("case a: sent.log records the mate answering %q", e.Text)
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatal("sent.log never recorded a mate entry with PONG; the line typed by send never reached the model")
}

// useLabSession rewrites workspace.yaml's session name to the provisioned
// lab, which the runner owns.
func useLabSession(t *testing.T, w *store.Workspace, session string) {
	t.Helper()
	path := w.WorkspaceFile()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	replaced := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "session:") {
			line = "session: " + session
			replaced = true
		}
		out = append(out, line)
	}
	if !replaced {
		t.Fatalf("%s carries no session key", path)
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// binaryPath builds the mate binary the Mate's hooks and manual point at.
func binaryPath(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mate")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/nguyenngocanh94/mate/cmd/mate")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build mate: %v\n%s", err, out)
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

func logReport(t *testing.T, label string, r send.Report) {
	t.Helper()
	var b strings.Builder
	b.WriteString(label + ": ")
	b.WriteString("text=" + r.Text)
	for _, s := range r.Steps {
		b.WriteString(" | " + s.What)
		if s.State != "" {
			b.WriteString("=" + string(s.State))
		}
		if s.Evidence != "" {
			b.WriteString(" <" + s.Evidence + ">")
		}
		if s.Detail != "" {
			b.WriteString(" (" + s.Detail + ")")
		}
	}
	for _, warn := range r.Warnings {
		b.WriteString(" | warning: " + warn)
	}
	t.Log(b.String())
}
