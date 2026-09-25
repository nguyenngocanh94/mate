package main

import (
	"testing"

	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

func TestParseCursorColumn(t *testing.T) {
	for in, want := range map[string]int{
		"\x1b[12;3R":          3,
		"junk\x1b[1;5R":       5,
		"\x1b[2;1R\x1b[40;9R": 9,
	} {
		if got, ok := parseCursorColumn([]byte(in)); !ok || got != want {
			t.Errorf("parseCursorColumn(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "\x1b[12;3", "\x1b[R", "\x1b[a;bR", "\x1b[3R", "\x1b[1;0R"} {
		if _, ok := parseCursorColumn([]byte(in)); ok {
			t.Errorf("parseCursorColumn(%q) parsed; want no column", in)
		}
	}
}

// The emoji width rule: emoji when 👨‍💻 is two cells, ◆ ◇ when it is not.
// A terminal that does not answer keeps the emoji. One measurement only,
// so the window in which a startup key can be lost stays one round trip.
func TestChooseKindGlyphs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cells map[string]int
		want  console.KindGlyphs
	}{
		{"clusters", map[string]int{"👨‍💻": 2, "◆": 1}, console.KindEmoji},
		{"tmux draws two emoji", map[string]int{"👨‍💻": 4, "◆": 1}, console.KindSymbol},
		{"no answer", map[string]int{}, console.KindEmoji},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got := chooseKindGlyphs(func(s string) (int, bool) {
				calls++
				n, ok := tc.cells[s]
				return n, ok
			})
			if got != tc.want {
				t.Fatalf("chooseKindGlyphs = %v, want %v", got, tc.want)
			}
			if calls != 1 {
				t.Fatalf("measured %d times, want one round trip", calls)
			}
		})
	}
}

func TestMateKindsOverridesTheProbe(t *testing.T) {
	for v, want := range map[string]console.KindGlyphs{
		"emoji": console.KindEmoji, "symbol": console.KindSymbol, "ASCII": console.KindASCII,
	} {
		if got := probeKindGlyphs(func(k string) string {
			if k == "MATE_KINDS" {
				return v
			}
			return ""
		}); got != want {
			t.Errorf("MATE_KINDS=%s: %v, want %v", v, got, want)
		}
	}
}
