package harness

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// Registry is the set of harnesses one binary launches. A kind is valid when
// it is registered; there is no other list. cmd/mate builds the registry
// from internal/harness/catalog and hands it down through each package's
// Deps, so a core test can register a harness of its own (plan section 3.4).
//
// The zero Registry has no harnesses: every Lookup and Parse fails, which is
// how a Deps that was never given one reports it.
type Registry struct {
	profiles map[Kind]Profile
	order    []Kind
	defaults map[AgentRole]Kind
}

// NewRegistry registers profiles in the order given, with the harness each
// role gets when nobody picks one. A duplicate kind, a profile with an empty
// or non-canonical kind, and a default that names an unregistered kind are
// refused.
func NewRegistry(defaults map[AgentRole]Kind, profiles ...Profile) (Registry, error) {
	r := Registry{profiles: map[Kind]Profile{}, defaults: map[AgentRole]Kind{}}
	for _, p := range profiles {
		k := p.Kind()
		if k == "" || Kind(strings.ToLower(strings.TrimSpace(string(k)))) != k {
			return Registry{}, fmt.Errorf("harness registry: kind %q is not lower-case and trimmed", k)
		}
		if _, dup := r.profiles[k]; dup {
			return Registry{}, fmt.Errorf("harness registry: kind %q registered twice", k)
		}
		r.profiles[k] = p
		r.order = append(r.order, k)
	}
	for role, k := range defaults {
		if _, ok := r.profiles[k]; !ok {
			return Registry{}, fmt.Errorf("harness registry: default %s harness %q is not registered", role, k)
		}
		r.defaults[role] = k
	}
	return r, nil
}

// Lookup is the profile of a registered kind.
func (r Registry) Lookup(k Kind) (Profile, error) {
	if p, ok := r.profiles[k]; ok {
		return p, nil
	}
	return nil, observability.NewError(observability.CodeUsage,
		fmt.Sprintf("no harness registered for kind %q (registered: %s)", k, r.list()))
}

// Parse reads a kind as a person or a file spells it: case and surrounding
// space do not matter. Empty and unregistered kinds are refused.
func (r Registry) Parse(s string) (Kind, error) {
	k := Kind(strings.ToLower(strings.TrimSpace(s)))
	if k == "" {
		return "", fmt.Errorf("harness kind: %w", ErrEmptyValue)
	}
	if _, ok := r.profiles[k]; !ok {
		return "", fmt.Errorf("harness kind %q: %w", s, ErrInvalidValue)
	}
	return k, nil
}

// Kinds are the registered kinds, in registration order.
func (r Registry) Kinds() []Kind { return append([]Kind(nil), r.order...) }

// EnvKeys are the variables every registered harness declares
// (Info().EnvKeys), in registration order: what a pane or a launch may
// carry besides mate's identity keys.
func (r Registry) EnvKeys() []string {
	var keys []string
	for _, k := range r.order {
		for _, key := range r.profiles[k].Info().EnvKeys {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// Default is the harness a role gets when neither the caller nor the
// workspace picked one.
func (r Registry) Default(role AgentRole) (Kind, error) {
	if k, ok := r.defaults[role]; ok {
		return k, nil
	}
	return "", observability.NewError(observability.CodeUsage,
		fmt.Sprintf("no default harness for the %s role (registered: %s)", role, r.list()))
}

func (r Registry) list() string {
	if len(r.order) == 0 {
		return "none"
	}
	names := make([]string, len(r.order))
	for i, k := range r.order {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}
