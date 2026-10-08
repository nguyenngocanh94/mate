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
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/migrate"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLiveMigrateThenSpawn is the live proof of `mate migrate` (plan
// workspace-layout-and-tools-2026-10-08, section 6): a workspace on the old
// layout, its repo `shop` beside `.mate/` and one closed crew with its
// worktree still on disk, is migrated; then a real Mate starts on it and a
// real Codex crew is spawned in the moved repo and answers "ok".
func TestLiveMigrateThenSpawn(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	w, err := store.Init(liveWorkspaceRoot(t), workspaceDefaults())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	// The resolved root (/private/var/... on macOS, not /var/...): migrate
	// prints and rewrites the paths store resolved, so the fixture's brief
	// and every assertion must use the same.
	root := w.Root()

	// The old layout, by hand: store no longer writes one.
	repo := filepath.Join(root, "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, repo, "add", "README.md")
	runGitOrFatal(t, repo, "commit", "-m", "readme")
	for _, dir := range []string{w.MateDir("shop"), w.CrewsDir("shop")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(w.ProjectFile("shop"), []byte("repos:\n    - name: shop\n      path: shop\n      default_branch: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := os.ReadFile(w.WorkspaceFile())
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(ws), "layout: 2\n", "", 1)
	old = strings.Replace(old, "projects: []\n", "projects:\n    - name: shop\n", 1)
	if err := os.WriteFile(w.WorkspaceFile(), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if !w.LayoutOld() || len(w.Projects()) != 1 {
		t.Fatalf("the fixture is not an old-layout workspace with project shop:\n%s", old)
	}

	// The closed crew: a finished k1 whose worktree is still on disk.
	k1 := w.WorktreeDir("shop", "k1")
	runGitOrFatal(t, repo, "worktree", "add", "-b", "mate/k1", k1)
	if err := w.WriteCrewMeta("shop", "k1", map[string]string{
		"task": "an old task", "state": "finished", "repo": "shop",
		"worktree": ".worktrees/shop-k1", "branch": "mate/k1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.CrewDir("shop", "k1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.CrewBrief("shop", "k1"), []byte("Work in "+repo+".\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps, rt := mergeLiveDeps(t, session, configHome)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	spec, err := spawn.SessionSpec(deps, w)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	var out bytes.Buffer
	if _, err := migrate.Run(ctx, w, migrate.Deps{Runtime: deps.Runtime, Session: spec}, &out); err != nil {
		t.Fatalf("migrate: %v\n%s", err, out.String())
	}
	t.Logf("migrate in %s:\n%s", time.Since(start).Round(time.Millisecond), out.String())
	if w, err = store.Open(root); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "shop", "shop")
	if w.Layout() != 2 || !strings.Contains(out.String(), "moved "+repo+" -> "+moved) || !strings.Contains(out.String(), "repaired 1 worktree(s)") {
		t.Fatalf("migrate did not move shop and re-attach k1:\n%s", out.String())
	}
	if got := strings.TrimSpace(gitOut(t, k1, "rev-parse", "--show-toplevel")); got != k1 {
		t.Fatalf("k1's worktree reports toplevel %q, want %s", got, k1)
	}
	if brief, err := os.ReadFile(w.CrewBrief("shop", "k1")); err != nil || string(brief) != "Work in "+moved+".\n" {
		t.Fatalf("k1's brief = %q (%v), want it to name %s", brief, err, moved)
	}
	if !strings.Contains(out.String(), "rewrote 1 brief(s)") {
		t.Fatalf("migrate did not rewrite k1's brief:\n%s", out.String())
	}

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k2", true)
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})
	mateRes, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: claude.KindClaude})
	if err != nil {
		t.Fatalf("StartMate after migrate: %v", err)
	}
	t.Logf("Mate %s is running in pane %s", mateRes.Agent, mateRes.Pane)
	manual, err := os.ReadFile(filepath.Join(w.MateDir("shop"), "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manual), moved) {
		t.Fatalf("the Mate's manual does not name the moved repo %s", moved)
	}

	crewRes, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k2", Harness: codex.KindCodex,
		BriefText: brieftest.Ship("Change no file. Hand back with the status line `wait-mate: ok`."),
	})
	if err != nil {
		t.Fatalf("SpawnCrew after migrate: %v", err)
	}
	t.Logf("spawned crew %s in pane %s, worktree %s", crewRes.Agent, crewRes.Pane, crewRes.Worktree)
	if list := gitOut(t, moved, "worktree", "list", "--porcelain"); !strings.Contains(list, "worktree "+crewRes.Worktree+"\n") {
		t.Fatalf("k2's worktree is not a worktree of the moved repo:\n%s", list)
	}
	tail := crewPaneTail(ctx, rt, session, configHome, crewRes.Agent, "k2")
	done := waitForBoxEntry(t, ctx, w, "shop", 5*time.Minute, tail, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Crew == "k2" && e.Verb == "wait-mate"
	})
	if !strings.Contains(strings.ToLower(done.Text), "ok") {
		t.Fatalf("crew k2 handed back %q, want ok\npane:\n%s", done.Text, tail())
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer stopCancel()
	if _, err := spawn.StopCrew(stopCtx, w, deps, "shop", "k2", true); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if _, err := spawn.StopMate(stopCtx, w, deps, "shop"); err != nil {
		t.Fatalf("StopMate: %v", err)
	}
}
