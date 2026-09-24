package spawn_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// Task 35's Codex measurements (docs/mvp.md section 7): whether codex-cli's
// TUI fires SessionStart in a Herdr pane and puts the hook's output in
// context (A3), and whether `codex resume <id>` brings a Codex Mate back to
// the same conversation (A4, B11).

// codexAnswers returns every task_complete message a Codex rollout holds, in
// order: one per finished turn.
func codexAnswers(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 16<<20), 16<<20)
	for sc.Scan() {
		var rec struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Message string `json:"last_agent_message"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if rec.Type == "event_msg" && rec.Payload.Type == "task_complete" {
			out = append(out, rec.Payload.Message)
		}
	}
	return out
}

// waitCodexAnswer waits for the rollout to hold more than `seen` finished
// turns, the newest with a non-empty answer (a /compact finishes a turn with
// no message), and returns that answer.
func waitCodexAnswer(t *testing.T, path string, seen int, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if got := codexAnswers(t, path); len(got) > seen && strings.TrimSpace(got[len(got)-1]) != "" {
			t.Logf("codex answer #%d: %q", len(got), got[len(got)-1])
			return got[len(got)-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited %s for Codex turn #%d in %s", timeout, seen+1, path)
		}
		time.Sleep(time.Second)
	}
}

// TestLiveSpawnMateResumeRemembersCodex is B11: a Codex Mate stopped with
// StopMate and started again resumes the same Codex session through `codex
// resume <id>` and remembers a word from before the restart. It runs in a
// lab CODEX_HOME, which has no Herdr Codex integration, so the session id is
// the one the Mate's own SessionStart hook records in mate.meta at the first
// prompt (task 37), and the stop keeps it.
func TestLiveSpawnMateResumeRemembersCodex(t *testing.T) {
	lab := newLiveLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	started, err := spawn.StartMate(ctx, lab.w, lab.deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex, Resume: true})
	if err != nil {
		t.Fatalf("StartMate (first): %v", err)
	}
	if started.Resumed || started.SessionID != "" {
		t.Fatalf("a first Codex start is fresh with no session id yet; got resumed=%v id=%q", started.Resumed, started.SessionID)
	}
	h := lab.handle(started)
	if err := lab.rt.PromptAgent(ctx, h, "remember the word ZEBRA and reply OK"); err != nil {
		t.Fatalf("PromptAgent (plant): %v", err)
	}

	// The Mate's SessionStart hook runs at the first prompt, not at
	// launch, and records the rollout id in mate.meta.
	var ref string
	deadline := time.Now().Add(2 * time.Minute)
	for ref == "" {
		ref = readMeta(t, lab.w, "shop")[spawn.MetaSessionID]
		if ref == "" {
			if time.Now().After(deadline) {
				t.Fatal("the Mate's SessionStart hook never recorded the Codex session id in mate.meta")
			}
			time.Sleep(time.Second)
		}
	}
	sessions, err := harness.CodexSessionsDir("")
	if err != nil {
		t.Fatal(err)
	}
	var rollout string
	for rollout == "" {
		if p, ok := harness.CodexRolloutPath(sessions, ref); ok {
			rollout = p
		} else if time.Now().After(deadline) {
			t.Fatalf("no rollout for session %s under %s", ref, sessions)
		} else {
			time.Sleep(time.Second)
		}
	}
	t.Logf("first start: agent %s, session %s, rollout %s", started.Agent, ref, rollout)
	waitCodexAnswer(t, rollout, 0, 4*time.Minute)

	stopped, err := spawn.StopMate(ctx, lab.w, lab.deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if stopped.SessionID != ref || readMeta(t, lab.w, "shop")[spawn.MetaSessionID] != ref {
		t.Fatalf("stop recorded session %q (meta %q), want the hook's %q", stopped.SessionID, readMeta(t, lab.w, "shop")[spawn.MetaSessionID], ref)
	}

	resumed, err := spawn.StartMate(ctx, lab.w, lab.deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex, Resume: true})
	if err != nil {
		t.Fatalf("StartMate (resume): %v", err)
	}
	t.Logf("resumed start: agent %s pane %s resumed=%v from=%s note=%q trust=%v update=%v",
		resumed.Agent, resumed.Pane, resumed.Resumed, resumed.ResumedFrom, resumed.ResumeNote, resumed.TrustDialog, resumed.UpdateDialog)
	if !resumed.Resumed || resumed.ResumedFrom != ref || resumed.SessionID != ref {
		t.Fatalf("second start did not resume %s: resumed=%v note=%q", ref, resumed.Resumed, resumed.ResumeNote)
	}
	rh := lab.handle(resumed)
	pane, err := lab.rt.ReadAgent(ctx, rh, 40)
	if err != nil {
		t.Fatalf("ReadAgent after resume: %v", err)
	}
	if class, err := harness.ClassifyStartupScreen(harness.KindCodex, pane); err != nil || class != harness.StartupScreenReady {
		t.Fatalf("resumed pane classifies as %q (%v); pane:\n%s", class, err, pane)
	}

	before := len(codexAnswers(t, rollout))
	if err := lab.rt.PromptAgent(ctx, rh, "what word did I ask you to remember? answer with the word only"); err != nil {
		t.Fatalf("PromptAgent (recall): %v", err)
	}
	recall := waitCodexAnswer(t, rollout, before, 4*time.Minute)
	if !strings.Contains(strings.ToUpper(recall), "ZEBRA") {
		t.Fatalf("the resumed Codex Mate answered %q, want ZEBRA", recall)
	}
	if got := readMeta(t, lab.w, "shop")[spawn.MetaSessionID]; got != ref {
		t.Fatalf("after the resumed turn mate.meta records session %q, want the same %q", got, ref)
	}

	final, err := spawn.StopMate(ctx, lab.w, lab.deps, "shop")
	if err != nil {
		t.Fatalf("final StopMate: %v", err)
	}
	if final.SessionID != ref {
		t.Fatalf("final stop recorded %q, want %q kept", final.SessionID, ref)
	}
}

// codexLabHome is the test's lab CODEX_HOME (codexlab.Home, which
// liveLabSession installed) with a global SessionStart canary hook added.
// Nothing is written to the operator's ~/.codex.
func codexLabHome(t *testing.T) (home, globalLog string) {
	t.Helper()
	home, err := harness.LaunchCodexHome("")
	if err != nil {
		t.Fatalf("no lab CODEX_HOME: %v", err)
	}
	hookDir := t.TempDir()
	globalLog = filepath.Join(hookDir, "session-start.log")
	writeHooksJSON(t, filepath.Join(home, "hooks.json"), writeCanaryHook(t, hookDir, globalLog, "HERON-global"))
	return home, globalLog
}

func writeHooksJSON(t *testing.T, path, command string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 10}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// codexLabAgent launches Codex (fresh, or resuming resumeID) in a new tab of
// a lab workspace whose panes carry CODEX_HOME=home.
func codexLabAgent(t *testing.T, ctx context.Context, lab liveLab, home, cwd, resumeID string, n int) runtime.AgentHandle {
	t.Helper()
	session := runtime.SessionHandle{Name: lab.session, ConfigHome: lab.configHome}
	ws, err := lab.rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: session, Label: "codexlab", Cwd: cwd,
		Env: []runtime.EnvVar{{Key: "CODEX_HOME", Value: home}},
	})
	if err != nil {
		t.Fatalf("EnsureProjectWorkspace: %v", err)
	}
	tab, err := lab.rt.CreateAgentTab(ctx, runtime.TabSpec{Workspace: ws, Label: "codexlab", Cwd: cwd})
	if err != nil {
		t.Fatalf("CreateAgentTab: %v", err)
	}
	launch, err := harness.Codex{}.BuildLaunchSpec(ctx, harness.AgentSpec{
		ID: "codexlab", Kind: harness.KindCodex, Cwd: cwd, ResumeSessionID: resumeID,
		Config: harness.Config{Kind: harness.KindCodex, CodexHome: home},
	})
	if err != nil {
		t.Fatalf("BuildLaunchSpec: %v", err)
	}
	reservation, err := runtime.AllocateAgentName(lab.deps.Names, session.Name, "codexlab", fmt.Sprint(n), runtime.FailOnCollision)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := runtime.NewAgentStartSpec(tab, reservation, launch, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	h, err := lab.rt.StartAgent(ctx, spec)
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	h.Kind = harness.KindCodex
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = lab.rt.StopAgent(stopCtx, h, runtime.StopForce)
	})
	settleCodexLab(t, ctx, lab, h)
	return h
}

// settleCodexLab answers the lab's startup dialogs one key at a time, with a
// re-read between presses: the directory-trust dialog through the product's
// own answer, and the hook-trust review (which mate does not answer, and
// the test does only because the hooks are its own) with "2. Trust all and
// continue", which writes trust into the lab CODEX_HOME only.
func settleCodexLab(t *testing.T, ctx context.Context, lab liveLab, h runtime.AgentHandle) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	sawReview := false
	for {
		screen, err := lab.rt.ReadAgent(ctx, h, 40)
		if err != nil {
			t.Fatalf("ReadAgent: %v", err)
		}
		class, err := harness.ClassifyStartupScreen(harness.KindCodex, screen)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case class == harness.StartupScreenReady:
			return
		case class == harness.StartupScreenTrustDialog:
			answer, err := harness.TrustDialogAnswerFor(harness.KindCodex)
			if err != nil {
				t.Fatal(err)
			}
			for _, k := range append(answer.SelectKeys, answer.ConfirmKey) {
				if err := lab.rt.SendKeys(ctx, h, []string{k}); err != nil {
					t.Fatal(err)
				}
				time.Sleep(500 * time.Millisecond)
			}
			t.Log("answered the directory-trust dialog")
		case strings.Contains(screen, "Hooks need review") && !sawReview:
			sawReview = true
			t.Logf("hook-trust review on screen:\n%s", harness.StartupScreenTail(screen, 10))
			if err := lab.rt.SendKeys(ctx, h, []string{"down"}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
			screen, err = lab.rt.ReadAgent(ctx, h, 40)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(screen, "› 2. Trust all and continue") {
				t.Fatalf("highlight is not on Trust all:\n%s", harness.StartupScreenTail(screen, 10))
			}
			if err := lab.rt.SendKeys(ctx, h, []string{"enter"}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
		case time.Now().After(deadline):
			t.Fatalf("Codex lab startup did not settle:\n%s", harness.StartupScreenTail(screen, 15))
		default:
			time.Sleep(250 * time.Millisecond)
		}
	}
}

// codexCanaryTurn prompts the lab Codex and returns its answer from the
// rollout the latest SessionStart payload names.
func codexCanaryTurn(t *testing.T, ctx context.Context, lab liveLab, h runtime.AgentHandle, logPath string, wantStarts int) (sessionStartEntry, string) {
	t.Helper()
	const question = "Without running any tool: list every session canary word in your context, or NONE, and nothing else."
	var seen int
	if starts := readSessionStarts(t, logPath); len(starts) > 0 {
		seen = len(codexAnswers(t, starts[len(starts)-1].TranscriptPath))
	}
	if err := lab.rt.PromptAgent(ctx, h, question); err != nil {
		t.Fatalf("PromptAgent: %v", err)
	}
	starts := waitSessionStart(t, logPath, wantStarts, 2*time.Minute)
	last := starts[len(starts)-1]
	if wantStarts > 1 && last.SessionID != starts[len(starts)-2].SessionID {
		seen = 0 // a new session writes a new rollout
	}
	return last, waitCodexAnswer(t, last.TranscriptPath, seen, 3*time.Minute)
}

// TestLiveCodexSessionStartHook is A3: codex-cli's TUI in a Herdr pane runs
// SessionStart hooks - from CODEX_HOME/hooks.json and from the cwd's
// .codex/hooks.json - with source startup, resume, compact and clear, at the
// first prompt after each rather than at launch, and their stdout reaches
// the model. It runs in a lab CODEX_HOME so the operator's hooks and trust
// are never touched.
func TestLiveCodexSessionStartHook(t *testing.T) {
	lab := newLiveLab(t)
	home, globalLog := codexLabHome(t)
	cwd := filepath.Join(lab.w.Root(), "codexlab")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harness.CodexInstructionPath(cwd), []byte("# Lab\n\nYou are a lab agent. Keep every reply to one line.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectHookDir := t.TempDir()
	projectLog := filepath.Join(projectHookDir, "session-start.log")
	writeHooksJSON(t, filepath.Join(cwd, ".codex", "hooks.json"), writeCanaryHook(t, projectHookDir, projectLog, "HERON-project"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	h := codexLabAgent(t, ctx, lab, home, cwd, "", 1)
	time.Sleep(3 * time.Second)
	if got := readSessionStarts(t, globalLog); len(got) != 0 {
		t.Fatalf("SessionStart ran before any prompt: %+v", got)
	}
	start, answer := codexCanaryTurn(t, ctx, lab, h, globalLog, 1)
	if start.Source != "startup" {
		t.Fatalf("first SessionStart = %+v", start)
	}
	requireCanary(t, answer, "HERON-global-startup")
	requireCanary(t, answer, "HERON-project-startup")
	if p := readSessionStarts(t, projectLog); len(p) != 1 || p[0].SessionID != start.SessionID {
		t.Fatalf("project hook log = %+v", p)
	}

	// Resume: the pane is closed (Codex has no graceful stop) and the same
	// session is resumed in a new tab.
	if err := lab.rt.StopAgent(ctx, h, runtime.StopForce); err != nil {
		t.Fatalf("StopAgent: %v", err)
	}
	h = codexLabAgent(t, ctx, lab, home, cwd, start.SessionID, 2)
	resume, answer := codexCanaryTurn(t, ctx, lab, h, globalLog, 2)
	if resume.Source != "resume" || resume.SessionID != start.SessionID {
		t.Fatalf("SessionStart after codex resume = %+v, want resume of %s", resume, start.SessionID)
	}
	requireCanary(t, answer, "HERON-global-resume")

	sendCodexSlash(t, ctx, lab, h, "/compact")
	time.Sleep(20 * time.Second)
	compact, answer := codexCanaryTurn(t, ctx, lab, h, globalLog, 3)
	if compact.Source != "compact" || compact.SessionID != start.SessionID {
		t.Fatalf("SessionStart after /compact = %+v", compact)
	}
	requireCanary(t, answer, "HERON-global-compact")

	sendCodexSlash(t, ctx, lab, h, "/clear")
	time.Sleep(3 * time.Second)
	clear, answer := codexCanaryTurn(t, ctx, lab, h, globalLog, 4)
	if clear.Source != "clear" || clear.SessionID == start.SessionID {
		t.Fatalf("SessionStart after /clear = %+v, want clear with a new session id", clear)
	}
	requireCanary(t, answer, "HERON-global-clear")
	if p := readSessionStarts(t, projectLog); len(p) != 4 {
		t.Fatalf("project hook fired %d times, want 4: %+v", len(p), p)
	}
}

func sendCodexSlash(t *testing.T, ctx context.Context, lab liveLab, h runtime.AgentHandle, cmd string) {
	t.Helper()
	report, err := send.Send(ctx, send.Deps{Runtime: lab.rt}, h, harness.KindCodex, cmd, send.Options{})
	if err != nil {
		t.Fatalf("send %s: %v (report %+v)", cmd, err, report)
	}
}
