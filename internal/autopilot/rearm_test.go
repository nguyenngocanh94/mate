package autopilot_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The mode follows the captain (docs/mvp.md task 57). While they are talking
// to the Mate it is manual and a Crew's news waits in their box; once the
// Mate has answered and they have left it alone for five minutes, the daemon
// turns auto back on and the Mate is woken with the digest.

// asked is the captain typing to the Mate, answered its turn ending - what
// the Mate's UserPromptSubmit and Stop hooks append.
func (f *fixture) asked(at time.Time) {
	f.t.Helper()
	f.appendSent(store.SentEntry{Time: at, Source: store.SourceUser, Target: store.TargetMate, Text: "how is it going"})
}

func (f *fixture) answered(at time.Time) {
	f.t.Helper()
	f.appendSent(store.SentEntry{Time: at, Source: store.SourceMate, Target: store.SourceUser, Text: "both crews are working"})
}

func (f *fixture) appendSent(e store.SentEntry) {
	f.t.Helper()
	if err := f.ws.AppendSent(project, e); err != nil {
		f.t.Fatal(err)
	}
}

func TestAQuietCaptainTurnsAutoBackOnAndTheMateIsWoken(t *testing.T) {
	f := newFixture(t)
	f.status("fix-cart-total", "needs-decision: pick A or B")
	f.asked(start.Add(-time.Minute))
	f.answered(start)

	f.clock.Advance(autopilot.DefaultQuietAfter - time.Second)
	f.mustTick()
	if f.ws.Auto(project) || len(f.typed()) != 0 {
		t.Fatalf("auto = %v, typed %#v: the captain has been quiet under five minutes", f.ws.Auto(project), f.typed())
	}

	f.clock.Advance(time.Second)
	f.mustTick()
	if !f.ws.Auto(project) {
		t.Fatal("auto mode is still off five minutes after the Mate answered")
	}
	typed := f.typed()
	if len(typed) != 1 || !strings.Contains(typed[0], "fix-cart-total needs-decision") {
		t.Fatalf("typed %#v, want the digest the crew's question is in", typed)
	}
	if !hasSent(f.sent(), autopilot.RearmText(autopilot.DefaultQuietAfter)) {
		t.Fatalf("sent.log = %+v, want the rearm recorded", f.sent())
	}
}

// The Mate still working on the captain's question is not the captain being
// quiet: nothing may be typed into a turn the captain is waiting on.
func TestAMateStillOnTheCaptainsQuestionIsNotQuiet(t *testing.T) {
	f := newFixture(t)
	f.status("fix-cart-total", "needs-decision: pick A or B")
	f.answered(start.Add(-time.Hour))
	f.asked(start)

	f.clock.Advance(time.Hour)
	f.mustTick()
	if f.ws.Auto(project) || len(f.typed()) != 0 {
		t.Fatalf("auto = %v, typed %#v while the Mate has not answered the captain", f.ws.Auto(project), f.typed())
	}
}

// Typing again restarts the five minutes, and is read even when it lands
// after the daemon has already read the log once.
func TestTheCaptainTypingAgainRestartsTheQuiet(t *testing.T) {
	f := newFixture(t)
	f.asked(start.Add(-time.Minute))
	f.answered(start)
	f.clock.Advance(4 * time.Minute)
	f.mustTick()

	f.asked(f.clock.Now())
	f.answered(f.clock.Now().Add(30 * time.Second))
	f.clock.Advance(2 * time.Minute)
	f.mustTick()
	if f.ws.Auto(project) {
		t.Fatal("auto came back on two minutes after the captain's last exchange")
	}
	f.clock.Advance(4 * time.Minute)
	f.mustTick()
	if !f.ws.Auto(project) {
		t.Fatal("auto is still off five minutes after the last exchange")
	}
}

// The captain choosing manual with the console's `m` key is a choice, not a
// pause: no quiet spell overrides it, and choosing auto releases it.
func TestManualChosenWithTheKeyIsHeld(t *testing.T) {
	f := newFixture(t)
	f.status("fix-cart-total", "needs-decision: pick A or B")
	f.answered(start)
	if err := f.ws.SetMode(project, false); err != nil {
		t.Fatal(err)
	}

	f.clock.Advance(time.Hour)
	f.mustTick()
	if f.ws.Auto(project) || len(f.typed()) != 0 {
		t.Fatalf("auto = %v, typed %#v under the captain's manual hold", f.ws.Auto(project), f.typed())
	}

	if err := f.ws.SetMode(project, true); err != nil {
		t.Fatal(err)
	}
	if f.ws.Held(project) || !f.ws.Auto(project) {
		t.Fatalf("held = %v, auto = %v after choosing auto", f.ws.Held(project), f.ws.Auto(project))
	}
}

// A Mate that has never finished a turn - a Codex Mate records none - is
// never switched to auto behind the captain's back.
func TestNoRecordedTurnNeverRearms(t *testing.T) {
	f := newFixture(t)
	f.status("fix-cart-total", "needs-decision: pick A or B")
	f.clock.Advance(time.Hour)
	f.mustTick()
	if f.ws.Auto(project) {
		t.Fatal("auto came on for a Mate with no recorded turn")
	}
}

func hasSent(entries []store.SentEntry, text string) bool {
	for _, e := range entries {
		if e.Text == text {
			return true
		}
	}
	return false
}
