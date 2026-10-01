package catalog

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
)

// namedProfile is Claude's profile under another kind: a harness a test
// registers without it being one mate ships.
type namedProfile struct {
	claude.Claude
	kind harness.Kind
}

func (p namedProfile) Kind() harness.Kind { return p.kind }

func TestRegistryParseIsTheRegisteredKinds(t *testing.T) {
	t.Parallel()
	r, err := harness.NewRegistry(map[harness.AgentRole]harness.Kind{harness.RoleCrew: "lab"}, codex.Codex{}, namedProfile{kind: "lab"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Kinds(); !slices.Equal(got, []harness.Kind{codex.KindCodex, "lab"}) {
		t.Fatalf("Kinds() = %v, want registration order", got)
	}
	for in, want := range map[string]harness.Kind{"lab": "lab", " LAB ": "lab", "Codex": codex.KindCodex} {
		if got, err := r.Parse(in); err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// Claude is a kind mate ships, and not one this registry holds.
	if _, err := r.Parse("claude"); !errors.Is(err, harness.ErrInvalidValue) {
		t.Errorf("Parse(claude) on a registry without it = %v, want ErrInvalidValue", err)
	}
	if _, err := r.Parse("  "); !errors.Is(err, harness.ErrEmptyValue) {
		t.Errorf("Parse(blank) = %v, want ErrEmptyValue", err)
	}
	if _, err := r.Lookup(claude.KindClaude); err == nil || !strings.Contains(err.Error(), "codex, lab") {
		t.Errorf("Lookup(claude) = %v, want a refusal listing what is registered", err)
	}
	if p, err := r.Lookup("lab"); err != nil || p.Kind() != "lab" {
		t.Errorf("Lookup(lab) = %v, %v", p, err)
	}
	if k, err := r.Default(harness.RoleCrew); err != nil || k != "lab" {
		t.Errorf("Default(crew) = %q, %v; want lab", k, err)
	}
	if _, err := r.Default(harness.RoleMate); err == nil {
		t.Error("Default(mate) with no Mate default named a harness")
	}
}

func TestNewRegistryRefusals(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		defaults map[harness.AgentRole]harness.Kind
		profiles []harness.Profile
	}{
		"duplicate kind":          {nil, []harness.Profile{claude.Claude{}, claude.Claude{}}},
		"empty kind":              {nil, []harness.Profile{namedProfile{kind: ""}}},
		"upper-case kind":         {nil, []harness.Profile{namedProfile{kind: "Lab"}}},
		"padded kind":             {nil, []harness.Profile{namedProfile{kind: " lab"}}},
		"unregistered default":    {map[harness.AgentRole]harness.Kind{harness.RoleMate: claude.KindClaude}, []harness.Profile{codex.Codex{}}},
		"default with no harness": {map[harness.AgentRole]harness.Kind{harness.RoleCrew: codex.KindCodex}, nil},
	} {
		if _, err := harness.NewRegistry(tc.defaults, tc.profiles...); err == nil {
			t.Errorf("%s: NewRegistry accepted it", name)
		}
	}
}

// A Deps that was never handed a registry holds the zero Registry; it must
// refuse every kind rather than fall back to one.
func TestZeroRegistryRefusesEverything(t *testing.T) {
	t.Parallel()
	var r harness.Registry
	if _, err := r.Parse("claude"); err == nil {
		t.Error("zero Registry parsed claude")
	}
	if _, err := r.Lookup(codex.KindCodex); err == nil || !strings.Contains(err.Error(), "none") {
		t.Errorf("zero Registry Lookup = %v, want a refusal saying none are registered", err)
	}
	if _, err := r.Default(harness.RoleCrew); err == nil {
		t.Error("zero Registry has a default")
	}
	if len(r.Kinds()) != 0 {
		t.Errorf("zero Registry Kinds() = %v", r.Kinds())
	}
}
