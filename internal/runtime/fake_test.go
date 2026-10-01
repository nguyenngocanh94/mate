package runtime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
)

func TestFakeStartPaneNotFoundDistinctFromAgentNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	missing := tab
	missing.PaneID = "w1:p-missing"
	spec := mustStartSpec(t, missing, res, launch)
	_, err := rt.StartAgent(ctx, spec)
	if err == nil {
		t.Fatal("expected pane not found")
	}
	coded, ok := err.(*observability.Error)
	if !ok {
		t.Fatalf("err type %T", err)
	}
	if coded.Code != observability.CodeUsage || coded.Details["herdr_code"] != runtime.HerdrAgentPaneNotFound {
		t.Fatalf("pane missing must be usage/agent_pane_not_found, got %+v", coded)
	}

	h, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.InspectAgent(ctx, runtime.AgentHandle{Session: session, Name: "no-such"})
	if err == nil {
		t.Fatal("expected agent not found")
	}
	coded, ok = err.(*observability.Error)
	if !ok || coded.Code != observability.CodeNotFound || coded.Details["herdr_code"] != runtime.HerdrAgentNotFound {
		t.Fatalf("missing name must be not_found/agent_not_found, got %+v", err)
	}
	_ = h
}

func TestFakeWaitUntilBlockedIsSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	h, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err != nil {
		t.Fatal(err)
	}
	rt.SetAgentStatus(session.Name, h.Name, runtime.AgentBlocked)
	obs, err := rt.WaitAgent(ctx, h, runtime.WaitCondition{Until: []runtime.AgentStatus{runtime.AgentBlocked}, Timeout: time.Second})
	if err != nil {
		t.Fatalf("blocked wait must succeed: %v", err)
	}
	if obs.Status != runtime.AgentBlocked {
		t.Fatalf("status = %q", obs.Status)
	}
	if _, err := rt.WaitAgent(ctx, h, runtime.WaitCondition{Until: []runtime.AgentStatus{runtime.AgentBlocked}}); err == nil {
		t.Fatal("zero timeout must be refused; it is not forever")
	}
}

func TestFakeAwaitReadinessTreatsBlockedAsSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	h, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err != nil {
		t.Fatal(err)
	}
	rt.SetAgentStatus(session.Name, h.Name, runtime.AgentBlocked)
	got, err := runtime.AwaitReadiness(ctx, rt, h, time.Second)
	if err != nil {
		t.Fatalf("blocked is a matching wait, not an error: %v", err)
	}
	if got.Kind != runtime.ReadinessBlocked {
		t.Fatalf("readiness = %+v", got)
	}

	if err := rt.RemoveTab(ctx, tab); err != nil {
		t.Fatal(err)
	}
	stale, err := runtime.AwaitReadiness(ctx, rt, h, time.Second)
	if err == nil || stale.Kind != runtime.ReadinessFailed {
		t.Fatalf("stale handle must not look ready: got %+v err=%v", stale, err)
	}
}

func TestFakeEnsureProjectWorkspaceReuseRefusesEnv(t *testing.T) {
	t.Parallel()
	rt := runtime.NewFake()
	id, err := runtime.ParseWorkspaceID("ws_test1")
	if err != nil {
		t.Fatal(err)
	}
	session, err := rt.EnsureSession(context.Background(), runtime.SessionSpec{WorkspaceID: id, ConfigHome: "/tmp/cfg"})
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if _, err := rt.EnsureProjectWorkspace(context.Background(), runtime.WorkspaceSpec{
		Session: session, Label: "P", Cwd: cwd,
		Env: []runtime.EnvVar{{Key: "MATE_AGENT_ID", Value: "mate_001"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = rt.EnsureProjectWorkspace(context.Background(), runtime.WorkspaceSpec{
		Session: session, Label: "P", Cwd: cwd,
		Env: []runtime.EnvVar{{Key: "MATE_AGENT_ID", Value: "mate_001"}},
	})
	if err == nil {
		t.Fatal("reuse with env would not inject it; refuse rather than drop")
	}
	if _, err := rt.EnsureProjectWorkspace(context.Background(), runtime.WorkspaceSpec{
		Session: session, Label: "P", Cwd: cwd,
	}); err != nil {
		t.Fatalf("reuse without env must still succeed: %v", err)
	}
}

func TestFakeCreateAgentTabRefusesUnknownEnv(t *testing.T) {
	t.Parallel()
	rt := runtime.NewFake()
	id, err := runtime.ParseWorkspaceID("ws_test1")
	if err != nil {
		t.Fatal(err)
	}
	session, err := rt.EnsureSession(context.Background(), runtime.SessionSpec{WorkspaceID: id, ConfigHome: "/tmp/cfg"})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := rt.EnsureProjectWorkspace(context.Background(), runtime.WorkspaceSpec{Session: session, Label: "P", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.CreateAgentTab(context.Background(), runtime.TabSpec{
		Workspace: ws, Label: "Crew", Cwd: t.TempDir(),
		Env: []runtime.EnvVar{{Key: "SECRET", Value: "nope"}},
	})
	if err == nil {
		t.Fatal("unknown env must be refused")
	}
}

func TestFakeStartArgvPutsSessionBeforeDashDash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	if _, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch)); err != nil {
		t.Fatal(err)
	}
	if len(rt.StartArgv) != 1 {
		t.Fatalf("argv calls = %d", len(rt.StartArgv))
	}
	if !runtime.SessionBeforeTerminator(rt.StartArgv[0]) {
		t.Fatalf("argv = %#v", rt.StartArgv[0])
	}
}

// The harness kind is derived from the launch spec, so a start-spec kind that
// disagrees with the validated argv is unrepresentable. The argv Herdr gets
// must carry that derived kind.
func TestAgentStartSpecKindDerivesFromLaunchSpec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	spec := mustStartSpec(t, tab, res, launch)
	if spec.Kind() != harness.KindClaude {
		t.Fatalf("kind = %q, want %q (derived from the launch spec)", spec.Kind(), harness.KindClaude)
	}
	if _, err := rt.StartAgent(ctx, spec); err != nil {
		t.Fatal(err)
	}
	argv := rt.StartArgv[0]
	found := false
	for i, a := range argv {
		if a == "--kind" && i+1 < len(argv) && argv[i+1] == string(harness.KindClaude) {
			found = true
		}
	}
	if !found {
		t.Fatalf("argv must carry --kind %s: %#v", harness.KindClaude, argv)
	}
}

// A live name is only expressible as a NameReservation from
// AllocateAgentName; a zero reservation proves nothing and must not construct.
func TestNewAgentStartSpecRefusesZeroReservation(t *testing.T) {
	t.Parallel()
	_, launch, _, tab := boot(t)
	_, err := runtime.NewAgentStartSpec(tab, runtime.NameReservation{}, launch, 0)
	if err == nil {
		t.Fatal("zero reservation must not construct a start spec")
	}
	if observability.ExitCode(err) != observability.ExitUsage {
		t.Fatalf("err = %v", err)
	}
}

// Herdr rejects agent start --timeout outside (3000, 300000] ms (ADR 0003);
// a spec carrying such a timeout must be unconstructible, not caught by the
// CLI at start time.
func TestNewAgentStartSpecEnforcesHerdrTimeoutBounds(t *testing.T) {
	t.Parallel()
	rt, launch, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	for _, timeout := range []time.Duration{2 * time.Second, 10 * time.Minute} {
		_, err := runtime.NewAgentStartSpec(tab, res, launch, timeout)
		if err == nil {
			t.Fatalf("timeout %v is outside Herdr's range and must not construct", timeout)
		}
		if observability.ExitCode(err) != observability.ExitUsage {
			t.Fatalf("timeout %v: err = %v", timeout, err)
		}
	}
	spec, err := runtime.NewAgentStartSpec(tab, res, launch, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Timeout() != time.Minute {
		t.Fatalf("timeout = %v", spec.Timeout())
	}
}

// A reservation from one session must not start an agent in a pane that
// lives in another session.
func TestNewAgentStartSpecRefusesCrossSessionReservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, _, tab := boot(t)
	id2, err := runtime.ParseWorkspaceID("ws_test2")
	if err != nil {
		t.Fatal(err)
	}
	other, err := rt.EnsureSession(ctx, runtime.SessionSpec{WorkspaceID: id2, ConfigHome: "/tmp/cfg"})
	if err != nil {
		t.Fatal(err)
	}
	if other.Name == tab.Session.Name {
		t.Fatalf("test setup: sessions must differ, both %q", other.Name)
	}
	res := mustReserve(t, rt, other, "mate_001")
	_, err = runtime.NewAgentStartSpec(tab, res, launch, 0)
	if err == nil {
		t.Fatal("a reservation from another session must not construct")
	}
	if observability.ExitCode(err) != observability.ExitUsage {
		t.Fatalf("err = %v", err)
	}
}

// A reservation proves allocation, not current ownership. A name released
// and re-allocated to a different raw id since must fail the start as a
// collision, not silently start under the new owner's name.
func TestFakeStartRefusesStaleReservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := boot(t)
	const prefix = "abcdefghijklmnopqrstuv"
	stale, err := runtime.AllocateAgentName(rt.Names, session.Name, prefix, "id-120227X", runtime.FailOnCollision)
	if err != nil {
		t.Fatal(err)
	}
	rt.Names.Release(session.Name, stale.Name())
	fresh, err := runtime.AllocateAgentName(rt.Names, session.Name, prefix, "id-139869X", runtime.FailOnCollision)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Name() != stale.Name() {
		t.Fatalf("test setup: documented collision pair must share a live name (%q vs %q)", fresh.Name(), stale.Name())
	}
	_, err = rt.StartAgent(ctx, mustStartSpec(t, tab, stale, launch))
	if err == nil {
		t.Fatal("stale reservation must not start under a name now owned by a different raw id")
	}
	if !errors.Is(err, runtime.ErrNameCollision) && observability.ExitCode(err) != observability.ExitStateConflict {
		t.Fatalf("err = %v", err)
	}
	if owner, ok := rt.Names.Occupied(session.Name, fresh.Name()); !ok || owner != "id-139869X" {
		t.Fatalf("refused start mutated the reservation: owner=%q ok=%v", owner, ok)
	}
	if len(rt.StartArgv) != 0 {
		t.Fatalf("refused start still emitted argv: %#v", rt.StartArgv)
	}
}

func TestFakeRefusesUnstartableLaunchSpec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, _, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	spec, err := runtime.NewAgentStartSpec(tab, res, harness.LaunchSpec{}, 0)
	if err == nil {
		t.Fatal("zero launch spec must not construct a start spec")
	}
	if spec != nil {
		t.Fatalf("failed construction must return a nil spec, got %#v", spec)
	}
	// nil is the only AgentStartSpec value expressible without the
	// constructor (the implementation is sealed); the adapter boundary must
	// refuse it rather than start.
	_, err = rt.StartAgent(ctx, nil)
	if err == nil {
		t.Fatal("nil start spec must not start")
	}
	if observability.ExitCode(err) != observability.ExitUsage {
		t.Fatalf("nil spec must be usage, got %v", err)
	}
}

func TestFakeStalePaneRequiresRecheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	h, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err != nil {
		t.Fatal(err)
	}
	rt.DropPane(tab.PaneID)
	obs, err := rt.InspectAgent(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if obs.LiveHandleOK {
		t.Fatal("stale pane must not look live; closed-id reuse is unproven")
	}
}

func TestFakeListAgentsIsTheInventoryNotFocus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	h, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err != nil {
		t.Fatal(err)
	}
	list, err := rt.ListAgents(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Handle.Name != h.Name {
		t.Fatalf("list = %#v", list)
	}
	other := session
	other.Name = "other-session"
	empty, err := rt.ListAgents(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("other session must not inherit this inventory: %#v", empty)
	}
}

func TestFakeStopLeavesAgentIsNotAConfirmedStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := boot(t)
	rt.StopLeavesAgent = true
	res := mustReserve(t, rt, session, "mate_001")
	h, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.StopAgent(ctx, h, runtime.StopForce); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.InspectAgent(ctx, h); err != nil {
		t.Fatalf("unconfirmed stop left the name live; inspect must still find it: %v", err)
	}
	if rt.LiveAgentCount() != 1 {
		t.Fatalf("inventory = %d, want 1", rt.LiveAgentCount())
	}
}

func mustReserve(t *testing.T, rt *runtime.Fake, session runtime.SessionHandle, raw string) runtime.NameReservation {
	t.Helper()
	res, err := runtime.AllocateAgentName(rt.Names, session.Name, "m", raw, runtime.FailOnCollision)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func mustStartSpec(t *testing.T, tab runtime.TabHandle, res runtime.NameReservation, launch harness.LaunchSpec) runtime.AgentStartSpec {
	t.Helper()
	spec, err := runtime.NewAgentStartSpec(tab, res, launch, 0)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func boot(t *testing.T) (*runtime.Fake, harness.LaunchSpec, runtime.SessionHandle, runtime.TabHandle) {
	t.Helper()
	ctx := context.Background()
	rt := runtime.NewFake()
	id, err := runtime.ParseWorkspaceID("ws_test1")
	if err != nil {
		t.Fatal(err)
	}
	session, err := rt.EnsureSession(ctx, runtime.SessionSpec{WorkspaceID: id, ConfigHome: "/tmp/cfg"})
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: session, Label: "P", Cwd: cwd,
		Env: []runtime.EnvVar{{Key: "MATE_AGENT_ID", Value: "mate_001"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: ws, Label: "Mate", Cwd: cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Env) != 1 || tab.Env[0].Key != "MATE_AGENT_ID" {
		t.Fatalf("tab env = %#v", tab.Env)
	}
	path := filepath.Join(cwd, "context.md")
	if err := os.WriteFile(path, []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	launch, err := harness.Claude{}.Build(ctx, harness.AgentSpec{
		Kind: harness.KindClaude, Cwd: cwd, ContextPath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rt, launch, session, tab
}

func TestFakeStartRejectingTakenNameLeavesNoSideEffect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	if _, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch)); err != nil {
		t.Fatal(err)
	}
	argvAfterFirst := len(rt.StartArgv)
	// The only way to aim a second start at a live name is the same
	// reservation; a different raw id cannot express this spec at all.
	_, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err == nil {
		t.Fatal("restarting a live name must fail")
	}
	coded, ok := err.(*observability.Error)
	if !ok || coded.Details["herdr_code"] != runtime.HerdrAgentNameTaken {
		t.Fatalf("err = %+v, want agent_name_taken", err)
	}
	if len(rt.StartArgv) != argvAfterFirst {
		t.Fatalf("failed start recorded argv: %d entries, want %d", len(rt.StartArgv), argvAfterFirst)
	}
	if raw, ok := rt.Names.Occupied(session.Name, res.Name()); !ok || raw != "mate_001" {
		t.Fatalf("failed start mutated the reservation: raw=%q ok=%v", raw, ok)
	}
}

// A LaunchSpec is validated against one absolute cwd; Codex discovers its
// context from the cwd the pane actually runs in and does not fail on a
// missing project file. A pane cwd that disagrees is unconstructible.
func TestStartSpecRefusesPaneCwdThatDisagreesWithLaunchSpec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	elsewhere := tab
	elsewhere.Cwd = t.TempDir()
	_, err := runtime.NewAgentStartSpec(elsewhere, res, launch, 0)
	if err == nil {
		t.Fatal("a pane running elsewhere than the validated cwd must not construct a start spec")
	}
	if observability.ExitCode(err) != observability.ExitUsage {
		t.Fatalf("err = %v", err)
	}
	_ = ctx
	if len(rt.StartArgv) != 0 {
		t.Fatalf("refused spec still emitted argv: %#v", rt.StartArgv)
	}
}

// Startable() is only a shape check; it does not prove the launch spec's
// required context is still deliverable. The start boundary must validate,
// not just shape-check, so a spec whose context stopped being deliverable
// after harness.NewLaunchSpec (or a same-package hand-assembled one) is refused
// (Codex counter-review, final item).
func TestStartBoundaryValidatesLaunchSpecNotJustShape(t *testing.T) {
	t.Parallel()
	rt, launch, session, tab := boot(t)
	res := mustReserve(t, rt, session, "mate_001")
	if err := os.Remove(launch.ContextPath()); err != nil {
		t.Fatal(err)
	}
	if !launch.Startable() {
		t.Fatal("test setup: the spec must still be startable-shaped")
	}
	_, err := runtime.NewAgentStartSpec(tab, res, launch, 0)
	if err == nil {
		t.Fatal("a startable-shaped spec whose required context is gone must not construct a start spec")
	}
	if !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired", err)
	}
}

// Herdr reports a pane cwd with symlinks resolved (macOS /var -> /private/var),
// while the launch spec may hold the spelling the caller passed. The cwd guard
// must treat both as the same directory, and still refuse a different one.
func TestNewAgentStartSpecAcceptsSymlinkedSpellingOfPaneCwd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, _, session, tab := boot(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(real, "context.md")
	if err := os.WriteFile(path, []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	launch, err := harness.Claude{}.Build(ctx, harness.AgentSpec{
		Kind: harness.KindClaude, Cwd: link, ContextPath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := mustReserve(t, rt, session, "mate_sym")

	viaLink := tab
	viaLink.Cwd = real
	if _, err := runtime.NewAgentStartSpec(viaLink, res, launch, 0); err != nil {
		t.Fatalf("symlinked spelling of the same cwd must be accepted: %v", err)
	}

	other := tab
	other.Cwd = t.TempDir()
	if _, err := runtime.NewAgentStartSpec(other, res, launch, 0); err == nil {
		t.Fatal("a different pane cwd must still be refused")
	}
}
