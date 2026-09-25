// Package host places a Herdr attach client in a sibling pane of the
// captain's terminal emulator.
//
// Herdr still owns the agent process (internal/runtime). This package only
// decides which host pane shows `herdr agent attach`. The driver is chosen
// from the process environment (Detect), not from config (docs/mvp.md M10).
//
// Ghostty's AppleScript command is always a shell string (libghostty
// embedding API), wrapped on macOS by login(1) and bash --noprofile --norc.
// A bare "herdr" is not on that PATH. Stage resolves the binary to an
// absolute path. The config-file "direct:" prefix is not parsed here.
package host
