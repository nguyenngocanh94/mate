// Package catalog is the list of tools compiled into mate. It is the one
// place that names them all, and only binaries (cmd/mate) import it:
// everything below them is handed the tool.Registry built from it
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, section 5.1).
package catalog

import "github.com/nguyenngocanh94/mate/internal/tool"

// Default is every tool this binary drives, in the order the console and
// a Mate's recall list them. No tool is registered yet: the plan's later
// PRs move each one behind the registry.
func Default() []tool.Profile {
	return nil
}
