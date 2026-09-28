package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The three commands a Mate runs at the moments it is tempted to keep its
// turn open end with a line telling it to end the turn, in every mode
// (docs/mvp.md tasks 31 and 57): a Mate inside a turn locks the captain out
// whether or not a digest is coming.

const (
	wantTurnSpawnLine = "turn: end it now; when k3 speaks it reaches the captain's box, and you as a digest. Do not poll."
	wantTurnStateLine = "turn: do not poll; end it and a digest will wake you when a crew needs you."
	wantTurnSendLine  = "turn: end it now; a digest will tell you when k3 hands back."
)

// autoTurnFixture is one project with one fake-runtime crew, k3, whose pane
// accepts a send.
func autoTurnFixture(t *testing.T) (*store.Workspace, autoTurnCrew) {
	t.Helper()
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}
	rt.SetReadOutput(handle, codexEmptyScreen)
	rt.OnSendText = func(h runtime.AgentHandle, text string) { rt.SetReadOutput(h, codexPendingScreen(text)) }
	rt.OnSendKeys = func(h runtime.AgentHandle, _ []string) { rt.SetReadOutput(h, codexBusyScreen) }
	return w, autoTurnCrew{deps: deps, res: res}
}

type autoTurnCrew struct {
	deps spawn.Deps
	res  spawn.CrewResult
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	return lines[len(lines)-1]
}

func setAuto(t *testing.T, w *store.Workspace, on bool) {
	t.Helper()
	if err := w.SetAuto("shop", on); err != nil {
		t.Fatalf("SetAuto(%v): %v", on, err)
	}
}

func TestCrewSpawnEndsWithTheTurnLineInEveryMode(t *testing.T) {
	w, fx := autoTurnFixture(t)
	for _, auto := range []bool{false, true} {
		setAuto(t, w, auto)
		var stdout, stderr bytes.Buffer
		writeCrewSpawnReport(&stdout, &stderr, w, fx.res)
		out := stdout.String()
		if !strings.HasPrefix(out, "spawned shop/k3: ") {
			t.Fatalf("auto=%v: spawn output does not start with the spawned line:\n%s", auto, out)
		}
		if got := lastLine(out); got != wantTurnSpawnLine {
			t.Fatalf("auto=%v: last line = %q, want %q", auto, got, wantTurnSpawnLine)
		}
		if n := strings.Count(out, "\n"); n != 4 {
			t.Fatalf("auto=%v: spawn printed %d lines, want the three lines plus one:\n%s", auto, n, out)
		}
	}
}

func TestStateEndsWithTheTurnLineInEveryMode(t *testing.T) {
	w, fx := autoTurnFixture(t)
	result, err := stateOfCrew(context.Background(), w, fx.deps, "shop", "k3")
	if err != nil {
		t.Fatalf("stateOfCrew: %v", err)
	}
	for _, auto := range []bool{false, true} {
		setAuto(t, w, auto)
		var out bytes.Buffer
		writeStateReport(&out, w, "shop", "k3", result)
		if got, want := out.String(), result.Line()+"\n"+wantTurnStateLine+"\n"; got != want {
			t.Fatalf("auto=%v: state printed %q, want the state line then %q", auto, got, wantTurnStateLine)
		}
	}
}

func TestMateSendEndsWithTheTurnLineInEveryMode(t *testing.T) {
	w, fx := autoTurnFixture(t)
	report, err := sendToCrew(context.Background(), w, fx.deps, "shop", "k3", "use blue", store.SourceMate, send.Options{})
	if err != nil {
		t.Fatalf("sendToCrew: %v", err)
	}
	trace := sendSummaryLine(report) + "\n"
	// k3 has its brief, so the Mate's send also carries the correction
	// nudge (nudge.go); the turn line stays last.
	mateTrace := trace + sendCorrectionNudge + "\n"
	for _, auto := range []bool{false, true} {
		setAuto(t, w, auto)
		var out bytes.Buffer
		writeSendReport(&out, w, "shop", "k3", store.SourceMate, report)
		if got, want := out.String(), mateTrace+wantTurnSendLine+"\n"; got != want {
			t.Fatalf("auto=%v: send printed %q, want the trace then %q", auto, got, wantTurnSendLine)
		}
	}

	// The captain sending from a shell is not the Mate answering a Crew.
	var captain bytes.Buffer
	writeSendReport(&captain, w, "shop", "k3", store.SourceUser, report)
	if got := captain.String(); got != trace {
		t.Fatalf("captain's send: printed %q, want the trace alone %q", got, trace)
	}
}
