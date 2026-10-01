package dispatch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
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
	tbl, ok, err := Load(p, catalog.Default())
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if len(tbl.Rules) != 2 || tbl.Rules[0].Why != "cheap and fast" || len(tbl.Rules[1].Use) != 2 {
		t.Fatalf("rules = %+v", tbl.Rules)
	}
	if got := tbl.Rules[0].Use[0]; got != (Profile{Harness: claude.KindClaude, Model: "haiku", Effort: harness.EffortLow}) {
		t.Fatalf("first profile = %+v", got)
	}
	if len(tbl.Default) != 1 || tbl.Default[0].Harness != codex.KindCodex || tbl.Default[0].Model != "" {
		t.Fatalf("default = %+v", tbl.Default)
	}
}

// No file is no table: spawns go on as before. It is not an error.
func TestAMissingTableIsNotAnError(t *testing.T) {
	_, ok, err := Load(filepath.Join(t.TempDir(), "crew-dispatch.json"), catalog.Default())
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
			_, _, err := Load(writeTable(t, body), catalog.Default())
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
	p := Profile{Harness: claude.KindClaude, Model: "haiku", Effort: harness.EffortLow}
	if got := p.Flags(); got != "--harness claude --model haiku --effort low" {
		t.Fatalf("flags = %q", got)
	}
	if got := (Profile{Harness: codex.KindCodex}).Flags(); got != "--harness codex" {
		t.Fatalf("flags = %q", got)
	}
}

// The built-in table is the captain's five rules, in their order, each with
// a Claude and a Codex profile of the same weight.
func TestTheBuiltInTableIsTheCaptains(t *testing.T) {
	tbl, err := Resolve(t.TempDir(), catalog.Default())
	if err != nil || !tbl.BuiltIn || tbl.Path != "" {
		t.Fatalf("Resolve without a file: %+v %v", tbl, err)
	}
	flags := func(ps []Profile) []string {
		out := make([]string, len(ps))
		for i, p := range ps {
			out[i] = p.Flags()
		}
		return out
	}
	want := [][]string{
		{"--harness claude --model sonnet --effort medium", "--harness codex --model gpt-6-luna --effort medium"},
		{"--harness claude --model sonnet --effort high", "--harness codex --model gpt-6-luna --effort high",
			"--harness claude --model opus --effort medium", "--harness codex --model gpt-5.6-terra --effort medium"},
		{"--harness claude --model opus --effort high", "--harness codex --model gpt-5.6-terra --effort high"},
		{"--harness claude --model opus --effort high", "--harness codex --model gpt-6-sol --effort high"},
		{"--harness claude --model sonnet --effort medium", "--harness codex --model gpt-6-luna --effort medium"},
	}
	if len(tbl.Rules) != len(want) {
		t.Fatalf("%d rules, want %d", len(tbl.Rules), len(want))
	}
	for i, r := range tbl.Rules {
		if got := flags(r.Use); strings.Join(got, "|") != strings.Join(want[i], "|") {
			t.Errorf("rule %d (%s) = %v, want %v", i+1, r.When, got, want[i])
		}
	}
	for i, prefix := range []string{"A ship that is a small", "A ship that is a medium", "A ship that is a large", "A scout that", "A light scout"} {
		if !strings.HasPrefix(tbl.Rules[i].When, prefix) {
			t.Errorf("rule %d = %q, want it to start %q", i+1, tbl.Rules[i].When, prefix)
		}
	}
}

// A harness is known when it is registered: the same table is refused by a
// registry that lacks one of its harnesses, the built-in one included.
func TestTablesAreCheckedAgainstTheRegistry(t *testing.T) {
	onlyCodex, err := harness.NewRegistry(nil, codex.Codex{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(t.TempDir(), onlyCodex); !errors.Is(err, ErrInvalid) {
		t.Fatalf("built-in table naming claude under a registry without it: err = %v, want ErrInvalid", err)
	}
	p := writeTable(t, `{"default": {"harness": "claude"}}`)
	if _, _, err := Load(p, onlyCodex); !errors.Is(err, ErrInvalid) {
		t.Fatalf("table naming claude under a registry without it: err = %v, want ErrInvalid", err)
	}
	if _, _, err := Load(p, catalog.Default()); err != nil {
		t.Fatalf("the same table under the catalog: %v", err)
	}
}

// A workspace file replaces the built-in table whole; a malformed one is
// an error, not a fall back.
func TestAWorkspaceTableReplacesTheBuiltIn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte(`{"default": {"harness": "claude"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tbl, err := Resolve(dir, catalog.Default())
	if err != nil || tbl.BuiltIn || len(tbl.Rules) != 0 || tbl.Path != Path(dir) {
		t.Fatalf("Resolve = %+v, %v", tbl, err)
	}
	if err := os.WriteFile(Path(dir), []byte(`{"rules": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(dir, catalog.Default()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed table: err = %v, want ErrInvalid", err)
	}
}

func TestDefaultForPrefersTheWorkspaceHarness(t *testing.T) {
	tbl, _ := Resolve(t.TempDir(), catalog.Default())
	for kind, want := range map[string]string{
		"codex":  "--harness codex --model gpt-6-luna --effort high",
		"claude": "--harness claude --model sonnet --effort high",
		"other":  "--harness codex --model gpt-6-luna --effort high",
	} {
		p, ok := tbl.DefaultFor(kind)
		if !ok || p.Flags() != want {
			t.Errorf("DefaultFor(%s) = %s, want %s", kind, p.Flags(), want)
		}
	}
	if _, ok := (Table{}).DefaultFor("codex"); ok {
		t.Error("a table with no default returned one")
	}
}
