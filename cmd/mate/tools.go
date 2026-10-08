package main

import (
	"fmt"

	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/tool"
	toolcatalog "github.com/nguyenngocanh94/mate/internal/tool/catalog"
)

// tools are the tools this binary drives. cmd/mate is where the tool
// catalog is named, as it is for the harness catalog (harnesses.go); the
// packages below it are handed this registry
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, section 5.1).
var tools = func() tool.Registry {
	r, err := tool.NewRegistry(toolcatalog.Default()...)
	if err != nil {
		// The catalog is fixed at compile time; a refusal is a bug in it,
		// caught by its contract suite, never a runtime state.
		panic(fmt.Sprintf("tool catalog: %v", err))
	}
	return r
}()

// consoleTools are the tool keys the read model carries, so the Console
// can bind and draw them without naming a tool.
func consoleTools() []query.ToolBinding { return toolBindings(tools) }

// toolBindings is r's console keys as the read model carries them.
func toolBindings(r tool.Registry) []query.ToolBinding {
	var out []query.ToolBinding
	for _, b := range r.Bindings() {
		out = append(out, query.ToolBinding{Key: b.Key, Label: b.Label, Scope: string(b.Scope), Role: b.Role})
	}
	return out
}
