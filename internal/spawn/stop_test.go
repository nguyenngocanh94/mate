package spawn_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
)

func TestStopMateConfirmsTheAgentIsGone(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	started, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}

	res, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if res.Agent != started.Agent || res.AlreadyGone {
		t.Fatalf("stop result = %+v, want it to have stopped a live agent", res)
	}
	if len(rt.Agents) != 0 {
		t.Fatalf("agents left behind: %v", rt.Agents)
	}
	if _, ok := rt.Tabs[started.Pane]; ok {
		t.Fatal("the Mate tab was not closed")
	}
	if !slices.Contains(rt.Calls, "StopAgent:graceful") {
		t.Fatalf("calls %v: Claude has a graceful stop and it must be tried first", rt.Calls)
	}

	meta := readMeta(t, w, "shop")
	for _, gone := range []string{spawn.MetaAgent, spawn.MetaPane, spawn.MetaTab, spawn.MetaWorkspace} {
		if _, ok := meta[gone]; ok {
			t.Errorf("meta still carries %s=%s after a stop", gone, meta[gone])
		}
	}
	if meta[spawn.MetaSessionID] != started.SessionID {
		t.Errorf("meta session_id = %q, want %q kept for resume", meta[spawn.MetaSessionID], started.SessionID)
	}
	if meta[spawn.MetaHarness] != "claude" || meta[spawn.MetaSession] != w.Session() {
		t.Errorf("meta = %v, want the harness and Herdr session kept", meta)
	}
	if meta[spawn.MetaStoppedAt] != "2026-09-17T10:00:00Z" {
		t.Errorf("meta stopped_at = %q", meta[spawn.MetaStoppedAt])
	}
}

func TestStopMateFailsWhenTheAgentSurvives(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	// Both stops report success and the agent is still in the inventory.
	rt.StopLeavesAgent = true

	_, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err == nil {
		t.Fatal("a stop that Herdr reported fine but did not perform must fail")
	}
	if !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("error = %v, want it to say the stop is not confirmed", err)
	}
	meta := readMeta(t, w, "shop")
	if meta[spawn.MetaAgent] == "" {
		t.Fatal("an unconfirmed stop must not clear the agent from the meta")
	}
}

func TestStopMateClearsAStaleRecord(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	started, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	rt.ClosePane(started.Pane)

	res, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate over a stale record: %v", err)
	}
	if !res.AlreadyGone {
		t.Fatal("the stop must report that the agent was already gone")
	}
	if meta := readMeta(t, w, "shop"); meta[spawn.MetaAgent] != "" || meta[spawn.MetaSessionID] == "" {
		t.Fatalf("meta = %v, want the agent cleared and the session id kept", meta)
	}
}

func TestStopMateWithoutARecordedAgent(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	if _, err := spawn.StopMate(context.Background(), w, fakeDeps(t, rt), "shop"); err == nil {
		t.Fatal("stopping a project with no recorded Mate must be an error")
	}
}

func TestMateStatusReportsWhatHerdrSees(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)

	stopped, err := spawn.MateStatus(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus: %v", err)
	}
	if stopped.State != spawn.StateStopped {
		t.Fatalf("state = %q, want stopped", stopped.State)
	}

	started, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	running, err := spawn.MateStatus(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus: %v", err)
	}
	if running.State != spawn.StateRunning {
		t.Fatalf("state = %q, want running", running.State)
	}
	if running.Observed != runtime.AgentIdle {
		t.Fatalf("observed = %q, want the Herdr status", running.Observed)
	}
	if !strings.Contains(running.Line(), "mate-shop") || !strings.Contains(running.Line(), "running") {
		t.Fatalf("line = %q", running.Line())
	}

	rt.ClosePane(started.Pane)
	stale, err := spawn.MateStatus(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus: %v", err)
	}
	if stale.State != spawn.StateStale {
		t.Fatalf("state = %q, want stale", stale.State)
	}
	if !strings.Contains(stale.Detail, "mate-shop") {
		t.Fatalf("detail = %q, want the recorded agent named", stale.Detail)
	}
}
