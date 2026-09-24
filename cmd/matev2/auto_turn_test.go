package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// The three commands a Mate runs at the moments it is tempted to keep its
// turn open end, in auto mode, with a line telling it to end the turn
// (docs/mvp.md task 31), and print nothing extra in manual mode.

const (
	wantAutoSpawnLine = "auto mode: end your turn now; the console will wake you with a digest when k3 speaks. Do not poll."
	wantAutoStateLine = "auto mode: do not poll; end your turn and wait for the digest."
	wantAutoSendLine  = "auto mode: end your turn; the digest will tell you when k3 hands back."
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

func TestCrewSpawnEndsWithTheAutoTurnLineOnlyInAutoMode(t *testing.T) {
	w, fx := autoTurnFixture(t)
	for _, auto := range []bool{false, true} {
		setAuto(t, w, auto)
		var stdout, stderr bytes.Buffer
		writeCrewSpawnReport(&stdout, &stderr, w, fx.res)
		out := stdout.String()
		if !strings.HasPrefix(out, "spawned shop/k3: ") {
			t.Fatalf("auto=%v: spawn output does not start with the spawned line:\n%s", auto, out)
		}
		if auto {
			if got := lastLine(out); got != wantAutoSpawnLine {
				t.Fatalf("auto: last line = %q, want %q", got, wantAutoSpawnLine)
			}
			if n := strings.Count(out, "\n"); n != 4 {
				t.Fatalf("auto: spawn printed %d lines, want the three lines plus one:\n%s", n, out)
			}
			continue
		}
		if strings.Contains(out, "auto mode") {
			t.Fatalf("manual: spawn output mentions auto mode:\n%s", out)
		}
		if got := lastLine(out); !strings.HasPrefix(got, "status ") {
			t.Fatalf("manual: last line = %q, want the status line", got)
		}
	}
}

func TestStateEndsWithTheAutoTurnLineOnlyInAutoMode(t *testing.T) {
	w, fx := autoTurnFixture(t)
	result, err := stateOfCrew(context.Background(), w, fx.deps, "shop", "k3")
	if err != nil {
		t.Fatalf("stateOfCrew: %v", err)
	}

	setAuto(t, w, false)
	var manual bytes.Buffer
	writeStateReport(&manual, w, "shop", "k3", result)
	if got, want := manual.String(), result.Line()+"\n"; got != want {
		t.Fatalf("manual: state printed %q, want exactly the one state line %q", got, want)
	}

	setAuto(t, w, true)
	var auto bytes.Buffer
	writeStateReport(&auto, w, "shop", "k3", result)
	if got, want := auto.String(), result.Line()+"\n"+wantAutoStateLine+"\n"; got != want {
		t.Fatalf("auto: state printed %q, want the state line then %q", got, wantAutoStateLine)
	}
}

func TestMateSendEndsWithTheAutoTurnLineOnlyInAutoMode(t *testing.T) {
	w, fx := autoTurnFixture(t)
	report, err := sendToCrew(context.Background(), w, fx.deps, "shop", "k3", "use blue", store.SourceMate, send.Options{})
	if err != nil {
		t.Fatalf("sendToCrew: %v", err)
	}
	trace := sendSummaryLine(report) + "\n"
	// k3 has its brief, so the Mate's send also carries the correction
	// nudge (nudge.go); the auto-mode line stays last.
	mateTrace := trace + sendCorrectionNudge + "\n"

	setAuto(t, w, false)
	var manual bytes.Buffer
	writeSendReport(&manual, w, "shop", "k3", store.SourceMate, report)
	if got := manual.String(); got != mateTrace {
		t.Fatalf("manual: send printed %q, want the trace and the nudge %q", got, mateTrace)
	}

	setAuto(t, w, true)
	var auto bytes.Buffer
	writeSendReport(&auto, w, "shop", "k3", store.SourceMate, report)
	if got, want := auto.String(), mateTrace+wantAutoSendLine+"\n"; got != want {
		t.Fatalf("auto: send printed %q, want the trace then %q", got, wantAutoSendLine)
	}

	// The captain sending from a shell is not the Mate answering a Crew.
	var captain bytes.Buffer
	writeSendReport(&captain, w, "shop", "k3", store.SourceUser, report)
	if got := captain.String(); got != trace {
		t.Fatalf("auto, captain's send: printed %q, want the trace alone %q", got, trace)
	}
}
