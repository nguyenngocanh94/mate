// Package spawn starts and stops the Mate of a project.
//
// It is the composition point of the four packages that already exist:
// store says where everything lives, mateassets renders the operating
// manual into the Mate's cwd, harness builds the launch argv for the chosen
// CLI, and runtime drives Herdr. Nothing here talks to Herdr or to the
// filesystem format directly.
//
// The whole of the recorded state is `mate/mate.meta`. There is no database,
// no bindings table and no operation claim: `mate.meta` is a hint, and every
// decision that depends on an agent being alive re-asks Herdr (docs/mvp.md
// section 7, "a command that reports success must re-check the real system").
package spawn
