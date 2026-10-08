// Package beads is the Beads task tracker as a tool: `bd` keeps a
// project's tasks in `<workspace>/<project>/.beads/`, and Beads Viewer
// (`bv`) shows them in the tab the console's `t` opens (docs/beads.md,
// docs/mvp.md section 1).
//
// The tracker is the project's, not mate's state: it lives in the project
// directory, and the only trace it leaves under `.mate` is the empty lock
// file the core hands this package through tool.CommandEnv.Lock. The
// package knows nothing of the store; it gets every path and the lock
// from the core.
package beads

import (
	"github.com/nguyenngocanh94/mate/internal/capability"
	"github.com/nguyenngocanh94/mate/internal/tool"
)

var info = tool.Info{
	Name:     "beads",
	Title:    "Beads",
	Binaries: []string{tracker, viewerBin},
	Install:  "see docs/beads.md",
	Docs:     "docs/beads.md",
	Measured: "bd 1.3.1 + bv 0.25.2",
}

// The two executables: the tracker, and its viewer.
const (
	tracker   = "bd"
	viewerBin = "bv"
)

// evidence is what every Beads capability rests on: the versions
// docs/beads.md pins, measured together when Beads replaced mate's own
// task manager.
var evidence = capability.Evidence{
	Version:  "bd 1.3.1 + bv 0.25.2",
	Measured: "2026-10-07",
	Proof:    "docs/beads.md; docs/mvp.md §1",
}

// New is Beads' profile.
func New() tool.Profile { return profile{} }

type profile struct{}

func (profile) Name() tool.Name { return info.Name }
func (profile) Info() tool.Info { return info }

func (profile) Capabilities() tool.Capabilities {
	return tool.Capabilities{
		Viewer:  capability.Cap[tool.Viewer]{Status: capability.Verified, Impl: viewer{}, Evidence: evidence},
		Command: capability.Cap[tool.Command]{Status: capability.Verified, Impl: command{}, Evidence: evidence},
		Recall:  capability.Cap[tool.Recall]{Status: capability.Verified, Impl: recall{}, Evidence: evidence},
		Skill:   capability.Cap[tool.Skill]{Status: capability.Verified, Impl: skill{}, Evidence: evidence},
		Data:    capability.Cap[tool.Data]{Status: capability.Verified, Impl: data{}, Evidence: evidence},
	}
}
