package spawn

import (
	"encoding/json"
	"strings"
)

// ClaudeSettings builds `.claude/settings.json` for a Mate: the two hooks
// that let `sent.log` capture the conversation and let the app know when the
// user is back (docs/mvp.md task 08). It is a pure function of the matev2
// binary path, so it can be golden-tested without touching Herdr or the
// filesystem, and both `StartMate` and its tests build the same file this
// way.
//
// UserPromptSubmit invokes `matev2 hook mate-prompt`; Stop invokes `matev2
// hook mate-stop`. Both read the hook's JSON payload from their own stdin,
// which Claude Code always supplies, so no `--` argument or extra flag is
// needed here.
func ClaudeSettings(binary string) ([]byte, error) {
	settings := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{hookMatcher(binary, "hook", "mate-prompt")},
			"Stop":             []any{hookMatcher(binary, "hook", "mate-stop")},
		},
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// hookMatcher builds one entry of a Claude Code hook array: an unconditional
// matcher (no "matcher" key) running a single command hook.
func hookMatcher(binary string, args ...string) map[string]any {
	command := shellQuote(binary)
	for _, a := range args {
		command += " " + a
	}
	return map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": command,
			},
		},
	}
}

// shellQuote wraps s in single quotes for the shell Claude Code runs hook
// commands through, escaping any single quote already in s. The matev2
// binary path is the only thing quoted here; it comes from os.Executable or
// an operator-supplied override, never from harness or hook input.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
