package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/capability"
	"github.com/nguyenngocanh94/mate/internal/tool"
	"github.com/nguyenngocanh94/mate/internal/tool/catalog"
)

// The contract suite (docs/plans/workspace-layout-and-tools-2026-10-08.md,
// section 6, items 1 to 4) runs over every tool catalog.Default()
// registers. A tool is onboarded when this suite passes for it; nothing
// here names one. An empty catalog passes; TestContractRefusesBrokenTools
// proves each rule on fakes, so the suite is known to bite before the
// first tool is registered.

func TestCatalogContract(t *testing.T) {
	ws := newWorkspace(t)
	if err := registryErr(catalog.Default()); err != nil {
		t.Error(err)
	}
	for _, p := range catalog.Default() {
		t.Run(string(p.Name()), func(t *testing.T) {
			for _, err := range profileErrs(p, ws) {
				t.Error(err)
			}
		})
	}
}

// workspace is a temporary workspace in the new layout: a project
// directory under the root, and a crew's state directory under .mate.
type workspace struct {
	projectDir, crewDir string
}

func newWorkspace(t *testing.T) workspace {
	t.Helper()
	root := t.TempDir()
	ws := workspace{
		projectDir: filepath.Join(root, "shop"),
		crewDir:    filepath.Join(root, ".mate", "projects", "shop", "crews", "c1"),
	}
	for _, dir := range []string{ws.projectDir, ws.crewDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

// registryErr is item 3: the catalog builds a registry, so no two tools
// bind one key on one row of the console.
func registryErr(profiles []tool.Profile) error {
	if _, err := tool.NewRegistry(profiles...); err != nil {
		return fmt.Errorf("the catalog does not build a registry: %w", err)
	}
	return nil
}

// profileErrs is every rule one tool breaks.
func profileErrs(p tool.Profile, ws workspace) []error {
	var errs []error
	info, caps := p.Info(), p.Capabilities()
	if info.Name != p.Name() {
		errs = append(errs, fmt.Errorf("Info().Name is %q, the profile is %q", info.Name, p.Name()))
	}
	// Item 1: every capability declared, Impl exactly when verified,
	// verified on evidence.
	if err := capability.Check(caps); err != nil {
		errs = append(errs, err)
	}
	// Item 2: a tool's data lives under the project directory, never
	// under .mate.
	if caps.Data.Verified() {
		if err := dataDirErr(caps.Data.Impl.Dir(ws.projectDir), ws.projectDir); err != nil {
			errs = append(errs, err)
		}
	}
	// Item 4: a Viewer builds its argv from the binaries findTool finds,
	// and says how to install them when it finds none.
	if caps.Viewer.Verified() {
		errs = append(errs, viewerErrs(caps.Viewer.Impl, info, ws)...)
	}
	return errs
}

func dataDirErr(dir, projectDir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("Data.Dir(%s) = %q, not an absolute path", projectDir, dir)
	}
	rel, err := filepath.Rel(projectDir, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("Data.Dir(%s) = %q, not below the project directory", projectDir, dir)
	}
	if slices.Contains(strings.Split(rel, string(filepath.Separator)), ".mate") {
		return fmt.Errorf("Data.Dir(%s) = %q, under .mate: a tool's data is not mate's state", projectDir, dir)
	}
	return nil
}

func viewerErrs(v tool.Viewer, info tool.Info, ws workspace) []error {
	var errs []error
	ctx := tool.ViewerContext{ProjectDir: ws.projectDir, CrewDir: ws.crewDir}
	if strings.TrimSpace(info.Install) == "" {
		errs = append(errs, errors.New("a verified Viewer needs Info.Install, the line its missing-binary error carries"))
	} else if _, err := v.Argv(ctx, func(string) string { return "" }); err == nil {
		errs = append(errs, errors.New("Viewer.Argv with no binary installed returned no error"))
	} else if !strings.Contains(err.Error(), info.Install) {
		errs = append(errs, fmt.Errorf("Viewer.Argv with no binary installed: %q does not say %q", err, info.Install))
	}
	const bin = "/contract/bin/"
	argv, err := v.Argv(ctx, func(name string) string { return bin + name })
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("Viewer.Argv with every binary installed: %w", err))
	case len(argv) == 0 || !strings.HasPrefix(argv[0], bin):
		errs = append(errs, fmt.Errorf("Viewer.Argv = %q, which does not start with a binary findTool found", argv))
	}
	return errs
}

// fake is a tool every rule passes on: a verified Viewer and Data, the
// rest unsupported. Each case of TestContractRefusesBrokenTools breaks one
// thing.
type fake struct {
	name    tool.Name
	install string
	keys    []tool.Binding
	dir     func(projectDir string) string
	argv    func(findTool func(string) string) ([]string, error)
	edit    func(*tool.Capabilities)
}

var proof = capability.Evidence{Version: "fake 1.0", Measured: "2026-10-08", Proof: "TestContractRefusesBrokenTools"}

func newFake(name tool.Name, key string) *fake {
	return &fake{
		name:    name,
		install: "brew install " + string(name),
		keys:    []tool.Binding{{Key: key, Label: string(name), Scope: tool.ScopeCrew, Role: string(name)}},
		dir:     func(p string) string { return filepath.Join(p, "."+string(name)) },
	}
}

func (f *fake) Name() tool.Name { return f.name }
func (f *fake) Info() tool.Info {
	return tool.Info{Name: f.name, Title: string(f.name), Binaries: []string{string(f.name)}, Install: f.install}
}
func (f *fake) Capabilities() tool.Capabilities {
	none := "a fake has none"
	c := tool.Capabilities{
		Viewer:  capability.Cap[tool.Viewer]{Status: capability.Verified, Impl: fakeViewer{f}, Evidence: proof},
		Command: capability.Cap[tool.Command]{Status: capability.Unsupported, Reason: none},
		Recall:  capability.Cap[tool.Recall]{Status: capability.Unsupported, Reason: none},
		Skill:   capability.Cap[tool.Skill]{Status: capability.Unsupported, Reason: none},
		Data:    capability.Cap[tool.Data]{Status: capability.Verified, Impl: fakeData{f}, Evidence: proof},
	}
	if f.edit != nil {
		f.edit(&c)
	}
	return c
}

type fakeViewer struct{ f *fake }

func (v fakeViewer) Bindings() []tool.Binding { return v.f.keys }
func (v fakeViewer) Argv(_ tool.ViewerContext, findTool func(string) string) ([]string, error) {
	if v.f.argv != nil {
		return v.f.argv(findTool)
	}
	path := findTool(string(v.f.name))
	if path == "" {
		return nil, fmt.Errorf("%s is not installed; %s", v.f.name, v.f.install)
	}
	return []string{path}, nil
}
func (fakeViewer) Placeholder() string { return "nothing yet" }

type fakeData struct{ f *fake }

func (d fakeData) Dir(projectDir string) string                         { return d.f.dir(projectDir) }
func (fakeData) Exists(string) (bool, error)                            { return false, nil }
func (fakeData) Init(context.Context, tool.CommandEnv, io.Writer) error { return nil }

func TestContractRefusesBrokenTools(t *testing.T) {
	ws := newWorkspace(t)
	if errs := profileErrs(newFake("alpha", "a"), ws); len(errs) != 0 {
		t.Fatalf("the well-formed fake breaks the contract: %v", errs)
	}
	for _, tc := range []struct {
		name string
		p    tool.Profile
		want string
	}{
		{"undeclared capability", broken(func(f *fake) {
			f.edit = func(c *tool.Capabilities) { c.Skill = capability.Cap[tool.Skill]{} }
		}), "Skill is undeclared"},
		{"verified without evidence", broken(func(f *fake) {
			f.edit = func(c *tool.Capabilities) { c.Data.Evidence = capability.Evidence{} }
		}), "Data is verified without full evidence"},
		{"info names another tool", renamed{newFake("alpha", "a"), "beta"}, "Info().Name"},
		{"data under .mate", broken(func(f *fake) {
			f.dir = func(p string) string { return filepath.Join(p, ".mate", "alpha") }
		}), "under .mate"},
		{"data beside the project", broken(func(f *fake) {
			f.dir = func(p string) string { return filepath.Join(filepath.Dir(p), ".alpha") }
		}), "not below the project directory"},
		{"data is the project", broken(func(f *fake) {
			f.dir = func(p string) string { return p }
		}), "not below the project directory"},
		{"data relative", broken(func(f *fake) {
			f.dir = func(string) string { return ".alpha" }
		}), "not an absolute path"},
		{"no install line", broken(func(f *fake) { f.install = "" }), "needs Info.Install"},
		{"missing binary not an error", broken(func(f *fake) {
			f.argv = func(find func(string) string) ([]string, error) { return []string{find("alpha")}, nil }
		}), "returned no error"},
		{"missing binary without install line", broken(func(f *fake) {
			f.argv = func(find func(string) string) ([]string, error) {
				if find("alpha") == "" {
					return nil, errors.New("alpha is missing")
				}
				return []string{find("alpha")}, nil
			}
		}), "does not say"},
		{"argv ignores findTool", broken(func(f *fake) {
			f.argv = func(find func(string) string) ([]string, error) {
				if find("alpha") == "" {
					return nil, errors.New(f.install)
				}
				return []string{"alpha"}, nil
			}
		}), "does not start with a binary findTool found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := profileErrs(tc.p, ws)
			for _, err := range errs {
				if strings.Contains(err.Error(), tc.want) {
					return
				}
			}
			t.Fatalf("errors %v, want one containing %q", errs, tc.want)
		})
	}
	t.Run("one key twice on one row", func(t *testing.T) {
		err := registryErr([]tool.Profile{newFake("alpha", "a"), newFake("beta", "a")})
		if err == nil || !strings.Contains(err.Error(), `key "a"`) {
			t.Fatalf("registryErr = %v, want the duplicate key refused", err)
		}
	})
}

// broken is the well-formed fake with one thing broken.
func broken(edit func(*fake)) *fake {
	f := newFake("alpha", "a")
	edit(f)
	return f
}

// renamed is a fake registered under a name its Info does not carry.
type renamed struct {
	*fake
	as tool.Name
}

func (r renamed) Name() tool.Name { return r.as }
