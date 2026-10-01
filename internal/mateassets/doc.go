// Package mateassets renders the embedded templates under assets/ and writes
// a Mate's operating manual into its working directory.
//
// Render and RenderBrief are pure: they take a Params/BriefParams value and
// return bytes, with no filesystem access. Write is the one function that
// touches disk: it writes `AGENTS.md` and the skills atomically (temp file
// plus rename) and creates `memory.md`/`backlog.md` with their headers
// only if they do not already exist - it never overwrites either, because
// they are the Mate's own memory, not generated content.
//
// This package does not import internal/store: callers resolve every path
// themselves and pass them in, both as the directory to write into and as
// the absolute paths baked into the rendered text.
package mateassets
