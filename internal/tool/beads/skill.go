package beads

import _ "embed"

// skill is the Mate's `task-management` skill: how to record, choose,
// dispatch and accept work in the project's tracker.
type skill struct{}

// skillTemplate is the skill's SKILL.md as a text/template over the
// manual's parameters (internal/mateassets.Params), which the core fills
// when it writes the skill beside the manual.
//
//go:embed skill.md.tmpl
var skillTemplate string

func (skill) SkillName() string     { return "task-management" }
func (skill) SkillMarkdown() string { return skillTemplate }

// ManualSection is the skill's line in the manual's list of skills and
// when to load them.
func (skill) ManualSection() string {
	return "- `task-management` - when recording requested work, choosing ready tasks, dispatching, tracking dependencies or accepting delivery in Beads."
}
