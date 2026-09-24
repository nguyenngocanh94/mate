package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/runtime"
)

// TestPeekPrintsTheRawPane is the happy path: a live crew's pane comes back
// verbatim on stdout, with no header and no interpretation.
func TestPeekPrintsTheRawPane(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}
	rt.SetReadOutput(handle, codexBusyScreen)

	var out bytes.Buffer
	if err := peekCrew(context.Background(), w, deps, "shop", "k3", 40, &out); err != nil {
		t.Fatalf("peekCrew: %v", err)
	}
	if out.String() != codexBusyScreen {
		t.Fatalf("peek output = %q, want the raw pane %q", out.String(), codexBusyScreen)
	}
}

// TestPeekFallsBackToStatusLogWhenTheAgentIsAbsent is the task 13 fallback:
// a crew whose meta names no agent (stopped, via spawn.StopCrew) still has
// something worth showing - the tail of crews/<id>.status - and peek must
// print that instead of failing.
func TestPeekFallsBackToStatusLogWhenTheAgentIsAbsent(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	spawnFakeCrew(t, w, deps, "shop", "k3")
	if err := w.AppendStatus("shop", "k3", "working: reading the ticket"); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "done: ready in branch"); err != nil {
		t.Fatal(err)
	}
	if _, err := stopFakeCrew(t, w, deps, "shop", "k3"); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}

	var out bytes.Buffer
	if err := peekCrew(context.Background(), w, deps, "shop", "k3", 40, &out); err != nil {
		t.Fatalf("peekCrew: %v", err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "agent absent; last status:\n") {
		t.Fatalf("peek output = %q, want the absent-agent header first", got)
	}
	for _, want := range []string{"working: reading the ticket", "done: ready in branch"} {
		if !strings.Contains(got, want) {
			t.Fatalf("peek output = %q, want it to carry %q", got, want)
		}
	}
}

// TestPeekClampsLines checks the 1..200 clamp docs/mvp.md task 13 sets on
// --lines, independent of any runtime.
func TestPeekClampsLines(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{0, defaultPeekLines},
		{-5, defaultPeekLines},
		{500, maxPeekLines},
		{10, 10},
	}
	for _, tc := range tests {
		if got := clampPeekLines(tc.in); got != tc.want {
			t.Errorf("clampPeekLines(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
