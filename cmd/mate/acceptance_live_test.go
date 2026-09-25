package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// acceptanceRequest is the captain's line, typed into the Mate's pane the way
// a human types it: no sentinel, no hints about which command to run. Every
// decision after this - crew id, brief, spawn, supervision, review, the
// report back - has to come from the rendered manual alone, which is exactly
// what docs/mvp.md task 17 is asking to prove.
const acceptanceRequest = `Add a line "Built with mate" to the end of README.md in project shop. Use a crew.`

// acceptanceLine is the text the Crew must have committed.
const acceptanceLine = "Built with mate"

// TestLiveAcceptanceMateRunsATask is the M2 capstone (docs/mvp.md task 17): a
// real Claude Mate, given one plain request, must write a brief, spawn a real
// Crew, supervise it to `wait-mate:`, and report back - with no scripted commands
// and no test-side nudging beyond that single line.
//
// The test asserts four things, and deliberately nothing about how the Mate
// got there:
//
//  1. a crew record appeared (the Mate spawned a Crew instead of doing the
//     work itself),
//  2. that crew's status file reached `wait-mate:`,
//  3. the crew's branch carries a commit that puts the line in README.md,
//  4. the Mate's own Stop hook wrote a mate → user line in sent.log naming
//     the branch or saying the work is ready.
//
// A failure here is a manual bug, not a test bug: the fix is to make the
// rendered AGENTS.md clearer, never to make this test accept less.
func TestLiveAcceptanceMateRunsATask(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	// TMPDIR must not go through a symlink (docs/mvp.md section 7).
	root := t.TempDir()
	w, err := store.Init(root)
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# shop\n\nA tiny shop.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, repo, "add", "README.md")
	runGitOrFatal(t, repo, "commit", "-m", "add README")
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

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
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
	t.Logf("mate %s in pane %s, cwd %s", started.Agent, started.Pane, started.MateDir)

	mate := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    started.Agent,
		RawID:   "shop",
		Kind:    harness.KindClaude,
		Tab: runtime.TabHandle{
			Session:     runtime.SessionHandle{Name: session, ConfigHome: configHome},
			WorkspaceID: started.Workspace,
			TabID:       started.Tab,
			PaneID:      started.Pane,
			Label:       "mate",
		},
	}

	// The captain types one line. No marker: this is a human talking.
	report, err := send.Send(ctx, send.Deps{Runtime: rt}, mate, harness.KindClaude, acceptanceRequest, send.Options{})
	if err != nil {
		t.Fatalf("send the captain's request: %v (report %+v)", err, report)
	}
	t.Logf("captain → mate: %s (%s → enter ×%d → %s)", acceptanceRequest, report.Before.State, report.Presses, report.After.State)

	// Everything from here is the Mate's own doing, observed from the files.
	budget := 4 * time.Minute
	crew := waitForCrewRecord(t, ctx, w, "shop", budget, func() string { return acceptancePanes(ctx, rt, mate, w, "shop") })
	t.Logf("the mate spawned crew %q", crew)

	meta, err := w.ReadCrewMeta("shop", crew)
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	branch := meta[spawn.MetaBranch]
	worktree := filepath.Join(w.Root(), meta[spawn.MetaWorktree])
	t.Logf("crew %s: branch %s, worktree %s, task %q", crew, branch, worktree, meta[spawn.MetaTask])

	status := waitForCrewStatus(t, ctx, w, "shop", crew, "wait-mate:", budget, func() string { return acceptancePanes(ctx, rt, mate, w, "shop") })
	t.Logf("crew status file:\n%s", status)

	// The branch must actually carry the change; a `wait-mate:` line is a claim,
	// not evidence.
	commits := gitOut(t, repo, "log", "--format=%H %s", "main.."+branch)
	if strings.TrimSpace(commits) == "" {
		t.Fatalf("branch %s has no commits over main", branch)
	}
	t.Logf("commits on %s:\n%s", branch, commits)
	touched := gitOut(t, repo, "diff", "--name-only", "main..."+branch)
	if !strings.Contains(touched, "README.md") {
		t.Fatalf("branch %s touches %q, want README.md", branch, strings.TrimSpace(touched))
	}
	readme := gitOut(t, repo, "show", branch+":README.md")
	if !strings.Contains(readme, acceptanceLine) {
		t.Fatalf("README.md on %s does not contain %q:\n%s", branch, acceptanceLine, readme)
	}
	t.Logf("README.md on %s:\n%s", branch, readme)

	// The Mate has to close the loop with the captain, in its own words.
	reported := waitForMateReport(t, ctx, w, "shop", branch, budget)
	t.Logf("mate → user: %s", reported)

	sent, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatalf("ReadSent: %v", err)
	}
	for _, e := range sent {
		t.Logf("sent.log %s → %s: %s", e.Source, e.Target, e.Text)
	}

	stopped, err := spawn.StopCrew(ctx, w, deps, "shop", crew, true)
	if err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if !stopped.TabClosed {
		t.Error("StopCrew did not close the crew tab")
	}
	if _, err := spawn.StopMate(ctx, w, deps, "shop"); err != nil {
		t.Fatalf("StopMate: %v", err)
	}
}

// waitForCrewRecord polls the project's crews directory for the first
// `<id>.meta` the Mate's own `mate crew spawn` wrote.
func waitForCrewRecord(t *testing.T, ctx context.Context, w *store.Workspace, project string, budget time.Duration, evidence func() string) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		entries, err := os.ReadDir(w.CrewsDir(project))
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read crews dir: %v", err)
		}
		for _, e := range entries {
			if name := e.Name(); strings.HasSuffix(name, ".meta") {
				return strings.TrimSuffix(name, ".meta")
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the mate spawned no crew within %s; it either did the work itself or never ran crew spawn\n%s", budget, evidence())
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled waiting for a crew record\n%s", evidence())
		case <-time.After(3 * time.Second):
		}
	}
}

// waitForCrewStatus polls the crew's status file until it holds want.
func waitForCrewStatus(t *testing.T, ctx context.Context, w *store.Workspace, project, crew, want string, budget time.Duration, evidence func() string) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	var text string
	for {
		entries, _, err := w.ReadStatus(project, crew, 0)
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
			t.Fatalf("crew %s never reported %q within %s\nstatus:\n%s\n%s", crew, want, budget, text, evidence())
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled waiting for %q\nstatus:\n%s", want, text)
		case <-time.After(3 * time.Second):
		}
	}
}

// waitForMateReport polls sent.log for a mate → user line that names the
// branch or says the work is ready. That entry is written by the Mate's own
// Stop hook, so its presence proves the Mate finished a turn saying so,
// rather than the test reading the pane and deciding for it.
func waitForMateReport(t *testing.T, ctx context.Context, w *store.Workspace, project, branch string, budget time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		entries, _, err := w.ReadSent(project, 0)
		if err != nil {
			t.Fatalf("ReadSent: %v", err)
		}
		for _, e := range entries {
			if e.Source != store.SourceMate {
				continue
			}
			lower := strings.ToLower(e.Text)
			if strings.Contains(e.Text, branch) || strings.Contains(lower, "ready") {
				return e.Text
			}
		}
		if time.Now().After(deadline) {
			var all []string
			for _, e := range entries {
				all = append(all, fmt.Sprintf("%s → %s: %s", e.Source, e.Target, e.Text))
			}
			t.Fatalf("the mate never reported the finished work within %s (no mate line naming %s or saying ready)\nsent.log:\n%s",
				budget, branch, strings.Join(all, "\n"))
		}
		select {
		case <-ctx.Done():
			t.Fatal("context cancelled waiting for the mate's report")
		case <-time.After(3 * time.Second):
		}
	}
}

// acceptancePanes is the failure evidence: the Mate's own pane, plus every
// crew pane mate knows about. A failure of this test is the Mate doing
// something the manual did not prepare it for, and the pane is where that is
// visible.
func acceptancePanes(ctx context.Context, rt runtime.Adapter, mate runtime.AgentHandle, w *store.Workspace, project string) string {
	var b strings.Builder
	b.WriteString("mate pane:\n")
	if screen, err := rt.ReadAgent(ctx, mate, 60); err == nil {
		b.WriteString(harness.StartupScreenTail(screen, 60))
	} else {
		fmt.Fprintf(&b, "(not readable: %v)", err)
	}
	crews, err := spawn.ListCrews(w, project)
	if err != nil {
		fmt.Fprintf(&b, "\n(crew list failed: %v)", err)
		return b.String()
	}
	for _, c := range crews {
		fmt.Fprintf(&b, "\n\ncrew %s (%s, state %s, note %q)", c.Crew, c.Harness, c.State, c.Note)
	}
	return b.String()
}

// gitOut runs a read-only git command in dir and returns its output.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
