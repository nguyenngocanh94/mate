package tool

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// Registry is the set of tools one binary drives. A tool exists when it is
// registered; there is no other list. cmd/mate builds the registry from
// internal/tool/catalog and hands it down, so a core test can register a
// tool of its own.
//
// The zero Registry has no tools: every Lookup and Parse fails and it binds
// no key.
type Registry struct {
	profiles map[Name]Profile
	order    []Name
	bindings []Binding
}

// ErrNoViewerImpl is a Viewer declared verified with no implementation.
// The contract suite refuses it too (capability.Check); NewRegistry refuses
// it by name rather than calling a nil Viewer.
var ErrNoViewerImpl = errors.New("tool registry: a verified Viewer has no implementation")

// NewRegistry registers profiles in the order given. A duplicate name, an
// empty or non-canonical name, a verified Viewer with no implementation, a
// binding with no key or an unknown scope, and one key bound twice on one
// row of the console are refused. Each binding is recorded with the name of
// the tool that declares it.
func NewRegistry(profiles ...Profile) (Registry, error) {
	r := Registry{profiles: map[Name]Profile{}}
	owner := map[Binding]Name{} // key and scope only
	for _, p := range profiles {
		n := p.Name()
		if n == "" || Name(strings.ToLower(strings.TrimSpace(string(n)))) != n {
			return Registry{}, fmt.Errorf("tool registry: name %q is not lower-case and trimmed", n)
		}
		if _, dup := r.profiles[n]; dup {
			return Registry{}, fmt.Errorf("tool registry: name %q registered twice", n)
		}
		r.profiles[n] = p
		r.order = append(r.order, n)

		viewer := p.Capabilities().Viewer
		if !viewer.Verified() {
			continue
		}
		if viewer.Impl == nil {
			return Registry{}, fmt.Errorf("%w: %s", ErrNoViewerImpl, n)
		}
		for _, b := range viewer.Impl.Bindings() {
			b.Tool = n
			if b.Key == "" {
				return Registry{}, fmt.Errorf("tool registry: %s binds a key with no key (label %q)", n, b.Label)
			}
			if b.Scope != ScopeProject && b.Scope != ScopeCrew {
				return Registry{}, fmt.Errorf("tool registry: %s binds key %q to scope %q, not %s or %s", n, b.Key, b.Scope, ScopeProject, ScopeCrew)
			}
			slot := Binding{Key: b.Key, Scope: b.Scope}
			if other, taken := owner[slot]; taken {
				return Registry{}, fmt.Errorf("tool registry: key %q on a %s row is bound by both %s and %s", b.Key, b.Scope, other, n)
			}
			owner[slot] = n
			r.bindings = append(r.bindings, b)
		}
	}
	return r, nil
}

// Lookup is the profile of a registered tool.
func (r Registry) Lookup(n Name) (Profile, error) {
	if p, ok := r.profiles[n]; ok {
		return p, nil
	}
	return nil, observability.NewError(observability.CodeUsage,
		fmt.Sprintf("no tool registered as %q (registered: %s)", n, r.list()))
}

// Parse reads a tool name as a person or a file spells it: case and
// surrounding space do not matter. Empty and unregistered names are
// refused.
func (r Registry) Parse(s string) (Name, error) {
	n := Name(strings.ToLower(strings.TrimSpace(s)))
	if n == "" {
		return "", fmt.Errorf("tool name is empty")
	}
	if _, ok := r.profiles[n]; !ok {
		return "", fmt.Errorf("tool %q is not registered (registered: %s)", s, r.list())
	}
	return n, nil
}

// Names are the registered tools, in registration order.
func (r Registry) Names() []Name { return append([]Name(nil), r.order...) }

// Bindings are the console keys of every tool whose Viewer is verified, in
// registration order and each tool's own order, each with its Tool set.
func (r Registry) Bindings() []Binding { return append([]Binding(nil), r.bindings...) }

func (r Registry) list() string {
	if len(r.order) == 0 {
		return "none"
	}
	names := make([]string, len(r.order))
	for i, n := range r.order {
		names[i] = string(n)
	}
	return strings.Join(names, ", ")
}
