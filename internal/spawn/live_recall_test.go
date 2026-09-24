package spawn_test

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/config"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/memory"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
)

// Task 37's live proofs of the SessionStart digest (docs/mvp.md M8): the
// hook matev2 installs prints `matev2 recall`, and after a compaction the
// Mate answers from the digest the hook printed again, not from what it
// remembered of the one before.

const canaryQuestion = "Without running any tool or reading any file: what is the deploy canary word in the memory.md of the most recent matev2 recall digest in your context? Answer with that word only, or NONE."

// seedCanary writes a memory.md whose one lesson names word.
func seedCanary(t *testing.T, path, word string) {
	t.Helper()
	e, err := memory.NewEntry(memory.LessonsSection, "The deploy canary word is "+word+".", "captain, 2026-09-24", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(memory.Append(memory.Header, memory.LessonsSection, e)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// transcriptLineWith waits for a transcript line holding every needle.
func transcriptLineWith(t *testing.T, path string, timeout time.Duration, needles ...string) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if data, err := os.ReadFile(path); err == nil {
			sc := bufio.NewScanner(bytes.NewReader(data))
			sc.Buffer(make([]byte, 16<<20), 16<<20)
			for sc.Scan() {
				line := sc.Text()
				all := true
				for _, n := range needles {
					if !strings.Contains(line, n) {
						all = false
						break
					}
				}
				if all {
					return line
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited %s for a line of %s holding %q", timeout, path, needles)
		}
		time.Sleep(time.Second)
	}
}

// TestLiveMateRecallOnCompact: a Claude Mate started the product's way gets
// the digest at startup, and again after /compact. The canary in memory.md
// changes between the two, so the answer after the compaction can only come
// from the digest the compact hook printed.
func TestLiveMateRecallOnCompact(t *testing.T) {
	lab := newLiveLab(t)
	memoryFile := lab.w.MemoryFile("shop")
	seedCanary(t, memoryFile, "PELICAN-SOUTH")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	started, err := spawn.StartMate(ctx, lab.w, lab.deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	h := lab.handle(started)
	answer, cursor := lab.askClaudeMate(t, ctx, h, 0, canaryQuestion)
	requireCanary(t, answer, "PELICAN-SOUTH")
	transcript := readMeta(t, lab.w, "shop")[spawn.MetaTranscript]
	if transcript == "" {
		t.Fatal("the Stop hook recorded no transcript")
	}
	transcriptLineWith(t, transcript, time.Second, `"hookName":"SessionStart:startup"`, "# matev2 recall shop", "PELICAN-SOUTH")

	seedCanary(t, memoryFile, "OSPREY-NORTH")
	sendSlash(t, ctx, lab, h, "/compact")
	line := transcriptLineWith(t, transcript, 5*time.Minute, `"hookName":"SessionStart:compact"`, "# matev2 recall shop")
	if !strings.Contains(line, "OSPREY-NORTH") || !strings.Contains(line, "session start: compact") {
		t.Fatalf("the compact hook's output lacks the new canary: %.600s", line)
	}
	answer, _ = lab.askClaudeMate(t, ctx, h, cursor, canaryQuestion)
	requireCanary(t, answer, "OSPREY-NORTH")
	if got := readMeta(t, lab.w, "shop")[spawn.MetaSessionID]; got != started.SessionID {
		t.Fatalf("mate.meta session_id = %q after compact, want %q (compact keeps the id)", got, started.SessionID)
	}
}

// TestLiveCodexMateRecallHook: a Codex Mate started the product's way
// installs `mate/.codex/hooks.json`, the startup settle walks Codex's hook
// review and trusts that hook and nothing else, the hook runs at the first
// prompt and puts the digest in context, records the rollout id in
// mate.meta, and after a /compact the Mate answers from the digest printed
// again. It runs in a lab CODEX_HOME, so the operator's hooks, trust and
// rollouts are neither read nor written; it launches Codex exactly once for
// that reason (see the note before /compact).
func TestLiveCodexMateRecallHook(t *testing.T) {
	lab := newLiveLab(t)
	home, err := os.MkdirTemp(os.Getenv("TMPDIR"), "codexhome-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	userHome, err := harness.EffectiveCodexHome("")
	if err != nil {
		t.Fatal(err)
	}
	auth, err := os.ReadFile(filepath.Join(userHome, "auth.json"))
	if err != nil {
		t.Fatalf("no Codex auth to copy into a lab CODEX_HOME: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), auth, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[features]\nhooks = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	lab.deps.CodexSessionsDir = filepath.Join(home, "sessions")

	// The Mate's pane takes its environment from the workspace create
	// (docs/mvp.md section 7, task 22), so the lab creates the project's
	// workspace first, with CODEX_HOME and the identity StartMate would
	// have given it, and StartMate adopts it.
	mateDir := lab.w.MateDir("shop")
	if err := os.MkdirAll(mateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	session := runtime.SessionHandle{Name: lab.session, ConfigHome: lab.configHome}
	if _, err := lab.rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: session, Label: "shop", Cwd: mateDir,
		Env: []runtime.EnvVar{
			{Key: "CODEX_HOME", Value: home},
			{Key: config.EnvProjectID, Value: "shop"},
			{Key: config.EnvCaller, Value: spawn.CallerMate},
		},
	}); err != nil {
		t.Fatalf("EnsureProjectWorkspace: %v", err)
	}

	seedCanary(t, lab.w.MemoryFile("shop"), "HERON-EAST")
	started, err := spawn.StartMate(ctx, lab.w, lab.deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if !started.HooksTrusted || !started.TrustDialog {
		t.Fatalf("first start: trust dialog %v, hooks trusted %v; want both answered in a fresh CODEX_HOME", started.TrustDialog, started.HooksTrusted)
	}
	h := lab.handle(started)
	if err := lab.rt.PromptAgent(ctx, h, canaryQuestion); err != nil {
		t.Fatalf("PromptAgent: %v", err)
	}
	// The hook runs at that first prompt and records the rollout id.
	var id string
	for deadline := time.Now().Add(2 * time.Minute); id == ""; {
		id = readMeta(t, lab.w, "shop")[spawn.MetaSessionID]
		if id == "" {
			if time.Now().After(deadline) {
				t.Fatal("the SessionStart hook never recorded the Codex session id in mate.meta")
			}
			time.Sleep(time.Second)
		}
	}
	rollout := readMeta(t, lab.w, "shop")[spawn.MetaTranscript]
	if p, ok := harness.CodexRolloutPath(lab.deps.CodexSessionsDir, id); !ok || p != rollout {
		t.Fatalf("mate.meta transcript %q, rollout for %s %q (found %v)", rollout, id, p, ok)
	}
	answer := waitCodexAnswer(t, rollout, 0, 3*time.Minute)
	requireCanary(t, answer, "HERON-EAST")
	if !strings.Contains(transcriptLineWith(t, rollout, time.Second, "# matev2 recall shop", "session start: startup"), "HERON-EAST") {
		t.Fatal("the startup digest in the rollout lacks the canary")
	}
	if strings.Contains(transcriptLineWith(t, rollout, time.Second, "# matev2 recall shop"), "truncated output") {
		t.Fatal("Codex truncated the digest despite additionalContextLimit")
	}

	// Codex recorded the trust matev2 gave its hook in the lab CODEX_HOME,
	// under the Mate's own hooks file, so the next launch draws no review.
	// There is deliberately no second launch here: a pane's environment
	// comes only from its workspace's create, and StopMate closes that
	// workspace, so a relaunch would run in the operator's own CODEX_HOME.
	cfg, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), `[hooks.state."`+spawn.CodexHooksPath(mateDir)+`:session_start:`) {
		t.Fatalf("the lab config.toml records no trust for %s:\n%s", spawn.CodexHooksPath(mateDir), cfg)
	}

	// /compact: the hook runs again at the next prompt with the digest as
	// it is now.
	seedCanary(t, lab.w.MemoryFile("shop"), "HERON-WEST")
	seen := len(codexAnswers(t, rollout))
	sendCodexSlash(t, ctx, lab, h, "/compact")
	// The compaction is a turn of its own, finished with no message.
	for deadline := time.Now().Add(4 * time.Minute); len(codexAnswers(t, rollout)) <= seen; {
		if time.Now().After(deadline) {
			t.Fatal("the /compact turn never finished")
		}
		time.Sleep(time.Second)
	}
	seen = len(codexAnswers(t, rollout))
	if err := lab.rt.PromptAgent(ctx, h, canaryQuestion); err != nil {
		t.Fatalf("PromptAgent after /compact: %v", err)
	}
	answer = waitCodexAnswer(t, rollout, seen, 4*time.Minute)
	requireCanary(t, answer, "HERON-WEST")
	transcriptLineWith(t, rollout, time.Second, "# matev2 recall shop", "session start: compact", "HERON-WEST")
}
