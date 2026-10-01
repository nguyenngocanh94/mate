// Package catalog is the list of harnesses compiled into mate. It is the
// one place that names them all, and only binaries (cmd/mate) import it:
// everything below them is handed the Registry it builds
// (docs/plans/harness-registry-2026-09-30.md, section 3.4).
package catalog

import (
	"fmt"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
)

// Default is every harness this binary launches, and the one each role gets
// when nobody picks: a Mate runs on Claude Code, a Crew on Codex.
func Default() harness.Registry {
	r, err := harness.NewRegistry(
		map[harness.AgentRole]harness.Kind{
			harness.RoleMate: claude.KindClaude,
			harness.RoleCrew: codex.KindCodex,
		},
		claude.Claude{},
		codex.Codex{},
	)
	if err != nil {
		// The list above is fixed at compile time; a refusal is a bug in
		// this file, caught by its test, never a runtime state.
		panic(fmt.Sprintf("catalog: %v", err))
	}
	return r
}
