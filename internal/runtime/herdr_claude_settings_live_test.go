package runtime_test

// This file is a throwaway proof, not a permanent regression test. It exists
// to settle ADR 0016's AssumptionClaudeArgsThroughHerdr the hard way: an
// EFFECT the generated --settings file demonstrably causes inside the real
// Claude child, not the outer argv Herdr was handed (that half is already
// covered by TestLiveHerdrSessionWorkspaceTabStart). The test injects a stale
// CLAUDE_CONFIG_DIR on the pane the agent will start in - its own `workspace
// create --env`, whose root pane CreateAgentTab then reuses - so this also
// proves default-account cleanup. It does NOT rely on the lab server carrying
// the value: the server's environment is not something this test controls.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
)

// TestLiveHerdrClaudeStopHookFiresWithRealPayload starts a real Claude child
// through a real Herdr pane in the provisioned fm-lab-* session, with a
// generated --settings file whose Stop hook writes the hook's own stdin JSON
// to a marker file outside the harness's own bookkeeping. If Herdr silently
// drops --settings (or Claude ignores it) the marker never appears and the
// test times out and fails; if it appears, its session_id must equal the
// UUID we generated and passed via --session-id, which is the strongest
// available proof that the settings file - and therefore a Stop hook
// configured through it - actually reaches the running child.
func TestLiveHerdrClaudeStopHookFiresWithRealPayload(t *testing.T) {
	requireLive(t)
	session, home := liveLabSession(t)
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = runtime.NewMemoryNameRegistry()
	rt.StartServer = func(context.Context, string) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	id, err := runtime.ParseWorkspaceID("ws_g512live")
	if err != nil {
		t.Fatal(err)
	}
	handle, err := rt.EnsureSession(ctx, runtime.SessionSpec{
		WorkspaceID: id,
		Name:        session,
		ConfigHome:  home,
		Names:       rt.Names,
	})
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(home, "mate", "session-owners", session))
	})

	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// This test creates its own stale identity: CLAUDE_CONFIG_DIR is injected
	// here, at workspace create --env, onto the workspace root pane that
	// CreateAgentTab then reuses via unusedRootTab - the pane the agent starts
	// in. The lab server's own environment is neither touched nor relied on.
	staleDir := filepath.Join(filepath.Dir(home), ".claude")
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: handle,
		Label:   "G5-12 stop hook live",
		Cwd:     cwd,
		Env: []runtime.EnvVar{
			{Key: "CLAUDE_CONFIG_DIR", Value: staleDir},
		},
	})
	if err != nil {
		t.Fatalf("EnsureProjectWorkspace: %v", err)
	}
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{Workspace: ws, Label: "G5-12 mate", Cwd: cwd})
	if err != nil {
		t.Fatalf("CreateAgentTab: %v", err)
	}
	// Assert the premise before StartAgent's clear runs. If workspace create
	// --env ever stops landing on this pane there would be nothing stale to
	// clear, and "Claude reached the default account" would prove nothing; the
	// test must fail loudly here rather than pass for the wrong reason.
	if got, probeErr := paneEnvValue(ctx, handle.Name, tab.PaneID, "CLAUDE_CONFIG_DIR"); probeErr != nil {
		t.Fatalf("premise probe on pane %s: %v", tab.PaneID, probeErr)
	} else if got != staleDir {
		t.Fatalf("premise lost: pane %s carries CLAUDE_CONFIG_DIR=%q, want %q (the value this test injected at workspace create --env)", tab.PaneID, got, staleDir)
	}

	ctxPath := filepath.Join(cwd, "context.md")
	if err := os.WriteFile(ctxPath, []byte("you are a gomate G5-12 live probe. When asked to say a word, say only that word."), 0o644); err != nil {
		t.Fatal(err)
	}

	markerPath := filepath.Join(cwd, "stop-hook-fired.json")
	scriptPath := filepath.Join(cwd, "stop-hook.sh")
	script := "#!/bin/sh\ncat > '" + markerPath + "'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(cwd, "settings.json")
	settings := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": scriptPath},
					},
				},
			},
		},
	}
	settingsBytes, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, settingsBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	sessionID := uuid.NewString()
	// A Crew-shaped launch takes its --settings from <state>/settings.json,
	// which is settingsPath; the file written above stands in for the one
	// Prepare names, so prep.Files is not written.
	prep, err := harness.Claude{}.Prepare(ctx, harness.PrepareRequest{
		Role: harness.RoleCrew, Cwd: cwd, StateDir: filepath.Dir(settingsPath), ContextPath: ctxPath,
		NewSessionID: func() string { return sessionID },
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	launch, err := harness.Claude{}.Build(ctx, harness.AgentSpec{
		Kind:        harness.KindClaude,
		Cwd:         cwd,
		ContextPath: prep.ContextPath,
		Launch:      prep.Launch,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	res, err := runtime.AllocateAgentName(rt.Names, handle.Name, "g512", "hook01", runtime.FailOnCollision)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := runtime.NewAgentStartSpec(tab, res, launch, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := rt.StartAgent(ctx, spec)
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	obs, err := rt.WaitAgent(ctx, agent, runtime.WaitCondition{
		Until:   []runtime.AgentStatus{runtime.AgentBlocked, runtime.AgentIdle, runtime.AgentDone},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("WaitAgent (readiness): %v", err)
	}
	if obs.Status == runtime.AgentWorking {
		t.Fatalf("readiness wait returned working: %+v", obs)
	}

	// The pane carries the stale CLAUDE_CONFIG_DIR this test injected at
	// workspace create --env (asserted above), while this launch uses Claude's
	// default configuration. StartAgent must clear the inherited value in this
	// pane before Claude resolves its account; otherwise the child lands on the
	// pre-login screen and no Stop hook can fire.
	if text, readErr := rt.ReadAgent(ctx, agent, 60); readErr == nil {
		lower := strings.ToLower(text)
		if strings.Contains(lower, "select login method") || strings.Contains(lower, "not logged in") {
			t.Fatalf("Claude retained the stale Herdr CLAUDE_CONFIG_DIR and reached the pre-login screen; pane:\n%s", text)
		}
	}

	if err := rt.PromptAgent(ctx, agent, "say PONG"); err != nil {
		t.Fatalf("PromptAgent: %v", err)
	}

	deadline := time.Now().Add(60 * time.Second)
	var markerBytes []byte
	for time.Now().Before(deadline) {
		markerBytes, err = os.ReadFile(markerPath)
		if err == nil && len(strings.TrimSpace(string(markerBytes))) > 0 {
			break
		}
		if text, readErr := rt.ReadAgent(ctx, agent, 60); readErr == nil {
			lower := strings.ToLower(text)
			if strings.Contains(lower, "select login method") || strings.Contains(lower, "not logged in") {
				t.Fatalf("Claude retained the stale Herdr CLAUDE_CONFIG_DIR and reached the pre-login screen; pane:\n%s", text)
			}
		}
		time.Sleep(1 * time.Second)
	}
	if len(markerBytes) == 0 {
		t.Fatalf("AssumptionClaudeArgsThroughHerdr DISPROVEN: Stop hook marker never appeared at %s within the deadline; --settings did not reach the child (or the child did not honor it)", markerPath)
	}

	var payload struct {
		SessionID            string `json:"session_id"`
		TranscriptPath       string `json:"transcript_path"`
		HookEventName        string `json:"hook_event_name"`
		LastAssistantMessage string `json:"last_assistant_message"`
	}
	if err := json.Unmarshal(markerBytes, &payload); err != nil {
		t.Fatalf("Stop hook fired but its stdin payload did not parse as JSON: %v\nraw=%s", err, markerBytes)
	}
	// "Claude answered" must not be read out of the pane as a whole: the prompt
	// ("say PONG") is echoed into the composer, so a pane-wide search for PONG
	// is satisfied by the prompt itself and proves nothing (PR #90
	// counter-review F1). Two signals the echo cannot produce:
	//   1. the Stop hook's own payload carries last_assistant_message,
	//      Claude's record of the turn that just ended;
	//   2. after the turn, the pane draws the assistant answer line
	//      (`⏺ PONG`) - never the composer's prompt line.
	if !strings.Contains(strings.ToUpper(payload.LastAssistantMessage), "PONG") {
		t.Fatalf("Stop hook fired but Claude recorded no PONG answer (last_assistant_message=%q); the echoed prompt alone must never satisfy this assertion", payload.LastAssistantMessage)
	}
	answerLine, err := waitForAssistantAnswer(ctx, rt, agent, "PONG", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != sessionID {
		t.Fatalf("AssumptionClaudeArgsThroughHerdr DISPROVEN: hook payload session_id = %q, want %q (the --session-id we launched with); the settings file may have reached a different or stale process", payload.SessionID, sessionID)
	}
	if strings.TrimSpace(payload.TranscriptPath) == "" {
		t.Fatal("Stop hook payload carried no transcript_path")
	}
	// Claude Code 2.1.274 (measured 2026-09-17) can report a transcript_path
	// the interactive session has not flushed yet when the Stop hook runs;
	// the headless -p path has it on disk at hook time. Poll rather than
	// stat once, and record how long it took.
	transcriptWait := time.Now()
	for {
		if _, statErr := os.Stat(payload.TranscriptPath); statErr == nil {
			break
		} else if time.Since(transcriptWait) > 20*time.Second {
			t.Fatalf("Stop hook's own transcript_path %q did not appear within 20s: %v", payload.TranscriptPath, statErr)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("transcript_path appeared %s after the Stop hook payload was read", time.Since(transcriptWait).Round(100*time.Millisecond))

	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	wantDefaultRoot := filepath.Join(userHome, ".claude", "projects")
	if !strings.HasPrefix(payload.TranscriptPath, wantDefaultRoot+string(filepath.Separator)) {
		t.Fatalf("default launch transcript path = %q, want under %q", payload.TranscriptPath, wantDefaultRoot)
	}
	t.Logf("G5_12_LIVE_STOP_HOOK_PROOF settings_forwarded=true prompt_answered=true answer_line=%q last_assistant_message=%q session_id_matched=true default_transcript_path=true transcript_path=%s hook_event=%s", answerLine, payload.LastAssistantMessage, payload.TranscriptPath, payload.HookEventName)
}

// claudeAnswerMarker is the assistant-turn marker the real claude-code
// terminal draws before an assistant message (the literal internal/ui/console's
// transcript parser reads as claudeCodeTurnMarker). The composer prompt is
// "❯", so no echoed prompt can carry this marker.
const claudeAnswerMarker = "⏺"

// waitForAssistantAnswer polls the pane until an assistant answer line
// carrying token appears. It is deliberately a separate signal from the Stop
// hook payload: the payload proves the turn happened, this proves the terminal
// shows the answer and not merely the prompt sitting in the composer.
func waitForAssistantAnswer(ctx context.Context, rt *runtime.Herdr, agent runtime.AgentHandle, token string, within time.Duration) (string, error) {
	deadline := time.Now().Add(within)
	var lastErr error
	for {
		if text, err := rt.ReadAgent(ctx, agent, 60); err == nil {
			for _, line := range strings.Split(text, "\n") {
				if !strings.Contains(line, claudeAnswerMarker) {
					continue
				}
				if strings.Contains(strings.ToUpper(line), strings.ToUpper(token)) {
					return strings.TrimSpace(line), nil
				}
			}
		} else {
			lastErr = err
		}
		if !time.Now().Before(deadline) {
			return "", fmt.Errorf("no pane line carried the %q assistant marker with %q within %s (last read error: %v)", claudeAnswerMarker, token, within, lastErr)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// paneEnvValue reads the value a pane's own shell currently carries for key, by
// typing a probe into it and reading the pane back. It is the premise check for
// this test's self-injected staleness (see the header comment): without it the
// test could lose its own setup and pass for the wrong reason.
func paneEnvValue(ctx context.Context, session, paneID, key string) (string, error) {
	const marker = "MATE_G512_PANE_ENV"
	runner := process.ExecRunner{}
	run := func(args []string) (process.Result, error) {
		return runner.Run(ctx, process.Spec{Name: "herdr", Args: runtime.WithSession(session, args)})
	}
	res, err := run([]string{"pane", "run", paneID, "echo " + marker + "=$" + key})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("herdr pane run exit %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := run([]string{"pane", "read", paneID, "--source", "visible", "--lines", "80", "--format", "text"})
		if err == nil {
			for _, line := range strings.Split(string(res.Stdout), "\n") {
				// The echoed command line is "❯ echo MARKER=$KEY"; only the
				// shell's own output line starts with the marker.
				if rest, ok := strings.CutPrefix(strings.TrimSpace(line), marker+"="); ok {
					return rest, nil
				}
			}
		}
		if !time.Now().Before(deadline) {
			return "", fmt.Errorf("pane %s never echoed %s within 10s", paneID, marker)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
