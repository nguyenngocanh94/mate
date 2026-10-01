package send_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLiveMarkerSurvivesToTheHook is the task 15 live-run defect-1 proof.
//
// The 0x1f byte send.Marker used to prefix (docs/mvp.md sections 4 and 5)
// never reached Claude Code's UserPromptSubmit payload in the field: the
// console forwarded a marked line and the hook still recorded it as
// Source: user, meaning it saw no marker. Something between
// `herdr pane send-text` and Claude's own composer strips the control byte.
//
// This test types three candidate prefixes into a real Claude Code Mate,
// one per turn, and reads what internal/hook's own sent.log line says
// arrived: the hook records the prompt's text verbatim whenever it does not
// recognise a leading marker (internal/hook/hook.go's HandlePrompt), so the
// Text field is exactly the byte-for-byte payload Claude Code handed the
// hook, independent of whether HandlePrompt has been taught the new
// sentinel yet.
//
// Measured 2026-09-17, Herdr 0.8.2, Claude Code 2.1.274:
//   - "\x1f" (the old byte)      : stripped before the hook ever saw it.
//   - "⟦mate⟧ " (chosen)       : arrives intact, byte for byte.
//   - "[mate] " (ASCII fallback): arrives intact, byte for byte.
//
// The bracket sentinel was kept over the plain ASCII fallback because it is
// visually distinct from anything a captain would type by hand while still
// being ordinary UTF-8 text a terminal, tmux and Claude's composer all pass
// through unmolested.
func TestLiveMarkerSurvivesToTheHook(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

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

	started, err := spawn.StartMate(ctx, w, spawnDeps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude})
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
		Kind:    harness.KindClaude,
	}
	deps := send.Deps{Harnesses: catalog.Default(), Runtime: rt}

	type candidate struct {
		name   string
		prefix string
	}
	candidates := []candidate{
		{name: "old 0x1f byte", prefix: "\x1f"},
		{name: "bracket sentinel (chosen)", prefix: "⟦mate⟧ "},
		{name: "ASCII fallback", prefix: "[mate] "},
	}

	type result struct {
		candidate string
		survived  bool
		source    string
		text      string
	}
	var results []result

	for i, c := range candidates {
		waitForComposer(ctx, t, rt, handle, send.StateEmpty)
		tag := fmt.Sprintf("reply with just the word measured round %d", i)
		text := c.prefix + tag
		report, err := send.Send(ctx, deps, handle, harness.KindClaude, text, send.Options{})
		logReport(t, c.name, report)
		if err != nil {
			t.Fatalf("Send(%s): %v", c.name, err)
		}

		before := len(readAllSent(t, w))
		_ = before
		entry := waitForSentAfter(t, w, i, func(e store.SentEntry) bool {
			return e.Source != store.SourceMate && strings.Contains(e.Text, tag)
		})
		survived := strings.HasPrefix(entry.Text, c.prefix) ||
			(c.prefix == "\x1f" && entry.Source == store.SourceApp)
		results = append(results, result{candidate: c.name, survived: survived, source: entry.Source, text: entry.Text})
		t.Logf("%s: source=%s text=%q survived=%v", c.name, entry.Source, entry.Text, survived)

		// Wait for the turn to finish so the next candidate starts from an
		// empty composer.
		waitForAnyMateReply(t, w, i)
	}

	for _, r := range results {
		t.Logf("measurement | %-28s survived=%v source=%s text=%q", r.candidate, r.survived, r.source, r.text)
	}

	// The chosen sentinel must survive; the old byte must not (that is the
	// defect this whole test exists to pin down).
	var oldByteSurvived, chosenSurvived bool
	for _, r := range results {
		switch r.candidate {
		case "old 0x1f byte":
			oldByteSurvived = r.survived
		case "bracket sentinel (chosen)":
			chosenSurvived = r.survived
		}
	}
	if oldByteSurvived {
		t.Errorf("the old 0x1f byte survived to the hook; the recorded field measurement (defect 1) did not reproduce")
	}
	if !chosenSurvived {
		t.Fatalf("the chosen bracket sentinel did not survive to the hook; pick a different sentinel")
	}
}

func readAllSent(t *testing.T, w *store.Workspace) []store.SentEntry {
	t.Helper()
	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatalf("ReadSent: %v", err)
	}
	return entries
}

// waitForSentAfter polls sent.log for the first entry (of any round so far)
// matching match, ignoring rounds already consumed isn't needed here since
// each round's tag is unique.
func waitForSentAfter(t *testing.T, w *store.Workspace, round int, match func(store.SentEntry) bool) store.SentEntry {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range readAllSent(t, w) {
			if match(e) {
				return e
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("round %d: sent.log never recorded the prompt", round)
	return store.SentEntry{}
}

// waitForAnyMateReply waits for the mate's Stop hook to append its Nth
// reply (1-indexed by round), so the next candidate is typed after the
// current turn has fully ended and the composer is free again.
func waitForAnyMateReply(t *testing.T, w *store.Workspace, round int) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	want := round + 1
	for time.Now().Before(deadline) {
		got := 0
		for _, e := range readAllSent(t, w) {
			if e.Source == store.SourceMate {
				got++
			}
		}
		if got >= want {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("round %d: the mate never answered", round)
}
