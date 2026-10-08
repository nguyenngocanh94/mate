package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/capability"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/tool"
	"github.com/nguyenngocanh94/mate/internal/tool/fresh"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// The Console's tool keys are the registry's, in its order, carried as
// plain data.
func TestConsoleToolsAreTheRegistrys(t *testing.T) {
	r, err := tool.NewRegistry(keyTool{"alpha", []tool.Binding{
		{Key: "x", Label: "alpha", Scope: tool.ScopeCrew, Role: "alpha"},
		{Key: "X", Label: "all alpha", Scope: tool.ScopeProject, Role: "alpha-all"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []query.ToolBinding{
		{Key: "x", Label: "alpha", Scope: "crew", Role: "alpha", Tool: "alpha"},
		{Key: "X", Label: "all alpha", Scope: "project", Role: "alpha-all", Tool: "alpha"},
	}
	if got := toolBindings(r); !reflect.DeepEqual(got, want) {
		t.Fatalf("toolBindings = %+v, want %+v", got, want)
	}
	if got := consoleTools(); len(got) != len(tools.Bindings()) {
		t.Fatalf("consoleTools = %+v, registry binds %+v", got, tools.Bindings())
	}
}

type keyTool struct {
	name tool.Name
	keys []tool.Binding
}

func (k keyTool) Name() tool.Name { return k.name }
func (k keyTool) Info() tool.Info { return tool.Info{Name: k.name} }
func (k keyTool) Capabilities() tool.Capabilities {
	return tool.Capabilities{Viewer: capability.Cap[tool.Viewer]{Status: capability.Verified, Impl: keyViewer(k.keys)}}
}

type keyViewer []tool.Binding

func (v keyViewer) Bindings() []tool.Binding { return v }
func (keyViewer) Argv(tool.ViewerContext, func(string) string) (tool.Invocation, error) {
	return tool.Invocation{}, nil
}
func (keyViewer) Placeholder() string { return "" }

// planTool is a project-scoped viewer: `p` on a project row opens `plan`
// on the project directory in the plan tab. It records what it was opened
// on.
type planTool struct{ opened *[]tool.ViewerContext }

func (planTool) Name() tool.Name { return "planner" }
func (planTool) Info() tool.Info {
	return tool.Info{Name: "planner", Title: "Planner", Binaries: []string{"plan"}, Install: "brew install plan"}
}
func (p planTool) Capabilities() tool.Capabilities {
	return tool.Capabilities{Viewer: capability.Cap[tool.Viewer]{Status: capability.Verified, Impl: planViewer(p)}}
}

type planViewer planTool

func (planViewer) Bindings() []tool.Binding {
	return []tool.Binding{
		{Key: "p", Label: "plan", Scope: tool.ScopeProject, Role: rolePlan},
		{Key: "P", Label: "crew plan", Scope: tool.ScopeCrew, Role: rolePlan},
	}
}
func (v planViewer) Argv(ctx tool.ViewerContext, findTool func(string) string) (tool.Invocation, error) {
	if v.opened != nil {
		*v.opened = append(*v.opened, ctx)
	}
	bin := findTool("plan")
	if bin == "" {
		return tool.Invocation{}, errors.New("no plan: brew install plan")
	}
	return tool.Invocation{Name: bin, Args: []string{ctx.ProjectDir}, Env: []string{"PLAN_QUIET=1"}}, nil
}
func (planViewer) Placeholder() string { return "mate · plan" }

// The Console plans one tab per role a key binds, whether or not the
// tool's binary is installed: a missing one is said on the status line,
// and by the key.
func TestConsoleColumnsPlanATabPerToolRole(t *testing.T) {
	reg, err := tool.NewRegistry(fresh.New(), planTool{})
	if err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	getenv := func(k string) string { return map[string]string{"HOME": empty}[k] }
	c, err := newConsoleColumns(reviewHost{}, getenv, reg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(c.dir) })
	if len(c.tabs) != 2 {
		t.Fatalf("tabs = %+v, want review and plan", c.tabs)
	}
	for _, role := range []string{roleReview, rolePlan} {
		tab, ok := c.tabs[role]
		if !ok || tab.col.Role != role || tab.socket != filepath.Join(c.dir, role+".sock") || !slices.Contains(tab.col.Argv, "--role") {
			t.Errorf("tab %s = %+v", role, tab)
		}
	}

	// The real disk may hold a Fresh in /opt/homebrew/bin, which no getenv
	// hides; the notices are asked of a findTool that finds nothing.
	c.findTool = func(string) string { return "" }
	want := []string{"e report needs Fresh: brew install fresh-editor", "p plan needs Planner: brew install plan"}
	if got := c.missingTools(reg); !slices.Equal(got, want) {
		t.Fatalf("missingTools = %q, want %q", got, want)
	}
	c.findTool = func(name string) string { return "/opt/" + name }
	if got := c.missingTools(reg); len(got) != 0 {
		t.Fatalf("missingTools with everything installed = %q", got)
	}
}

// A project-scoped key opens its tool on the project directory, in its
// own tab; a key bound on another row says where it acts; an unbound key
// says so.
func TestConsoleToolViewOnAProject(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	var opened []tool.ViewerContext
	reg, err := tool.NewRegistry(fresh.New(), planTool{&opened})
	if err != nil {
		t.Fatal(err)
	}
	rec := newRecordingColumns(t)
	view := consoleToolView(w, rec.consoleColumns, reg)
	ctx := context.Background()

	if err := view(ctx, "p", console.StageTarget{ProjectID: "shop"}); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(w.Root(), "shop")
	if len(opened) != 1 || opened[0] != (tool.ViewerContext{ProjectDir: project}) {
		t.Fatalf("opened on %+v, want the project directory only", opened)
	}
	shown := rec.of(rolePlan)
	if len(shown) != 1 || !slices.Equal(shown[0].Argv, []string{"/opt/plan", project}) || shown[0].Dir != project ||
		!slices.Equal(shown[0].Env, append(slices.Clone(rec.env), "PLAN_QUIET=1")) {
		t.Fatalf("plan tab shown %+v, want the tool's environment over the Console's", shown)
	}
	if len(rec.of(roleReview)) != 0 {
		t.Fatal("the review tab was touched")
	}

	for key, target := range map[string]console.StageTarget{
		"p": {Kind: console.StageCrew, ID: "k3", ProjectID: "shop"},
		"e": {ProjectID: "shop"},
	} {
		err := view(ctx, key, target)
		if err == nil || !strings.Contains(err.Error(), key+" opens a ") {
			t.Errorf("%s on %+v = %v, want where %s acts", key, target, err, key)
		}
	}
	if err := view(ctx, "x", console.StageTarget{ProjectID: "shop"}); err == nil || err.Error() != "no tool bound to x" {
		t.Errorf("x = %v, want no tool bound to x", err)
	}
	if len(opened) != 1 {
		t.Fatalf("a refused key reached the tool: %+v", opened)
	}
}

// A tool's pane waits with its Viewer's placeholder; mate's own role keeps
// its own.
func TestPaneIdleComesFromTheTool(t *testing.T) {
	reg, err := tool.NewRegistry(fresh.New(), planTool{})
	if err != nil {
		t.Fatal(err)
	}
	for role, want := range map[string]string{
		roleReview: fresh.New().Capabilities().Viewer.Impl.Placeholder(),
		rolePlan:   "mate · plan",
		roleStage:  paneIdle[roleStage],
	} {
		if got, ok := paneIdleOf(role, reg); !ok || got != want {
			t.Errorf("paneIdleOf(%s) = %q, %v; want %q", role, got, ok, want)
		}
	}
	if _, ok := paneIdleOf("nope", reg); ok {
		t.Error("an unknown role has a pane")
	}
	if got := paneRoles(tools); got != "stage|review|tasks" {
		t.Errorf("paneRoles = %q, want today's stage|review|tasks", got)
	}
	if got, ok := paneIdleOf(roleTasks, tools); !ok || got != "mate · tasks\r\n\r\nt on a project opens Beads Viewer here." {
		t.Errorf("paneIdleOf(tasks) = %q, %v; want Beads Viewer's placeholder", got, ok)
	}
}

// dataTool is a project-scoped viewer whose tool keeps data: `d` opens it,
// and every Exists, Init and Argv lands in calls, in order.
type dataTool struct {
	calls  *[]string
	exists *bool
}

func (dataTool) Name() tool.Name { return "keeper" }
func (dataTool) Info() tool.Info {
	return tool.Info{Name: "keeper", Title: "Keeper", Binaries: []string{"keep"}, Install: "brew install keep"}
}
func (d dataTool) Capabilities() tool.Capabilities {
	return tool.Capabilities{
		Viewer: capability.Cap[tool.Viewer]{Status: capability.Verified, Impl: dataViewer(d)},
		Data:   capability.Cap[tool.Data]{Status: capability.Verified, Impl: dataStore(d)},
	}
}

type dataViewer dataTool

func (dataViewer) Bindings() []tool.Binding {
	return []tool.Binding{{Key: "d", Label: "data", Scope: tool.ScopeProject, Role: rolePlan}}
}
func (v dataViewer) Argv(ctx tool.ViewerContext, findTool func(string) string) (tool.Invocation, error) {
	*v.calls = append(*v.calls, "argv")
	return tool.Invocation{Name: findTool("keep"), Args: []string{ctx.ProjectDir}}, nil
}
func (dataViewer) Placeholder() string { return "mate · data" }

type dataStore dataTool

func (d dataStore) Dir(projectDir string) string { return filepath.Join(projectDir, ".keep") }
func (d dataStore) Exists(string) (bool, error) {
	*d.calls = append(*d.calls, "exists")
	return *d.exists, nil
}
func (d dataStore) Init(_ context.Context, env tool.CommandEnv, _ io.Writer) error {
	*d.calls = append(*d.calls, "init "+filepath.Base(env.ProjectDir)+" "+filepath.Base(env.DataDir))
	*d.exists = true
	return nil
}

// A tool key on a project whose tool keeps data it does not have yet makes
// the data first, once, then opens the viewer; with the data there it only
// opens the viewer.
func TestConsoleToolViewMakesMissingToolData(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	var calls []string
	exists := false
	reg, err := tool.NewRegistry(dataTool{&calls, &exists})
	if err != nil {
		t.Fatal(err)
	}
	rec := newRecordingColumns(t)
	view := consoleToolView(w, rec.consoleColumns, reg)
	for i := 0; i < 2; i++ {
		if err := view(context.Background(), "d", console.StageTarget{ProjectID: "shop"}); err != nil {
			t.Fatal(err)
		}
	}
	calls = slices.DeleteFunc(calls, func(c string) bool { return c == "exists" })
	want := []string{"init shop .keep", "argv", "argv"}
	if !slices.Equal(calls, want) || len(rec.of(rolePlan)) != 2 {
		t.Fatalf("calls %q, shown %d; want %q", calls, len(rec.of(rolePlan)), want)
	}
}
