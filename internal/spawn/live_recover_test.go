package spawn_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/recover"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLiveRecoverAfterHerdrDies is the M17 proof: a real Claude Mate and a
// real Claude crew have each had a conversation, their panes are lost, and
// recover.Run - what the console runs when it opens - brings both back with
// resumed=true and the recorded session ids.
//
// The panes are lost by force-stopping the two agents behind mate's back, not
// by killing the server: the lab session is provisioned and owned by the
// runner (these tests never start a Herdr server), so a dead server would
// have nothing to bring it back. What recovery reads is the same either way:
// meta that records a pane, and Herdr that does not list the agent.
func TestLiveRecoverAfterHerdrDies(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

	root := t.TempDir()
	w, err := store.Init(root, store.Defaults{})
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k8", true)
	})

	mate, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: claude.KindClaude, Resume: true})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	crew, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k8", Harness: claude.KindClaude,
		BriefText: brieftest.Ship("Reply with the single word ok and do nothing else."),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	handle := func(name, raw, ws, tab, pane string) runtime.AgentHandle {
		sess := runtime.SessionHandle{Name: session, ConfigHome: configHome}
		return runtime.AgentHandle{Session: sess, Name: name, RawID: raw, Kind: claude.KindClaude,
			Tab: runtime.TabHandle{Session: sess, WorkspaceID: ws, TabID: tab, PaneID: pane}}
	}
	mateHandle := handle(mate.Agent, "shop", mate.Workspace, mate.Tab, mate.Pane)
	crewHandle := handle(crew.Agent, "k8", crew.Workspace, crew.Tab, crew.Pane)
	if err := rt.PromptAgent(ctx, mateHandle, "remember the word ZEBRA and reply OK"); err != nil {
		t.Fatalf("PromptAgent: %v", err)
	}
	// Claude writes a session to disk with its first turn; both must have
	// answered once before there is a conversation to resume.
	time.Sleep(45 * time.Second)

	for _, h := range []runtime.AgentHandle{mateHandle, crewHandle} {
		if err := rt.StopAgent(ctx, h, runtime.StopForce); err != nil && !runtime.IsAgentNotFound(err) {
			t.Fatalf("losing %s: %v", h.Name, err)
		}
	}

	res := recover.Run(ctx, w, deps, nil)
	if res.Err != nil {
		t.Fatalf("recover.Run: %v", res.Err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("recovered %+v, want the Mate and the crew", res.Items)
	}
	for _, it := range res.Items {
		if it.Err != nil || !it.Resumed {
			t.Fatalf("%s: err %v resumed %v note %q, want it back with its conversation", it.Name(), it.Err, it.Resumed, it.Note)
		}
	}
	meta, err := w.ReadCrewMeta("shop", "k8")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaSessionID] != crew.SessionID || meta[spawn.MetaResumed] != "true" {
		t.Fatalf("crew meta = %v, want session %s resumed", meta, crew.SessionID)
	}
}
