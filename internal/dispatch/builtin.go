package dispatch

import "fmt"

// BuiltInJSON is the table a workspace uses until the captain writes its
// own `.mate/crew-dispatch.json`, which then replaces it whole. It is the
// captain's (2026-09-26): each rule names a Claude profile and the Codex
// one of the same weight, and the Mate picks the less loaded. Codex names
// are its catalogue slugs (codex models_cache.json, 2026-09-26): luna is
// gpt-6-luna, terra exists only as gpt-5.6-terra, sol is gpt-6-sol.
//
// The three change rules say "ship" and the two scout rules say "scout", so
// the first rule that fits is the only one: a ship never matches a scout
// rule, and the other way round.
const BuiltInJSON = `{
  "rules": [
    {
      "when": "A ship that is a small change: a narrow edit in one or a few files with a clear, low-risk outcome, such as a bug fix whose cause is known, a copy or config tweak, or a rote rename.",
      "use": [
        { "harness": "claude", "model": "sonnet", "effort": "medium" },
        { "harness": "codex", "model": "gpt-6-luna", "effort": "medium" }
      ],
      "why": "A mid-size model at medium effort is enough when the change is narrow and the outcome is clear."
    },
    {
      "when": "A ship that is a medium change: a feature or fix across several files, with some design choices but a contained blast radius.",
      "use": [
        { "harness": "claude", "model": "sonnet", "effort": "high" },
        { "harness": "codex", "model": "gpt-6-luna", "effort": "high" },
        { "harness": "claude", "model": "opus", "effort": "medium" },
        { "harness": "codex", "model": "gpt-5.6-terra", "effort": "medium" }
      ],
      "why": "Either a mid-size model thinking harder or a large model at medium effort; pick the less loaded."
    },
    {
      "when": "A ship that is a large change: a big task, a cross-cutting refactor, a migration, or anything whose mistakes would reach many callers, users or data.",
      "use": [
        { "harness": "claude", "model": "opus", "effort": "high" },
        { "harness": "codex", "model": "gpt-5.6-terra", "effort": "high" }
      ],
      "why": "Wide impact needs the strongest coding profile."
    },
    {
      "when": "A scout that combines reading code with outside research, such as docs, libraries or prior art, to reach a recommendation.",
      "use": [
        { "harness": "claude", "model": "opus", "effort": "high" },
        { "harness": "codex", "model": "gpt-6-sol", "effort": "high" }
      ],
      "why": "Research across code and sources needs the strongest reasoning profile."
    },
    {
      "when": "A light scout: a quick look at the code, or one narrow question with a short answer.",
      "use": [
        { "harness": "claude", "model": "sonnet", "effort": "medium" },
        { "harness": "codex", "model": "gpt-6-luna", "effort": "medium" }
      ],
      "why": "A narrow question does not need a large model."
    }
  ],
  "default": [
    { "harness": "codex", "model": "gpt-6-luna", "effort": "high" },
    { "harness": "claude", "model": "sonnet", "effort": "high" }
  ]
}
`

// builtIn is BuiltInJSON, checked once. A built-in table that does not
// parse is a bug in this file, caught by its test, never a runtime state.
var builtIn = func() Table {
	t, err := parse([]byte(BuiltInJSON))
	if err != nil {
		panic(fmt.Sprintf("dispatch: the built-in table is invalid: %v", err))
	}
	t.BuiltIn = true
	return t
}()

// Resolve is the table that governs a workspace whose `.mate/` directory
// is mateDir: its own file when there is one, else the built-in table. A
// malformed file is an error (ErrInvalid), never a fall back to the
// built-in: the captain meant their table.
func Resolve(mateDir string) (Table, error) {
	t, ok, err := Load(Path(mateDir))
	if err != nil {
		return Table{}, err
	}
	if !ok {
		return builtIn, nil
	}
	return t, nil
}

// DefaultFor is the default profile for a spawn that named no harness: the
// first default alternative on the workspace's default harness, else the
// first alternative. ok is false when the table has no default.
func (t Table) DefaultFor(kind string) (Profile, bool) {
	if len(t.Default) == 0 {
		return Profile{}, false
	}
	for _, p := range t.Default {
		if string(p.Harness) == kind {
			return p, true
		}
	}
	return t.Default[0], true
}
