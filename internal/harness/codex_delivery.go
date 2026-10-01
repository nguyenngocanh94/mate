package harness

import "path/filepath"

const (
	// CodexFactoryMaxBytes is project_doc_max_bytes as Codex ships it
	// (observed 0.151.0 through 0.154.0): the concatenated instruction
	// chain is silently truncated to this many bytes.
	CodexFactoryMaxBytes = 32 * 1024
	// CodexDefaultMaxBytes is the cap every mate Codex launch runs under.
	// The launch raises Codex's own cap to this value with
	// `-c project_doc_max_bytes=` (CodexProjectDocMaxBytesOverride), and the
	// meter in this package charges against the same number, so the two
	// can never disagree. 128 KiB is four times the factory cap: the Mate
	// manual with its seven-state table sits near 29 KiB and grew past
	// the factory cap on 2026-09-18, and cutting prose to fit a limit a
	// flag can lift was the wrong trade. Mate still refuses to start past
	// this cap rather than trust Codex to complain.
	CodexDefaultMaxBytes = 128 * 1024
	// CodexOverrideName is the Mate-written discovery file at the process cwd.
	CodexOverrideName = "AGENTS.override.md"
	// CodexBaseName is the tracked project file Codex loads only when no
	// override exists at that directory.
	CodexBaseName = "AGENTS.md"
)

// CodexInstructionPath is the Mate-written discovery file at cwd.
func CodexInstructionPath(cwd string) string {
	return filepath.Join(cwd, CodexOverrideName)
}
