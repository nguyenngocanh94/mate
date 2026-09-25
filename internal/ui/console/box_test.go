package console

import (
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
)

func bxAt(h, m int) time.Time { return time.Date(2026, 9, 10, h, m, 0, 0, time.UTC) }

// bxAsk is an unanswered question from crew at t.
func bxAsk(crew, text string, at time.Time) query.BoxEntry {
	return query.BoxEntry{At: at, Kind: query.BoxStatus, Source: "crew", Target: "crew:" + crew, Crew: crew,
		Verb: "needs-decision", Text: text, Attention: true, Resolve: testResolveLine(crew, text)}
}

// bxWithBox puts a box on a Project of the design tree.
func bxWithBox(tree query.Snapshot, project int, inbox, log []query.BoxEntry) query.Snapshot {
	tree.Projects[project].Box = query.KnownField(query.BoxView{Inbox: inbox, Entries: log, Awaiting: len(inbox)})
	return tree
}

// The workspace box gathers every Project's inbox, newest first, and names
// the Project an item came from.
func TestTheWorkspaceBoxGathersEveryProjectNewestFirst(t *testing.T) {
	tree := designTree()
	older := bxAsk("cz", "which region?", bxAt(12, 0))
	tree = bxWithBox(tree, 0, []query.BoxEntry{older}, []query.BoxEntry{older})
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	items := m.boxItems()
	if len(items) != 2 {
		t.Fatalf("workspace box = %d items, want both Projects' questions", len(items))
	}
	if items[0].project != "payments-api" || items[1].project != "auth-gateway" {
		t.Fatalf("order = %s, %s; want the newest (payments-api 13:56) first", items[0].project, items[1].project)
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "payments-api · decide · 6m") {
		t.Fatalf("the workspace box does not name the item's Project:\n%s", frame)
	}
}

// On a Project the box holds that Project's inbox only, and names the crew.
func TestAProjectBoxHoldsOnlyItsOwnInbox(t *testing.T) {
	tree := designTree()
	other := bxAsk("cz", "which region?", bxAt(14, 0))
	tree = bxWithBox(tree, 0, []query.BoxEntry{other}, []query.BoxEntry{other})
	m, _, _ := bxPayments(t, 40, 36)
	tree.AsOf = goldenAsOf
	m.tree = tree
	items := m.boxItems()
	if len(items) != 1 || items[0].e.Crew != "k3" {
		t.Fatalf("payments-api box = %+v, want only its own k3", items)
	}
	frame := renderFrame(t, m)
	for _, want := range []string{"─ box ", "🤖 Backfill ledger v2", "[assign]", "k3 · decide · 6m"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the Project box is missing %q:\n%s", want, frame)
		}
	}
}

// Without anything waiting there is no box pane at all, and Tab never
// lands on one.
func TestNoBoxPaneWhenNothingWaits(t *testing.T) {
	tree := bxWithBox(designTree(), 7, nil, nil)
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	if _, ok := m.plan().slot(slotBox); ok {
		t.Fatal("an empty box still takes a pane")
	}
	for i := 0; i < 3; i++ {
		m, _ = send(t, m, key("tab"))
		if m.focus == paneBox {
			t.Fatal("Tab focused a box that is not drawn")
		}
	}
}

// The selection follows the newest item until the reader moves it.
func TestBoxSelectionFollowsTheNewestUntilMoved(t *testing.T) {
	tree := designTree()
	a, b := bxAsk("k3", "first?", bxAt(13, 0)), bxAsk("k7", "second?", bxAt(13, 50))
	tree = bxWithBox(tree, 7, []query.BoxEntry{a, b}, []query.BoxEntry{a, b})
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	m, _ = send(t, m, key("enter"))
	items := m.boxItems()
	if got := items[m.boxSelection(items)].e.Crew; got != "k7" {
		t.Fatalf("default selection = %s, want the newest (k7)", got)
	}
	m = bxFocusBox(t, m)
	m, _ = send(t, m, key("down"))
	items = m.boxItems()
	if got := items[m.boxSelection(items)].e.Crew; got != "k3" {
		t.Fatalf("selection after down = %s, want k3", got)
	}
	newer := bxAsk("k7", "third?", bxAt(14, 0))
	m.tree = bxWithBox(m.tree, 7, []query.BoxEntry{a, b, newer}, []query.BoxEntry{a, b, newer})
	if got := m.boxItems()[m.boxSelection(m.boxItems())]; got.e.Text == "third?" {
		t.Fatal("a moved selection jumped to a newer item")
	}
}

// The inbox is the default; l shows the whole log and back.
func TestBoxInboxIsTheDefaultAndLShowsTheLog(t *testing.T) {
	tree := designTree()
	ask := bxAsk("k3", "Run on the prod replica now?", bxAt(13, 56))
	working := query.BoxEntry{At: bxAt(13, 40), Kind: query.BoxStatus, Crew: "k7", Verb: "working", Text: "reading"}
	tree = bxWithBox(tree, 7, []query.BoxEntry{ask}, []query.BoxEntry{working, ask})
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	m, _ = send(t, m, key("enter"))
	m = bxFocusBox(t, m)
	if n := len(m.boxItems()); n != 1 {
		t.Fatalf("default box = %d items, want the inbox (1)", n)
	}
	m, _ = send(t, m, key("l"))
	if n := len(m.boxItems()); n != 2 || !m.boxAll {
		t.Fatalf("after l: %d items all=%v, want the log", n, m.boxAll)
	}
	if !strings.Contains(renderFrame(t, m), "all 2") {
		t.Fatalf("the box rule does not say it shows the whole log:\n%s", renderFrame(t, m))
	}
	m, _ = send(t, m, key("l"))
	if n := len(m.boxItems()); n != 1 || m.boxAll {
		t.Fatalf("second l: %d items all=%v, want the inbox again", n, m.boxAll)
	}
}

// Compact shows the top item's title and meta; focused, the selected item
// opens in full: its whole question, and where [assign] sends it.
func TestAFocusedBoxOpensTheItemInFull(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	compact := renderFrame(t, m)
	if strings.Contains(compact, "Run on the prod replica now?") {
		t.Fatalf("the unfocused box shows the whole question:\n%s", compact)
	}
	m = bxFocusBox(t, m)
	full := renderFrame(t, m)
	for _, want := range []string{"1 waiting", "Plan ready: backfill ledger_v2 in", "replica now?", "[assign] hands it to"} {
		if !strings.Contains(full, want) {
			t.Fatalf("the focused box is missing %q:\n%s", want, full)
		}
	}
	// Detail keeps only its first 8 rows, rule included (design D).
	if sl := bxSlot(t, m, slotDetail); sl.h > boxFocusDetailRows {
		t.Fatalf("detail is %d rows with the box focused, want at most %d", sl.h, boxFocusDetailRows)
	}
}

// An assigned item says what [assign] did instead of offering it again.
func TestABoxItemSaysWhetherItWasAssigned(t *testing.T) {
	at := bxAt(13, 56)
	base := bxAsk("k3", "pick A or B", at)
	queued := base
	queued.Assigned = query.BoxAssign{State: query.BoxAssignQueued, At: at}
	sent := base
	sent.Assigned = query.BoxAssign{State: query.BoxAssignSent, At: at, SentAt: at.Add(3 * time.Minute)}
	for _, tc := range []struct {
		name string
		e    query.BoxEntry
		want string
	}{
		{"not assigned", base, "[assign]"},
		{"queued", queued, "queued"},
		{"sent", sent, "sent 13:59"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := bxPayments(t, 40, 36)
			m.tree = bxWithBox(m.tree, 7, []query.BoxEntry{tc.e}, []query.BoxEntry{tc.e})
			frame := renderFrame(t, m)
			if !strings.Contains(frame, tc.want) {
				t.Fatalf("frame does not carry %q:\n%s", tc.want, frame)
			}
			if tc.e.Assigned.State != "" && strings.Contains(frame, "[assign]") {
				t.Fatalf("an assigned item still offers [assign]:\n%s", frame)
			}
		})
	}
}

// The need is named by its kind, not by the crew's words.
func TestBoxNeedNamesTheNeed(t *testing.T) {
	for _, tc := range []struct {
		e    query.BoxEntry
		want string
	}{
		{query.BoxEntry{Kind: query.BoxStatus, Verb: "needs-decision", Text: "red or blue?"}, "decide"},
		{query.BoxEntry{Kind: query.BoxStatus, Verb: "blocked"}, "blocked"},
		{query.BoxEntry{Kind: query.BoxIncident, Verb: "stale", Text: "no pane change for 4m"}, "stuck"},
		{query.BoxEntry{Kind: query.BoxIncident, Verb: "runtime_lost"}, "agent gone"},
		{query.BoxEntry{Kind: query.BoxIncident, Verb: "wedged"}, "wedged"},
		{query.BoxEntry{Kind: query.BoxIncident, Verb: "budget"}, "over budget"},
		{query.BoxEntry{Kind: query.BoxIncident, Verb: "novel"}, "novel"},
	} {
		if got := boxNeed(tc.e); got != tc.want {
			t.Errorf("boxNeed(%s %s) = %q, want %q", tc.e.Kind, tc.e.Verb, got, tc.want)
		}
	}
}

// The Mate's own incident is asked by the Mate and named by its Project.
func TestTheMatesOwnIncidentCarriesTheMateMark(t *testing.T) {
	wedged := query.BoxEntry{At: bxAt(13, 58), Kind: query.BoxIncident, Crew: "mate", Verb: "wedged",
		Text: "the composer did not take the line", Attention: true, Resolve: "resolve: mate wedged"}
	m, _, _ := bxPayments(t, 40, 36)
	m.tree = bxWithBox(m.tree, 7, []query.BoxEntry{wedged}, []query.BoxEntry{wedged})
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "👨‍💻 payments-api") || !strings.Contains(frame, "wedged") {
		t.Fatalf("the Mate's incident is not drawn as the Mate's:\n%s", frame)
	}
}
