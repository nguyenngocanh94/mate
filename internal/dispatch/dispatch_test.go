package dispatch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

func writeTable(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "crew-dispatch.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// firstmate's schema: rules with a natural-language `when`, a `use` that is
// one profile or an array of alternatives, an optional `why`, and an
// optional `default` of the same shape.
func TestLoadReadsFirstmatesSchema(t *testing.T) {
	p := writeTable(t, `{
  "rules": [
    {"when": "a trivial mechanical edit", "use": {"harness": "claude", "model": "haiku", "effort": "low"}, "why": "cheap and fast"},
    {"when": "a big or ambiguous feature", "use": [
      {"harness": "claude", "model": "opus", "effort": "high"},
      {"harness": "codex", "model": "gpt-5.5", "effort": "xhigh"}
    ]}
  ],
  "default": {"harness": "codex", "effort": "medium"}
}`)
	tbl, ok, err := Load(p)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if len(tbl.Rules) != 2 || tbl.Rules[0].Why != "cheap and fast" || len(tbl.Rules[1].Use) != 2 {
		t.Fatalf("rules = %+v", tbl.Rules)
	}
	if got := tbl.Rules[0].Use[0]; got != (Profile{Harness: harness.KindClaude, Model: "haiku", Effort: harness.EffortLow}) {
		t.Fatalf("first profile = %+v", got)
	}
	if len(tbl.Default) != 1 || tbl.Default[0].Harness != harness.KindCodex || tbl.Default[0].Model != "" {
		t.Fatalf("default = %+v", tbl.Default)
	}
}

// No file is no table: spawns go on as before. It is not an error.
func TestAMissingTableIsNotAnError(t *testing.T) {
	_, ok, err := Load(filepath.Join(t.TempDir(), "crew-dispatch.json"))
	if ok || err != nil {
		t.Fatalf("Load of a missing file: ok=%v err=%v", ok, err)
	}
}

// A malformed table is reported and corrected, never selected around.
func TestLoadRefusesAMalformedTable(t *testing.T) {
	for name, body := range map[string]string{
		"not json":           `{"rules": [`,
		"rule without when":  `{"rules": [{"use": {"harness": "codex"}}]}`,
		"rule without use":   `{"rules": [{"when": "x"}]}`,
		"empty use array":    `{"rules": [{"when": "x", "use": []}]}`,
		"profile no harness": `{"rules": [{"when": "x", "use": {"model": "opus"}}]}`,
		"unknown harness":    `{"rules": [{"when": "x", "use": {"harness": "gemini"}}]}`,
		"bad effort":         `{"rules": [{"when": "x", "use": {"harness": "claude", "effort": "hgih"}}]}`,
		"unsupported effort": `{"rules": [{"when": "x", "use": {"harness": "codex", "effort": "max"}}]}`,
		"flag-like model":    `{"rules": [{"when": "x", "use": {"harness": "codex", "model": "-c"}}]}`,
		"unknown field":      `{"rules": [{"when": "x", "use": {"harness": "codex", "modle": "gpt-5.5"}}]}`,
		"empty table":        `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := Load(writeTable(t, body))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Load accepted a malformed table (err=%v)", err)
			}
			if !strings.Contains(err.Error(), "crew-dispatch.json") {
				t.Fatalf("the error does not name the file: %v", err)
			}
		})
	}
}

func TestProfileFlags(t *testing.T) {
	p := Profile{Harness: harness.KindClaude, Model: "haiku", Effort: harness.EffortLow}
	if got := p.Flags(); got != "--harness claude --model haiku --effort low" {
		t.Fatalf("flags = %q", got)
	}
	if got := (Profile{Harness: harness.KindCodex}).Flags(); got != "--harness codex" {
		t.Fatalf("flags = %q", got)
	}
}
