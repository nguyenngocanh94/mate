package runtime

import "testing"

func TestPickWorkspaceIDPrefersOccupiedThenLowestNumber(t *testing.T) {
	t.Parallel()
	ids := []string{"w10", "w2", "w1"}
	if got := pickWorkspaceID(ids, nil); got != "w1" {
		t.Fatalf("empty extras: got %q, want w1", got)
	}
	if got := pickWorkspaceID(ids, map[string]bool{"w10": true}); got != "w10" {
		t.Fatalf("occupied extra wins: got %q, want w10", got)
	}
	if got := pickWorkspaceID(ids, map[string]bool{"w2": true, "w10": true}); got != "w2" {
		t.Fatalf("lowest occupied: got %q, want w2", got)
	}
}

// Live Herdr 0.8.2 numbers workspaces w1..w9 then wA, wB, ... wH, wJ, wK, wM
// (Crockford base32) and is not monotone: after w1Z a lab session produced w10
// and then w21. Two processes racing on such a session must still pick the
// same workspace from the same set. The decimal reading did not order these
// ids at all; it fell back to byte order, which is not stable across widths.
func TestPickWorkspaceIDOrdersLiveHerdrIDsPastNine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ids  []string
		want string
	}{
		{"a base32 digit sorts before a letter", []string{"wA", "w9"}, "w9"},
		{"letters sort among themselves", []string{"wC", "wA", "wB"}, "wA"},
		{"a shorter suffix sorts first", []string{"w10", "wA"}, "wA"},
		{"equal widths sort by bytes", []string{"w11", "w10"}, "w10"},
		{"order of the input does not matter", []string{"w9", "wA"}, "w9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := pickWorkspaceID(tc.ids, nil); got != tc.want {
				t.Fatalf("pick(%v) = %q, want %q", tc.ids, got, tc.want)
			}
		})
	}
}

func TestWorkspaceIDLessIsATotalOrder(t *testing.T) {
	t.Parallel()
	ids := []string{"w1", "w9", "wA", "wJ", "w10", "w11", "", "weird"}
	for _, a := range ids {
		if workspaceIDLess(a, a) {
			t.Fatalf("%q < %q must be false", a, a)
		}
		for _, b := range ids {
			if a == b {
				continue
			}
			if workspaceIDLess(a, b) == workspaceIDLess(b, a) {
				t.Fatalf("%q and %q are not ordered: less both ways or neither", a, b)
			}
		}
	}
}
