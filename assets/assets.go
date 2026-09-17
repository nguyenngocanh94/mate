// Package assets embeds the Mate's operating manual and the Crew brief
// skeleton so the app ships them inside the binary. internal/mateassets is
// the only intended reader: it renders these templates and writes them out
// per docs/mvp.md section 6.
package assets

import "embed"

// FS holds the files under this directory that the app writes into a
// project's `mate/` directory or renders into a Crew's brief.
//
//go:embed mate/AGENTS.md.tmpl mate/CLAUDE.md mate/skills crew/brief.md.tmpl
var FS embed.FS
