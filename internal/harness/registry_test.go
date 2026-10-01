package harness

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// namedProfile is Claude's profile under another kind: a harness a test
// registers without it being one mate ships.
type namedProfile struct {
	Claude
	kind Kind
}

func (p namedProfile) Kind() Kind { return p.kind }

func TestRegistryParseIsTheRegisteredKinds(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(map[AgentRole]Kind{RoleCrew: "lab"}, Codex{}, namedProfile{kind: "lab"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Kinds(); !slices.Equal(got, []Kind{KindCodex, "lab"}) {
		t.Fatalf("Kinds() = %v, want registration order", got)
	}
	for in, want := range map[string]Kind{"lab": "lab", " LAB ": "lab", "Codex": KindCodex} {
		if got, err := r.Parse(in); err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// Claude is a kind mate ships, and not one this registry holds.
	if _, err := r.Parse("claude"); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("Parse(claude) on a registry without it = %v, want ErrInvalidValue", err)
	}
	if _, err := r.Parse("  "); !errors.Is(err, ErrEmptyValue) {
		t.Errorf("Parse(blank) = %v, want ErrEmptyValue", err)
	}
	if _, err := r.Lookup(KindClaude); err == nil || !strings.Contains(err.Error(), "codex, lab") {
		t.Errorf("Lookup(claude) = %v, want a refusal listing what is registered", err)
	}
	if p, err := r.Lookup("lab"); err != nil || p.Kind() != "lab" {
		t.Errorf("Lookup(lab) = %v, %v", p, err)
	}
	if k, err := r.Default(RoleCrew); err != nil || k != "lab" {
		t.Errorf("Default(crew) = %q, %v; want lab", k, err)
	}
	if _, err := r.Default(RoleMate); err == nil {
		t.Error("Default(mate) with no Mate default named a harness")
	}
}

func TestNewRegistryRefusals(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		defaults map[AgentRole]Kind
		profiles []Profile
	}{
		"duplicate kind":          {nil, []Profile{Claude{}, Claude{}}},
		"empty kind":              {nil, []Profile{namedProfile{kind: ""}}},
		"upper-case kind":         {nil, []Profile{namedProfile{kind: "Lab"}}},
		"padded kind":             {nil, []Profile{namedProfile{kind: " lab"}}},
		"unregistered default":    {map[AgentRole]Kind{RoleMate: KindClaude}, []Profile{Codex{}}},
		"default with no harness": {map[AgentRole]Kind{RoleCrew: KindCodex}, nil},
	} {
		if _, err := NewRegistry(tc.defaults, tc.profiles...); err == nil {
			t.Errorf("%s: NewRegistry accepted it", name)
		}
	}
}

// A Deps that was never handed a registry holds the zero Registry; it must
// refuse every kind rather than fall back to one.
func TestZeroRegistryRefusesEverything(t *testing.T) {
	t.Parallel()
	var r Registry
	if _, err := r.Parse("claude"); err == nil {
		t.Error("zero Registry parsed claude")
	}
	if _, err := r.Lookup(KindCodex); err == nil || !strings.Contains(err.Error(), "none") {
		t.Errorf("zero Registry Lookup = %v, want a refusal saying none are registered", err)
	}
	if _, err := r.Default(RoleCrew); err == nil {
		t.Error("zero Registry has a default")
	}
	if len(r.Kinds()) != 0 {
		t.Errorf("zero Registry Kinds() = %v", r.Kinds())
	}
}
