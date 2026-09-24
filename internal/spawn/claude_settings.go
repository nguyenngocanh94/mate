package spawn

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AutoMemoryKey is the Claude Code setting that turns its auto-memory off
// for one session when false. Measured 2026-09-24 on Claude Code 2.1.281 in a
// Herdr 0.8.2 pane (docs/mvp.md section 7, task 35): with it false - in a
// file passed with --settings, whether or not that file is also the cwd's
// `.claude/settings.json` - Claude neither loads an existing
// `~/.claude/projects/<slug>/memory/MEMORY.md` nor writes to that directory
// when told to remember something; with it absent it does both.
// CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 in the pane does the same, but a Mate's
// pane only takes env on its workspace's first create, so the setting is the
// one mechanism that reaches every Mate and every Crew.
const AutoMemoryKey = "autoMemoryEnabled"

// ClaudeSettings builds `.claude/settings.json` for a Mate: the two hooks
// that let `sent.log` capture the conversation and let the app know when the
// user is back (docs/mvp.md task 08), and auto-memory off, because the
// Mate's memory is `mate/memory.md` and nothing else (docs/mvp.md M8, B6).
// It is a pure function of the matev2 binary path, so it can be
// golden-tested without touching Herdr or the filesystem, and both
// `StartMate` and its tests build the same file this way.
//
// UserPromptSubmit invokes `matev2 hook mate-prompt`; Stop invokes `matev2
// hook mate-stop`. Both read the hook's JSON payload from their own stdin,
// which Claude Code always supplies, so no `--` argument or extra flag is
// needed here.
func ClaudeSettings(binary string) ([]byte, error) {
	settings := map[string]any{
		AutoMemoryKey: false,
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

// CrewClaudeSettings is the settings file a Claude Crew launches with. It
// wires no hooks (the Mate's hooks are the Mate's) and turns auto-memory off
// for the same reason as the Mate's: what a Crew should know is in its brief,
// and what it learns goes back through its report and the Mate, never into a
// per-machine directory outside the workspace.
func CrewClaudeSettings() []byte {
	data, _ := json.MarshalIndent(map[string]any{AutoMemoryKey: false}, "", "  ")
	return append(data, '\n')
}

// EnsureAutoMemoryOff adds `"autoMemoryEnabled": false` to an existing
// settings file that does not set the key, keeping every other key. A file
// that already sets it, either way, is the captain's decision and is left
// byte for byte (changed is false). The result is re-indented JSON; key
// order is not preserved, content is.
func EnsureAutoMemoryOff(data []byte) (out []byte, changed bool, err error) {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, false, fmt.Errorf("claude settings are not a JSON object: %w", err)
	}
	if settings == nil {
		settings = map[string]json.RawMessage{}
	}
	if _, ok := settings[AutoMemoryKey]; ok {
		return data, false, nil
	}
	settings[AutoMemoryKey] = json.RawMessage("false")
	out, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}
