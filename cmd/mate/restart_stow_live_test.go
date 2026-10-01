package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// TestLiveRestartMateStowsFirst is task 37's B7 proof: a Claude Mate is told
// a durable fact in conversation only, the captain restarts it from the
// console's Actions menu, and the fact is in the Mate's files afterwards
// because the restart asked the Mate to stow before it stopped it. The
// outcome line says "stowed".
func TestLiveRestartMateStowsFirst(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	repo := filepath.Join(w.Root(), stowProject)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject(stowProject, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if err := os.WriteFile(w.ProjectDoc(stowProject), []byte(memory.ProjectTemplate(stowProject)), 0o644); err != nil {
		t.Fatal(err)
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
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = spawn.StopMate(stopCtx, w, deps, stowProject)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	action := consoleAction(w, deps)

	out, err := action(ctx, console.ActionRequest{Action: console.ActionStart, Target: stowProject, TargetKind: "mate", Harness: query.HarnessClaude})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Logf("start: %s", out)
	handle, kind, err := spawn.MateHandle(ctx, w, deps, stowProject)
	if err != nil {
		t.Fatal(err)
	}
	evidence := func() string {
		s, _ := rt.ReadAgent(context.Background(), handle, harness.ReadRecentUnwrapped, 40)
		return s
	}
	// The fact exists only in this conversation: the Mate is told not to
	// file it, which the check below confirms before the restart.
	const fact = "One thing for you to know, and do not write it to any file right now: this project's release train leaves every Thursday at 15:00, and the captain calls it Project Lantern. Reply with OK only."
	before := len(sentEntries(t, w, stowProject))
	typeCaptainLine(t, ctx, deps, action, stowProject, handle, kind, fact, 3*time.Minute, evidence)
	waitForSentAfter(t, ctx, w, stowProject, before, 3*time.Minute, func(e store.SentEntry) bool { return e.Source == store.SourceMate })
	if filed := filedFact(t, w); filed != "" {
		t.Fatalf("the Mate filed the fact before the restart (%s), so the restart cannot show the stow did", filed)
	}

	started := time.Now()
	out, err = action(ctx, console.ActionRequest{Action: console.ActionRestartMate, Target: stowProject, TargetKind: "project"})
	if err != nil {
		t.Fatalf("restart: %v\n%s", err, evidence())
	}
	t.Logf("restart (%s): %s", time.Since(started).Round(time.Second), out)
	if !strings.HasPrefix(out, "stowed; stopped ") || !strings.Contains(out, "is running on claude") {
		t.Fatalf("restart outcome = %q, want it to open with stowed and name the restart", out)
	}
	filed := filedFact(t, w)
	if filed == "" {
		mem, _ := os.ReadFile(w.MemoryFile(stowProject))
		doc, _ := os.ReadFile(w.ProjectDoc(stowProject))
		t.Fatalf("after the stowed restart neither file holds the fact.\nmemory.md:\n%s\nPROJECT.md:\n%s", mem, doc)
	}
	t.Logf("the fact was filed in %s:", filed)
	for _, path := range []string{w.MemoryFile(stowProject), w.ProjectDoc(stowProject), w.BacklogFile(stowProject)} {
		data, _ := os.ReadFile(path)
		for _, l := range strings.Split(string(data), "\n") {
			if low := strings.ToLower(l); strings.Contains(low, "lantern") || strings.Contains(low, "thursday") {
				t.Logf("  %s: %s", filepath.Base(path), l)
			}
		}
	}
	for _, e := range sentEntries(t, w, stowProject) {
		if e.Source == store.SourceMate {
			t.Logf("  mate → %s: %.300s", e.Target, e.Text)
		}
	}
	lines := sentEntries(t, w, stowProject)
	stow := 0
	for _, e := range lines {
		if e.Source == store.SourceApp && e.Text == memory.StowLine {
			stow++
		}
	}
	if stow != 2 {
		t.Fatalf("sent.log holds the stow line %d time(s), want 2 (the outbox and the Mate's hook)", stow)
	}
	if _, _, err := spawn.MateHandle(ctx, w, deps, stowProject); err != nil {
		t.Fatalf("no Mate after the restart: %v", err)
	}
}

// stowProject is the restart test's own project: `go test` runs this
// package beside internal/spawn's, whose live Mates are all `shop`, and two
// Mates of one name cannot share a lab session.
const stowProject = "harbor"

// filedFact says which of the Mate's files mentions the fact, or "".
func filedFact(t *testing.T, w *store.Workspace) string {
	t.Helper()
	for _, f := range []struct{ name, path string }{
		{"memory.md", w.MemoryFile(stowProject)},
		{"PROJECT.md", w.ProjectDoc(stowProject)},
		{"backlog.md", w.BacklogFile(stowProject)},
	} {
		data, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		text := strings.ToLower(string(data))
		if strings.Contains(text, "lantern") || strings.Contains(text, "thursday") {
			return f.name
		}
	}
	return ""
}
