package harness

import (
	"strings"
)

// The settings and hook files a launch writes, which Prepare names. They came
// here from internal/spawn with plan PR 2 (docs/plans/harness-registry-
// 2026-09-30.md, section 6), because Prepare computes their bytes. Which of
// a Mate's hooks the startup settle may trust, and how long a digest the
// hook may print, each profile answers through its Hooks capability (plan
// PR 4).

// SessionHookName is the `mate hook` subcommand both harnesses' SessionStart
// hook runs.
const SessionHookName = "mate-session"

// SessionHookCommand is the command a Mate's SessionStart hook runs, before
// any --harness a harness adds to name itself.
func SessionHookCommand(binary string) string {
	return ShellQuote(binary) + " hook " + SessionHookName
}

// ShellQuote wraps s in single quotes for the shell Claude Code runs hook
// commands through, escaping any single quote already in s. The mate
// binary path is the only thing quoted here; it comes from os.Executable or
// an operator-supplied override, never from harness or hook input.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
