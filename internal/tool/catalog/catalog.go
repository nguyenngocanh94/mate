// Package catalog is the list of tools compiled into mate. It is the one
// place that names them all, and only binaries (cmd/mate) import it:
// everything below them is handed the tool.Registry built from it
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, section 5.1).
package catalog

import (
	"github.com/nguyenngocanh94/mate/internal/tool"
	"github.com/nguyenngocanh94/mate/internal/tool/beads"
	"github.com/nguyenngocanh94/mate/internal/tool/fresh"
)

// Default is every tool this binary drives, in the order the console and
// a Mate's recall list them.
func Default() []tool.Profile {
	return []tool.Profile{fresh.New(), beads.New()}
}
