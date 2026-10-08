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
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/harness/harnesstest"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/screen"
	screenfixture "github.com/nguyenngocanh94/mate/internal/screen/fixture"
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
	res, err := f.stowSender(claude.KindClaude, sleeper).Stow(context.Background(), project, outbox.StowOptions{})
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
//
// Codex's screens are read through ReadVisible here, the source Codex does
// not use, so a read that spells recent-unwrapped instead of asking the
// profile fails the check at the end.
func TestStowEndsOnACodexComposerBackToEmpty(t *testing.T) {
	f := newFixture(t)
	f.harnesses = harnesstest.ReadingVisible(f.harnesses)
	const codexEmpty = "› Ask Codex to do anything\n\n  gpt-5.6 · /m\n"
	const codexBusy = "• Working (3s • esc to interrupt)\n\n› Ask Codex to do anything\n\n  gpt-5.6 · /m\n"
	f.rt.SetReadOutput(f.handle, codexEmpty)
	f.rt.OnSendText = func(h runtime.AgentHandle, _ string) { f.rt.SetReadOutput(h, codexBusy) }
	sleeper := &tickSleeper{clock: f.clock, onSleep: func(n int) {
		if n == 4 {
			f.rt.SetReadOutput(f.handle, codexEmpty)
		}
	}}
	res, err := f.stowSender(codex.KindCodex, sleeper).Stow(context.Background(), project, outbox.StowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stowed {
		t.Fatalf("result = %+v", res)
	}
	if sleeper.n < 5 {
		t.Fatalf("the stow ended after %d sleeps, before the composer had been empty twice", sleeper.n)
	}
	if len(f.rt.ReadSources) == 0 {
		t.Fatal("the stow never read the pane")
	}
	for i, src := range f.rt.ReadSources {
		if src != harness.ReadVisible {
			t.Fatalf("read %d went through %q, want the profile's %q", i, src, harness.ReadVisible)
		}
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
	res, err := f.stowSender(codex.KindCodex, sleeper).Stow(context.Background(), project, outbox.StowOptions{})
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

// codexWithoutTurnEnd is Codex declaring no turn-end evidence.
type codexWithoutTurnEnd struct{ codex.Codex }

func (c codexWithoutTurnEnd) Capabilities() harness.Capabilities {
	caps := c.Codex.Capabilities()
	caps.TurnEnd = harness.Cap[harness.TurnEndEvidence]{Status: harness.CapUnknown, Reason: "a test harness that never measured it"}
	return caps
}

// TestStowFallsBackToTheComposerWithoutTurnEndEvidence: the stow asks the
// harness for its evidence, not the kind. The same Codex Mate with its
// rollout named, registered without turn-end evidence, is judged by the
// composer alone (plan section 3.7): busy, then empty twice.
func TestStowFallsBackToTheComposerWithoutTurnEndEvidence(t *testing.T) {
	f := newFixture(t)
	const codexEmpty = "› Ask Codex to do anything\n\n  gpt-5.6 · /m\n"
	const codexBusy = "• Working (3s • esc to interrupt)\n\n› Ask Codex to do anything\n\n  gpt-5.6 · /m\n"
	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(rollout, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.ws.WriteMateMeta(project, map[string]string{"harness": "codex", "transcript": rollout}); err != nil {
		t.Fatal(err)
	}
	f.rt.SetReadOutput(f.handle, codexEmpty)
	f.rt.OnSendText = func(h runtime.AgentHandle, _ string) { f.rt.SetReadOutput(h, codexBusy) }
	sleeper := &tickSleeper{clock: f.clock, onSleep: func(n int) {
		if n == 4 {
			f.rt.SetReadOutput(f.handle, codexEmpty)
		}
	}}
	reg, err := harness.NewRegistry(nil, codexWithoutTurnEnd{})
	if err != nil {
		t.Fatal(err)
	}
	d := f.deps()
	d.Harnesses, d.Sleeper, d.Handle = reg, sleeper, func(context.Context, string) (runtime.AgentHandle, harness.Kind, error) {
		return f.handle, codex.KindCodex, nil
	}
	ws, err := store.Open(f.ws.Root())
	if err != nil {
		t.Fatal(err)
	}
	res, err := outbox.New(ws, d).Stow(context.Background(), project, outbox.StowOptions{Ceiling: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stowed {
		t.Fatalf("result = %+v: with no turn-end evidence the composer decides", res)
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
		if got := codex.CodexTurnCompletedAfter([]byte(c.rollout), at); got != c.want {
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
	res, err := f.stowSender(claude.KindClaude, &tickSleeper{clock: f.clock}).Stow(context.Background(), project, outbox.StowOptions{Ceiling: time.Minute})
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
			res, err := f.stowSender(claude.KindClaude, &tickSleeper{clock: f.clock}).Stow(context.Background(), project, tc.opts)
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
	res, err := f.stowSender(claude.KindClaude, sleeper).Stow(context.Background(), project, outbox.StowOptions{RequireCompletion: true, Ceiling: 20 * time.Second})
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
	res, err := f.stowSender(claude.KindClaude, sleeper).Stow(context.Background(), project, outbox.StowOptions{RequireCompletion: true, Ceiling: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stowed {
		t.Fatal("a crew message is not a Stop hook")
	}
}

// The stow's own read of the Mate's composer goes through Deps.Observer: an
// observer that says busy refuses a stow that needs an empty composer, on a
// pane that shows one.
func TestStowReadsThePaneThroughTheObserver(t *testing.T) {
	f := newFixture(t)
	observer := &seesBusy{}
	d := f.deps()
	d.Observer = observer
	s := outbox.New(f.ws, d)
	res, err := s.Stow(context.Background(), project, outbox.StowOptions{RequireEmpty: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome() != "not stowed: the Mate is mid-turn" || observer.calls != 1 {
		t.Fatalf("result %q after %d observations, want the observer's busy", res.Outcome(), observer.calls)
	}
	if len(f.outboxItems()) != 0 || len(f.rt.SentText) != 0 {
		t.Fatal("a refused stow queued or typed something")
	}
}

// countingObserver reads every pane as the fixture observer does, and
// counts the screens it was asked about.
type countingObserver struct{ screens []string }

func (o *countingObserver) Observe(ctx context.Context, profile harness.ScreenProfile, pane string) (screen.Observation, error) {
	o.screens = append(o.screens, pane)
	return screenfixture.New().Observe(ctx, profile, pane)
}

// The stow's wait asks the observer only about a screen it has not read:
// a Mate busy on one unchanged screen for twenty polls costs one
// observation, and its composer back to empty one more, however many looks
// the wait takes.
func TestStowObservesOnlyWhenTheScreenChanges(t *testing.T) {
	f := newFixture(t)
	const codexEmpty = "› Ask Codex to do anything\n\n  gpt-5.6 · /m\n"
	const codexBusy = "• Working (3s • esc to interrupt)\n\n› Ask Codex to do anything\n\n  gpt-5.6 · /m\n"
	f.rt.SetReadOutput(f.handle, codexEmpty)
	f.rt.OnSendText = func(h runtime.AgentHandle, _ string) { f.rt.SetReadOutput(h, codexBusy) }
	sleeper := &tickSleeper{clock: f.clock, onSleep: func(n int) {
		if n == 20 {
			f.rt.SetReadOutput(f.handle, codexEmpty)
		}
	}}
	observer := &countingObserver{}
	d := f.deps()
	d.Sleeper = sleeper
	d.Observer = observer
	d.Handle = func(context.Context, string) (runtime.AgentHandle, harness.Kind, error) {
		return f.handle, codex.KindCodex, nil
	}
	res, err := outbox.New(f.ws, d).Stow(context.Background(), project, outbox.StowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stowed || sleeper.n < 21 {
		t.Fatalf("result %+v after %d sleeps, want stowed once the composer was empty again", res, sleeper.n)
	}
	// The stow's first look (empty), the send's own look before typing
	// (empty), then the busy screen once and the empty screen once.
	want := []string{codexEmpty, codexEmpty, codexBusy, codexEmpty}
	if fmt.Sprint(observer.screens) != fmt.Sprint(want) {
		t.Fatalf("observed %d screens %q, want %d: %q", len(observer.screens), observer.screens, len(want), want)
	}
}
