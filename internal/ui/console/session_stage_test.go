package console

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestEnterOnMateWithStageStaysOnTree(t *testing.T) {
	t.Parallel()
	var calls []SessionTarget
	m := projectFrame(t, sampleTree())
	m = m.WithStage(func(_ context.Context, target SessionTarget) error {
		calls = append(calls, target)
		return nil
	})
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter with StageFunc returned no Cmd")
	}
	m, _ = send(t, m, cmd())
	if m.sess.phase != sessionIdle {
		t.Fatalf("session phase = %v, want idle (no embedded view)", m.sess.phase)
	}
	frame := renderFrame(t, m)
	if strings.Contains(frame, "TERMINAL") {
		t.Fatalf("drew a session frame:\n%s", frame)
	}
	if !strings.Contains(frame, "Showing mate-payments-api in the next pane") {
		t.Fatalf("message line missing stage confirmation:\n%s", frame)
	}
	if len(calls) != 1 {
		t.Fatalf("stage calls = %d, want 1", len(calls))
	}
	if calls[0].Kind != SessionTargetMate || calls[0].AgentName != "mate-payments-api" {
		t.Fatalf("stage target = %+v", calls[0])
	}
}

func TestClickOnMateRowStages(t *testing.T) {
	t.Parallel()
	var calls []SessionTarget
	m := projectFrame(t, sampleTree())
	m = m.WithStage(func(_ context.Context, target SessionTarget) error {
		calls = append(calls, target)
		return nil
	})
	m, cmd := send(t, m, tea.MouseMsg{
		X: 2, Y: 4,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if cmd == nil {
		t.Fatal("click on the Mate row returned no Cmd")
	}
	m, _ = send(t, m, cmd())
	if m.sess.phase != sessionIdle {
		t.Fatalf("session phase = %v, want idle", m.sess.phase)
	}
	if len(calls) != 1 || calls[0].AgentName != "mate-payments-api" {
		t.Fatalf("stage calls = %+v", calls)
	}
}

func TestEnterOnMateWithoutStageStillOpensSession(t *testing.T) {
	t.Parallel()
	ctrl := &recordingSessionController{snap: SessionSnapshot{
		Transcript: SessionTranscript{Raw: "hello"},
	}}
	m := projectFrame(t, sampleTree())
	m = m.WithSession(ctrl.Read, ctrl.Prompt, ctrl.Close)
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter without StageFunc returned no Cmd")
	}
	m, _ = send(t, m, cmd())
	if m.sess.phase != sessionActive {
		t.Fatalf("session phase = %v, want active", m.sess.phase)
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "TERMINAL") {
		t.Fatalf("wanted the embedded session frame:\n%s", frame)
	}
}

func TestStageFailureStaysOnTree(t *testing.T) {
	t.Parallel()
	m := projectFrame(t, sampleTree())
	m = m.WithStage(func(context.Context, SessionTarget) error {
		return errors.New("the pane to the right is not mate's stage; close it or leave it empty")
	})
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd())
	if m.sess.phase != sessionIdle {
		t.Fatalf("session phase = %v, want idle", m.sess.phase)
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "the pane to the right is not mate's stage") {
		t.Fatalf("wanted the refusal on the message line:\n%s", frame)
	}
}
