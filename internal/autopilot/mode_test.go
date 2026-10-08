package autopilot_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/hook"
)

// The mode is only what the captain chose (docs/mvp.md M18). A prompt to the
// Mate does not turn auto off, so a crew that reports while the captain is
// talking is still told to the Mate.

// captainPrompts runs the Mate's UserPromptSubmit hook on a plain prompt, the
// way the harness does when the captain types to the Mate.
func (f *fixture) captainPrompts(text string) {
	f.t.Helper()
	if err := hook.HandlePrompt(f.ws, project, []byte(`{"prompt":"`+text+`"}`)); err != nil {
		f.t.Fatal(err)
	}
}

// TestACaptainPromptThenWaitMateStillWakesTheMate replays 2026-10-07 09:50:
// the captain messaged the Mate, the crew said wait-mate 24 seconds later,
// and for more than five minutes nobody was told. In auto mode the digest
// reaches the Mate.
func TestACaptainPromptThenWaitMateStillWakesTheMate(t *testing.T) {
	f := newFixture(t)
	f.auto(true)

	f.captainPrompts("how is it going")
	if !f.ws.Auto(project) {
		t.Fatal("a captain prompt turned auto mode off")
	}
	f.clock.Advance(24 * time.Second)
	f.status("fix-cart-total", "wait-mate: branch ready")
	f.clock.Advance(time.Minute)
	f.mustTick()

	typed := f.typed()
	if len(typed) != 1 || !strings.Contains(typed[0], "fix-cart-total wait-mate") {
		t.Fatalf("typed %#v, want one digest carrying the crew's wait-mate", typed)
	}
}

// TestManualWaitMateReachesTheCaptainsInbox is the other half: with manual
// held by the captain nothing is typed, and the inbox carries the crew's
// wait-mate, so one side is always told.
func TestManualWaitMateReachesTheCaptainsInbox(t *testing.T) {
	f := newFixture(t)
	if err := f.ws.SetMode(project, false); err != nil {
		t.Fatal(err)
	}

	f.captainPrompts("how is it going")
	f.clock.Advance(24 * time.Second)
	f.status("fix-cart-total", "wait-mate: branch ready")
	f.clock.Advance(5 * time.Minute)
	f.mustTick()

	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v into a manual project", typed)
	}
	items := box.Inbox(f.view())
	if len(items) != 1 || items[0].Crew() != "fix-cart-total" || items[0].State != box.StateWaitMate {
		t.Fatalf("inbox = %+v, want the crew's wait-mate", items)
	}
}

// The captain choosing manual with the console's `m` key is a choice no
// elapsed time overrides, and choosing auto releases it.
func TestManualChosenWithTheKeyIsHeld(t *testing.T) {
	f := newFixture(t)
	f.status("fix-cart-total", "needs-decision: pick A or B")
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
