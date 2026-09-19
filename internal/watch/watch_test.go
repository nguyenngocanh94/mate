package watch_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/watch"
)

// Every test here is written from docs/mvp.md section 4b: `stale` is a
// status file and a pane that have both stopped moving while the composer is
// not busy and the crew is not waiting on a human; `runtime_lost` is Herdr
// answering that the agent is gone; an incident is open when the last line
// of its (crew, kind) pair says `open`, and it is closed by a new line, never
// by an edit.

// ---------- stale: opening ----------

func TestWatchOpensStaleWhenNeitherPaneNorStatusMovesForTheThreshold(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")

	f.poll()
	f.assertIncidents()

	f.clock.advance(4 * time.Minute)
	f.poll()
	f.assertIncidents("k3 stale open")

	// The text names the evidence, so a reader is told what was measured
	// rather than a verdict.
	line := f.incidents()[0]
	if !strings.Contains(line.Text, "4m0s") || !strings.Contains(line.Text, "composer empty") {
		t.Fatalf("incident text = %q, want the quiet time and the composer state", line.Text)
	}
}

func TestWatchDoesNotOpenStaleBeforeTheThreshold(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")

	f.poll()
	f.clock.advance(2*time.Minute + 59*time.Second)
	f.poll()
	f.assertIncidents()
}

func TestWatchDoesNotOpenStaleWhileTheComposerIsBusy(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: running the suite")
	f.setScreen(codexBusyScreen)

	f.poll()
	f.clock.advance(10 * time.Minute)
	// The screen has not changed for ten minutes, but the harness says it
	// is mid-turn: a long build is not a stuck crew.
	f.poll()
	f.assertIncidents()

	if h, _ := f.health("k3"); h.Composer != send.StateBusy {
		t.Fatalf("health composer = %q, want busy", h.Composer)
	}
}

func TestWatchDoesNotOpenStaleForACrewThatIsWaitingOnAHuman(t *testing.T) {
	// A crew that asked, handed back, or reported it was done is silent on
	// purpose. Calling that stale would put the same crew in the inbox
	// twice, once as its own question and once as an incident.
	for _, verb := range []string{"needs-decision", "wait-mate", "done"} {
		t.Run(verb, func(t *testing.T) {
			f := newFixture(t)
			f.appendStatus("k3", verb+": which database?")

			f.poll()
			f.clock.advance(30 * time.Minute)
			f.poll()
			f.assertIncidents()
		})
	}
}

func TestWatchOpensStaleAfterAWaitingCrewGoesBackToWorking(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "needs-decision: which database?")
	f.poll()
	f.clock.advance(30 * time.Minute)
	f.poll()
	f.assertIncidents()

	// Answered, and back at work: silence means something again.
	f.appendStatus("k3", "working: on it")
	f.poll()
	f.clock.advance(4 * time.Minute)
	f.poll()
	f.assertIncidents("k3 stale open")
}

func TestWatchOpensStaleOnlyOnce(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")
	f.poll()
	f.clock.advance(4 * time.Minute)
	f.poll()
	f.clock.advance(4 * time.Minute)
	f.poll()
	f.assertIncidents("k3 stale open")
}

// ---------- stale: resolving ----------

func TestWatchResolvesStaleWhenThePaneChanges(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")
	f.poll()
	f.clock.advance(4 * time.Minute)
	f.poll()
	f.assertIncidents("k3 stale open")

	f.setScreen(codexIdleScreen + "› now typing something\n")
	f.clock.advance(5 * time.Second)
	f.poll()
	f.assertIncidents("k3 stale open", "k3 stale resolved")
}

func TestWatchResolvesStaleWhenTheCrewWritesAStatusLine(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")
	f.poll()
	f.clock.advance(4 * time.Minute)
	f.poll()
	f.assertIncidents("k3 stale open")

	f.appendStatus("k3", "working: still here")
	f.clock.advance(5 * time.Second)
	f.poll()
	f.assertIncidents("k3 stale open", "k3 stale resolved")
}

func TestWatchResolvesStaleWhenTheComposerGoesBusyAgain(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")
	f.poll()
	f.clock.advance(4 * time.Minute)
	f.poll()
	f.assertIncidents("k3 stale open")

	// The pane changed only in the way that proves the turn restarted.
	f.setScreen(codexBusyScreen)
	f.clock.advance(5 * time.Second)
	f.poll()
	f.assertIncidents("k3 stale open", "k3 stale resolved")
}

func TestWatchReopensStaleAfterARecovery(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")
	f.poll()
	f.clock.advance(4 * time.Minute)
	f.poll()
	f.appendStatus("k3", "working: still here")
	f.clock.advance(5 * time.Second)
	f.poll()
	f.clock.advance(4 * time.Minute)
	f.poll()
	f.assertIncidents("k3 stale open", "k3 stale resolved", "k3 stale open")
}

// ---------- runtime_lost ----------

func TestWatchOpensRuntimeLostWhenHerdrNoLongerHasTheAgent(t *testing.T) {
	f := newFixture(t)
	f.poll()
	f.assertIncidents()

	f.rt.DropAgent(f.handle)
	f.clock.advance(5 * time.Second)
	f.poll()
	f.assertIncidents("k3 runtime_lost open")
	if text := f.incidents()[0].Text; !strings.Contains(text, "crew-k3") {
		t.Fatalf("incident text = %q, want the agent name", text)
	}

	h, ok := f.health("k3")
	if !ok || h.AgentPresent {
		t.Fatalf("health = %+v (ok %v), want an observation saying the agent is gone", h, ok)
	}
}

func TestWatchResolvesRuntimeLostWhenTheAgentIsBack(t *testing.T) {
	f := newFixture(t)
	f.poll()
	f.rt.DropAgent(f.handle)
	f.clock.advance(5 * time.Second)
	f.poll()
	f.assertIncidents("k3 runtime_lost open")

	f.rt.PutAgent(f.handle, runtime.AgentIdle)
	f.rt.SetReadOutput(f.handle, codexIdleScreen)
	f.clock.advance(5 * time.Second)
	f.poll()
	f.assertIncidents("k3 runtime_lost open", "k3 runtime_lost resolved")
}

func TestWatchOpensRuntimeLostOnlyOnce(t *testing.T) {
	f := newFixture(t)
	f.poll()
	f.rt.DropAgent(f.handle)
	for i := 0; i < 3; i++ {
		f.clock.advance(5 * time.Second)
		f.poll()
	}
	f.assertIncidents("k3 runtime_lost open")
}

func TestWatchKeepsTheTwoKindsApart(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")
	f.poll()
	f.clock.advance(4 * time.Minute)
	f.poll()
	f.assertIncidents("k3 stale open")

	// The agent disappears while stale is open: a second finding, not a
	// replacement, and resolving one must not resolve the other.
	f.rt.DropAgent(f.handle)
	f.clock.advance(5 * time.Second)
	f.poll()
	f.assertIncidents("k3 stale open", "k3 runtime_lost open")
}

// ---------- what the observer refuses to conclude ----------

func TestWatchConcludesNothingWhenHerdrCannotAnswer(t *testing.T) {
	f := newFixture(t)
	f.poll()

	f.rt.InspectErr = errors.New("herdr: connection refused")
	f.clock.advance(4 * time.Minute)
	f.pollIgnoringErrors()
	f.assertIncidents()
}

func TestWatchConcludesNothingWhenThePaneCannotBeRead(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")
	f.poll()

	f.rt.ReadErr = errors.New("herdr: pane read failed")
	f.clock.advance(4 * time.Minute)
	f.pollIgnoringErrors()
	f.assertIncidents()
}

func TestWatchConcludesNothingForACrewWithNoAgentRecorded(t *testing.T) {
	f := newFixture(t)
	// A crew whose spawn never got as far as an agent: mvp.md says that is
	// `state=failed` written by spawn, not an incident opened by the
	// observer.
	f.writeMeta("k3", map[string]string{"harness": "codex", "state": "spawned"})
	f.poll()
	f.clock.advance(10 * time.Minute)
	f.poll()
	f.assertIncidents()
	if _, ok := f.health("k3"); ok {
		t.Fatal("health was recorded for a crew the observer could not look at")
	}
}

func TestWatchNeverWritesToTheStatusFile(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")
	before, err := os.ReadFile(f.ws.CrewStatus("shop", "k3"))
	if err != nil {
		t.Fatal(err)
	}

	f.poll()
	f.clock.advance(4 * time.Minute)
	f.poll()
	f.rt.DropAgent(f.handle)
	f.clock.advance(5 * time.Second)
	f.poll()

	after, err := os.ReadFile(f.ws.CrewStatus("shop", "k3"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the observer wrote to the status file:\nbefore %q\nafter  %q", before, after)
	}
	if len(f.incidents()) == 0 {
		t.Fatal("the round found nothing at all; this test would pass vacuously")
	}
}

// ---------- which crews are watched ----------

func TestWatchIgnoresClosedCrews(t *testing.T) {
	cases := map[string]map[string]string{
		"stopped_at": {"harness": "codex", "agent": "crew-k3", "pane": "pane-k3", "stopped_at": "2026-09-18T11:00:00Z"},
		"finished":   {"harness": "codex", "agent": "crew-k3", "pane": "pane-k3", "state": "finished"},
		"failed":     {"harness": "codex", "agent": "crew-k3", "pane": "pane-k3", "state": "failed"},
	}
	for name, meta := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.writeMeta("k3", meta)
			f.rt.DropAgent(f.handle)

			f.poll()
			f.clock.advance(10 * time.Minute)
			f.poll()
			f.assertIncidents()
			if _, ok := f.health("k3"); ok {
				t.Fatal("a closed crew still has health")
			}
		})
	}
}

func TestWatchForgetsACrewOnceItIsClosed(t *testing.T) {
	f := newFixture(t)
	f.poll()
	if _, ok := f.health("k3"); !ok {
		t.Fatal("the open crew has no health after a poll")
	}

	f.writeMeta("k3", map[string]string{"harness": "codex", "agent": "crew-k3", "pane": "pane-k3", "state": "finished"})
	f.clock.advance(5 * time.Second)
	f.poll()
	if _, ok := f.health("k3"); ok {
		t.Fatal("a closed crew keeps its health; the console would keep drawing it")
	}
}

// ---------- the file is the state ----------

func TestWatchReadsOpenIncidentsBackFromTheLog(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")
	f.poll()
	f.clock.advance(4 * time.Minute)
	f.poll()
	f.assertIncidents("k3 stale open")

	// A second console over the same workspace: it has never seen this
	// crew, and must not re-open an incident the log already holds.
	second := watch.New(f.ws, f.deps())
	if err := second.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	f.assertIncidents("k3 stale open")

	// And it resolves the incident it inherited.
	f.appendStatus("k3", "working: still here")
	f.clock.advance(5 * time.Second)
	if err := second.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	f.assertIncidents("k3 stale open", "k3 stale resolved")
}

// ---------- health ----------

func TestWatchHealthReportsThePaneAndTheQuietTime(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")
	f.poll()

	h, ok := f.health("k3")
	if !ok {
		t.Fatal("no health after the first poll")
	}
	if !h.AgentPresent || h.Composer != send.StateEmpty || h.QuietFor != 0 {
		t.Fatalf("health = %+v, want a present agent, an empty composer and no quiet time yet", h)
	}
	if !h.ObservedAt.Equal(f.clock.Now()) {
		t.Fatalf("ObservedAt = %v, want the clock's own %v", h.ObservedAt, f.clock.Now())
	}

	f.clock.advance(90 * time.Second)
	f.poll()
	h, _ = f.health("k3")
	if h.QuietFor != 90*time.Second {
		t.Fatalf("QuietFor = %s, want 90s", h.QuietFor)
	}

	// A pane that moved resets the quiet time, whatever the state column
	// says.
	f.setScreen(codexIdleScreen + "› typing\n")
	f.clock.advance(10 * time.Second)
	f.poll()
	h, _ = f.health("k3")
	if h.QuietFor != 0 {
		t.Fatalf("QuietFor after a pane change = %s, want 0", h.QuietFor)
	}
}

func TestWatchSnapshotIsACopy(t *testing.T) {
	f := newFixture(t)
	f.poll()
	snap := f.w.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot = %+v, want one crew", snap)
	}
	delete(snap, watch.CrewRef{Project: "shop", Crew: "k3"})
	if _, ok := f.health("k3"); !ok {
		t.Fatal("deleting from a snapshot changed the watcher's own map")
	}
}

func TestWatchWatchesEveryProject(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.ws.Root()+"/blog", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.ws.AddProject("blog", store.ProjectConfig{Repo: "blog"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if err := f.ws.WriteCrewMeta("blog", "k9", map[string]string{
		"harness": "codex", "agent": "crew-k9", "pane": "pane-k9", "state": "spawned",
	}); err != nil {
		t.Fatal(err)
	}
	blog := f.handle
	blog.Name = "crew-k9"
	f.rt.PutAgent(blog, runtime.AgentIdle)
	f.rt.SetReadOutput(blog, codexIdleScreen)

	f.poll()
	if _, ok := f.w.Health("blog", "k9"); !ok {
		t.Fatal("a project registered after New is not watched")
	}
}

// ---------- the polling goroutine ----------

// stepSleeper hands the run loop back one round at a time: Sleep releases a
// waiting test and then blocks until the test asks for the next round, so
// the test never sleeps for real and never races the loop.
type stepSleeper struct {
	rounds chan struct{}
	resume chan struct{}
}

func newStepSleeper() *stepSleeper {
	return &stepSleeper{rounds: make(chan struct{}, 8), resume: make(chan struct{})}
}

func (s *stepSleeper) Sleep(ctx context.Context, _ time.Duration) error {
	select {
	case s.rounds <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-s.resume:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestWatchStartPollsUntilStopped(t *testing.T) {
	f := newFixture(t)
	f.appendStatus("k3", "working: reading the tests")
	sleeper := newStepSleeper()
	deps := f.deps()
	deps.Sleeper = sleeper
	w := watch.New(f.ws, deps)

	w.Start(context.Background())
	waitRound(t, sleeper)
	if _, ok := w.Health("shop", "k3"); !ok {
		t.Fatal("the first round recorded no health")
	}

	f.clock.advance(4 * time.Minute)
	sleeper.resume <- struct{}{}
	waitRound(t, sleeper)
	f.assertIncidents("k3 stale open")

	w.Stop()
	// Stop waits for the round in flight, so a poll after it is impossible:
	// anything appended now must still be there unchanged.
	before := len(f.incidents())
	f.clock.advance(10 * time.Minute)
	time.Sleep(20 * time.Millisecond)
	if got := len(f.incidents()); got != before {
		t.Fatalf("incidents kept arriving after Stop: %d, want %d", got, before)
	}
}

func waitRound(t *testing.T, s *stepSleeper) {
	t.Helper()
	select {
	case <-s.rounds:
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher did not finish a round")
	}
}

// TestWatchRuntimeIsNarrow pins the seam: the observer may look, and it may
// not act. The check is on the interface itself, not on whatever adapter is
// passed in - the real one can do everything - because the interface is what
// stops a later change from making the observer a second writer of the world
// it describes.
func TestWatchRuntimeIsNarrow(t *testing.T) {
	rt := reflect.TypeOf((*watch.Runtime)(nil)).Elem()
	want := map[string]bool{"InspectAgent": true, "ReadAgentStyled": true}
	if rt.NumMethod() != len(want) {
		t.Fatalf("watch.Runtime has %d methods, want %d", rt.NumMethod(), len(want))
	}
	for i := 0; i < rt.NumMethod(); i++ {
		if !want[rt.Method(i).Name] {
			t.Fatalf("watch.Runtime exposes %s; the observer may only look", rt.Method(i).Name)
		}
	}
}

// TestPollCountsHowLongTheComposerHasBeenBusy: a working harness redraws
// its spinner every poll, so QuietFor resets each round and read "busy 0s"
// in the console (2026-09-19). ComposerFor counts from the poll the
// composer last changed state, and resets when it changes back.
func TestPollCountsHowLongTheComposerHasBeenBusy(t *testing.T) {
	f := newFixture(t)
	f.poll()
	f.setScreen(codexBusyScreen)
	f.clock.advance(5 * time.Second)
	f.poll()
	f.setScreen("• Working (7s • esc to interrupt)\n› Ask Codex to do anything\n\n  model · cwd\n")
	f.clock.advance(5 * time.Second)
	f.poll()
	h, ok := f.health("k3")
	if !ok || h.Composer != send.StateBusy {
		t.Fatalf("health = %+v (%v), want a busy composer", h, ok)
	}
	if h.QuietFor != 0 {
		t.Fatalf("QuietFor = %s, want 0: the spinner changed the pane", h.QuietFor)
	}
	if h.ComposerFor != 5*time.Second {
		t.Fatalf("ComposerFor = %s, want 5s (busy since the second poll)", h.ComposerFor)
	}
	f.setScreen(codexIdleScreen)
	f.clock.advance(5 * time.Second)
	f.poll()
	if h, _ := f.health("k3"); h.Composer != send.StateEmpty || h.ComposerFor != 0 {
		t.Fatalf("after going idle: %+v, want an empty composer with ComposerFor reset", h)
	}
}
