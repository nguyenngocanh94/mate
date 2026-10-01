// Package harnesstest holds what the tests of the harness packages share:
// the live gate, launch fixtures (fixture.go), and wrappers of registered
// harnesses for the core tests that prove a pane is read through the source
// its harness's ScreenProfile names (docs/plans/harness-registry-2026-09-30.md,
// section 3.2). Claude and Codex both read recent-unwrapped, so a caller that
// ignored the profile and spelled that source itself would pass every test
// built on them; the same screens read through ReadVisible catch it.
package harnesstest

import (
	"fmt"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// ReadingVisible is r with every profile's screens read through
// harness.ReadVisible, and nothing else changed: the same kinds, defaults,
// launches and screen tables.
func ReadingVisible(r harness.Registry) harness.Registry {
	var profiles []harness.Profile
	for _, k := range r.Kinds() {
		p, err := r.Lookup(k)
		if err != nil {
			panic(fmt.Sprintf("harnesstest: %v", err))
		}
		profiles = append(profiles, visibleProfile{p})
	}
	defaults := map[harness.AgentRole]harness.Kind{}
	for _, role := range []harness.AgentRole{harness.RoleMate, harness.RoleCrew} {
		if k, err := r.Default(role); err == nil {
			defaults[role] = k
		}
	}
	out, err := harness.NewRegistry(defaults, profiles...)
	if err != nil {
		panic(fmt.Sprintf("harnesstest: %v", err))
	}
	return out
}

// ScreenReadingVisible is s read through harness.ReadVisible.
func ScreenReadingVisible(s harness.ScreenProfile) harness.ScreenProfile {
	return visibleScreen{s}
}

type visibleProfile struct{ harness.Profile }

func (p visibleProfile) Screen() harness.ScreenProfile {
	return visibleScreen{p.Profile.Screen()}
}

type visibleScreen struct{ harness.ScreenProfile }

func (visibleScreen) ReadSource() harness.ReadSource { return harness.ReadVisible }
