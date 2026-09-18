package autopilot_test

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/autopilot"
	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// The three things a digest reports, and the two it does not: a `working`
// line (nobody has to do anything about it) and an already-answered question
// (it left the inbox).
func TestGatherTakesTheInboxAndEachCrewsLatestWaitMate(t *testing.T) {
	f := newFixture(t)
	f.status("k3", "working: reading the schema")
	f.status("k3", "needs-decision: pick A or B")
	f.status("k7", "working: writing the report")
	f.status("k7", "wait-mate: report.md is ready")
	f.incident("k9", box.IncidentStale, store.IncidentOpen, "no status line and no pane change for 3m0s")

	items := autopilot.Gather(f.view(), nil, f.clock.Now())
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.Crew+" "+string(item.Kind))
	}
	sort.Strings(got)
	want := []string{"k3 needs-decision", "k7 wait-mate", "k9 blocked"}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Fatalf("gathered %v, want %v", got, want)
	}
}

// A `wait-mate` the crew has spoken after is history: the crew picked the
// work back up, and reporting the hand-back would send the Mate after
// something that has already moved on.
func TestGatherSkipsAWaitMateTheCrewHasSpokenAfter(t *testing.T) {
	f := newFixture(t)
	f.status("k3", "wait-mate: first pass done")
	f.status("k3", "working: second pass")

	if items := autopilot.Gather(f.view(), nil, f.clock.Now()); len(items) != 0 {
		t.Fatalf("gathered %+v, want nothing: the crew's latest line is `working`", items)
	}
}

// A closed crew has no pane to answer into, and its lines are history.
func TestGatherSkipsClosedCrews(t *testing.T) {
	f := newFixture(t)
	f.status("k3", "wait-mate: report.md is ready")
	f.status("k9", "needs-decision: pick A or B")
	f.crewMeta("k3", map[string]string{"state": "finished"})
	f.crewMeta("k9", map[string]string{"state": "failed"})

	if items := autopilot.Gather(f.view(), nil, f.clock.Now()); len(items) != 0 {
		t.Fatalf("gathered %+v, want nothing from crews the captain closed", items)
	}
}

// The cursor is the whole of "already digested": everything at or before it
// is gone, and everything after it is new.
func TestGatherHonoursTheCursorPerFile(t *testing.T) {
	f := newFixture(t)
	f.status("k3", "needs-decision: pick A or B")
	f.status("k9", "needs-decision: rebase or merge")

	view := f.view()
	all := autopilot.Gather(view, nil, f.clock.Now())
	if len(all) != 2 {
		t.Fatalf("gathered %+v, want both questions", all)
	}

	// Digest only k3's question, then gather again: k9's is still new.
	cursor := autopilot.Advance(nil, all[:1])
	rest := autopilot.Gather(view, cursor, f.clock.Now())
	if len(rest) != 1 || rest[0].Crew != "k9" {
		t.Fatalf("gathered %+v after digesting k3, want only k9", rest)
	}
	if left := autopilot.Gather(view, autopilot.Advance(cursor, rest), f.clock.Now()); len(left) != 0 {
		t.Fatalf("gathered %+v after digesting both, want nothing", left)
	}
}

// Advance never moves a cursor backwards: a file whose newest digested line
// is older than one already recorded keeps the recorded offset.
func TestAdvanceNeverMovesACursorBackwards(t *testing.T) {
	cursor := map[string]int64{"/w/crews/k3.status": 100}
	next := autopilot.Advance(cursor, []autopilot.Item{{File: "/w/crews/k3.status", Offset: 40}})
	if next["/w/crews/k3.status"] != 100 {
		t.Fatalf("cursor = %d, want it held at 100", next["/w/crews/k3.status"])
	}
	if cursor["/w/crews/k3.status"] != 100 {
		t.Fatal("Advance mutated the cursor it was given")
	}
}

// The daemon's own wedged incident is about the delivery that just failed.
// Sending it would mean reporting the failure through it.
func TestGatherSkipsTheDaemonsOwnWedgedIncident(t *testing.T) {
	f := newFixture(t)
	f.incident(autopilot.MateCrew, box.IncidentWedged, store.IncidentOpen, "no digest has reached the Mate for 5m0s")

	if items := autopilot.Gather(f.view(), nil, f.clock.Now()); len(items) != 0 {
		t.Fatalf("gathered %+v, want the Mate's own wedged incident left out", items)
	}
}

// A resolved incident is history, not an inbox item (mvp.md section 4b).
func TestGatherSkipsAResolvedIncident(t *testing.T) {
	f := newFixture(t)
	f.incident("k9", box.IncidentStale, store.IncidentOpen, "quiet")
	f.incident("k9", box.IncidentStale, store.IncidentResolved, "the pane changed")

	if items := autopilot.Gather(f.view(), nil, f.clock.Now()); len(items) != 0 {
		t.Fatalf("gathered %+v, want nothing: the observer cleared the incident", items)
	}
}

// The exact format written into docs/mvp.md section 5, which task 20 teaches
// the Mate to read. A change here is a change to that contract.
func TestLineIsTheFormatTheSpecPromises(t *testing.T) {
	items := []autopilot.Item{
		{Kind: autopilot.ItemNeedsDecision, Crew: "k3", Text: "pick A or B"},
		{Kind: autopilot.ItemBlocked, Crew: "k9", IncidentKind: box.IncidentStale, QuietFor: 4 * time.Minute},
		{Kind: autopilot.ItemWaitMate, Crew: "k7", Text: "report.md is ready"},
	}
	want := `digest: 3 item(s) — k3 needs-decision: "pick A or B" · k9 blocked: stale, quiet for 4m0s · ` +
		`k7 wait-mate: "report.md is ready" — status files under /w/.matev2/projects/shop/crews; ` +
		`act per AGENTS.md section 10`
	if got := autopilot.Line(items, "/w/.matev2/projects/shop/crews"); got != want {
		t.Fatalf("Line =\n%s\nwant\n%s", got, want)
	}
}

// A crew line is arbitrary text: it can be long, it can hold newlines
// somebody echoed in, and it can hold the quote character the digest uses to
// delimit it. All three have to come out as one quoted line of bounded
// length, because the composer this goes into wraps otherwise and send's own
// verification then cannot read it back.
func TestLineFlattensAndBoundsWhatACrewWrote(t *testing.T) {
	long := strings.Repeat("ab", 200)
	got := autopilot.Line([]autopilot.Item{{
		Kind: autopilot.ItemNeedsDecision, Crew: "k3",
		Text: "use the \"fast\" path\nor the slow one? " + long,
	}}, "/crews")
	if strings.Contains(got, "\n") {
		t.Fatalf("the digest carries a newline: %q", got)
	}
	quoted := between(t, got, `k3 needs-decision: "`, `" — status files`)
	if strings.Contains(quoted, `"`) {
		t.Fatalf("the quoted question can close its own quotes: %q", quoted)
	}
	if runes := []rune(quoted); len(runes) != autopilot.MaxItemRunes+1 {
		t.Fatalf("quoted question is %d runes, want %d plus the ellipsis", len(runes), autopilot.MaxItemRunes)
	}
	if !strings.HasSuffix(quoted, "…") {
		t.Fatalf("a truncated question does not say so: %q", quoted)
	}
}

// The count is always the truth even when the line cannot spell every item
// out: a Mate told "12 item(s)" and shown five knows to go and read the rest.
func TestLineNamesEveryItemInTheCountAndTheRestAsMore(t *testing.T) {
	var items []autopilot.Item
	for i := 0; i < autopilot.MaxItemsListed+3; i++ {
		items = append(items, autopilot.Item{Kind: autopilot.ItemNeedsDecision, Crew: "k3", Text: "q"})
	}
	got := autopilot.Line(items, "/crews")
	if !strings.HasPrefix(got, "digest: 8 item(s) — ") {
		t.Fatalf("Line = %q, want all eight counted", got)
	}
	if n := strings.Count(got, "k3 needs-decision"); n != autopilot.MaxItemsListed {
		t.Fatalf("Line spells out %d items, want %d: %q", n, autopilot.MaxItemsListed, got)
	}
	if !strings.Contains(got, "· +3 more —") {
		t.Fatalf("Line does not name the remainder: %q", got)
	}
}

// An incident the observer could not attribute names no crew, and the digest
// must not invent one - a Mate sent after `crews/.status` finds nothing.
func TestLineNamesNoCrewForAnUnattributedIncident(t *testing.T) {
	got := autopilot.Line([]autopilot.Item{{
		Kind: autopilot.ItemBlocked, IncidentKind: box.IncidentRuntimeLost, QuietFor: 90 * time.Second,
	}}, "/crews")
	if !strings.Contains(got, "- blocked: runtime_lost, quiet for 1m30s") {
		t.Fatalf("Line = %q", got)
	}
}

func between(t *testing.T, s, open, close string) string {
	t.Helper()
	_, rest, ok := strings.Cut(s, open)
	if !ok {
		t.Fatalf("%q has no %q", s, open)
	}
	out, _, ok := strings.Cut(rest, close)
	if !ok {
		t.Fatalf("%q has no %q", s, close)
	}
	return out
}
