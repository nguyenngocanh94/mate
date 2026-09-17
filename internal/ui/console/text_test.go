package console

import (
	"fmt"
	"strings"
	"testing"
)

func TestShortIDKeepsThePrefixAndBothEnds(t *testing.T) {
	cases := []struct {
		name, in, want string
		g              glyphSet
	}{
		{name: "crew id", in: "crew_01J9P6Q6W0E5V8XK2M4B8DT", want: "crew_01J9P6…B8DT", g: unicodeGlyphs},
		{name: "crew id ascii", in: "crew_01J9P6Q6W0E5V8XK2M4B8DT", want: "crew_01J9P6..B8DT", g: asciiGlyphs},
		{name: "task id", in: "task_01J9P2B4C6D8E0F2G4H6J8K0LM", want: "task_01J9P2…K0LM", g: unicodeGlyphs},
		{name: "no prefix", in: "01J9P6Q6W0E5V8XK2M4B8DT", want: "01J9P6…B8DT", g: unicodeGlyphs},
		{name: "already short", in: "crew_01J9", want: "crew_01J9", g: unicodeGlyphs},
		{name: "exactly at the limit", in: "crew_01234567890", want: "crew_01234567890", g: unicodeGlyphs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortID(tc.in, tc.g); got != tc.want {
				t.Fatalf("shortID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestShortIDKeepsTwoIDsDistinguishable is the whole point of abbreviating
// head *and* tail. mate's ids (internal/domain/id.go: prefix_ plus 16 hex
// characters from crypto/rand, not a ULID) carry no timestamp or other
// structure in the head, so two crews of the same task can share a long
// run of matching characters at either end purely by chance; a head-only
// abbreviation would print them identically and invite the reader to act on
// the wrong attempt. Keeping both ends is collision-resistant, not a
// guarantee - see shortID's own doc comment.
func TestShortIDKeepsTwoIDsDistinguishable(t *testing.T) {
	for _, g := range []glyphSet{unicodeGlyphs, asciiGlyphs} {
		t.Run(g.Name, func(t *testing.T) {
			// Same head, different tail: only the tail can save this.
			tailOnly := []string{
				"crew_01J9P6Q6W0E5V8XK2M4B8DT",
				"crew_01J9P6Q6W0E5V8XK2M4C9FX",
			}
			if a, b := shortID(tailOnly[0], g), shortID(tailOnly[1], g); a == b {
				t.Fatalf("two ids differing only in the tail both abbreviate to %q", a)
			}
			// Same tail, different head: only the head can save this.
			headOnly := []string{
				"crew_01J9P6Q6W0E5V8XK2M4B8DT",
				"crew_01J9Q0Z1Y2X3W4V5U6T7B8DT",
			}
			if a, b := shortID(headOnly[0], g), shortID(headOnly[1], g); a == b {
				t.Fatalf("two ids differing only in the head both abbreviate to %q", a)
			}
			// Different kinds of thing never collide, whatever the body.
			if a, b := shortID("crew_01J9P6Q6W0E5V8XK2M4B8DT", g), shortID("mate_01J9P6Q6W0E5V8XK2M4B8DT", g); a == b {
				t.Fatalf("a crew and a mate id both abbreviate to %q", a)
			}
		})
	}
}

func TestTruncateEndMarksTheCutAndNeverExceedsTheWidth(t *testing.T) {
	const title = "Fix webhook idempotency so retried deliveries do not double-charge"
	cases := []struct {
		name string
		in   string
		w    int
		want string
	}{
		{name: "fits", in: "short title", w: 20, want: "short title"},
		{name: "exact fit", in: "12345", w: 5, want: "12345"},
		{name: "cut with ellipsis", in: title, w: 20, want: "Fix webhook idempot…"},
		{name: "no room for a marker", in: title, w: 1, want: "F"},
		{name: "zero width", in: title, w: 0, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateEnd(tc.in, tc.w, unicodeGlyphs)
			if got != tc.want {
				t.Fatalf("truncateEnd(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
			}
			if cells(got) > tc.w {
				t.Fatalf("truncateEnd(%q, %d) = %q, which is %d cells", tc.in, tc.w, got, cells(got))
			}
		})
	}
}

// TestCellsWidthCorpus pins the load-bearing display-width primitive against
// precomposed and decomposed Vietnamese, CJK, ZWJ and skin-tone graphemes.
func TestCellsWidthCorpus(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"Vietnamese precomposed", "ế", 1},
		{"Vietnamese decomposed", "e\u0302\u0303", 1},
		{"CJK", "中", 2},
		{"ZWJ emoji", "👨‍👩‍👧‍👦", 2},
		{"skin-tone emoji", "👍🏽", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cells(tc.in); got != tc.want {
				t.Fatalf("cells(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestTruncateEndCountsCellsNotRunes: a title from the database is
// arbitrary text. A two-cell rune counted as one would shift every column
// to its right by one cell per rune.
func TestTruncateEndCountsCellsNotRunes(t *testing.T) {
	const wide = "支払いのべき等性を修正する" // 13 runes, 26 cells
	if cells(wide) != 26 {
		t.Fatalf("sample is %d cells, want 26 - fix the sample", cells(wide))
	}
	got := truncateEnd(wide, 10, unicodeGlyphs)
	if cells(got) > 10 {
		t.Fatalf("truncateEnd(wide, 10) = %q, which is %d cells", got, cells(got))
	}
	if !strings.HasSuffix(got, unicodeGlyphs.Ellipsis) {
		t.Fatalf("truncateEnd(wide, 10) = %q, want it to mark the cut", got)
	}
}

// TestCutCellsNeverSplitsAGraphemeCluster is F3: cutCells once summed each
// rune's own width, which disagrees with cells' measurement of a cluster
// whose runes are not each independently as wide as the cluster is
// together - a base rune plus a variation selector, a ZWJ family, a flag
// pair of regional indicators - and either split the cluster mid-way or
// mismeasured it entirely.
func TestCutCellsNeverSplitsAGraphemeCluster(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"variation selector", "‼️tail"},
		{"zwj family", "👨‍👩‍👧‍👦tail"},
		{"regional indicator flag", "🇻🇳tail"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clusters := graphemeClusters(tc.in)
			first := clusters[0]
			fw := cells(first)
			if fw < 2 {
				t.Fatalf("sample cluster %q is %d cells, want a wide (>=2 cell) cluster to make this test meaningful", first, fw)
			}
			for w := 0; w < fw; w++ {
				if got := cutCells(tc.in, w); got != "" {
					t.Fatalf("cutCells(%q, %d) = %q, want empty: the leading cluster does not fit at this width and must not be split", tc.in, w, got)
				}
			}
			if got := cutCells(tc.in, fw); got != first {
				t.Fatalf("cutCells(%q, %d) = %q, want the leading cluster %q whole", tc.in, fw, got, first)
			}
		})
	}
}

// TestWrapAfterSlashMeasuresGraphemeClustersNotRunes reproduces the
// counter-review's exact failure: summing per-rune widths under-counts a
// run of variation-selector emoji, so a wrapped line ends up wider than the
// budget it was asked for - which is how an inspector value that "never
// truncates" silently overflowed its column and lost the text after it.
func TestWrapAfterSlashMeasuresGraphemeClustersNotRunes(t *testing.T) {
	in := strings.Repeat("⚠️ ", 10) + "webhook idempotency END"
	for _, w := range []int{8, 31} {
		t.Run(fmt.Sprintf("width %d", w), func(t *testing.T) {
			lines := wrapAfterSlash(in, w)
			for i, l := range lines {
				if cells(l) > w {
					t.Fatalf("line %d is %d cells, want at most %d: %q", i, cells(l), w, l)
				}
			}
			if !containsWhole(lines, "END") {
				t.Fatalf("wrapping lost the tail of the value: %q", lines)
			}
		})
	}
}

// TestWrapAfterSlashKeepsPathSuffixesAndIDsIntact is the inspector's rule:
// a path is never truncated, so it must break somewhere harmless. Breaking
// after '/' keeps the segment that distinguishes two attempts - "/a1"
// against "/a2" - readable on one line.
func TestWrapAfterSlashKeepsPathSuffixesAndIDsIntact(t *testing.T) {
	const a1 = "/Users/dev/work/acme/repos/payments-api/.worktrees/crew_01J9P4Q5R6S7T8U9V0W1X2A7CS/a1"
	const a2 = "/Users/dev/work/acme/repos/payments-api/.worktrees/crew_01J9P4Q5R6S7T8U9V0W1X2A7CS/a2"
	for _, w := range []int{31, 41} {
		t.Run("width "+string(rune('0'+w/10))+string(rune('0'+w%10)), func(t *testing.T) {
			for _, p := range []string{a1, a2} {
				lines := wrapAfterSlash(p, w)
				if joined := strings.Join(lines, ""); joined != p {
					t.Fatalf("wrapping lost or added characters:\n in  %q\n out %q", p, joined)
				}
				for i, l := range lines {
					if cells(l) > w {
						t.Fatalf("line %d is %d cells, want at most %d: %q", i, cells(l), w, l)
					}
				}
				if !containsWhole(lines, "crew_01J9P4Q5R6S7T8U9V0W1X2A7CS") {
					t.Fatalf("the crew id was split across lines: %q", lines)
				}
				if !containsWhole(lines, p[len(p)-3:]) {
					t.Fatalf("the %q suffix was split across lines: %q", p[len(p)-3:], lines)
				}
			}
			// And the two attempts stay distinguishable after wrapping.
			if strings.Join(wrapAfterSlash(a1, w), "\n") == strings.Join(wrapAfterSlash(a2, w), "\n") {
				t.Fatalf("/a1 and /a2 wrapped to the same thing at width %d", w)
			}
		})
	}
}

func TestWrapAfterSlashBreaksAtSpacesAndDropsThem(t *testing.T) {
	got := wrapAfterSlash("harness exited 1 before reporting anything at all", 20)
	for i, l := range got {
		if cells(l) > 20 {
			t.Fatalf("line %d is %d cells: %q", i, cells(l), l)
		}
		if strings.HasPrefix(l, " ") || strings.HasSuffix(l, " ") {
			t.Fatalf("line %d has an edge space: %q", i, l)
		}
	}
	if len(got) < 2 {
		t.Fatalf("expected the sentence to wrap, got %q", got)
	}
}

func containsWhole(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// TestDropPriorityDropsWholeItemsInOrder: a note column that runs out of
// room drops whole items, never half a word, and never reorders - the first
// item that does not fit ends the line, so a short low-priority item cannot
// take the place of the high-priority one that was dropped.
func TestDropPriorityDropsWholeItemsInOrder(t *testing.T) {
	items := []string{"! stale binding", "! failed", "retry of #1"}
	const sep = " · "
	cases := []struct {
		w    int
		want []string
	}{
		{w: 80, want: items},
		{w: 27, want: []string{"! stale binding", "! failed"}},
		{w: 26, want: []string{"! stale binding", "! failed"}}, // 15 + 3 + 8, exactly
		{w: 25, want: []string{"! stale binding"}},
		{w: 15, want: []string{"! stale binding"}},
		{w: 14, want: []string{}},
		{w: 0, want: []string{}},
	}
	for _, tc := range cases {
		got := dropPriority(items, tc.w, sep)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Fatalf("dropPriority(w=%d) = %q, want %q", tc.w, got, tc.want)
		}
		if joined := joinDropped(items, tc.w, sep); cells(joined) > tc.w {
			t.Fatalf("joinDropped(w=%d) = %q, which is %d cells", tc.w, joined, cells(joined))
		}
	}
}

// TestDropPriorityNeverPromotesALaterItem: the ordering is by urgency, so
// dropping must stop at the first item that does not fit rather than
// scanning on for something smaller.
func TestDropPriorityNeverPromotesALaterItem(t *testing.T) {
	got := dropPriority([]string{"a very long high priority item", "tiny"}, 10, " · ")
	if len(got) != 0 {
		t.Fatalf("dropPriority = %q, want nothing: the smaller item must not jump the queue", got)
	}
}
