package spawn

// The SessionStart digest's size, per harness. Both are measured limits on
// what a hook's output puts in context (docs/mvp.md section 7, task 37).
const (
	// ClaudeSessionHookMaxBytes: Claude Code 2.1.281 replaces any hook
	// output over 10,000 characters, plain stdout and JSON
	// additionalContext alike, with a file path and a 2 KB preview, so the
	// model never reads the rest. A byte bound is a character bound from
	// above (UTF-8 never spends fewer bytes than UTF-16 code units), and
	// the margin keeps the cut notice inside it.
	ClaudeSessionHookMaxBytes = 9500
	// CodexSessionHookMaxBytes: codex-cli 0.156.1 keeps the head and the
	// tail of a SessionStart hook's output and drops the middle past about
	// 2.5K tokens unless the hook raises additionalContextLimit; with the
	// limit at 20000, 28K characters arrived whole. The digest is bounded
	// well inside harness.CodexHookContextLimit.
	CodexSessionHookMaxBytes = 48000
)
