package main

import (
	"strings"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// harnesses are the harnesses this binary launches. cmd/mate is where the
// catalog is named; every package below it is handed this registry through
// its Deps (docs/plans/harness-registry-2026-09-30.md, section 3.4).
var harnesses = catalog.Default()

// harnessChoices spells the harnesses a role may launch for a usage line:
// the role's default first, then the others in registration order, joined
// with sep.
func harnessChoices(role harness.AgentRole, sep string) string {
	var names []string
	if k, err := harnesses.Default(role); err == nil {
		names = append(names, string(k))
	}
	for _, k := range harnesses.Kinds() {
		if len(names) == 0 || string(k) != names[0] {
			names = append(names, string(k))
		}
	}
	return strings.Join(names, sep)
}

// workspaceDefaults are the harnesses a new workspace.yaml records: the
// registry's default for each role, so the file says what the workspace
// runs. The store fills in nothing itself.
func workspaceDefaults() store.Defaults {
	var d store.Defaults
	if k, err := harnesses.Default(harness.RoleMate); err == nil {
		d.MateHarness = string(k)
	}
	if k, err := harnesses.Default(harness.RoleCrew); err == nil {
		d.CrewHarness = string(k)
	}
	return d
}

// consoleHarnesses is the harness catalog the read model carries, so the
// Console offers and draws the harnesses this binary launches without
// naming any of them.
func consoleHarnesses() query.Harnesses {
	var out query.Harnesses
	for _, k := range harnesses.Kinds() {
		p, err := harnesses.Lookup(k)
		if err != nil {
			continue
		}
		icon := p.Info().Icon
		out.List = append(out.List, query.Harness{
			Kind: query.HarnessKind(k),
			Icon: query.HarnessIcon{Nerd: icon.Nerd, Unicode: icon.Unicode, ASCII: icon.ASCII},
		})
	}
	if k, err := harnesses.Default(harness.RoleMate); err == nil {
		out.MateDefault = query.HarnessKind(k)
	}
	return out
}
