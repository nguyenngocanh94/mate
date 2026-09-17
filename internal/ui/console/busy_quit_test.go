package console

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// ---------- N13: quitting during an in-flight action must not abandon it silently ----------
//
// Reproduced by the counter-review: runAction (actions.go) called
// m.action(context.Background(), ...) - not a context the program could act
// on - and onKey (update.go) let q/ctrl+c quit unconditionally, even with
// actionBusy set, so a saga like orchestration.SpawnCrew could be mid-flight
// (between git worktree add, tab create and agent start) when the process
// exited with no compensation and no word to the operator.
//
// The fix keeps q quitting - it always has, at every phase - but the
// context an in-flight ActionFunc runs under is now a cancellable child of
// the program's own context (model.baseCtx, set via WithContext), and
// quitting while actionBusy cancels it and records what was abandoned
// (Model.AbandonedAction) instead of leaving silently. cmd/matev2's
// handleConsole is where that gets told to the operator, since the Console
// itself has nothing left to draw once tea.Quit takes effect.

// blockingAction returns an ActionFunc that reports the context it was
// called with on started, then blocks until release is closed - standing in
// for a saga (SpawnCrew) still running when the operator tries to leave.
func blockingAction(started chan<- context.Context, release <-chan struct{}) ActionFunc {
	return func(ctx context.Context, _ ActionRequest) (string, error) {
		started <- ctx
		<-release
		return "", ctx.Err()
	}
}

// confirmAndRunStop drives the Console to a confirmed, running "stop" on
// the sample tree's active-binding Crew attempt, and returns the tea.Cmd
// runAction queued - the caller must run it (in a goroutine, since the
// ActionFunc here blocks) to actually invoke the ActionFunc.
func confirmAndRunStop(t *testing.T, action ActionFunc) (Model, tea.Cmd) {
	t.Helper()
	m := New(func(context.Context) (query.Snapshot, error) { return sampleTree(), nil }, nil, action)
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m, _ = send(t, m, m.Init()())
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	m, _ = send(t, m, key("down")) // stop
	m, _ = send(t, m, key("enter"))
	m, cmd := send(t, m, key("enter")) // confirmed; runAction's Cmd
	if cmd == nil || !m.actionBusy {
		t.Fatalf("confirmed stop did not start running: cmd=%v busy=%v", cmd, m.actionBusy)
	}
	return m, cmd
}

func TestQuittingWhileAnActionIsRunningCancelsItsContext(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	defer close(release)
	m, cmd := confirmAndRunStop(t, blockingAction(started, release))

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	var ctx context.Context
	select {
	case ctx = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("action never started")
	}
	select {
	case <-ctx.Done():
		t.Fatal("action's context was already cancelled before quitting")
	default:
	}

	m, quitCmd := send(t, m, key("q"))
	if quitCmd == nil || !m.Quitting() {
		t.Fatalf("q while an action is running must still quit: cmd=%v quitting=%v", quitCmd, m.Quitting())
	}

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("quitting mid-action did not cancel the context the ActionFunc is running under")
	}

	release <- struct{}{}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("action goroutine never returned after its context was cancelled")
	}
}

func TestQuittingWhileAnActionIsRunningReportsWhatWasAbandoned(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	defer close(release)
	m, cmd := confirmAndRunStop(t, blockingAction(started, release))
	go func() { cmd() }()
	<-started

	if desc, abandoned := m.AbandonedAction(); abandoned || desc != "" {
		t.Fatalf("AbandonedAction before quitting = %q, %v, want nothing yet", desc, abandoned)
	}

	m, _ = send(t, m, key("q"))
	desc, abandoned := m.AbandonedAction()
	if !abandoned {
		t.Fatalf("quitting mid-action must report an abandonment")
	}
	if !strings.Contains(desc, "stop") {
		t.Fatalf("abandoned-action description = %q, want it to name the stop that was running", desc)
	}
}

// TestActionRunsUnderTheProgramsOwnContextNotBackground pins the other half
// of N13: runAction must not hand the ActionFunc context.Background()
// directly - a context nothing can ever cancel - regardless of whether the
// operator quits. WithContext is how cmd/matev2 attaches the context it
// already gave tea.WithContext; a Model built without it (every other test
// in this package) still gets a context of its own, just rooted at
// context.Background() rather than handed that literal value.
func TestActionRunsUnderAContextThatWithContextControls(t *testing.T) {
	type ctxKey struct{}
	base := context.WithValue(context.Background(), ctxKey{}, "program-owned")
	seen := make(chan context.Context, 1)
	m := New(func(context.Context) (query.Snapshot, error) { return sampleTree(), nil }, nil,
		func(ctx context.Context, _ ActionRequest) (string, error) {
			seen <- ctx
			return "", nil
		}).WithContext(base)
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m, _ = send(t, m, m.Init()())
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter"))
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("confirmed stop did not queue the runner")
	}
	m, _ = send(t, m, cmd())
	select {
	case got := <-seen:
		if got.Value(ctxKey{}) != "program-owned" {
			t.Fatalf("ActionFunc ran under a context not derived from WithContext's base")
		}
	default:
		t.Fatal("ActionFunc was never called")
	}
	_ = m
}
