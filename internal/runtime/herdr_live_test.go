package runtime_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex/codexlab"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
)

// recordingRunner wraps a ProcessRunner and records argv so the live test
// can assert --session stays before `--` on a real herdr binary.
type recordingRunner struct {
	inner process.Runner
	mu    sync.Mutex
	calls [][]string
}

func (r *recordingRunner) Run(ctx context.Context, spec process.Spec) (process.Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string(nil), spec.Args...))
	r.mu.Unlock()
	return r.inner.Run(ctx, spec)
}

func (r *recordingRunner) snapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// TestLiveHerdrSessionWorkspaceTabStart talks to a provisioned fm-lab-*
// session. `go test ./...` skips it. The worker runs it with
// MATE_HERDR_LIVE_SESSION set after helper provision; it refuses default
// and firstmate. Production argv ( --session before -- ) is used; the lab
// helper's trailing --session is not the adapter's isolation mechanism.
func liveLabSession(t *testing.T) (session, home string) {
	t.Helper()
	session = strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_SESSION"))
	if session == "" {
		t.Skip("set MATE_HERDR_LIVE_SESSION to a provisioned fm-lab-* Herdr session")
	}
	if session == "default" || session == "firstmate" {
		t.Fatal("refusing to run the live adapter against the default or firstmate session")
	}
	if !strings.HasPrefix(session, "fm-lab-") {
		t.Fatalf("live test requires an fm-lab- session, got %q", session)
	}
	userHome := strings.TrimSpace(os.Getenv("HOME"))
	if userHome == "" {
		t.Fatal("HOME is required to resolve the Herdr socket; do not use XDG_CONFIG_HOME for lab isolation")
	}
	// Every live test that reaches a lab session runs Codex in a lab
	// CODEX_HOME, never the operator's ~/.codex (internal/harness/codex/codexlab).
	codexlab.Home(t)
	home = filepath.Join(userHome, ".config")
	return session, home
}

func TestLiveHerdrLookupSessionDoesNotStartServer(t *testing.T) {
	requireLive(t)
	session, home := liveLabSession(t)
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("LookupSession started a herdr server")
		return nil
	}
	id, err := runtime.ParseWorkspaceID("ws_g406b2")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "mate", "session-owners", session)
	_ = os.Remove(marker)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	h, ok, err := rt.LookupSession(ctx, runtime.SessionSpec{
		WorkspaceID: id,
		Name:        session,
		ConfigHome:  home,
		Names:       runtime.NewMemoryNameRegistry(),
	})
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	if !ok {
		t.Fatal("provisioned lab session must look running")
	}
	if h.Name != session {
		t.Fatalf("name = %q, want %q", h.Name, session)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("LookupSession wrote an owner marker: %v", statErr)
	}
}

func TestLiveHerdrSessionWorkspaceTabStart(t *testing.T) {
	requireLive(t)
	session, home := liveLabSession(t)
	rec := &recordingRunner{inner: process.ExecRunner{}}
	rt := runtime.NewHerdr(rec)
	rt.Names = runtime.NewMemoryNameRegistry()
	rt.StartServer = func(context.Context, string) error {
		return nil // lab helper owns server lifecycle; do not spawn another
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	id, err := runtime.ParseWorkspaceID("ws_g4s3live")
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
	if handle.Name != session {
		t.Fatalf("session handle %q, want %q", handle.Name, session)
	}
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(home, "mate", "session-owners", session))
	})
	if !strings.Contains(handle.SocketPath, "/herdr/sessions/"+session+"/herdr.sock") {
		t.Fatalf("socket = %q", handle.SocketPath)
	}

	cwd := t.TempDir()
	label := "G4-04 live"
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: handle,
		Label:   label,
		Cwd:     cwd,
		Env: []runtime.EnvVar{
			{Key: "MATE_AGENT_ID", Value: "mate_g4s3"},
			{Key: "CLAUDE_CONFIG_DIR", Value: filepath.Join(filepath.Dir(home), ".claude")},
		},
	})
	if err != nil {
		t.Fatalf("EnsureProjectWorkspace: %v", err)
	}
	if ws.WorkspaceID == "" {
		t.Fatal("workspace id is empty")
	}

	// Idempotent reuse: Herdr is not unique on label (live w1+w2 both
	// "Project A"), so the adapter must not create a second one.
	again, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: handle,
		Label:   label,
		Cwd:     cwd,
	})
	if err != nil {
		t.Fatalf("EnsureProjectWorkspace reuse: %v", err)
	}
	if again.WorkspaceID != ws.WorkspaceID {
		t.Fatalf("label reuse created %q then %q", ws.WorkspaceID, again.WorkspaceID)
	}

	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: ws,
		Label:     "G4-04 mate",
		Cwd:       cwd,
	})
	if err != nil {
		t.Fatalf("CreateAgentTab: %v", err)
	}
	if tab.PaneID == "" || tab.TabID == "" {
		t.Fatalf("tab = %+v", tab)
	}
	if !strings.HasSuffix(tab.TabID, ":t1") || !strings.HasSuffix(tab.PaneID, ":p1") {
		t.Fatalf("Mate tab must be the workspace root tab, got %+v", tab)
	}

	ctxPath := filepath.Join(cwd, "context.md")
	if err := os.WriteFile(ctxPath, []byte("you are a gomate G4-04 live probe. reply with PONG."), 0o644); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(cwd, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	// A Crew-shaped launch takes its --settings from <state>/settings.json,
	// which is settingsPath; the file written above stands in for the one
	// Prepare names, so prep.Files is not written.
	prep, err := claude.Claude{}.Prepare(ctx, harness.PrepareRequest{
		Role: harness.RoleCrew, Cwd: cwd, StateDir: filepath.Dir(settingsPath), ContextPath: ctxPath,
		NewSessionID: func() string { return sessionID },
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	launch, err := claude.Claude{}.Build(ctx, harness.AgentSpec{
		Kind:        claude.KindClaude,
		Cwd:         cwd,
		ContextPath: prep.ContextPath,
		Launch:      prep.Launch,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	res, err := runtime.AllocateAgentName(rt.Names, handle.Name, "g4", "live01", runtime.FailOnCollision)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := runtime.NewAgentStartSpec(tab, res, launch, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := rt.StartAgent(ctx, spec)
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}

	obs, err := rt.WaitAgent(ctx, agent, runtime.WaitCondition{
		Until:   []runtime.AgentStatus{runtime.AgentBlocked, runtime.AgentIdle, runtime.AgentDone},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("WaitAgent: %v", err)
	}
	if obs.Status == runtime.AgentWorking {
		t.Fatalf("wait returned working: %+v", obs)
	}
	sawLocatorArgs := false
	for _, args := range rec.snapshot() {
		if !runtime.SessionBeforeTerminator(args) {
			t.Fatalf("live argv missing --session before --: %#v", args)
		}
		inOptions := true
		locatorArgs := []string{}
		for _, a := range args {
			if a == "--" {
				inOptions = false
				continue
			}
			if !inOptions && a == "--session" {
				t.Fatalf("live argv leaked --session after --: %#v", args)
			}
			if !inOptions {
				locatorArgs = append(locatorArgs, a)
			}
		}
		if len(locatorArgs) == 0 {
			continue
		}
		if sawLocatorArgs {
			t.Fatalf("multiple Herdr calls carried Claude locator args: %#v", rec.snapshot())
		}
		sawLocatorArgs = true
		want := []string{"--dangerously-skip-permissions", "--session-id", sessionID, "--settings", settingsPath, "--append-system-prompt-file", ctxPath}
		if !slices.Equal(locatorArgs, want) {
			t.Fatalf("live Claude args after herdr agent start -- = %#v, want %#v", locatorArgs, want)
		}
	}
	if !sawLocatorArgs {
		t.Fatal("real Herdr agent start did not carry any Claude locator args after --")
	}
	t.Log("G5_S6D_LIVE_OUTER_PROOF herdr_start_tail_observed=true child_side_forwarding=proven_by_stop_hook_probe")
}

// TestLiveHerdrDuplicateLabelSelectsMatchingCwd re-runs the reviewer's F3
// scenario against real Herdr: two workspaces with the same label and
// different cwds must not bind the first label match.
func TestLiveHerdrDuplicateLabelSelectsMatchingCwd(t *testing.T) {
	requireLive(t)
	session, home := liveLabSession(t)
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = runtime.NewMemoryNameRegistry()
	rt.StartServer = func(context.Context, string) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id, err := runtime.ParseWorkspaceID("ws_g4s3live")
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

	cwd1 := t.TempDir()
	cwd2 := t.TempDir()
	if cwd1 == cwd2 {
		t.Fatal("test setup: cwds must differ")
	}
	const label = "Duplicate"
	w1, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: handle, Label: label, Cwd: cwd1,
	})
	if err != nil {
		t.Fatalf("first Duplicate at %s: %v", cwd1, err)
	}
	w2, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: handle, Label: label, Cwd: cwd2,
	})
	if err != nil {
		t.Fatalf("second Duplicate at %s: %v", cwd2, err)
	}
	if w1.WorkspaceID == w2.WorkspaceID {
		t.Fatalf("same-label different cwd reused %s; Herdr permits duplicate labels", w1.WorkspaceID)
	}
	again, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: handle, Label: label, Cwd: cwd2,
	})
	if err != nil {
		t.Fatalf("reuse second cwd: %v", err)
	}
	if again.WorkspaceID != w2.WorkspaceID {
		t.Fatalf("cwd %s belongs to %s, adapter returned %s", cwd2, w2.WorkspaceID, again.WorkspaceID)
	}
	first, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: handle, Label: label, Cwd: cwd1,
	})
	if err != nil {
		t.Fatalf("reuse first cwd: %v", err)
	}
	if first.WorkspaceID != w1.WorkspaceID {
		t.Fatalf("cwd %s belongs to %s, adapter returned %s", cwd1, w1.WorkspaceID, first.WorkspaceID)
	}
}

// TestLiveHerdrFocusedCrewDoesNotDuplicateWorkspace is the reviewer's
// probe: root at cwd1, Crew tab at cwd2 focused, ensure(cwd1) must return
// the existing workspace.
func TestLiveHerdrFocusedCrewDoesNotDuplicateWorkspace(t *testing.T) {
	requireLive(t)
	session, home := liveLabSession(t)
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = runtime.NewMemoryNameRegistry()
	rt.StartServer = func(context.Context, string) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	id, err := runtime.ParseWorkspaceID("ws_g4s3live")
	if err != nil {
		t.Fatal(err)
	}
	handle, err := rt.EnsureSession(ctx, runtime.SessionSpec{
		WorkspaceID: id, Name: session, ConfigHome: home, Names: rt.Names,
	})
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(home, "mate", "session-owners", session))
	})

	cwd1 := t.TempDir()
	cwd2 := t.TempDir()
	const label = "DupFocus"
	w1, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: handle, Label: label, Cwd: cwd1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: w1, Label: "Mate", Cwd: cwd1,
	}); err != nil {
		t.Fatalf("Mate tab: %v", err)
	}
	crew, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: w1, Label: "Crew", Cwd: cwd2,
	})
	if err != nil {
		t.Fatalf("Crew tab: %v", err)
	}
	focus, err := process.ExecRunner{}.Run(ctx, process.Spec{
		Name: "herdr",
		Args: runtime.WithSession(session, []string{"tab", "focus", crew.TabID}),
	})
	if err != nil {
		t.Fatalf("tab focus: %v", err)
	}
	if focus.ExitCode != 0 {
		t.Fatalf("tab focus exit %d: %s", focus.ExitCode, focus.Stderr)
	}
	again, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: handle, Label: label, Cwd: cwd1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.WorkspaceID != w1.WorkspaceID {
		t.Fatalf("focused Crew tab made ensure(%s) create %s instead of reusing %s", cwd1, again.WorkspaceID, w1.WorkspaceID)
	}
}

// TestLiveHerdrIdentityEnvReachesStartedAgentProcess is the G4-05 proof:
// MATE_* is read from the started agent's process table (ps eww), not from
// the argv mate constructed. Mate env is workspace create --env; Crew env
// is tab create --env. agent start has no --env flag.
func TestLiveHerdrIdentityEnvReachesStartedAgentProcess(t *testing.T) {
	requireLive(t)
	session, home := liveLabSession(t)
	rec := &recordingRunner{inner: process.ExecRunner{}}
	rt := runtime.NewHerdr(rec)
	rt.Names = runtime.NewMemoryNameRegistry()
	rt.StartServer = func(context.Context, string) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	id, err := runtime.ParseWorkspaceID("ws_g4s4live")
	if err != nil {
		t.Fatal(err)
	}
	handle, err := rt.EnsureSession(ctx, runtime.SessionSpec{
		WorkspaceID: id, Name: session, ConfigHome: home, Names: rt.Names,
	})
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(home, "mate", "session-owners", session))
	})

	mateID := "mate_g4s4_" + filepath.Base(t.TempDir())
	crewID := "crew_g4s4_" + filepath.Base(t.TempDir())
	mateCwd := t.TempDir()
	crewCwd := t.TempDir()
	mateEnv := []runtime.EnvVar{
		{Key: config.EnvAgentID, Value: mateID},
		{Key: config.EnvAgentRole, Value: "mate"},
		{Key: config.EnvWorkspaceID, Value: "ws_g4s4live"},
		{Key: config.EnvProjectID, Value: "prj_g4s4"},
		{Key: config.EnvRuntimeSessionID, Value: session},
	}
	crewEnv := []runtime.EnvVar{
		{Key: config.EnvAgentID, Value: crewID},
		{Key: config.EnvAgentRole, Value: "crew"},
		{Key: config.EnvWorkspaceID, Value: "ws_g4s4live"},
		{Key: config.EnvProjectID, Value: "prj_g4s4"},
		{Key: config.EnvCrewID, Value: crewID},
		{Key: config.EnvRuntimeSessionID, Value: session},
	}

	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: handle, Label: "G4-05 live", Cwd: mateCwd, Env: mateEnv,
	})
	if err != nil {
		t.Fatalf("EnsureProjectWorkspace: %v", err)
	}
	mateTab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: ws, Label: "G4-05 mate", Cwd: mateCwd,
	})
	if err != nil {
		t.Fatalf("Mate tab: %v", err)
	}
	crewTab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: ws, Label: "G4-05 crew", Cwd: crewCwd, Env: crewEnv,
	})
	if err != nil {
		t.Fatalf("Crew tab: %v", err)
	}

	mateAgent := startLiveProbe(t, ctx, rt, handle, mateTab, "g4s4m")
	crewAgent := startLiveProbe(t, ctx, rt, handle, crewTab, "g4s4c")

	mateReady, err := runtime.AwaitReadiness(ctx, rt, mateAgent, 8*time.Second)
	if err != nil {
		t.Fatalf("Mate readiness: %v", err)
	}
	if mateReady.Kind != runtime.ReadinessReady && mateReady.Kind != runtime.ReadinessBlocked {
		t.Fatalf("Mate readiness = %+v (blocked is a normal observation)", mateReady)
	}
	crewReady, err := runtime.AwaitReadiness(ctx, rt, crewAgent, 8*time.Second)
	if err != nil {
		t.Fatalf("Crew readiness: %v", err)
	}
	if crewReady.Kind != runtime.ReadinessReady && crewReady.Kind != runtime.ReadinessBlocked {
		t.Fatalf("Crew readiness = %+v", crewReady)
	}

	mateLine := processTableLine(t, mateID)
	assertProcessEnv(t, mateLine, mateEnv)
	if strings.Contains(mateLine, "SECRET=") {
		t.Fatal("secret leaked into the Mate agent process")
	}
	crewLine := processTableLine(t, crewID)
	assertProcessEnv(t, crewLine, crewEnv)

	for _, args := range rec.snapshot() {
		if argvHasLive(args, "agent", "start") && argvHasLive(args, "--env") {
			t.Fatalf("agent start must not carry --env (Herdr has no such flag): %#v", args)
		}
		if argvHasLive(args, "workspace", "create") && !argvHasLive(args, "--env", config.EnvAgentID+"="+mateID) {
			t.Fatalf("Mate env must go through workspace create --env: %#v", args)
		}
		if argvHasLive(args, "tab", "create") && !argvHasLive(args, "--env", config.EnvAgentID+"="+crewID) {
			t.Fatalf("Crew env must go through tab create --env: %#v", args)
		}
	}
}

// TestLiveHerdrReadinessDistinguishesBlockedTimeoutAndMissing talks to real
// Herdr. blocked is a matching wait (exit 0). A missing name is not_found.
// A wait-until-idle on a blocked agent times out. None of that uses focus.
func TestLiveHerdrReadinessDistinguishesBlockedTimeoutAndMissing(t *testing.T) {
	requireLive(t)
	session, home := liveLabSession(t)
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = runtime.NewMemoryNameRegistry()
	rt.StartServer = func(context.Context, string) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	id, err := runtime.ParseWorkspaceID("ws_g4s4live")
	if err != nil {
		t.Fatal(err)
	}
	handle, err := rt.EnsureSession(ctx, runtime.SessionSpec{
		WorkspaceID: id, Name: session, ConfigHome: home, Names: rt.Names,
	})
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(home, "mate", "session-owners", session))
	})

	_, err = runtime.AwaitReadiness(ctx, rt, runtime.AgentHandle{Session: handle, Name: "no-such-g4s4"}, 2*time.Second)
	if err == nil {
		t.Fatal("missing agent must be a genuine failure")
	}
	coded, ok := err.(*observability.Error)
	if !ok || coded.Code != observability.CodeNotFound {
		t.Fatalf("missing agent: %v", err)
	}

	cwd := t.TempDir()
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: handle, Label: "G4-05 ready", Cwd: cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{Workspace: ws, Label: "ready-mate", Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	agent := startLiveProbe(t, ctx, rt, handle, tab, "g4s4r")
	got, err := runtime.AwaitReadiness(ctx, rt, agent, 8*time.Second)
	if err != nil {
		t.Fatalf("start wait: %v", err)
	}
	if got.Kind == runtime.ReadinessFailed || got.Kind == runtime.ReadinessUnknown {
		t.Fatalf("live agent classified as %s: %+v", got.Kind, got)
	}

	if got.Kind == runtime.ReadinessBlocked {
		obs, werr := rt.WaitAgent(ctx, agent, runtime.WaitCondition{
			Until:   []runtime.AgentStatus{runtime.AgentIdle},
			Timeout: 1500 * time.Millisecond,
		})
		if werr == nil {
			t.Fatalf("wait-until idle on a blocked agent must time out, got %+v", obs)
		}
		to, ok := werr.(*observability.Error)
		if !ok || to.Code != observability.CodeTimeout {
			t.Fatalf("expected timeout, got %v", werr)
		}
		blocked, berr := rt.WaitAgent(ctx, agent, runtime.WaitCondition{
			Until:   []runtime.AgentStatus{runtime.AgentBlocked},
			Timeout: 2 * time.Second,
		})
		if berr != nil {
			t.Fatalf("wait-until blocked is success: %v", berr)
		}
		if runtime.ClassifyObservation(blocked).Kind != runtime.ReadinessBlocked {
			t.Fatalf("blocked observation = %+v", blocked)
		}
	}
}

func startLiveProbe(t *testing.T, ctx context.Context, rt *runtime.Herdr, session runtime.SessionHandle, tab runtime.TabHandle, raw string) runtime.AgentHandle {
	t.Helper()
	ctxPath := filepath.Join(tab.Cwd, "context.md")
	if err := os.WriteFile(ctxPath, []byte("you are a gomate G4-05 live probe."), 0o644); err != nil {
		t.Fatal(err)
	}
	launch, err := claude.Claude{}.Build(ctx, harness.AgentSpec{
		Kind: claude.KindClaude, Cwd: tab.Cwd, ContextPath: ctxPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := runtime.AllocateAgentName(rt.Names, session.Name, "g4", raw, runtime.FailOnCollision)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := runtime.NewAgentStartSpec(tab, res, launch, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := rt.StartAgent(ctx, spec)
	if err != nil {
		t.Fatalf("StartAgent %s: %v", raw, err)
	}
	return agent
}

func processTableLine(t *testing.T, unique string) string {
	t.Helper()
	out, err := exec.Command("ps", "eww", "-ax").Output()
	if err != nil {
		t.Fatalf("ps eww: %v", err)
	}
	needle := []byte("MATE_AGENT_ID=" + unique)
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if bytes.Contains(line, needle) {
			return string(line)
		}
	}
	t.Fatalf("no started-agent process in ps eww carried MATE_AGENT_ID=%s (env did not reach the process)", unique)
	return ""
}

func assertProcessEnv(t *testing.T, line string, env []runtime.EnvVar) {
	t.Helper()
	for _, v := range env {
		want := v.Key + "=" + v.Value
		if !strings.Contains(line, want) {
			t.Fatalf("process env missing %s", want)
		}
	}
}

func argvHasLive(args []string, want ...string) bool {
	if len(want) == 0 {
		return false
	}
	for i := 0; i+len(want) <= len(args); i++ {
		ok := true
		for j := range want {
			if args[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
