package outbox_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The stow before a restart (docs/mvp.md task 37, B7), against fake panes:
// the line goes through the outbox, the wait ends on the Mate's own
// turn-end signal, and a line that never got in is withdrawn so the next
// Mate never sees it.

// tickSleeper advances the fake clock by each sleep and runs onSleep with
// the number of sleeps so far, the way the pane would move on while the
// stow waits.
type tickSleeper struct {
	clock   *fakeClock
	n       int
	onSleep func(n int)
}

func (s *tickSleeper) Sleep(ctx context.Context, d time.Duration) error {
	s.clock.Advance(d)
	s.n++
	if s.onSleep != nil {
		s.onSleep(s.n)
	}
	return ctx.Err()
}

func (f *fixture) stowSender(kind harness.Kind, sleeper *tickSleeper) *outbox.Sender {
	f.t.Helper()
	d := f.deps()
	d.Sleeper = sleeper
	d.Handle = func(context.Context, string) (runtime.AgentHandle, harness.Kind, error) {
		if f.handleErr != nil {
			return runtime.AgentHandle{}, "", f.handleErr
		}
		return f.handle, kind, nil
	}
	ws, err := store.Open(f.ws.Root())
	if err != nil {
		f.t.Fatal(err)
	}
	return outbox.New(ws, d)
}

func (f *fixture) outboxItems() []store.OutboxItem {
	f.t.Helper()
	items, err := f.ws.ReadOutbox(project)
	if err != nil {
		f.t.Fatal(err)
	}
	return items
}

// TestStowEndsOnTheStopHookAnswer: a Claude Mate takes the stow line, is
// busy, and its Stop hook writes its answer to sent.log; the stow is done.
func TestStowEndsOnTheStopHookAnswer(t *testing.T) {
	f := newFixture(t)
	f.rt.OnSendText = func(h runtime.AgentHandle, _ string) { f.rt.SetReadOutput(h, claudeBusyScreen()) }
	sleeper := &tickSleeper{clock: f.clock, onSleep: func(n int) {
		if n == 3 {
			if err := f.ws.AppendSent(project, store.SentEntry{Source: store.SourceMate, Target: store.SourceUser, Text: "stowed: 2 lessons"}); err != nil {
				t.Fatal(err)
			}
			f.rt.SetReadOutput(f.handle, claudeScreen(""))
		}
	}}
	res, err := f.stowSender(harness.KindClaude, sleeper).Stow(context.Background(), project, outbox.StowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stowed || res.Outcome() != "stowed" {
		t.Fatalf("result = %+v", res)
	}
	if len(f.rt.SentText) != 1 || f.rt.SentText[0].Text != send.Marker+memory.StowLine {
		t.Fatalf("typed %+v, want the marked stow line once", f.rt.SentText)
	}
	items := f.outboxItems()
	if len(items) != 1 || items[0].Source != store.OutboxSourceStow || items[0].State != store.OutboxSent {
		t.Fatalf("outbox = %+v", items)
	}
}

// TestStowEndsOnACodexComposerBackToEmpty: Codex has no Stop hook; busy
// after the line, then empty twice, is the end of its turn.
func TestStowEndsOnACodexComposerBackToEmpty(t *testing.T) {
	f := newFixture(t)
	const codexEmpty = "› Ask Codex to do anything\n\n  gpt-5.6 · /m\n"
	const codexBusy = "• Working (3s • esc to interrupt)\n\n› Ask Codex to do anything\n\n  gpt-5.6 · /m\n"
	f.rt.SetReadOutput(f.handle, codexEmpty)
	f.rt.OnSendText = func(h runtime.AgentHandle, _ string) { f.rt.SetReadOutput(h, codexBusy) }
	sleeper := &tickSleeper{clock: f.clock, onSleep: func(n int) {
		if n == 4 {
			f.rt.SetReadOutput(f.handle, codexEmpty)
		}
	}}
	res, err := f.stowSender(harness.KindCodex, sleeper).Stow(context.Background(), project, outbox.StowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stowed {
		t.Fatalf("result = %+v", res)
	}
	if sleeper.n < 5 {
		t.Fatalf("the stow ended after %d sleeps, before the composer had been empty twice", sleeper.n)
	}
}

// TestStowWaitsForTheCodexRolloutToFinishTheTurn: a Codex composer reads
// empty between tool calls, so once mate.meta names the Mate's rollout the
// stow ends only on a task_complete stamped after the stow line - measured
// 2026-09-24 (task 38): an empty-composer stow was cut off mid-turn by the
// restart it said was safe.
func TestStowWaitsForTheCodexRolloutToFinishTheTurn(t *testing.T) {
	f := newFixture(t)
	const codexEmpty = "› Ask Codex to do anything\n\n  gpt-5.6 · /m\n"
	const codexBusy = "• Working (3s • esc to interrupt)\n\n› Ask Codex to do anything\n\n  gpt-5.6 · /m\n"
	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	old := fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t0","last_agent_message":"earlier"}}`+"\n",
		f.clock.Now().Add(-time.Minute).Format(time.RFC3339Nano))
	if err := os.WriteFile(rollout, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.ws.WriteMateMeta(project, map[string]string{"harness": "codex", "transcript": rollout}); err != nil {
		t.Fatal(err)
	}
	f.rt.SetReadOutput(f.handle, codexEmpty)
	f.rt.OnSendText = func(h runtime.AgentHandle, _ string) { f.rt.SetReadOutput(h, codexBusy) }
	sleeper := &tickSleeper{clock: f.clock, onSleep: func(n int) {
		switch {
		case n == 2:
			// Between two tool calls: empty, and it stays empty.
			f.rt.SetReadOutput(f.handle, codexEmpty)
		case n == 12:
			line := fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":"stow receipt"}}`+"\n",
				f.clock.Now().Format(time.RFC3339Nano))
			fh, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			fh.WriteString(line)
			fh.Close()
		}
	}}
	res, err := f.stowSender(harness.KindCodex, sleeper).Stow(context.Background(), project, outbox.StowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stowed {
		t.Fatalf("result = %+v", res)
	}
	if sleeper.n < 12 {
		t.Fatalf("the stow ended after %d sleeps, on the empty composer, before the rollout recorded the turn's end", sleeper.n)
	}
}

func TestCodexTurnCompletedAfter(t *testing.T) {
	at := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	rec := func(kind string, ts time.Time) string {
		return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":%q,"turn_id":"t"}}`, ts.Format(time.RFC3339Nano), kind)
	}
	for _, c := range []struct {
		name    string
		rollout string
		want    bool
	}{
		{"finished after", rec("task_complete", at.Add(time.Second)), true},
		{"finished before", rec("task_complete", at.Add(-time.Second)), false},
		{"aborted after", rec("turn_aborted", at.Add(time.Second)), false},
		{"started only", rec("task_started", at.Add(time.Second)), false},
		{"garbage then finished", "not json\n" + rec("task_complete", at.Add(time.Millisecond)), true},
	} {
		if got := harness.CodexTurnCompletedAfter([]byte(c.rollout), at); got != c.want {
			t.Errorf("%s: CodexTurnCompletedAfter = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestStowWithdrawsALineThatNeverGotIn: a Mate busy for the whole ceiling
// never takes the line, the restart goes ahead, and the line is dropped
// rather than left queued for the Mate the restart starts.
func TestStowWithdrawsALineThatNeverGotIn(t *testing.T) {
	f := newFixture(t)
	f.rt.SetReadOutput(f.handle, claudeBusyScreen())
	res, err := f.stowSender(harness.KindClaude, &tickSleeper{clock: f.clock}).Stow(context.Background(), project, outbox.StowOptions{Ceiling: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stowed || res.Outcome() != "not stowed: the stow line did not reach the Mate within 1 minute" {
		t.Fatalf("outcome = %q", res.Outcome())
	}
	items := f.outboxItems()
	if len(items) != 1 || items[0].State != store.OutboxDropped {
		t.Fatalf("outbox = %+v, want the stow line dropped", items)
	}
	if len(f.rt.SentText) != 0 {
		t.Fatalf("typed %+v into a busy Mate", f.rt.SentText)
	}
	// A later sender finds nothing to type.
	if a, err := f.sender().Attempt(context.Background(), project); err != nil || a.Tried() {
		t.Fatalf("a later attempt = %+v, %v", a, err)
	}
}

// TestStowNeverTypesOverUnsentText, and a stop that wants the Mate gone
// now does not wait on a busy one.
func TestStowRefusalsQueueNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		screen string
		opts   outbox.StowOptions
		held   bool
		want   string
	}{
		{"pending", claudeScreen("half a thought"), outbox.StowOptions{}, true, "not stowed: the composer holds unsent text"},
		{"busy, empty required", claudeBusyScreen(), outbox.StowOptions{RequireEmpty: true}, false, "not stowed: the Mate is mid-turn"},
		{"no composer", "a dialog nobody measured\n", outbox.StowOptions{}, false, "not stowed: its pane shows no composer to type into"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.rt.SetReadOutput(f.handle, tc.screen)
			res, err := f.stowSender(harness.KindClaude, &tickSleeper{clock: f.clock}).Stow(context.Background(), project, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if res.Held != tc.held || res.Outcome() != tc.want {
				t.Fatalf("result = %+v (%q)", res, res.Outcome())
			}
			if tc.held && !strings.Contains(res.Pending, "half a thought") {
				t.Fatalf("pending = %q", res.Pending)
			}
			if len(f.outboxItems()) != 0 || len(f.rt.SentText) != 0 {
				t.Fatal("a refused stow queued or typed something")
			}
		})
	}
}

func TestStrictStowRejectsEmptyComposerWithoutTurnEnd(t *testing.T) {
	f := newFixture(t)
	sleeper := &tickSleeper{clock: f.clock}
	res, err := f.stowSender(harness.KindClaude, sleeper).Stow(context.Background(), project, outbox.StowOptions{RequireCompletion: true, Ceiling: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stowed {
		t.Fatal("empty composer alone must not authorize a context refresh")
	}
}

func TestStrictStowIgnoresMateMessagesToCrews(t *testing.T) {
	f := newFixture(t)
	sleeper := &tickSleeper{clock: f.clock, onSleep: func(n int) {
		if n == 3 {
			_ = f.ws.AppendSent(project, store.SentEntry{Source: store.SourceMate, Target: "crew-x", Text: "continue"})
		}
	}}
	res, err := f.stowSender(harness.KindClaude, sleeper).Stow(context.Background(), project, outbox.StowOptions{RequireCompletion: true, Ceiling: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stowed {
		t.Fatal("a crew message is not a Stop hook")
	}
}
