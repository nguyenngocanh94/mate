package console

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// Quitting during an in-flight action must not abandon it silently. q still
// quits - it always has - but the context an ActionFunc runs under is a
// cancellable child of the program's own (WithContext), and quitting while
// actionBusy cancels it and records what was abandoned, which cmd/mate tells
// the operator once the terminal is back.

// blockingAction reports the context it was called with on started, then
// blocks until release - a saga still running when the operator leaves.
func blockingAction(started chan<- context.Context, release <-chan struct{}) ActionFunc {
	return func(ctx context.Context, _ ActionRequest) (string, error) {
		started <- ctx
		<-release
		return "", ctx.Err()
	}
}

// confirmAndRunStop drives the Console to a confirmed, running stop on the
// sample tree's active crew and returns the Cmd runAction queued; the
// caller runs it (in a goroutine, since the ActionFunc blocks).
func confirmAndRunStop(t *testing.T, action ActionFunc) (Model, tea.Cmd) {
	t.Helper()
	m := New(func(context.Context) (query.Snapshot, error) { return sampleTree(), nil }, action)
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 36})
	m, _ = send(t, m, m.Init()())
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	m, _ = send(t, m, key("x"))
	m, cmd := send(t, m, key("x")) // confirmed; runAction's Cmd
	if cmd == nil || !m.actionBusy {
		t.Fatalf("confirmed stop did not start running: cmd=%v busy=%v", cmd != nil, m.actionBusy)
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
		t.Fatalf("q while an action is running must still quit: cmd=%v quitting=%v", quitCmd != nil, m.Quitting())
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("quitting mid-action did not cancel the context the ActionFunc runs under")
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
		t.Fatal("quitting mid-action must report an abandonment")
	}
	if !strings.Contains(desc, "stop") {
		t.Fatalf("abandoned-action description = %q, want it to name the stop that was running", desc)
	}
}

// Every other key waits while an action runs: nothing may start a second
// action under the first.
func TestKeysOtherThanQuitWaitWhileAnActionRuns(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	defer close(release)
	m, cmd := confirmAndRunStop(t, blockingAction(started, release))
	go func() { cmd() }()
	<-started
	for _, k := range []string{"a", "x", "enter", "s", "m", "n"} {
		next, c := send(t, m, key(k))
		if c != nil || next.actions || next.confirm != nil || next.actionInputMode {
			t.Fatalf("%q acted while an action was running: cmd=%v sheet=%v confirm=%v input=%v", k, c != nil, next.actions, next.confirm != nil, next.actionInputMode)
		}
		m = next
	}
}

// runAction must not hand the ActionFunc context.Background() itself - a
// context nothing can cancel.
func TestActionRunsUnderAContextThatWithContextControls(t *testing.T) {
	type ctxKey struct{}
	base := context.WithValue(context.Background(), ctxKey{}, "program-owned")
	seen := make(chan context.Context, 1)
	m := New(func(context.Context) (query.Snapshot, error) { return sampleTree(), nil },
		func(ctx context.Context, _ ActionRequest) (string, error) {
			seen <- ctx
			return "", nil
		}).WithContext(base)
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 36})
	m, _ = send(t, m, m.Init()())
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	m, _ = send(t, m, key("x"))
	m, cmd := send(t, m, key("x"))
	if cmd == nil {
		t.Fatal("confirmed stop did not queue the runner")
	}
	_, _ = send(t, m, cmd())
	select {
	case got := <-seen:
		if got.Value(ctxKey{}) != "program-owned" {
			t.Fatal("ActionFunc ran under a context not derived from WithContext's base")
		}
	default:
		t.Fatal("ActionFunc was never called")
	}
}

// After a quit mid-action cmd/mate waits on AbandonedActionDone, so the
// action's own compensation runs instead of dying with the process.
func TestAbandonedActionDoneClosesWhenTheActionReturns(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	m, cmd := confirmAndRunStop(t, blockingAction(started, release))
	if m.AbandonedActionDone() != nil {
		t.Fatal("AbandonedActionDone is non-nil before anything was abandoned")
	}
	go func() { cmd() }()
	<-started

	m, _ = send(t, m, key("q"))
	done := m.AbandonedActionDone()
	if done == nil {
		t.Fatal("quitting mid-action returned no channel to wait on")
	}
	select {
	case <-done:
		t.Fatal("done closed while the action was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("done never closed after the action returned")
	}
}

// A long action reads as a hang without its elapsed time: while it runs
// the refresh tick rewrites the running line, the elapsed time first so a
// 40-column status line cuts the object's name and not the time; once done
// a tick leaves it.
func TestRunningActionShowsElapsedTime(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	m, cmd := confirmAndRunStop(t, blockingAction(started, release))
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started
	if !strings.HasSuffix(m.msg.text, m.g.Ellipsis) {
		t.Fatalf("initial running line = %q, want it unchanged until the first tick", m.msg.text)
	}
	m, _ = send(t, m, treeTickMsg{gen: m.treeGen, at: m.actionStartedAt.Add(12*time.Second + 400*time.Millisecond)})
	if !strings.HasPrefix(m.msg.text, "12s "+m.g.Dot+" Running stop") {
		t.Fatalf("running line after 12.4s = %q, want it to lead with the elapsed 12s", m.msg.text)
	}
	if status := strings.Split(renderFrame(t, m), "\n")[m.h-2]; !strings.HasPrefix(status, "12s") {
		t.Fatalf("the status line at %d columns lost the elapsed time: %q", m.w, status)
	}
	close(release)
	m, _ = send(t, m, <-result)
	after := m.msg
	m, _ = send(t, m, treeTickMsg{gen: m.treeGen, at: m.actionStartedAt.Add(time.Minute)})
	if m.msg != after {
		t.Fatalf("a tick after the action finished rewrote the line to %q", m.msg.text)
	}
}
