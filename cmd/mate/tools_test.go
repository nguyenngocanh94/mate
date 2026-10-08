package main

import (
	"reflect"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/capability"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/tool"
)

// The Console's tool keys are the registry's, in its order, carried as
// plain data.
func TestConsoleToolsAreTheRegistrys(t *testing.T) {
	r, err := tool.NewRegistry(keyTool{"alpha", []tool.Binding{
		{Key: "a", Label: "alpha", Scope: tool.ScopeCrew, Role: "alpha"},
		{Key: "A", Label: "all alpha", Scope: tool.ScopeProject, Role: "alpha-all"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []query.ToolBinding{
		{Key: "a", Label: "alpha", Scope: "crew", Role: "alpha"},
		{Key: "A", Label: "all alpha", Scope: "project", Role: "alpha-all"},
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
func (keyViewer) Argv(tool.ViewerContext, func(string) string) ([]string, error) {
	return nil, nil
}
func (keyViewer) Placeholder() string { return "" }
