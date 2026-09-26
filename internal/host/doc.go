// Package host lays out the Console's sibling columns in the captain's
// terminal emulator (docs/mvp.md M10, M13): to the right of the Console,
// the agent stage, then the file review. Each column is created once and
// runs one long-lived program for its whole life - the pane runner
// (internal/panerun) - which swaps what the column shows. So this package
// never kills, re-splits or resizes a column after it made it.
//
// The driver is chosen from the process environment (Detect), not from
// config. Ghostty's AppleScript command is always a shell string (the
// libghostty embedding API), wrapped on macOS by login(1) and bash
// --noprofile --norc, so a column's program must be an absolute path.
package host
