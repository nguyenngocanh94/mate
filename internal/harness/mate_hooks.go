package harness

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// The settings and hook files a launch writes, which Prepare names. They came
// here from internal/spawn with plan PR 2 (docs/plans/harness-registry-
// 2026-09-30.md, section 6), because Prepare computes their bytes. Which of
// a Mate's hooks the startup settle may trust, and how long a digest the
// hook may print, each profile answers through its Hooks capability (plan
// PR 4).

const (
	// ClaudeSettingsFile is the name of the settings file a Claude launch
	// passes with --settings: `<mate>/.claude/settings.json` for a Mate,
	// `crews/<id>/settings.json` for a Crew.
	ClaudeSettingsFile = "settings.json"
	// CodexHooksFile is the Codex Mate's hook file, in the `.codex/` of its
	// cwd, which Codex loads once the directory is trusted (task 35, A3).
	// The operator's own `$CODEX_HOME/hooks.json` is never read or written.
	CodexHooksFile = "hooks.json"
)

// ClaudeSettingsPath is the settings file a Claude Mate in mateDir launches
// with.
func ClaudeSettingsPath(mateDir string) string {
	return filepath.Join(mateDir, Claude{}.Info().ConfigDir, ClaudeSettingsFile)
}

// CodexHooksPath is the hook file of a Codex Mate in mateDir.
func CodexHooksPath(mateDir string) string {
	return filepath.Join(mateDir, Codex{}.Info().ConfigDir, CodexHooksFile)
}

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
// It is a pure function of the mate binary path, so it can be
// golden-tested without touching Herdr or the filesystem, and both a Mate
// launch and its tests build the same file this way.
//
// UserPromptSubmit invokes `mate hook mate-prompt`; Stop invokes `mate
// hook mate-stop`. Both read the hook's JSON payload from their own stdin,
// which Claude Code always supplies, so no `--` argument or extra flag is
// needed here.
//
// SessionStart invokes `mate hook mate-session` (docs/mvp.md task 37,
// B2): one hook with no matcher, which Claude Code 2.1.281 fires once each
// for startup, clear, compact and resume in a Herdr pane, its stdout reaching
// the model before the next turn (task 35, A2). It prints `mate recall`.
func ClaudeSettings(binary string) ([]byte, error) {
	settings := map[string]any{
		AutoMemoryKey: false,
		"hooks": map[string]any{
			"UserPromptSubmit": []any{hookMatcher(binary, "hook", "mate-prompt")},
			"Stop":             []any{hookMatcher(binary, "hook", "mate-stop")},
			"SessionStart":     []any{hookMatcher(binary, "hook", SessionHookName)},
		},
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// SessionHookName is the `mate hook` subcommand both harnesses' SessionStart
// hook runs.
const SessionHookName = "mate-session"

const (
	// CodexHookContextLimit is the additionalContextLimit, in tokens, the
	// Codex Mate's hook is installed with: codex-cli 0.156.1 keeps the head
	// and the tail of a SessionStart hook's output and drops the middle past
	// about 2.5K tokens unless the hook raises it (docs/mvp.md section 7,
	// task 37).
	CodexHookContextLimit = 32000
	// CodexHookTimeoutSeconds bounds the hook, which reads files and git
	// metadata only.
	CodexHookTimeoutSeconds = 30
)

// The SessionStart digest's size, per harness (HookInstaller.DigestMaxBytes).
// Both are measured limits on what a hook's output puts in context
// (docs/mvp.md section 7, task 37).
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
	// well inside CodexHookContextLimit.
	CodexSessionHookMaxBytes = 48000
)

// SessionHookCommand is the exact command a Mate's SessionStart hook runs.
// The Codex form names its harness so the digest is sized for it. The
// startup settle compares a hook Codex asks to trust against this string,
// so it is the one place it is spelled.
func SessionHookCommand(binary string, kind Kind) string {
	command := shellQuote(binary) + " hook " + SessionHookName
	if kind == KindCodex {
		command += " --harness codex"
	}
	return command
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

// EnsureSessionHook adds mate's SessionStart hook to an existing settings
// file that has none, keeping every other key and every SessionStart entry
// already there (the captain's own included). A file that already runs
// `hook mate-session` from any SessionStart entry is left byte for byte
// (changed is false). This is the path EnsureAutoMemoryOff took for task 35,
// so a Mate directory made before task 37 gets its digest at the next start.
func EnsureSessionHook(data []byte, binary string) (out []byte, changed bool, err error) {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, false, fmt.Errorf("claude settings are not a JSON object: %w", err)
	}
	if settings == nil {
		settings = map[string]json.RawMessage{}
	}
	hooks := map[string]json.RawMessage{}
	if raw, ok := settings["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil || hooks == nil {
			return nil, false, fmt.Errorf("claude settings: \"hooks\" is not a JSON object")
		}
	}
	var entries []json.RawMessage
	if raw, ok := hooks["SessionStart"]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, false, fmt.Errorf("claude settings: hooks.SessionStart is not a JSON array")
		}
	}
	for _, entry := range entries {
		var matcher struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if json.Unmarshal(entry, &matcher) != nil {
			continue
		}
		for _, h := range matcher.Hooks {
			if strings.HasSuffix(strings.TrimSpace(h.Command), " hook "+SessionHookName) {
				return data, false, nil
			}
		}
	}
	ours, err := json.Marshal(hookMatcher(binary, "hook", SessionHookName))
	if err != nil {
		return nil, false, err
	}
	entries = append(entries, ours)
	if hooks["SessionStart"], err = json.Marshal(entries); err != nil {
		return nil, false, err
	}
	if settings["hooks"], err = json.Marshal(hooks); err != nil {
		return nil, false, err
	}
	out, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

// CodexHooks is `mate/.codex/hooks.json` for a Codex Mate: the same
// SessionStart hook, with its output limit raised so the digest arrives
// whole (see CodexHookContextLimit). Codex runs it at the first prompt
// after a launch, resume, /compact or /clear (task 35, A3). It is the app's
// file, rewritten on every start; the same bytes keep the same trust.
func CodexHooks(binary string) []byte {
	data, _ := json.MarshalIndent(map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type":                   "command",
					"command":                SessionHookCommand(binary, KindCodex),
					"timeout":                CodexHookTimeoutSeconds,
					"additionalContextLimit": CodexHookContextLimit,
				}},
			}},
		},
	}, "", "  ")
	return append(data, '\n')
}

// shellQuote wraps s in single quotes for the shell Claude Code runs hook
// commands through, escaping any single quote already in s. The mate
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
