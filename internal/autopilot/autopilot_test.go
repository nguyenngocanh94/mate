package autopilot_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/autopilot"
	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// Manual mode sends nothing, ever - mvp.md section 5's first line, and
// the rule the whole daemon has to be safe under. The project here has a full
// inbox and a live Mate with an empty composer: everything except the flag.
func TestManualModeSendsNothing(t *testing.T) {
	f := newFixture(t)
	f.status("k3", "needs-decision: pick A or B")
	f.incident("k9", box.IncidentStale, store.IncidentOpen, "quiet for 3m0s")

	f.mustTick()

	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v into the Mate with no .auto flag", typed)
	}
	if sent := f.sent(); len(sent) != 0 {
		t.Fatalf("sent.log = %+v, want nothing recorded", sent)
	}
	if cursor := f.cursor(); len(cursor) != 0 {
		t.Fatalf("cursor = %v, want none written in manual mode", cursor)
	}
}

// A tick with nothing new says nothing. The daemon is not a heartbeat: a Mate
// that gets a line every ninety seconds whether or not anything happened
// stops reading them.
func TestNothingNewMeansNoSend(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "working: reading the schema")

	f.mustTick()

	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v with nothing to report", typed)
	}
}

// The happy path, end to end: one line, marked, verified into the composer,
// recorded in sent.log, cursor advanced.
func TestOneVerifiedDigestPerTick(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.status("k7", "wait-mate: report.md is ready")

	f.mustTick()

	line := f.requireOneDigest()
	if !strings.HasPrefix(line, send.Marker+"digest: 2 item(s) — ") {
		t.Fatalf("digest = %q", line)
	}
	for _, want := range []string{
		`k3 needs-decision: "pick A or B"`,
		`k7 wait-mate: "report.md is ready"`,
		"status files under " + f.ws.CrewsDir(project),
		"act per AGENTS.md section 10",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("digest %q does not carry %q", line, want)
		}
	}

	// sent.log is the daemon's claim that the Mate received it, so it is
	// written only after the composer cleared - and without the marker, the
	// way every other app line is recorded.
	sent := f.sent()
	if len(sent) != 1 {
		t.Fatalf("sent.log = %+v, want one entry", sent)
	}
	if sent[0].Source != store.SourceApp || sent[0].Target != store.TargetMate {
		t.Fatalf("sent entry = %+v, want an app line to the Mate", sent[0])
	}
	if sent[0].Text != strings.TrimPrefix(line, send.Marker) {
		t.Fatalf("sent.log text %q is not the line that was typed", sent[0].Text)
	}

	if status := f.daemonStatus(); status.Sends != 1 || !status.LastSentAt.Equal(f.clock.Now()) {
		t.Fatalf("daemon status = %+v, want one send at %s", status, f.clock.Now())
	}
}

// The line is typed and submitted; it is never handed to `herdr agent prompt`,
// which reports success against a modal or a half-typed human line (mvp.md
// section 7).
func TestTheDigestIsTypedAndNeverPrompted(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")

	f.mustTick()

	for _, call := range f.rt.Calls {
		if call == "PromptAgent" {
			t.Fatalf("the daemon used herdr agent prompt; calls = %v", f.rt.Calls)
		}
	}
	if len(f.rt.SentKeys) == 0 {
		t.Fatal("nothing was submitted: the line would be sitting in the composer")
	}
}

// An item is digested exactly once. The second tick has nothing new even
// though the question is still unanswered and still in the inbox.
func TestAnItemIsNeverDigestedTwice(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")

	f.mustTick()
	f.clock.Advance(autopilot.DefaultInterval)
	f.mustTick()

	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v, want the question digested once", typed)
	}
}

// The cursor is on disk, so the same is true of a console that was killed and
// reopened - which is the case that matters, because an in-memory cursor
// would re-send the whole inbox on every restart.
func TestARestartDoesNotResendWhatWasAlreadyDigested(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.mustTick()
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v on the first tick", typed)
	}

	restarted := f.restart()
	f.clock.Advance(autopilot.DefaultInterval)
	if err := restarted.Tick(context.Background()); err != nil {
		t.Fatalf("Tick after restart: %v", err)
	}
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v, want the restarted daemon to send nothing", typed)
	}

	// And a genuinely new line still goes out.
	f.status("k9", "needs-decision: rebase or merge")
	f.clock.Advance(autopilot.DefaultInterval)
	if err := restarted.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	typed := f.typed()
	if len(typed) != 2 || !strings.Contains(typed[1], "k9 needs-decision") {
		t.Fatalf("typed %#v, want the new question and only it", typed)
	}
	if strings.Contains(typed[1], "k3") {
		t.Fatalf("the restarted daemon re-sent k3's question: %q", typed[1])
	}
}

// The captain shares the Mate's composer. Unsubmitted text in it is theirs,
// and the daemon must not type over it, must not lose the digest, and must
// say once - not once per item - why nothing went out.
func TestAPendingComposerRefusesAndRetriesNextTick(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.status("k9", "needs-decision: rebase or merge")
	f.rt.SetReadOutput(f.handle, claudeScreen("half a thought the captain is stil"))

	f.mustTick()

	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v over the captain's own text", typed)
	}
	if sent := f.sent(); len(sent) != 0 {
		t.Fatalf("sent.log = %+v, want nothing recorded for a line nobody received", sent)
	}
	status := f.daemonStatus()
	if status.Notice == "" {
		t.Fatal("the daemon recorded no notice for a refused digest")
	}
	if !strings.Contains(status.Notice, "unsubmitted text") {
		t.Fatalf("notice = %q, want internal/send's own observation", status.Notice)
	}
	if n := strings.Count(status.Notice, "not delivered"); n != 1 {
		t.Fatalf("notice names the failure %d times, want once per tick: %q", n, status.Notice)
	}

	// The composer clears; both questions are still owed and go out together.
	f.rt.SetReadOutput(f.handle, claudeScreen(""))
	f.clock.Advance(autopilot.DefaultInterval)
	f.mustTick()

	line := f.requireOneDigest()
	if !strings.Contains(line, "2 item(s)") || !strings.Contains(line, "k3") || !strings.Contains(line, "k9") {
		t.Fatalf("digest = %q, want both refused items", line)
	}
	if got := f.daemonStatus().Notice; got != "" {
		t.Fatalf("notice = %q, want it cleared by the delivery", got)
	}
}

// A harness mid-turn is the other refusal: the Mate is thinking, and a line
// typed now would queue behind the turn rather than be read.
func TestABusyComposerRefusesAndRetriesNextTick(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.rt.SetReadOutput(f.handle, claudeBusyScreen())

	f.mustTick()
	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v into a pane mid-turn", typed)
	}
	if got := f.daemonStatus().Notice; !strings.Contains(got, "mid-turn") {
		t.Fatalf("notice = %q, want the busy observation", got)
	}

	f.rt.SetReadOutput(f.handle, claudeScreen(""))
	f.clock.Advance(autopilot.DefaultInterval)
	f.mustTick()
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v, want the digest on the tick the pane freed up", typed)
	}
}

// Five minutes of undelivered digests is a `wedged` incident on the Mate,
// and a delivery resolves it. The incident is the durable half: the footer
// line dies with the console, the inbox does not.
func TestUndeliveredForTooLongOpensAndResolvesWedged(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.rt.SetReadOutput(f.handle, claudeScreen("the captain is mid-sentence"))

	f.mustTick()
	if got := f.incidents(); len(got) != 0 {
		t.Fatalf("incidents = %+v, want none before the wedge threshold", got)
	}

	// Just under, then just over.
	f.clock.Advance(autopilot.DefaultWedgedAfter - time.Second)
	f.mustTick()
	if got := f.incidents(); len(got) != 0 {
		t.Fatalf("incidents = %+v at %s, want none before %s",
			got, autopilot.DefaultWedgedAfter-time.Second, autopilot.DefaultWedgedAfter)
	}
	f.clock.Advance(time.Second)
	f.mustTick()

	opened := f.incidents()
	if len(opened) != 1 {
		t.Fatalf("incidents = %+v, want exactly one", opened)
	}
	if opened[0].Crew != autopilot.MateCrew || opened[0].Kind != string(box.IncidentWedged) ||
		opened[0].State != store.IncidentOpen {
		t.Fatalf("incident = %+v, want an open wedged incident on the Mate", opened[0])
	}

	// It is opened once, not once per tick.
	f.clock.Advance(autopilot.DefaultInterval)
	f.mustTick()
	if got := f.incidents(); len(got) != 1 {
		t.Fatalf("incidents = %+v, want the open line written once", got)
	}

	// The inbox shows it: that is how a captain who opens the console later
	// finds out auto mode has been delivering nothing.
	inbox := box.Inbox(f.view())
	if len(inbox) != 2 {
		t.Fatalf("inbox = %+v, want the crew's question and the wedged incident", inbox)
	}

	// A verified send resolves it.
	f.rt.SetReadOutput(f.handle, claudeScreen(""))
	f.clock.Advance(autopilot.DefaultInterval)
	f.mustTick()
	resolved := f.incidents()
	if len(resolved) != 2 || resolved[1].State != store.IncidentResolved ||
		resolved[1].Crew != autopilot.MateCrew || resolved[1].Kind != string(box.IncidentWedged) {
		t.Fatalf("incidents = %+v, want the wedge resolved", resolved)
	}
	if got := box.OpenIncidents(f.view(), autopilot.MateCrew); len(got) != 0 {
		t.Fatalf("open incidents = %+v, want none", got)
	}
}

// A Mate that is not running is the other way a digest fails to arrive, and
// it wedges on the same clock: auto mode silently delivering nothing for five
// minutes is the state the incident exists to surface, whichever half of the
// path is missing.
func TestAMissingMateWedgesToo(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.handleErr = errors.New("the Mate of shop is not running")

	f.mustTick()
	if got := f.daemonStatus().Notice; !strings.Contains(got, "not running") {
		t.Fatalf("notice = %q, want the handle failure named", got)
	}
	f.clock.Advance(autopilot.DefaultWedgedAfter)
	f.mustTick()

	got := f.incidents()
	if len(got) != 1 || got[0].Kind != string(box.IncidentWedged) {
		t.Fatalf("incidents = %+v, want a wedged incident", got)
	}
	if !strings.Contains(got[0].Text, "not running") {
		t.Fatalf("incident text = %q, want it to name which half failed", got[0].Text)
	}
}

// A console that restarts while a wedge is open does not open a second one:
// the file is the state (mvp.md section 4b).
func TestARestartDoesNotReopenAnOpenWedge(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.rt.SetReadOutput(f.handle, claudeScreen("mid-sentence"))
	f.mustTick()
	f.clock.Advance(autopilot.DefaultWedgedAfter)
	f.mustTick()
	if got := f.incidents(); len(got) != 1 {
		t.Fatalf("incidents = %+v, want one", got)
	}

	restarted := f.restart()
	f.clock.Advance(autopilot.DefaultWedgedAfter * 2)
	for i := 0; i < 2; i++ {
		if err := restarted.Tick(context.Background()); err != nil {
			t.Fatalf("Tick after restart: %v", err)
		}
	}
	if got := f.incidents(); len(got) != 1 {
		t.Fatalf("incidents = %+v, want the restarted daemon to leave the open one alone", got)
	}
}

// The `.auto` flag disappearing stops the daemon within one tick, whoever
// deleted it - the Mate's hook on an unmarked captain prompt, the console's
// `m` key, or a hand. Nothing else is consulted and nothing is cached.
func TestTheFlagDisappearingStopsTheDaemonWithinOneTick(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.mustTick()
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v on the first tick", typed)
	}

	f.auto(false)
	f.status("k9", "needs-decision: rebase or merge")
	f.clock.Advance(autopilot.DefaultInterval)
	f.mustTick()

	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v after .auto was deleted, want nothing more", typed)
	}
	if got := f.daemonStatus().Notice; got != "" {
		t.Fatalf("notice = %q, want manual mode to clear it: nothing is pending", got)
	}
}

// The flag is read again immediately before the line is typed, because the
// merge and the handle lookup both take time, and a captain who takes the
// composer during that window has already had `.auto` deleted by the Mate's
// own hook. Answering them a moment later is exactly the takeover auto mode
// promises not to fight.
func TestTheFlagIsReadAgainRightBeforeTyping(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.onHandle = func() { f.auto(false) }

	f.mustTick()

	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v after the captain took over mid-tick", typed)
	}
	if got := f.incidents(); len(got) != 0 {
		t.Fatalf("incidents = %+v: a captain taking over is not a wedge", got)
	}
	if got := f.daemonStatus().Notice; got != "" {
		t.Fatalf("notice = %q: a captain taking over is not a failure", got)
	}
	// Nothing was digested, so the question is still owed when auto returns.
	f.auto(true)
	f.onHandle = nil
	f.clock.Advance(autopilot.DefaultInterval)
	f.mustTick()
	if line := f.requireOneDigest(); !strings.Contains(line, "k3 needs-decision") {
		t.Fatalf("digest = %q, want the question that was held back", line)
	}
}

// One workspace, two projects, one flag: the daemon is per project and the
// other one is untouched.
func TestOnlyProjectsWithTheFlagAreDigested(t *testing.T) {
	f := newFixture(t)
	f.addProject("blog")
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	if err := f.ws.AppendStatus("blog", "b1", "needs-decision: which template"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}

	f.mustTick()

	line := f.requireOneDigest()
	if strings.Contains(line, "b1") {
		t.Fatalf("digest = %q, want nothing from the manual-mode project", line)
	}
	entries, _, err := f.ws.ReadSent("blog", 0)
	if err != nil {
		t.Fatalf("ReadSent(blog): %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("blog's sent.log = %+v, want nothing", entries)
	}
}

// Start/Stop is the console's own lifecycle: the daemon ticks on its own
// goroutine and stops with the workspace.
func TestStartTicksAndStopEndsIt(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")

	ticked := make(chan struct{})
	f.pilot = autopilot.New(f.ws, f.deps(func() {
		select {
		case ticked <- struct{}{}:
		default:
		}
	}))
	f.pilot.Start(context.Background())
	select {
	case <-ticked:
	case <-time.After(5 * time.Second):
		f.pilot.Stop()
		t.Fatal("the daemon never ticked")
	}
	f.pilot.Stop()

	if typed := f.typed(); len(typed) == 0 {
		t.Fatal("the running daemon sent nothing")
	}
}

// Task 30: the daemon types nothing itself. A digest the Mate cannot take
// yet is queued in `mate/.outbox`, and the cursor stays where it was until
// the outbox reports that digest sent - the invariant task 19 put on the
// cursor ("only a verified send advances it"), now kept across two
// components rather than inside one tick.
func TestTheDigestGoesThroughTheOutboxAndTheCursorWaitsForSent(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.rt.SetReadOutput(f.handle, claudeBusyScreen())

	f.mustTick()

	items := f.outboxItems()
	if len(items) != 1 {
		t.Fatalf("outbox = %+v, want the one digest queued", items)
	}
	queued := items[0]
	if queued.Source != store.OutboxSourceDigest || queued.State != store.OutboxQueued ||
		!strings.HasPrefix(queued.Text, "digest: 1 item(s) — k3 needs-decision") {
		t.Fatalf("outbox item = %+v, want a queued digest carrying k3's question", queued)
	}
	if queued.Attempts != 1 || !strings.Contains(queued.LastRefusal, "mid-turn") {
		t.Fatalf("outbox item = %+v, want the tick's one immediate attempt refused as mid-turn", queued)
	}
	if cursor := f.cursor(); len(cursor) != 0 {
		t.Fatalf("cursor = %v after a digest was only queued, want it untouched", cursor)
	}

	// A second tick with nothing new does not queue a second digest.
	f.clock.Advance(autopilot.DefaultInterval)
	f.mustTick()
	if items := f.outboxItems(); len(items) != 1 {
		t.Fatalf("outbox = %+v, want still the one digest", items)
	}

	// The Mate's turn ends; the console's sender loop delivers it.
	f.rt.SetReadOutput(f.handle, claudeScreen(""))
	f.drain()

	line := f.requireOneDigest()
	if line != send.Marker+queued.Text {
		t.Fatalf("typed %q, want the queued digest %q", line, queued.Text)
	}
	items = f.outboxItems()
	if len(items) != 1 || items[0].State != store.OutboxSent || !items[0].SentAt.Equal(f.clock.Now()) {
		t.Fatalf("outbox = %+v, want the digest marked sent now", items)
	}
	cursor := f.cursor()
	if got, ok := cursor[f.ws.CrewStatus(project, "k3")]; !ok || got != 0 || len(cursor) != 1 {
		t.Fatalf("cursor = %v, want k3's status file advanced to the question's offset 0", cursor)
	}

	// And the next tick has nothing new: the item was digested exactly once.
	f.clock.Advance(autopilot.DefaultInterval)
	f.mustTick()
	f.drain()
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v, want the one digest only", typed)
	}
	if status := f.daemonStatus(); status.Sends != 1 || status.Notice != "" {
		t.Fatalf("daemon status = %+v, want one delivered digest and no notice", status)
	}
}

// A digest waiting in the outbox while the inbox changes is refreshed in
// place rather than joined by a second one: the Mate gets one line with the
// current picture, and the wedged clock (the item's `at`) keeps running.
func TestAQueuedDigestIsRefreshedNotDuplicated(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.rt.SetReadOutput(f.handle, claudeBusyScreen())
	f.mustTick()
	first := f.outboxItems()[0]

	f.status("k9", "needs-decision: rebase or merge")
	f.clock.Advance(autopilot.DefaultInterval)
	f.mustTick()

	items := f.outboxItems()
	if len(items) != 1 {
		t.Fatalf("outbox = %+v, want one digest refreshed in place", items)
	}
	if items[0].ID != first.ID || !items[0].At.Equal(first.At) {
		t.Fatalf("refreshed digest = %+v, want the first one's id and queue time kept", items[0])
	}
	if !strings.Contains(items[0].Text, "2 item(s)") || !strings.Contains(items[0].Text, "k9") {
		t.Fatalf("refreshed digest = %q, want both questions", items[0].Text)
	}

	f.rt.SetReadOutput(f.handle, claudeScreen(""))
	f.drain()
	if line := f.requireOneDigest(); !strings.Contains(line, "k3") || !strings.Contains(line, "k9") {
		t.Fatalf("digest = %q, want both questions in the one line", line)
	}
}

// A digest queued while auto mode was on is withdrawn, not typed, once the
// captain takes the composer back - even by the sender loop, long after the
// tick that queued it.
func TestAQueuedDigestIsWithdrawnWhenAutoGoesOff(t *testing.T) {
	f := newFixture(t)
	f.auto(true)
	f.status("k3", "needs-decision: pick A or B")
	f.rt.SetReadOutput(f.handle, claudeBusyScreen())
	f.mustTick()

	f.auto(false)
	f.rt.SetReadOutput(f.handle, claudeScreen(""))
	f.drain()

	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v after auto mode went off", typed)
	}
	items := f.outboxItems()
	if len(items) != 1 || items[0].State != store.OutboxDropped {
		t.Fatalf("outbox = %+v, want the digest dropped", items)
	}
	if cursor := f.cursor(); len(cursor) != 0 {
		t.Fatalf("cursor = %v, want the question still owed", cursor)
	}
}
