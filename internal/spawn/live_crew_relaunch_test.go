package spawn_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLiveCrewRelaunchAfterThePaneDies is the recovery proof for a Herdr
// pane that is gone: a real Codex crew is spawned, its agent is force-stopped
// behind mate's back (the harness died; the pane is empty), and `crew
// relaunch` starts it again in the same worktree on the same branch, with
// the meta naming the new pane. Nothing here starts a Herdr server: the
// runner provisions the lab.
func TestLiveCrewRelaunchAfterThePaneDies(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	useLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
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
	deps := spawn.Deps{
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

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k9", true)
	})

	res, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k9", Harness: harness.KindCodex,
		BriefText: brieftest.Ship("Reply with the single word ok and do nothing else."),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	// The pane dies behind mate's back: the agent is gone, the worktree and
	// the branch are not.
	dead := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session},
		Name:    res.Agent,
		RawID:   "k9",
		Kind:    harness.KindCodex,
		Tab: runtime.TabHandle{
			Session: runtime.SessionHandle{Name: session}, WorkspaceID: res.Workspace,
			TabID: res.Tab, PaneID: res.Pane, Label: spawn.CrewTabLabelPrefix + "k9",
		},
	}
	if _, err := rt.InspectAgent(ctx, dead); err != nil {
		t.Fatalf("the freshly spawned agent is not inspectable: %v", err)
	}
	if err := rt.StopAgent(ctx, dead, runtime.StopForce); err != nil && !runtime.IsAgentNotFound(err) {
		t.Fatalf("force-stopping the crew's agent: %v", err)
	}

	again, err := spawn.RelaunchCrew(ctx, w, deps, "shop", "k9", "the harness had died")
	if err != nil {
		t.Fatalf("RelaunchCrew: %v", err)
	}
	if again.Pane == res.Pane || again.Agent == "" {
		t.Fatalf("relaunch = %+v, want a new live pane", again)
	}
	live := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session},
		Name:    again.Agent,
		RawID:   "k9",
		Kind:    harness.KindCodex,
		Tab: runtime.TabHandle{
			Session: runtime.SessionHandle{Name: session}, WorkspaceID: again.Workspace,
			TabID: again.Tab, PaneID: again.Pane, Label: spawn.CrewTabLabelPrefix + "k9",
		},
	}
	observed, err := rt.InspectAgent(ctx, live)
	if err != nil {
		t.Fatalf("the relaunched agent is not live: %v", err)
	}
	if !observed.LiveHandleOK {
		t.Fatalf("the relaunched agent's pane is stale: %+v", observed)
	}
	if got := strings.TrimSpace(git(t, again.Worktree, "rev-parse", "--abbrev-ref", "HEAD")); got != "mate/k9" {
		t.Fatalf("relaunch moved the worktree off its branch: HEAD = %q", got)
	}
	meta, err := w.ReadCrewMeta("shop", "k9")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaAgent] != again.Agent || meta[spawn.MetaPane] != again.Pane {
		t.Fatalf("meta names %q/%q, want %q/%q", meta[spawn.MetaAgent], meta[spawn.MetaPane], again.Agent, again.Pane)
	}
}
