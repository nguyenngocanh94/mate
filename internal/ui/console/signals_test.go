package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// TestSelectionFocusAndStatusAreThreeDistinctDrawings is the rule the
// design states outright: the three signals never share a drawing method.
// The test asserts it the only way that matters - with colour stripped,
// because a reader on a monochrome terminal must still see all three.
func TestSelectionFocusAndStatusAreThreeDistinctDrawings(t *testing.T) {
	p := plainPalette()
	for _, g := range []glyphSet{unicodeGlyphs, asciiGlyphs} {
		t.Run(g.Name, func(t *testing.T) {
			selectedFocused := markerSpan(true, true, g, p).text
			selectedUnfocused := markerSpan(true, false, g, p).text
			unselected := markerSpan(false, true, g, p).text
			header := markerSpan(false, false, g, p).text

			if selectedFocused == selectedUnfocused {
				t.Errorf("a selected row looks the same in a focused and an unfocused pane: %q", selectedFocused)
			}
			if selectedFocused == unselected || selectedUnfocused == unselected {
				t.Errorf("selection is indistinguishable from no selection: %q / %q / %q", selectedFocused, selectedUnfocused, unselected)
			}
			if strings.TrimSpace(unselected) != "" || strings.TrimSpace(header) != "" {
				t.Errorf("an unselected row and a column header must leave the marker cell blank: %q / %q", unselected, header)
			}
			// A column header is dim text on the same grid, and is never
			// selectable - so it is drawn by its own constructor rather than
			// by reusing a row's.
			if got := columnHeaderSpan("STATUS", p); got.text != "STATUS" {
				t.Errorf("columnHeaderSpan text = %q, want the header word itself", got.text)
			}
			for name, marker := range map[string]string{"selected+focused": selectedFocused, "selected+unfocused": selectedUnfocused, "unselected": unselected} {
				if n := cells(marker); n != 1 {
					t.Errorf("%s marker %q is %d cells, want exactly 1", name, marker, n)
				}
			}
			// The marker plus its separating cell is the same width on every
			// row, so columns line up whatever the selection does.
			for _, sel := range []bool{true, false} {
				for _, focused := range []bool{true, false} {
					l := newLine().addSpans(rowPrefix(sel, focused, g, p)...)
					if got := l.width(); got != 2 {
						t.Errorf("rowPrefix(sel=%v, focused=%v) is %d cells, want 2", sel, focused, got)
					}
				}
			}
		})
	}
}

// TestSelectionDoesNotRestyleTheRowsOwnText: a failed row that happens to
// be selected must still read as failed. The design puts selection in the
// background and the marker precisely so status keeps its own colour.
func TestSelectionDoesNotRestyleTheRowsOwnText(t *testing.T) {
	p := ansiPalette()
	status := statusSpan("failed", p)
	unselected := newLine().addSpan(status).render(10)
	selected := selectRow(newLine(), true, p).addSpan(status).render(10)
	if unselected == selected {
		t.Fatalf("a selected row is byte-identical to an unselected one")
	}
	if !strings.Contains(unselected, "failed") || !strings.Contains(selected, "failed") {
		t.Fatalf("the status word did not survive selection: %q / %q", unselected, selected)
	}
	// The status keeps its own foreground on a selected row: two rows with
	// the same text and different status hues still differ once selected.
	// (Comparing against p.Red.Render(...) directly would not work - the
	// selection background merges into the same SGR sequence.)
	selRed := selectRow(newLine(), true, p).add("word", p.Red).render(10)
	selPlain := selectRow(newLine(), true, p).add("word", p.Fg).render(10)
	if selRed == selPlain {
		t.Fatalf("selection flattened the status colour: both rendered %q", selRed)
	}
	// And the selection background reaches every blank cell, not just the
	// glyphs, so the highlight covers the whole row width. Both the padding
	// at the end and a gap in the middle: the two go through different code
	// paths, and a gap that loses the background leaves a stripe across the
	// selected row.
	if strings.HasSuffix(selRed, "    ") {
		t.Fatalf("the selected row's trailing padding carries no background: %q", selRed)
	}
	gapped := selectRow(newLine(), true, p).add("a", p.Fg).pad(3).add("b", p.Fg).render(10)
	if strings.Contains(gapped, "   ") && !strings.Contains(gapped, "\x1b") {
		t.Fatalf("the selected row's interior gap carries no background: %q", gapped)
	}
	if unstyledGap := "a" + strings.Repeat(" ", 3) + "b"; strings.Contains(gapped, unstyledGap) {
		t.Fatalf("the selected row's interior gap was emitted unstyled: %q", gapped)
	}
}

// TestPaneTitleCarriesFocusAsWellAsTheMarker: the marker is one cell and
// easy to miss, so the focused pane's title is accent too - and exactly one
// pane is accent at a time.
func TestPaneTitleCarriesFocus(t *testing.T) {
	p := ansiPalette()
	focused := newLine().addSpan(paneTitleSpan("CREW", true, p)).render(8)
	unfocused := newLine().addSpan(paneTitleSpan("CREW", false, p)).render(8)
	if focused == unfocused {
		t.Fatalf("a focused and an unfocused pane title render identically: %q", focused)
	}
}

// TestOnlyOnePaneIsAccentAtATime: focus is which pane the keys move in, so
// two accent panes would be a lie about where the next keystroke lands.
func TestOnlyOnePaneIsAccentAtATime(t *testing.T) {
	m := newFixture(t, sampleTree(), 160, 48, unicodeGlyphs)
	m.p = ansiPalette()
	for _, focus := range []pane{paneList, paneInspector} {
		m.focus = focus
		l := layout(m.w, m.h)
		listTitle := m.listLines(l.List, l.Body)[0].render(l.List)
		inspTitle := m.inspectorLines(l.Inspector, l.valueWidth(), l.Body, focus == paneInspector)[0].render(l.Inspector)
		accent := m.p.Acc.Render("")
		_ = accent
		listAccent := strings.Contains(listTitle, m.p.Acc.Render("PROJECTS")[:5])
		inspAccent := strings.Contains(inspTitle, m.p.Acc.Render("PROJECT")[:5])
		if listAccent == inspAccent {
			t.Fatalf("focus=%v: list accent=%v inspector accent=%v, want exactly one", focus, listAccent, inspAccent)
		}
	}
}

// TestStatusIsAlwaysTheDomainWordNeverColourAlone. Every recorded status
// prints its own value - "awaiting_review", not "Review" - and every status
// remains distinguishable from every other with colour stripped.
func TestStatusIsAlwaysTheDomainWordNeverColourAlone(t *testing.T) {
	plain := plainPalette()
	statuses := []string{
		string(query.CrewReserved), string(query.CrewPreparing), string(query.CrewRunning),
		string(query.CrewAwaitingReview), string(query.CrewSucceeded), string(query.CrewFailed),
		string(query.CrewBlocked), string(query.CrewNeedsRebase), string(query.CrewNeedsRepair),
	}
	seen := map[string]string{}
	for _, s := range statuses {
		got := statusSpan(s, plain)
		if got.text != s {
			t.Errorf("statusSpan(%q).text = %q, want the domain word itself", s, got.text)
		}
		if prev, dup := seen[got.text]; dup {
			t.Errorf("statuses %q and %q render the same text %q", prev, s, got.text)
		}
		seen[got.text] = s
	}
	// running is not green, and not a dot: it is a recorded status, not
	// proof that an agent is alive.
	coloured := ansiPalette()
	if statusStyle("running", coloured).GetForeground() == coloured.Green.GetForeground() {
		t.Errorf("running is drawn green; the design reserves green for succeeded")
	}
	if statusStyle("awaiting_review", coloured).GetForeground() != coloured.Amber.GetForeground() {
		t.Errorf("awaiting_review is not amber")
	}
	if statusStyle("failed", coloured).GetForeground() != coloured.Red.GetForeground() {
		t.Errorf("failed is not red")
	}
	if statusStyle("succeeded", coloured).GetForeground() != coloured.Green.GetForeground() {
		t.Errorf("succeeded is not green")
	}
}

// TestAttentionAndUnknownCarryTheirOwnLabels: "!" for attention and "?" for
// unknown are labels, not icons, and they are what makes the signal survive
// a monochrome terminal.
func TestAttentionAndUnknownCarryTheirOwnLabels(t *testing.T) {
	p := plainPalette()
	if got := attentionSpan("review", p).text; got != "! review" {
		t.Errorf("attentionSpan = %q, want %q", got, "! review")
	}
	if got := failureSpan("failed", p).text; got != "! failed" {
		t.Errorf("failureSpan = %q, want %q", got, "! failed")
	}
	if got := unknownMarkSpan("binding", p).text; got != "? binding" {
		t.Errorf("unknownMarkSpan = %q, want %q", got, "? binding")
	}
	coloured := ansiPalette()
	if attentionSpan("rebase", coloured).style.GetForeground() == failureSpan("rebase", coloured).style.GetForeground() {
		t.Errorf("attention and failure share a colour")
	}
}

// TestTheAmberAndRedVocabulariesAreDisjoint is what actually keeps
// attention and failure from being a colour-only distinction. Both are
// drawn "! <word>", so the guarantee cannot live in the span constructors:
// it lives in the vocabulary, where no status word is ever both. A reader
// without colour sees "! failed" against "! review" - two words, not two
// shades of the same one.
func TestTheAmberAndRedVocabulariesAreDisjoint(t *testing.T) {
	coloured := ansiPalette()
	amber, red := map[string]bool{}, map[string]bool{}
	for _, word := range []string{
		string(query.CrewReserved), string(query.CrewPreparing), string(query.CrewRunning),
		string(query.CrewAwaitingReview), string(query.CrewSucceeded), string(query.CrewFailed),
		string(query.CrewBlocked), string(query.CrewNeedsRebase), string(query.CrewNeedsRepair),
		string(query.MateCreated), string(query.MateStarting), string(query.MateStopping),
		string(query.MateStopped), string(query.MateUnknown),
		string(query.BindingReserved), string(query.BindingActive), string(query.BindingStale),
		"none", "unknown", "missing", "present (clean)", "present (dirty)",
	} {
		switch statusStyle(word, coloured).GetForeground() {
		case coloured.Amber.GetForeground():
			amber[word] = true
		case coloured.Red.GetForeground():
			red[word] = true
		}
	}
	if len(amber) == 0 || len(red) == 0 {
		t.Fatalf("expected both vocabularies to be non-empty: amber=%v red=%v", amber, red)
	}
	for word := range amber {
		if red[word] {
			t.Errorf("%q is drawn both amber and red, so its state is carried by colour alone", word)
		}
	}
	// The design's token table names these two explicitly: a stale binding
	// is amber, a missing worktree is red. Listing them above is not enough
	// to catch either falling through to plain fg - only asserting their
	// bucket membership is.
	if !amber[string(query.BindingStale)] {
		t.Errorf("%q is not in the amber vocabulary", query.BindingStale)
	}
	if !red["missing"] {
		t.Errorf("%q is not in the red vocabulary", "missing")
	}
}

// TestKnownAbsentAndUnknownAreThreeDifferentWords: Unknown means the read
// failed and must never render as Absent or as an empty cell, and a Known
// empty value must never collapse into either.
func TestKnownAbsentAndUnknownAreThreeDifferentWords(t *testing.T) {
	p := plainPalette()
	g := unicodeGlyphs
	text := func(spans []span) string {
		var b strings.Builder
		for _, s := range spans {
			b.WriteString(s.text)
		}
		return b.String()
	}
	known := text(availabilitySpans(query.Known, "/repos/x/.worktrees/c1", "", p.Fg, g, p))
	knownEmpty := text(availabilitySpans(query.Known, "", "", p.Fg, g, p))
	absent := text(availabilitySpans(query.Absent, "", "no worktree row", p.Dim, g, p))
	unknown := text(availabilitySpans(query.Unknown, "", "lookup timed out (2s)", p.Dim, g, p))

	if known != "/repos/x/.worktrees/c1" {
		t.Errorf("Known rendered %q, want the value itself", known)
	}
	for _, pair := range [][2]string{{knownEmpty, absent}, {knownEmpty, unknown}, {absent, unknown}} {
		if pair[0] == pair[1] {
			t.Errorf("two different availabilities render identically: %q", pair[0])
		}
	}
	if !strings.HasPrefix(absent, "none") {
		t.Errorf("Absent rendered %q, want it to start with %q", absent, "none")
	}
	if !strings.HasPrefix(unknown, "unknown") {
		t.Errorf("Unknown rendered %q, want it to start with %q", unknown, "unknown")
	}
	if !strings.Contains(unknown, "lookup timed out (2s)") {
		t.Errorf("Unknown rendered %q, want it to carry why the read failed", unknown)
	}
	if strings.TrimSpace(knownEmpty) == "" {
		t.Errorf("a Known empty value rendered as blank, which is how Absent would look")
	}
	// And Unknown is the only one that is amber.
	coloured := ansiPalette()
	unknownSpans := availabilitySpans(query.Unknown, "", "", coloured.Dim, g, coloured)
	if unknownSpans[0].style.GetForeground() != coloured.Amber.GetForeground() {
		t.Errorf("Unknown is not amber")
	}
	absentSpans := availabilitySpans(query.Absent, "", "", coloured.Dim, g, coloured)
	if absentSpans[0].style.GetForeground() == coloured.Amber.GetForeground() {
		t.Errorf("Absent is amber; it must be dim, or it reads as a failure")
	}
}
