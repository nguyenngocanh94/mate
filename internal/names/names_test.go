package names

import "testing"

func TestValidProject(t *testing.T) {
	for name, want := range map[string]bool{
		"ledger-audit":                      true,
		"a":                                 true,
		"a1-b2":                             true,
		"":                                  false,
		"1abc":                              false,
		"Ledger":                            false,
		"ledger_audit":                      false,
		"ledger audit":                      false,
		"-ledger":                           false,
		"abcdefghijklmnopqrstuvwxyz012345":  true,
		"abcdefghijklmnopqrstuvwxyz0123456": false,
	} {
		if got := ValidProject(name); got != want {
			t.Errorf("ValidProject(%q) = %v, want %v", name, got, want)
		}
	}
}
