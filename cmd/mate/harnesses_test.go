package main

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// The Console's harness catalog is the registry's: every kind in its order,
// each with a mark in every alphabet, and the registry's default for a Mate.
func TestConsoleHarnessesAreTheCatalogs(t *testing.T) {
	got := consoleHarnesses()
	kinds := harnesses.Kinds()
	if len(got.List) != len(kinds) {
		t.Fatalf("catalog has %d harnesses, registry %d", len(got.List), len(kinds))
	}
	for i, h := range got.List {
		if h.Kind != query.HarnessKind(kinds[i]) {
			t.Errorf("catalog[%d] = %q, registry %q", i, h.Kind, kinds[i])
		}
		if h.Icon.Nerd == "" || h.Icon.Unicode == "" || h.Icon.ASCII == "" {
			t.Errorf("%s draws no mark in some alphabet: %+v", h.Kind, h.Icon)
		}
	}
	def, err := harnesses.Default(harness.RoleMate)
	if err != nil || got.MateDefault != query.HarnessKind(def) {
		t.Errorf("MateDefault = %q, registry %q (%v)", got.MateDefault, def, err)
	}
}

// A new workspace records the registry's defaults, so its file says what it
// runs; and a usage line lists the role's default first.
func TestNewWorkspaceRecordsTheRegistryDefaults(t *testing.T) {
	d := workspaceDefaults()
	mate, _ := harnesses.Default(harness.RoleMate)
	crew, _ := harnesses.Default(harness.RoleCrew)
	if d.MateHarness != string(mate) || d.CrewHarness != string(crew) {
		t.Fatalf("defaults = %+v, want %s and %s", d, mate, crew)
	}
	if !strings.HasPrefix(harnessChoices(harness.RoleCrew, "|"), string(crew)+"|") {
		t.Fatalf("crew choices %q do not start with %s", harnessChoices(harness.RoleCrew, "|"), crew)
	}
}
