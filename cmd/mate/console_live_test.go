package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/codexlab"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// requireConsoleLive gates this file the same way internal/spawn/live_test.go
// gates its own proofs: a live Herdr lab session and a real Claude binary are
// machine facts, and MATE_LIVE=1 is the single opt-in that claims them.
// scripts/gotestreport allows TestLive* and nothing else to skip.
func requireConsoleLive(t *testing.T) {
	t.Helper()
	if os.Getenv("MATE_LIVE") != "1" {
		t.Skip("set MATE_LIVE=1 to run live Herdr proofs")
	}
}

func consoleLiveLab(t *testing.T) (session, configHome string) {
	t.Helper()
	session = strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_SESSION"))
	if session == "" {
		t.Skip("set MATE_HERDR_LIVE_SESSION to a provisioned fm-lab-* Herdr session")
	}
	if session == "default" || session == "firstmate" {
		t.Fatal("refusing to run a live console session against the default or firstmate session")
	}
	if !strings.HasPrefix(session, "fm-lab-") {
		t.Fatalf("live test requires an fm-lab- session, got %q", session)
	}
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		t.Fatal("HOME is required to resolve the Herdr socket")
	}
	// Every live test that reaches a lab session runs Codex in a lab
	// CODEX_HOME, never the operator's ~/.codex (internal/harness/codexlab).
	codexlab.Home(t)
	return session, filepath.Join(home, ".config")
}

// liveWorkspaceRoot is the directory a live proof builds its workspace in.
// By default it is t.TempDir(), removed with the test. With
// MATE_LIVE_KEEP=<dir> it is a fresh directory under <dir> that outlives
// the test, so the run's own files - status files, sent.log, briefs,
// hand-backs, the repositories - stay where the transcripts recorded them
// and `mate reindex` can rebuild the timeline from them afterwards
// (docs/mvp.md task 34 measures prompting that way). The directory must not
// go through a symlink, for the same reason TMPDIR must not.
func liveWorkspaceRoot(t *testing.T) string {
	t.Helper()
	keep := strings.TrimSpace(os.Getenv("MATE_LIVE_KEEP"))
	if keep == "" {
		return t.TempDir()
	}
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatalf("MATE_LIVE_KEEP: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(keep)
	if err != nil {
		t.Fatalf("MATE_LIVE_KEEP: %v", err)
	}
	if abs, _ := filepath.Abs(keep); abs != resolved {
		t.Fatalf("MATE_LIVE_KEEP=%s goes through a symlink (resolves to %s); Herdr reports resolved cwds", keep, resolved)
	}
	// A subtest's name holds a "/", which MkdirTemp refuses in a pattern.
	root, err := os.MkdirTemp(resolved, strings.ReplaceAll(t.Name(), "/", "-")+"-")
	if err != nil {
		t.Fatalf("MATE_LIVE_KEEP: %v", err)
	}
	t.Logf("workspace kept at %s (MATE_LIVE_KEEP)", root)
	return root
}

// TestLiveConsoleStreamMate is the mvp.md task 09 proof: the Console's own
// ActionFunc starts a real Claude Mate, the Console's own
// SessionStreamFactory opens that Mate's terminal at the size the session
// frame would ask for, the agent's composer arrives through the PTY, the
// detach sequence goes back down it, and the Mate is stopped afterwards.
//
// Everything below the Bubble Tea event loop is the code cmdConsole wires:
// consoleAction, consoleSessionStream and consoleSessionMetadata, over the
// real Herdr adapter. The Program itself is not run - it needs a terminal -
// but no part of the path being proved lives inside it.
func TestLiveConsoleStreamMate(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	// TMPDIR must not go through a symlink: Herdr reports a pane cwd with
	// symlinks resolved and the launch guard compares the two (docs/mvp.md
	// section 7). The runner sets TMPDIR; t.TempDir honours it.
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if w.Session() != session {
		t.Fatalf("workspace session = %q, want the lab session %q", w.Session(), session)
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	action := consoleAction(w, deps)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	// 1. Start, through the Console's action seam, with the harness the
	// picker would have chosen.
	startOut, err := action(ctx, console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate", Harness: query.HarnessClaude})
	if err != nil {
		t.Fatalf("console start action: %v", err)
	}
	t.Logf("start action: %s", startOut)

	// 2. The Console's snapshot must now offer the Mate as a session
	// target, with its mode on it - that is what the header renders.
	snap, err := query.Load(ctx, w)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if snap.Projects[0].Mode != query.ModeManual {
		t.Fatalf("mode = %q, want manual by default", snap.Projects[0].Mode)
	}
	target := console.SessionTarget{
		Kind:        console.SessionTargetMate,
		ID:          snap.Projects[0].Mate.Designated.Value.MateID,
		ProjectID:   "shop",
		HarnessKind: query.HarnessClaude,
		AgentName:   snap.Projects[0].Mate.AgentName.Value,
		Mode:        snap.Projects[0].Mode,
	}

	// 3. The metadata side channel says the same thing the header does.
	meta, err := consoleSessionMetadata(w, deps)(ctx, target)
	if err != nil {
		t.Fatalf("session metadata: %v", err)
	}
	if meta.RecordedStatus.Value != string(spawn.StateRunning) || meta.Runtime.Status != query.Known {
		t.Fatalf("metadata = %+v/%+v, want a running Mate observed live", meta.RecordedStatus, meta.Runtime)
	}

	// 4. Open the live terminal through the Console's own factory, at the
	// geometry a 120x36 Console would ask for.
	factory := consoleSessionStream(w, rt)
	if factory == nil {
		t.Fatal("the live adapter provides no session stream")
	}
	channel, err := factory(ctx, target, console.TerminalSize{Cols: 120, Rows: 36})
	if err != nil {
		t.Fatalf("open session stream: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = channel.Close(context.Background())
		}
	}()

	// 5. Read until Claude's composer glyph arrives in the raw stream. The
	// composer is the proof the reader is inside the agent's own terminal,
	// not looking at a scrape of it.
	var seen strings.Builder
	deadline := time.Now().Add(90 * time.Second)
	for !strings.Contains(seen.String(), "❯") {
		if time.Now().After(deadline) {
			t.Fatalf("Claude's composer glyph never arrived in the stream; last bytes:\n%s",
				harness.StartupScreenTail(seen.String(), 12))
		}
		readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
		data, readErr := channel.Read(readCtx)
		readCancel()
		seen.Write(data)
		if readErr != nil && !os.IsTimeout(readErr) {
			if time.Now().After(deadline) {
				t.Fatalf("stream read: %v", readErr)
			}
		}
	}
	t.Logf("composer reached the console stream:\n%s", harness.StartupScreenTail(seen.String(), 12))

	// 6. The detach sequence. It is the Console's, not the agent's: the
	// bytes are swallowed by onSessionStreamKey and never written to the
	// PTY, so what is exercised here is that the channel survives a close
	// taken while the agent is live and that the agent outlives it.
	if err := channel.Resize(ctx, console.TerminalSize{Cols: 100, Rows: 30}); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := channel.Close(ctx); err != nil {
		t.Fatalf("close the session stream: %v", err)
	}
	closed = true

	// Detaching does not stop the agent (ADR 0010/0026).
	status, err := spawn.MateStatus(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus after detach: %v", err)
	}
	if status.State != spawn.StateRunning {
		t.Fatalf("status after detach = %q, want the Mate still running", status.Line())
	}
	t.Logf("after detach: %s", status.Line())

	// 7. The mode key's action, on a live project.
	modeOut, err := action(ctx, console.ActionRequest{Action: console.ActionMode, Target: "shop", TargetKind: "project"})
	if err != nil {
		t.Fatalf("mode action: %v", err)
	}
	if !w.Auto("shop") {
		t.Fatal("the mode action did not create .auto")
	}
	t.Logf("mode action: %s", modeOut)
	if _, err := action(ctx, console.ActionRequest{Action: console.ActionMode, Target: "shop", TargetKind: "project"}); err != nil {
		t.Fatalf("mode action back: %v", err)
	}

	// 8. Stop, through the same seam.
	stopOut, err := action(ctx, console.ActionRequest{Action: console.ActionStop, Target: "shop", TargetKind: "mate"})
	if err != nil {
		t.Fatalf("console stop action: %v", err)
	}
	t.Logf("stop action: %s", stopOut)
	after, err := spawn.MateStatus(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus after stop: %v", err)
	}
	if after.State != spawn.StateStopped {
		t.Fatalf("status after stop = %q, want stopped", after.Line())
	}

	// 9. And a stopped Mate is refused by the factory with the stopped
	// state, not by opening a PTY against a pane nobody owns.
	if _, err := factory(ctx, target, console.TerminalSize{Cols: 120, Rows: 36}); err == nil ||
		!strings.Contains(err.Error(), "stopped") {
		t.Fatalf("factory on a stopped Mate = %v, want the stopped state", err)
	}
}

// consoleUseLabSession rewrites workspace.yaml's session name to the
// provisioned lab, which the runner owns: store.Init derives the name from
// the workspace path, and a lab run must not use that.
func consoleUseLabSession(t *testing.T, w *store.Workspace, session string) {
	t.Helper()
	path := w.WorkspaceFile()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	scanner := bufio.NewScanner(f)
	replaced := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "session:") {
			line = "session: " + session
			replaced = true
		}
		out = append(out, line)
	}
	f.Close()
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !replaced {
		t.Fatalf("%s carries no session key", path)
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// consoleBinaryPath builds the mate binary the rendered manual points at,
// so the live Mate's AGENTS.md names a real executable, not the test binary.
func consoleBinaryPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "mate")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/nguyenngocanh94/mate/cmd/mate")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build mate: %v\n%s", err, out)
	}
	return bin
}

// TestLiveConsoleStreamCrew is the crew half of task 09, wired only on
// 2026-09-18 after Enter on a crew row failed in the user's workspace: a
// real Codex crew is spawned, the Console's own SessionStreamFactory opens
// that crew's terminal, Codex's composer arrives through the PTY, the
// metadata side channel reports the crew's own status verb beside Herdr's
// observation, and detaching leaves the crew running.
func TestLiveConsoleStreamCrew(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	res, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project:   "shop",
		Crew:      "k3",
		Harness:   harness.KindCodex,
		BriefText: brieftest.Ship("Append working: looking to the status file, then wait for further instructions. Do not edit any file."),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
	})
	t.Logf("spawned crew %s in pane %s", res.Agent, res.Pane)

	// The snapshot offers the crew as a session target the same way the
	// tree's Enter would build it.
	snap, err := query.Load(ctx, w)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if len(snap.Projects[0].Crews) != 1 {
		t.Fatalf("crews = %+v, want the one just spawned", snap.Projects[0].Crews)
	}
	crew := snap.Projects[0].Crews[0]
	target := console.SessionTarget{
		Kind:        console.SessionTargetCrew,
		ID:          crew.CrewID,
		ProjectID:   "shop",
		HarnessKind: query.HarnessCodex,
		AgentName:   crew.AgentName.Value,
		Worktree:    crew.Worktree.Value.Path,
	}

	meta, err := consoleSessionMetadata(w, deps)(ctx, target)
	if err != nil {
		t.Fatalf("session metadata: %v", err)
	}
	if meta.Runtime.Status != query.Known {
		t.Fatalf("metadata runtime = %+v, want the crew observed live", meta.Runtime)
	}
	t.Logf("metadata: recorded=%s runtime=%v", meta.RecordedStatus.Value, meta.Runtime.Status)

	factory := consoleSessionStream(w, rt)
	if factory == nil {
		t.Fatal("the live adapter provides no session stream")
	}
	channel, err := factory(ctx, target, console.TerminalSize{Cols: 120, Rows: 36})
	if err != nil {
		t.Fatalf("open the crew's session stream: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = channel.Close(context.Background())
		}
	}()

	// Codex's composer glyph in the raw stream is the proof the reader is
	// inside the crew's own terminal.
	var seen strings.Builder
	deadline := time.Now().Add(90 * time.Second)
	for !strings.Contains(seen.String(), "›") {
		if time.Now().After(deadline) {
			t.Fatalf("Codex's composer glyph never arrived in the stream; last bytes:\n%s",
				harness.StartupScreenTail(seen.String(), 12))
		}
		readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
		data, readErr := channel.Read(readCtx)
		readCancel()
		seen.Write(data)
		if readErr != nil && !os.IsTimeout(readErr) && time.Now().After(deadline) {
			t.Fatalf("stream read: %v", readErr)
		}
	}
	t.Logf("composer reached the console stream:\n%s", harness.StartupScreenTail(seen.String(), 12))

	if err := channel.Close(ctx); err != nil {
		t.Fatalf("close the session stream: %v", err)
	}
	closed = true

	status, err := spawn.CrewStatus(ctx, w, deps, "shop", "k3")
	if err != nil {
		t.Fatalf("CrewStatus after detach: %v", err)
	}
	if status.State != spawn.StateRunning {
		t.Fatalf("status after detach = %q, want the crew still running", status.Line())
	}

	// Torn down, the factory refuses with the stopped state and the tree
	// shows the crew's own last word, never `reserved`.
	if _, err := spawn.StopCrew(ctx, w, deps, "shop", "k3", true); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if _, err := factory(ctx, target, console.TerminalSize{Cols: 120, Rows: 36}); err == nil ||
		!strings.Contains(err.Error(), "stopped") {
		t.Fatalf("factory on a stopped crew = %v, want the stopped state", err)
	}
	if snap, err = query.Load(ctx, w); err != nil {
		t.Fatalf("query.Load after stop: %v", err)
	}
	if got := snap.Projects[0].Crews[0].Status; got == query.CrewSpawned {
		t.Fatalf("a torn-down crew is shown as %q", got)
	}
	t.Logf("tree after stop: %s", snap.Projects[0].Crews[0].Status)
}
