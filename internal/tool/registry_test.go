package tool_test

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/capability"
	"github.com/nguyenngocanh94/mate/internal/tool"
)

// fakeProfile is a tool that is only a name and, when it has bindings, a
// verified Viewer declaring them.
type fakeProfile struct {
	name     tool.Name
	bindings []tool.Binding
	viewer   capability.CapStatus
}

func (p fakeProfile) Name() tool.Name { return p.name }
func (p fakeProfile) Info() tool.Info { return tool.Info{Name: p.name, Title: string(p.name)} }
func (p fakeProfile) Capabilities() tool.Capabilities {
	var c tool.Capabilities
	switch p.viewer {
	case capability.Verified:
		c.Viewer = capability.Cap[tool.Viewer]{Status: capability.Verified, Impl: fakeViewer{p.bindings}}
	case "":
	default:
		c.Viewer = capability.Cap[tool.Viewer]{Status: p.viewer, Reason: "fake"}
	}
	return c
}

type fakeViewer struct{ bindings []tool.Binding }

func (v fakeViewer) Bindings() []tool.Binding { return v.bindings }
func (fakeViewer) Argv(tool.ViewerContext, func(string) string) ([]string, error) {
	return nil, nil
}
func (fakeViewer) Placeholder() string { return "" }

func viewing(name tool.Name, bindings ...tool.Binding) fakeProfile {
	return fakeProfile{name: name, bindings: bindings, viewer: capability.Verified}
}

func TestEmptyRegistry(t *testing.T) {
	for name, r := range map[string]tool.Registry{"zero": {}, "built": mustRegistry(t)} {
		t.Run(name, func(t *testing.T) {
			if n := r.Names(); len(n) != 0 {
				t.Errorf("Names() = %v, want none", n)
			}
			if b := r.Bindings(); len(b) != 0 {
				t.Errorf("Bindings() = %v, want none", b)
			}
			if _, err := r.Lookup("alpha"); err == nil || !strings.Contains(err.Error(), "registered: none") {
				t.Errorf("Lookup on an empty registry = %v, want a refusal listing none", err)
			}
			if _, err := r.Parse("alpha"); err == nil {
				t.Error("Parse on an empty registry accepted a name")
			}
		})
	}
}

func mustRegistry(t *testing.T, profiles ...tool.Profile) tool.Registry {
	t.Helper()
	r, err := tool.NewRegistry(profiles...)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func TestRegistryLooksUpAndParsesInRegistrationOrder(t *testing.T) {
	r := mustRegistry(t, fakeProfile{name: "beta"}, fakeProfile{name: "alpha"})
	if got := r.Names(); len(got) != 2 || got[0] != "beta" || got[1] != "alpha" {
		t.Fatalf("Names() = %v, want [beta alpha]", got)
	}
	p, err := r.Lookup("alpha")
	if err != nil || p.Name() != "alpha" {
		t.Fatalf("Lookup(alpha) = %v, %v", p, err)
	}
	if _, err := r.Lookup("gamma"); err == nil || !strings.Contains(err.Error(), "registered: beta, alpha") {
		t.Errorf("Lookup(gamma) = %v, want a refusal listing beta, alpha", err)
	}
	if n, err := r.Parse("  Alpha "); err != nil || n != "alpha" {
		t.Errorf("Parse(%q) = %q, %v; want alpha", "  Alpha ", n, err)
	}
	for _, s := range []string{"", "  ", "gamma"} {
		if _, err := r.Parse(s); err == nil {
			t.Errorf("Parse(%q) accepted it", s)
		}
	}
	names := r.Names()
	names[0] = "mutated"
	if r.Names()[0] != "beta" {
		t.Error("Names() hands out the registry's own slice")
	}
}

func TestNewRegistryRefusesBadNames(t *testing.T) {
	for _, tc := range []struct {
		name     string
		profiles []tool.Profile
		want     string
	}{
		{"empty", []tool.Profile{fakeProfile{name: ""}}, "not lower-case and trimmed"},
		{"upper case", []tool.Profile{fakeProfile{name: "Alpha"}}, "not lower-case and trimmed"},
		{"spaced", []tool.Profile{fakeProfile{name: " alpha"}}, "not lower-case and trimmed"},
		{"twice", []tool.Profile{fakeProfile{name: "alpha"}, fakeProfile{name: "alpha"}}, "registered twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tool.NewRegistry(tc.profiles...); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewRegistry = %v, want a refusal containing %q", err, tc.want)
			}
		})
	}
}

// Bindings are every verified Viewer's keys, in registration order; a tool
// whose Viewer is not verified binds nothing.
func TestBindingsComeFromVerifiedViewers(t *testing.T) {
	report := tool.Binding{Key: "e", Label: "report", Scope: tool.ScopeCrew, Role: "review"}
	tasks := tool.Binding{Key: "t", Label: "tasks", Scope: tool.ScopeProject, Role: "tasks"}
	unmeasured := fakeProfile{name: "gamma", bindings: []tool.Binding{{Key: "g", Label: "g", Scope: tool.ScopeCrew}}, viewer: capability.Unknown}
	r := mustRegistry(t, viewing("alpha", report), unmeasured, fakeProfile{name: "delta"}, viewing("beta", tasks))
	got := r.Bindings()
	if len(got) != 2 || got[0] != report || got[1] != tasks {
		t.Fatalf("Bindings() = %+v, want [%+v %+v]", got, report, tasks)
	}
	got[0].Key = "z"
	if r.Bindings()[0].Key != "e" {
		t.Error("Bindings() hands out the registry's own slice")
	}
}

// Two tools that claim one key on one row of the console are refused when
// the registry is built, not when the key is pressed. The same key on the
// other row is a different binding.
func TestNewRegistryRefusesAKeyBoundTwice(t *testing.T) {
	crewE := tool.Binding{Key: "e", Label: "report", Scope: tool.ScopeCrew, Role: "review"}
	projectE := tool.Binding{Key: "e", Label: "edit", Scope: tool.ScopeProject, Role: "edit"}
	if _, err := tool.NewRegistry(viewing("alpha", crewE), viewing("beta", projectE)); err != nil {
		t.Fatalf("one key on two rows: %v", err)
	}
	_, err := tool.NewRegistry(viewing("alpha", crewE), viewing("beta", tool.Binding{Key: "e", Label: "other", Scope: tool.ScopeCrew, Role: "other"}))
	if err == nil || !strings.Contains(err.Error(), `key "e"`) || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Fatalf("one key on one row twice = %v, want a refusal naming the key and both tools", err)
	}
	if _, err := tool.NewRegistry(viewing("alpha", crewE, crewE)); err == nil {
		t.Fatal("one tool binding one key twice was accepted")
	}
}

func TestNewRegistryRefusesMalformedBindings(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    tool.Binding
		want string
	}{
		{"no key", tool.Binding{Label: "x", Scope: tool.ScopeCrew, Role: "x"}, "no key"},
		{"no scope", tool.Binding{Key: "x", Label: "x", Role: "x"}, "scope"},
		{"unknown scope", tool.Binding{Key: "x", Label: "x", Scope: "workspace", Role: "x"}, "scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tool.NewRegistry(viewing("alpha", tc.b)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewRegistry = %v, want a refusal containing %q", err, tc.want)
			}
		})
	}
}
