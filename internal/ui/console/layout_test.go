package console

import "testing"

// TestLayoutMatchesTheDesignBreakpoints pins all four breakpoints at the
// sizes the design names, plus each boundary column. The inspector widths
// are not free numbers: 50 is chosen so the value column is 31 cells -
// comfortable headroom over any id mate actually generates (prefix_ plus 16
// hex characters, internal/domain/id.go) - so an id never splits at the
// narrower breakpoint.
func TestLayoutMatchesTheDesignBreakpoints(t *testing.T) {
	cases := []struct {
		name                                  string
		w, h                                  int
		insp, list, body, value               int
		tooSmall                              bool
		wantSplitWhenReady, wantDetailOverlay bool
	}{
		{name: "wide 160x48", w: 160, h: 48, insp: 60, list: 99, body: 42, value: 41, wantSplitWhenReady: true},
		{name: "medium 120x36", w: 120, h: 36, insp: 50, list: 69, body: 30, value: 31, wantSplitWhenReady: true},
		{name: "single region 80x24", w: 80, h: 24, insp: 0, list: 80, body: 18, value: 0, wantDetailOverlay: true},
		{name: "too small 56x14", w: 56, h: 14, insp: 0, list: 56, body: 8, value: 0, tooSmall: true},

		{name: "wide boundary 140", w: 140, h: 40, insp: 60, list: 79, body: 34, value: 41, wantSplitWhenReady: true},
		{name: "just under wide 139", w: 139, h: 40, insp: 50, list: 88, body: 34, value: 31, wantSplitWhenReady: true},
		{name: "inspector boundary 100", w: 100, h: 40, insp: 50, list: 49, body: 34, value: 31, wantSplitWhenReady: true},
		{name: "just under inspector 99", w: 99, h: 40, insp: 0, list: 99, body: 34, value: 0, wantDetailOverlay: true},
		{name: "minimum usable 60x16", w: 60, h: 16, insp: 0, list: 60, body: 10, value: 0, wantDetailOverlay: true},
		{name: "one column short 59x16", w: 59, h: 16, insp: 0, list: 59, body: 10, value: 0, tooSmall: true},
		{name: "one row short 60x15", w: 60, h: 15, insp: 0, list: 60, body: 9, value: 0, tooSmall: true},
		{name: "wide but short 160x15", w: 160, h: 15, insp: 60, list: 99, body: 9, value: 41, tooSmall: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := layout(tc.w, tc.h)
			if l.Inspector != tc.insp {
				t.Errorf("Inspector = %d, want %d", l.Inspector, tc.insp)
			}
			if l.List != tc.list {
				t.Errorf("List = %d, want %d", l.List, tc.list)
			}
			if l.Body != tc.body {
				t.Errorf("Body = %d, want %d", l.Body, tc.body)
			}
			if l.TooSmall != tc.tooSmall {
				t.Errorf("TooSmall = %v, want %v", l.TooSmall, tc.tooSmall)
			}
			if got := l.valueWidth(); got != tc.value {
				t.Errorf("valueWidth = %d, want %d", got, tc.value)
			}
			if got := l.split(true); got != tc.wantSplitWhenReady {
				t.Errorf("split(true) = %v, want %v", got, tc.wantSplitWhenReady)
			}
			if tc.wantDetailOverlay && l.Inspector != 0 {
				t.Errorf("Inspector = %d, want 0 so Tab opens Detail over the main region", l.Inspector)
			}
			// The frame is always the main region plus its six lines of
			// chrome, whatever the width does.
			if l.Body+chromeRows != tc.h {
				t.Errorf("Body+chrome = %d, want h = %d", l.Body+chromeRows, tc.h)
			}
			// The panes plus the one divider column fill the frame exactly.
			if l.Inspector > 0 && l.List+1+l.Inspector != tc.w {
				t.Errorf("list %d + divider + inspector %d = %d, want w = %d", l.List, l.Inspector, l.List+1+l.Inspector, tc.w)
			}
		})
	}
}

// TestNarrowInspectorValueColumnHoldsAPrefixedULIDWhole is the reason the
// narrow inspector is 50 cells and not, say, 48: an ID that wraps is an ID
// a reader has to reassemble by eye before they can paste it into a
// command. The 26-character body below is a stress case, not a real mate
// id - mate generates prefix_ plus 16 hex characters (internal/domain/
// id.go), 21 cells for "crew_" - kept because it proves the column holds
// even an id longer than any mate produces today.
func TestNarrowInspectorValueColumnHoldsAPrefixedULIDWhole(t *testing.T) {
	const id = "crew_01ARZ3NDEKTSV4RRFFQ69G5FAV" // stress case: "crew_" + a 26-character ULID-shaped body, not a real mate id
	if got := cells(id); got != 31 {
		t.Fatalf("sample id is %d cells, want 31 - fix the sample, not the layout", got)
	}
	l := layout(120, 36)
	if l.valueWidth() != 31 {
		t.Fatalf("valueWidth at 120 cols = %d, want 31", l.valueWidth())
	}
	if lines := wrapAfterSlash(id, l.valueWidth()); len(lines) != 1 || lines[0] != id {
		t.Fatalf("id wrapped in the narrow inspector: %q", lines)
	}
}

// TestPageStepStaysPositiveOnATinyBody keeps PgUp/PgDn from becoming a
// no-op (or a backwards jump) on a short terminal.
func TestPageStepStaysPositiveOnATinyBody(t *testing.T) {
	for _, h := range []int{16, 20, 24, 48} {
		if got := layout(80, h).page(); got < 1 {
			t.Fatalf("page() at h=%d is %d, want at least 1", h, got)
		}
	}
	if got := layout(80, 7).page(); got != 1 {
		t.Fatalf("page() with a 1-line body = %d, want 1", got)
	}
}
