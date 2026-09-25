package console

import (
	"strings"
	"testing"
)

// bxLines is a rendered frame's lines.
func bxLines(t *testing.T, m Model) []string {
	t.Helper()
	return strings.Split(renderFrame(t, m), "\n")
}

// bxLineWith is the first frame line containing s.
func bxLineWith(t *testing.T, m Model, s string) string {
	t.Helper()
	for _, l := range bxLines(t, m) {
		if strings.Contains(l, s) {
			return l
		}
	}
	t.Fatalf("no line contains %q:\n%s", s, renderFrame(t, m))
	return ""
}

// Selection is the one-cell marker in col 0: ▌ in the focused pane, ▏ in
// the others. Colour stripped, the marker alone carries it.
func TestSelectionIsTheMarkerAndFocusMovesItsShape(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	if l := bxLineWith(t, m, "👨‍💻 payments-api    "); !strings.HasPrefix(l, "▌ ") {
		t.Fatalf("focused list's selected row = %q, want ▌ in col 0", l)
	}
	for _, l := range bxLines(t, m) {
		if strings.Contains(l, "Add index") && !strings.HasPrefix(l, "  ") {
			t.Fatalf("an unselected row carries a marker: %q", l)
		}
	}
	m, _ = send(t, m, key("tab")) // detail
	if l := bxLineWith(t, m, "👨‍💻 payments-api    "); !strings.HasPrefix(l, "▏ ") {
		t.Fatalf("unfocused list's selected row = %q, want ▏", l)
	}
	if l := bxLineWith(t, m, "agent"); !strings.HasPrefix(l, "▌ agent") {
		t.Fatalf("detail's cursor field = %q, want ▌ on it", l)
	}
}

// In colour the selected row also sits on the sel background across the
// whole width, and only that row does.
func TestSelectionPaintsTheWholeRowAndOnlyThatRow(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	m.p = ansiPalette()
	lines := strings.Split(m.View(), "\n")
	bg := 0
	for _, l := range lines {
		if strings.Contains(l, "100m") || strings.Contains(l, ";100") {
			bg++
		}
	}
	if bg != 2 {
		t.Fatalf("%d lines carry the selection background, want the Mate row's two", bg)
	}
}

// Focus is the tinted title: the focused pane's rule title is acc and
// bold, the others dim. Only one pane is tinted at a time.
func TestFocusIsTheTintedTitle(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	m.p = ansiPalette()
	for _, f := range []pane{paneList, paneDetail, paneBox} {
		m = m.setFocus(f)
		p := m.plan()
		tinted := 0
		for _, sl := range p.slots {
			rule := m.paneRule(sl.kind, p.w)
			acc := false
			for _, s := range rule.segs {
				if s.t == tAcc {
					acc = true
				}
			}
			if acc {
				tinted++
				if bxSlotPane(sl.kind) != f {
					t.Errorf("focus %v: the %v rule is tinted", f, sl.kind)
				}
			}
		}
		if tinted != 1 {
			t.Errorf("focus %v: %d tinted titles, want 1", f, tinted)
		}
	}
}

func bxSlotPane(k slotKind) pane {
	switch k {
	case slotDetail:
		return paneDetail
	case slotBox:
		return paneBox
	}
	return paneList
}

// Status is a word, tinted only when it needs somebody.
func TestStatusIsAWordTintedOnlyWhenItNeedsSomebody(t *testing.T) {
	for word, want := range map[string]tok{
		"running": tDim, "working": tDim, "stopped": tDim, "created": tDim, "spawned": tDim,
		"needs-decision": tAmber, "wait-mate": tAmber, "blocked": tAmber, "unknown": tAmber, "no mate": tAmber,
		"failed": tRed, "finished": tGreen,
	} {
		if got := statusTok(word); got != want {
			t.Errorf("statusTok(%q) = %d, want %d", word, got, want)
		}
	}
	m, _, _ := bxPayments(t, 40, 36)
	frame := renderFrame(t, m)
	for _, w := range []string{"running", "working", "needs-decision"} {
		if !strings.Contains(frame, w) {
			t.Errorf("the status word %q is not on the frame", w)
		}
	}
}

// The one-line status column is at most seven cells.
func TestShortStatusFitsItsColumn(t *testing.T) {
	for word, want := range map[string]string{
		"needs-decision": "decide", "wait-mate": "review", "finished": "done",
		"starting": "start", "stopping": "stop", "running": "running", "unknown": "unknown",
	} {
		got := shortStatus(word)
		if got != want || cells(got) > shortStatusWidth {
			t.Errorf("shortStatus(%q) = %q, want %q within %d cells", word, got, want, shortStatusWidth)
		}
	}
}

// Attention is written out: ! plus a count, red when one of them failed,
// amber otherwise; "no mate" and "unknown" are words, not colour.
func TestAttentionIsWrittenOut(t *testing.T) {
	m := newFixture(t, designTree(), 40, 36, unicodeGlyphs)
	for _, tc := range []struct{ row, want string }{
		{"docs-site", "!1"}, {"payments-api", "!2"}, {"search-indexer", "!1"},
	} {
		if l := bxLineWith(t, m, tc.row); !strings.Contains(l, tc.want) {
			t.Errorf("%s row = %q, want %s", tc.row, l, tc.want)
		}
	}
	if l := bxLineWith(t, m, "─ acme"); !strings.Contains(l, "!4") {
		t.Errorf("workspace rule = %q, want the workspace-wide !4", l)
	}
	if !strings.Contains(renderFrame(t, m), "no mate · 🤖 1") {
		t.Errorf("docs-site does not say no mate in words:\n%s", renderFrame(t, m))
	}
	p := designPayments()
	if _, failed := projectAttention(p); !failed || bangTok(failed) != tRed {
		t.Error("payments-api has a failed crew; its !N must be red")
	}
	if n, failed := projectAttention(designTree().Projects[3]); n != 1 || failed || bangTok(failed) != tAmber {
		t.Errorf("docs-site attention = %d failed=%v, want an amber !1", n, failed)
	}
}
