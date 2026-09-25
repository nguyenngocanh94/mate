package console

import (
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The message box's own fixtures (mvp.md task 15). The session-mate-* and
// project-* goldens already cover the rail and the panel in their default
// state; these pin what only exists because of this task and has no other
// fixture: an inbox item selected with its `[assign]` button, an empty
// inbox, the `[all]` filter, the outcome line, and the Actions menu the box
// zone opens.

// TestGoldenBoxPanelFocused renders the project frame with Tab moved onto
// the box panel: the panel's title goes accent, its selection marker becomes
// the focused one, and the key line names the panel's own bare keys.
func TestGoldenBoxPanelFocused(t *testing.T) {
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("tab")) // list -> inspector
	m, _ = send(t, m, key("tab")) // inspector -> box
	if m.focus != paneBox {
		t.Fatalf("focus = %v, want paneBox after two Tabs", m.focus)
	}
	// The cursor and the pointer both on the one inbox item, so the panel's
	// own action strip is in the fixture too: it is the same strip the rail
	// draws, and the project frame is the surface where it is easiest to
	// lose. The panel shows the inbox, not the log - the sample project's
	// box holds four entries and exactly one of them is waiting.
	m.boxSel = 0
	m.boxHover = 0
	assertGolden(t, "box-panel-focused-120x36-unicode", renderFrame(t, m))
}

// TestBoxDigestReplacesThePanelWhenTheTableWouldLoseTooMuch pins the one
// layout decision the panel makes: at 80x24 the crews table cannot spare
// eight rows, so the box collapses to the one-line digest instead.
func TestBoxDigestReplacesThePanelWhenTheTableWouldLoseTooMuch(t *testing.T) {
	wide := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	wide, _ = send(t, wide, key("enter"))
	if h, panel := wide.boxRegion(layout(120, 36)); !panel || h != boxPanelRows {
		t.Fatalf("at 120x36 boxRegion = (%d, %v), want the full panel", h, panel)
	}
	narrow := newFixture(t, sampleTree(), 80, 24, unicodeGlyphs)
	narrow, _ = send(t, narrow, key("enter"))
	h, panel := narrow.boxRegion(layout(80, 24))
	if panel || h != 1 {
		t.Fatalf("at 80x24 boxRegion = (%d, %v), want the one-line digest", h, panel)
	}
	// And the digest must actually carry Summarize's figures, or the frame
	// that dropped the panel says less than the panel it replaced.
	line := boxDigestLine(sampleBox(), unicodeGlyphs, plainPalette()).render(80)
	for _, want := range []string{"1 waiting", "4 entries", "1 crew"} {
		if !strings.Contains(line, want) {
			t.Errorf("the digest line %q does not carry %q", line, want)
		}
	}
}

// TestBoxSelectionFollowsTheNewestUntilMoved pins the rail's default: a
// reader who has not touched j/k is looking at the newest item, and stays on
// it as a crew asks something new - but an absolute selection, once made,
// keeps naming the same item rather than sliding.
func TestBoxSelectionFollowsTheNewestUntilMoved(t *testing.T) {
	b := boxList{field: sessionTestBox()}
	if got := boxDefaultSelection(b); got != 1 {
		t.Fatalf("default selection = %d, want the newest inbox item (1)", got)
	}
	grown := b
	grown.field.Value.Inbox = append(append([]query.BoxEntry{}, b.rows()...),
		query.BoxEntry{Seq: 9, Kind: query.BoxStatus, Crew: "k4", Verb: "blocked", Text: "no credentials", Attention: true})
	if got := boxDefaultSelection(grown); got != 2 {
		t.Fatalf("default selection after a new question = %d, want the new newest (2)", got)
	}
	if e, ok := boxSelectedEntry(grown, 0); !ok || e.Verb != "needs-decision" {
		t.Fatalf("index 0 after a new question = %+v, want the same needs-decision item", e)
	}
}

// TestBoxInboxIsTheDefaultAndAllShowsTheLog pins the one thing the `[all]`
// toggle is for: the rail draws the inbox, the toggle draws the merge, and
// nothing in between drops a line from the record.
func TestBoxInboxIsTheDefaultAndAllShowsTheLog(t *testing.T) {
	v := sessionTestBox()
	inbox := boxList{field: v}
	all := boxList{field: v, all: true}
	if got := len(inbox.rows()); got != 2 {
		t.Fatalf("inbox rows = %d, want the two open questions", got)
	}
	if got := len(all.rows()); got != len(v.Value.Entries) {
		t.Fatalf("[all] rows = %d, want the whole log (%d)", got, len(v.Value.Entries))
	}
	for _, e := range inbox.rows() {
		if e.Verb != "needs-decision" && e.Verb != "blocked" {
			t.Fatalf("inbox holds a %q entry; only an open question or an incident belongs there", e.Verb)
		}
		if !e.Resolvable() {
			t.Fatalf("inbox item %+v carries no resolve line", e)
		}
	}
	// The inbox names the need and never the text (2026-09-19: the crew's
	// status text is its own summary of a question it asked in full in its
	// pane, and reading the summary never replaced looking); `[all]` is
	// the record, verb and text on the entry's own line.
	if inbox.wraps() || all.wraps() {
		t.Fatalf("wraps() = inbox %v, all %v; no surface lays text under a row any more", inbox.wraps(), all.wraps())
	}
	for _, e := range inbox.rows() {
		line := boxEntryText(e, false, unicodeGlyphs)
		if !strings.Contains(line, "needs an answer") {
			t.Fatalf("inbox line %q does not say what the crew needs", line)
		}
		if e.Text != "" && strings.Contains(line, e.Text) {
			t.Fatalf("inbox line %q carries the crew's text; the pane and [assign] are where that goes", line)
		}
		if strings.Contains(line, "needs-decision") {
			t.Fatalf("inbox line %q shows the raw verb", line)
		}
	}
	for _, e := range all.rows() {
		if e.Text == "" {
			continue
		}
		if line := boxEntryText(e, true, unicodeGlyphs); !strings.Contains(line, e.Verb) || !strings.Contains(line, sanitizeText(e.Text)) {
			t.Fatalf("[all] line %q lost the verb or the text of %+v", line, e)
		}
	}
}

// TestBoxEmptyInboxSaysNothingWaiting pins the placeholder and the header
// count: an empty inbox is a state, not a failed read, and the header must
// say so in words rather than by going blank. The words are the reader's own
// (2026-09-19): what the rail counts is crews waiting on somebody, and only
// the Mate's manual still calls answering it "resolve".
func TestBoxEmptyInboxSaysNothingWaiting(t *testing.T) {
	b := boxList{field: sessionTestEmptyBox()}
	if got := len(b.rows()); got != 0 {
		t.Fatalf("empty-inbox fixture has %d rows", got)
	}
	body := boxBodyLines(b, -1, -1, true, unicodeGlyphs, plainPalette(), 44, 4)
	if !strings.Contains(body[0].render(44), "nothing waiting") {
		t.Fatalf("empty inbox body = %q, want the placeholder", body[0].render(44))
	}
	head := boxCountLine(b, unicodeGlyphs, plainPalette()).render(44)
	if !strings.Contains(head, "nothing waiting") {
		t.Fatalf("empty inbox header = %q, want the count line to say so", head)
	}
	two := boxCountLine(boxList{field: sessionTestBox()}, unicodeGlyphs, plainPalette()).render(44)
	if !strings.Contains(two, "2 waiting") {
		t.Fatalf("header = %q, want \"2 waiting\"", two)
	}
}

// TestBoxInboxRowSaysWhetherItWasAssigned is mvp.md task 30's row: an item
// handed to the Mate stays in the inbox (the crew is not answered yet), and
// says so after its need - "assigned, queued" while the line waits for the
// Mate's composer, "assigned HH:MM" once it got there - whole, at the rail's
// own width, with the [assign] button gone because pressing it again would do
// nothing.
func TestBoxInboxRowSaysWhetherItWasAssigned(t *testing.T) {
	at := time.Date(2026, 9, 24, 14, 32, 0, 0, time.UTC)
	base := query.BoxEntry{Seq: 1, At: at, Kind: query.BoxStatus, Crew: "buybtn", Verb: "needs-decision",
		Text: "pick A or B", Attention: true, Resolve: "resolve: buybtn asked", AssignKey: "crews/buybtn.status@0"}
	queued := base
	queued.Assigned = query.BoxAssign{State: query.BoxAssignQueued, At: at}
	sent := base
	sent.Assigned = query.BoxAssign{State: query.BoxAssignSent, At: at, SentAt: at.Add(3 * time.Minute)}

	for _, tc := range []struct {
		name string
		e    query.BoxEntry
		want string
	}{
		{"not assigned", base, "14:32  buybtn  needs an answer"},
		{"queued", queued, "14:32  buybtn  needs an answer · assigned, queued"},
		{"sent", sent, "14:32  buybtn  needs an answer · assigned 14:35"},
	} {
		view := query.KnownField(query.BoxView{Entries: []query.BoxEntry{tc.e}, Inbox: []query.BoxEntry{tc.e}})
		rail := strings.Join(RenderInboxRail(view, 0, 54, 8), "\n")
		if !strings.Contains(rail, tc.want) {
			t.Errorf("%s: rail does not carry %q whole:\n%s", tc.name, tc.want, rail)
		}
		if tc.e.Assigned.State == "" {
			if !strings.Contains(rail, "[assign]") {
				t.Errorf("%s: the selected unassigned row shows no [assign]:\n%s", tc.name, rail)
			}
			if strings.Contains(rail, "assigned") && !strings.Contains(rail, "[assign]") {
				t.Errorf("%s: an unassigned row says assigned:\n%s", tc.name, rail)
			}
		} else if strings.Contains(rail, "[assign]") {
			t.Errorf("%s: an assigned row still offers [assign]:\n%s", tc.name, rail)
		}
	}
	// The ASCII glyph set spells the separator in ASCII too.
	if got := boxEntryText(queued, false, asciiGlyphs); got != "14:32  buybtn  needs an answer . assigned, queued" {
		t.Errorf("ascii row = %q", got)
	}
}

// TestBoxNeedPhraseNamesTheNeedNotTheText pins the inbox vocabulary: one
// plain phrase per kind, the pane for the words.
func TestBoxNeedPhraseNamesTheNeedNotTheText(t *testing.T) {
	cases := []struct {
		e    query.BoxEntry
		want string
	}{
		{query.BoxEntry{Kind: query.BoxStatus, Verb: "needs-decision", Text: "red or blue?"}, "needs an answer"},
		{query.BoxEntry{Kind: query.BoxStatus, Verb: "blocked", Text: "legacy verb"}, "needs an answer"},
		{query.BoxEntry{Kind: query.BoxIncident, Verb: "stale", Text: "no pane change for 4m"}, "stuck, quiet too long"},
		{query.BoxEntry{Kind: query.BoxIncident, Verb: "runtime_lost"}, "agent gone"},
		{query.BoxEntry{Kind: query.BoxIncident, Verb: "wedged"}, "send wedged"},
		{query.BoxEntry{Kind: query.BoxIncident, Verb: "budget"}, "over budget"},
		{query.BoxEntry{Kind: query.BoxIncident, Verb: "novel"}, "incident novel"},
	}
	for _, tc := range cases {
		if got := boxNeedPhrase(tc.e); got != tc.want {
			t.Errorf("boxNeedPhrase(%s %s) = %q, want %q", tc.e.Kind, tc.e.Verb, got, tc.want)
		}
	}
}

// sessionTestBox is task 15's pinned rail fixture: a log of five entries -
// `working`, a message, two open questions and a `done` - of which exactly
// two reach the inbox. That is the shape the rail now has to prove: the log
// keeps everything, the rail draws only what somebody still has to decide,
// and `[all]` is the only way to see the rest.
func sessionTestBox() query.Field[query.BoxView] {
	working := query.BoxEntry{
		Seq: 0, At: sessionTestClock(13, 41), Kind: query.BoxStatus,
		Source: "crew", Target: "crew:k3", Crew: "k3",
		Verb: "working", Text: "reading the ticket",
		Resolve: testResolveLine("k3", "reading the ticket"),
	}
	message := query.BoxEntry{
		Seq: 1, At: sessionTestClock(13, 52), Kind: query.BoxMessage,
		Source: "user", Target: "mate",
		Text: "spawn a crew for the webhook fix",
	}
	asked := query.BoxEntry{
		Seq: 2, At: sessionTestClock(14, 1), Kind: query.BoxStatus,
		Source: "crew", Target: "crew:k3", Crew: "k3",
		Verb: "needs-decision", Text: "migration for idempotency_keys, or key off stripe_events?",
		Attention: true, Resolve: testResolveLine("k3", "migration for idempotency_keys, or key off stripe_events?"),
	}
	blocked := query.BoxEntry{
		Seq: 3, At: sessionTestClock(14, 6), Kind: query.BoxStatus,
		Source: "crew", Target: "crew:k9", Crew: "k9",
		Verb: "blocked", Text: "the staging database refuses the new migration; drop and recreate it, or patch the constraint in place?",
		Attention: true, Resolve: testResolveLine("k9", "the staging database refuses the new migration; drop and recreate it, or patch the constraint in place?"),
	}
	done := query.BoxEntry{
		Seq: 4, At: sessionTestClock(14, 9), Kind: query.BoxStatus,
		Source: "crew", Target: "crew:k2", Crew: "k2",
		Verb: "done", Text: "PR ready for review",
		Attention: true, Resolve: testResolveLine("k2", "PR ready for review"),
	}
	return query.KnownField(query.BoxView{
		Entries: []query.BoxEntry{working, message, asked, blocked, done},
		Inbox:   []query.BoxEntry{asked, blocked},
		Crews:   3, Awaiting: 2, LastAt: sessionTestClock(14, 9),
	})
}

// sessionTestEmptyBox is the same project with nothing waiting: the log is
// not empty, the inbox is.
func sessionTestEmptyBox() query.Field[query.BoxView] {
	v := sessionTestBox()
	v.Value.Inbox = nil
	v.Value.Awaiting = 0
	return v
}

func sessionTestClock(h, m int) time.Time {
	return time.Date(2026, 9, 10, h, m, 0, 0, time.UTC)
}
