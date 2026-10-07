// Package host lays out the Console's sibling columns in the captain's
// terminal emulator (docs/mvp.md M10, M13): to the right of the Console,
// the agent stage. The file review is a tab in that same window: a column
// left Fresh about half the window, and a separate window covered the
// console. Each surface is created once and runs one long-lived program
// for its whole life - the pane runner (internal/panerun) - which swaps
// what it shows. So this package never kills, re-splits or resizes a
// surface after it made it.
//
// The driver is chosen from the process environment (Detect), not from
// config. Ghostty's AppleScript command is always a shell string (the
// libghostty embedding API), wrapped on macOS by login(1) and bash
// --noprofile --norc, so a column's program must be an absolute path.
package host
