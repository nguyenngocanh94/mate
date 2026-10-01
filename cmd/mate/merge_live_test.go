package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// The two live proofs of docs/mvp.md task 22. Both spend a real Codex crew
// on a one-line commit, because the thing being proved is the merge, and a
// bigger task only buys a longer wait for the same branch.

// mergeLiveWorkspace is the fixture both tests start from: a lab-session
// workspace with one real git project on `main`, carrying one commit so a
// branch can be taken from it.
func mergeLiveWorkspace(t *testing.T, session string, yolo bool) *store.Workspace {
	t.Helper()
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, repo, "add", "README.md")
	runGitOrFatal(t, repo, "commit", "-m", "readme")
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}, Yolo: yolo}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

// mergeLiveDeps is the real Herdr adapter, wired the way every other live
// test in this package wires it: the lab session is the runner's, so this
// must never start a server of its own.
func mergeLiveDeps(t *testing.T, session, configHome string) (spawn.Deps, *runtime.Herdr) {
	t.Helper()
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
	return deps, rt
}

// shipBrief is the task both crews are given: one committed line, then hand
// back. The brief template already tells a Crew to commit on its branch and
// to append `wait-mate: ready in branch <branch>` when it has (assets/crew).
const shipBrief = "Append the single line `merged by mate` to README.md in this worktree and commit it. Change nothing else."

// crewPaneTail reads a crew's pane so a timeout says what the agent was
// doing, not only that nothing arrived.
func crewPaneTail(ctx context.Context, rt *runtime.Herdr, session, configHome, agent, crew string) func() string {
	handle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    agent, RawID: crew, Kind: harness.KindCodex,
	}
	return func() string {
		screen, err := rt.ReadAgent(ctx, handle, 40)
		if err != nil {
			return "(pane not readable: " + err.Error() + ")"
		}
		return harness.StartupScreenTail(screen, 20)
	}
}

// assertMergedAndFinished is the end state both tests assert, and it is the
// whole claim of task 22: the crew's commit is on the project's default
// branch, the crew records `finished`, its branch and worktree are gone, and
// the console's own snapshot no longer lists it.
func assertMergedAndFinished(t *testing.T, w *store.Workspace, project, crew, branch, worktree string) {
	t.Helper()
	ctx := context.Background()
	repo := w.RepoDir(project)
	readme, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil {
		t.Fatalf("README.md in the primary checkout: %v", err)
	}
	if !strings.Contains(string(readme), "merged by mate") {
		t.Fatalf("the crew's line is not in the primary checkout's README.md:\n%s", readme)
	}
	if n := strings.TrimSpace(gitOut(t, repo, "rev-list", "--count", "--merges", "HEAD")); n != "0" {
		t.Fatalf("main carries %s merge commit(s); a merge must be a fast-forward", n)
	}
	if head := strings.TrimSpace(gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")); head != "main" {
		t.Fatalf("the primary repo is on %q after the merge, want main", head)
	}

	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaState] != spawn.CrewStateFinished {
		t.Fatalf("crew %s meta state = %q, want finished", crew, meta[spawn.MetaState])
	}
	if worktree != "" {
		if _, err := os.Stat(worktree); !os.IsNotExist(err) {
			t.Fatalf("the crew worktree survived the merge: %v", err)
		}
	}
	exists, err := gitx.New().BranchExists(ctx, repo, branch)
	if err != nil || exists {
		t.Fatalf("the crew branch survived the merge: %v, %v", exists, err)
	}

	// The console's own read: a finished crew is not a row any more.
	snap, err := query.Load(ctx, w)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	for _, p := range snap.Projects {
		if p.ProjectID != project {
			continue
		}
		for _, c := range p.Crews {
			if c.CrewID == crew {
				t.Fatalf("crew %s is still a console row after the merge: %+v", crew, c)
			}
		}
		if p.ClosedCrews != 1 {
			t.Fatalf("project %s counts %d closed crew(s), want 1", project, p.ClosedCrews)
		}
	}
}

// TestLiveMergeFromConsoleFinishesTheCrew is the captain's half of task 22:
// a real Codex crew commits on its branch and hands back with `wait-mate`,
// the captain runs `merge` from the Console's own Actions menu, and the crew
// ends `finished` with its branch landed in `main`.
//
// The merge is driven through consoleAction - the exact ActionFunc cmdConsole
// installs - with the request the menu's merge entry builds, so what is
// proved is the path a keystroke takes, not a second implementation of it.
func TestLiveMergeFromConsoleFinishesTheCrew(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	w := mergeLiveWorkspace(t, session, false)
	deps, rt := mergeLiveDeps(t, session, configHome)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
	})

	crewRes, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: harness.KindCodex, BriefText: brieftest.Ship(shipBrief),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Logf("spawned crew %s in pane %s (branch %s)", crewRes.Agent, crewRes.Pane, crewRes.Branch)
	if crewRes.DeliveryWarning != "" {
		t.Logf("brief delivery warning: %s\n%s", crewRes.DeliveryWarning, crewRes.PaneTail)
	}
	tail := crewPaneTail(ctx, rt, session, configHome, crewRes.Agent, "k3")

	done := waitForBoxEntry(t, ctx, w, "shop", 5*time.Minute, tail, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Verb == "wait-mate"
	})
	t.Logf("crew handed back: %s: %s", done.Verb, done.Text)

	// The console only offers merge on a `wait-mate` row, so the snapshot
	// the menu is built from has to agree before the action is driven.
	snap, err := query.Load(ctx, w)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	var crew query.CrewNode
	for _, p := range snap.Projects {
		for _, c := range p.Crews {
			if c.CrewID == "k3" {
				crew = c
			}
		}
	}
	if crew.Status != query.CrewWaitMate {
		t.Fatalf("console snapshot shows crew k3 as %q, want wait-mate - the merge entry would not be offered\npane:\n%s",
			crew.Status, tail())
	}

	action := consoleAction(w, deps)
	line, err := action(ctx, console.ActionRequest{
		Action: console.ActionMerge, Target: "shop", TargetKind: "crew", Crew: "k3",
	})
	if err != nil {
		t.Fatalf("console merge action: %v\npane:\n%s", err, tail())
	}
	t.Logf("merge action: %s", line)
	for _, want := range []string{"shop/k3: merged", "into main", "crew finished, worktree and branch removed"} {
		if !strings.Contains(line, want) {
			t.Fatalf("merge line %q is missing %q", line, want)
		}
	}

	assertMergedAndFinished(t, w, "shop", "k3", crewRes.Branch, crewRes.Worktree)
}

// TestLiveMateMergesUnderYolo is the Mate's half: on a project with `yolo`
// on and auto mode on, a real Claude Mate receives the daemon's digest of a
// crew's `wait-mate`, reviews it, and lands the branch itself with
// `mate merge` - which closes the crew as part of the merge.
//
// The wiring is task 19/20's (consolePilot's daemon over the real Herdr
// adapter). Nothing in this test merges anything: the only way `main` can
// carry the crew's commit at the end is that the Mate ran the command.
func TestLiveMateMergesUnderYolo(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	w := mergeLiveWorkspace(t, session, true)
	deps, rt := mergeLiveDeps(t, session, configHome)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	mateRes, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	t.Logf("Mate %s is running on %s in pane %s", mateRes.Agent, mateRes.Harness, mateRes.Pane)

	// The manual the Mate just read must carry the yolo value this project
	// actually has: it is rendered at start, which is why `project yolo`
	// tells a reader a running Mate learns it at the next restart.
	manual, err := os.ReadFile(filepath.Join(w.MateDir("shop"), "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manual), "`yolo` flag is currently `true`") {
		t.Fatal("the rendered manual does not tell the Mate that yolo is on")
	}

	if err := w.SetAuto("shop", true); err != nil {
		t.Fatalf("SetAuto: %v", err)
	}

	crewRes, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: harness.KindCodex, BriefText: brieftest.Ship(shipBrief),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Logf("spawned crew %s in pane %s (branch %s)", crewRes.Agent, crewRes.Pane, crewRes.Branch)
	if crewRes.DeliveryWarning != "" {
		t.Logf("brief delivery warning: %s\n%s", crewRes.DeliveryWarning, crewRes.PaneTail)
	}
	tail := crewPaneTail(ctx, rt, session, configHome, crewRes.Agent, "k3")

	done := waitForBoxEntry(t, ctx, w, "shop", 4*time.Minute, tail, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Verb == "wait-mate"
	})
	t.Logf("crew handed back: %s: %s", done.Verb, done.Text)

	// The digest, exactly as cmdConsole wires it (console_auto.go).
	pilot := consoleAutoPilot(w, deps)
	digest := tickUntilDigest(t, ctx, pilot, w, "shop", 2*time.Minute)
	t.Logf("digest: %s", digest)
	if !strings.Contains(digest, "k3 wait-mate") {
		t.Fatalf("digest = %q, want the crew's wait-mate item", digest)
	}

	// The Mate acts on it. Nothing else in this process can land the
	// branch, so waiting for the crew's meta to read `finished` is waiting
	// for the Mate to have run `mate merge`.
	mateHandle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    mateRes.Agent, RawID: "shop", Kind: harness.KindClaude,
	}
	mateTail := func() string {
		screen, readErr := rt.ReadAgent(ctx, mateHandle, 60)
		if readErr != nil {
			return "(mate pane not readable: " + readErr.Error() + ")"
		}
		return screen
	}
	waitForCrewState(t, ctx, w, "shop", "k3", spawn.CrewStateFinished, 5*time.Minute, mateTail)

	assertMergedAndFinished(t, w, "shop", "k3", crewRes.Branch, crewRes.Worktree)

	// The trace: the Mate's own pane shows the command it ran and the one
	// line it printed. `mate merge` writes nothing to sent.log - sent.log
	// records lines typed into panes, and a merge types into none - so the
	// pane is where the evidence is.
	pane := mateTail()
	t.Logf("mate pane:\n%s", pane)
	if !strings.Contains(pane, "merge") {
		t.Fatalf("the Mate's pane shows no trace of a merge:\n%s", pane)
	}

	if _, err := spawn.StopMate(ctx, w, deps, "shop"); err != nil {
		t.Fatalf("StopMate: %v", err)
	}
}

// waitForCrewState polls `crews/<id>.meta` until the crew records want.
func waitForCrewState(t *testing.T, ctx context.Context, w *store.Workspace, project, crew, want string,
	within time.Duration, tail func() string) {
	t.Helper()
	deadline := time.Now().Add(within)
	last := ""
	for time.Now().Before(deadline) {
		meta, err := w.ReadCrewMeta(project, crew)
		if err != nil {
			t.Fatalf("ReadCrewMeta: %v", err)
		}
		last = meta[spawn.MetaState]
		if last == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for crew %s to reach %s (it reads %q)\nmate pane:\n%s", crew, want, last, tail())
		case <-time.After(5 * time.Second):
		}
	}
	t.Fatalf("crew %s never reached %s within %s (it reads %q)\nmate pane:\n%s", crew, want, within, last, tail())
}
