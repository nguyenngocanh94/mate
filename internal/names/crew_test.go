package names

import "testing"

func TestValidCrew(t *testing.T) {
	for id, want := range map[string]bool{
		"fix-cart-total":            true,
		"scout-login-timeout":       true,
		"k3":                        true, // an old id stays valid
		"readme2":                   true,
		"a":                         false,
		"-cart":                     false,
		"cart-":                     false,
		"cart--total":               false,
		"Cart-total":                false,
		"cart_total":                false,
		"2cart":                     false,
		"abcdefghij-klmnopqrst-uvw": false, // 25
		"abcdefghij-klmnopqrst-uv":  true,  // 24
	} {
		if got := ValidCrew(id); got != want {
			t.Errorf("ValidCrew(%q) = %v, want %v", id, got, want)
		}
	}
}
