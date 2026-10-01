// Package dispatch reads the captain's crew dispatch table,
// `.mate/crew-dispatch.json`: which harness, model and effort suit which
// kind of task. The schema is firstmate's (config/crew-dispatch.json):
//
//	{
//	  "rules": [
//	    {"when": "<a kind of task, in words>",
//	     "use": {"harness": "claude", "model": "haiku", "effort": "low"},
//	     "why": "<optional rationale>"}
//	  ],
//	  "default": {"harness": "codex", "model": "gpt-5.5", "effort": "medium"}
//	}
//
// `use` and `default` take one profile or an array of alternatives. The
// rules are natural language: the Mate matches a task to a rule with its
// own judgment and passes concrete --harness, --model and --effort to
// `mate crew spawn`. Nothing here matches a rule. This package only checks
// that every profile is one the app can launch, so a table with a typo is
// reported rather than silently launched around. A workspace without the
// file is governed by the built-in table (builtin.go).
package dispatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// FileName is the table's name under `.mate/`.
const FileName = "crew-dispatch.json"

// ErrInvalid wraps every reason a table is refused.
var ErrInvalid = errors.New("invalid crew dispatch table")

// Profile is one concrete launch: a harness, and optionally a model and an
// effort. Empty model or effort is the harness's own default.
type Profile struct {
	Harness harness.Kind
	Model   string
	Effort  harness.Effort
}

// Flags is the profile as `mate crew spawn` flags.
func (p Profile) Flags() string {
	parts := []string{"--harness", string(p.Harness)}
	if p.Model != "" {
		parts = append(parts, "--model", p.Model)
	}
	if p.Effort != "" {
		parts = append(parts, "--effort", string(p.Effort))
	}
	return strings.Join(parts, " ")
}

// Rule is one kind of task and the profiles that suit it.
type Rule struct {
	When string
	Use  []Profile
	Why  string
}

// Table is a loaded, checked dispatch table. Path is empty and BuiltIn true
// for the table compiled into mate (builtin.go).
type Table struct {
	Path    string
	BuiltIn bool
	Rules   []Rule
	Default []Profile
}

// Path is the table's path in a workspace whose `.mate/` directory is dir.
func Path(mateDir string) string { return filepath.Join(mateDir, FileName) }

// Load reads and checks the table at path; a profile's harness must be one
// of harnesses. ok is false, with no error, when there is no file: Resolve
// then falls back to the built-in table.
func Load(path string, harnesses harness.Registry) (Table, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Table{}, false, nil
	}
	if err != nil {
		return Table{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	t, err := parse(raw, harnesses)
	if err != nil {
		return Table{}, false, fmt.Errorf("%w %s: %v", ErrInvalid, path, err)
	}
	t.Path = path
	return t, true, nil
}

type rawProfile struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

type rawRule struct {
	When string          `json:"when"`
	Use  json.RawMessage `json:"use"`
	Why  string          `json:"why"`
}

type rawTable struct {
	Rules   []rawRule       `json:"rules"`
	Default json.RawMessage `json:"default"`
}

func parse(raw []byte, harnesses harness.Registry) (Table, error) {
	var rt rawTable
	if err := strictUnmarshal(raw, &rt); err != nil {
		return Table{}, err
	}
	var t Table
	for i, r := range rt.Rules {
		where := fmt.Sprintf("rule %d", i+1)
		if strings.TrimSpace(r.When) == "" {
			return Table{}, fmt.Errorf("%s: `when` is required", where)
		}
		if len(r.Use) == 0 {
			return Table{}, fmt.Errorf("%s: `use` is required", where)
		}
		use, err := profiles(r.Use, where, harnesses)
		if err != nil {
			return Table{}, err
		}
		t.Rules = append(t.Rules, Rule{When: strings.TrimSpace(r.When), Use: use, Why: strings.TrimSpace(r.Why)})
	}
	if len(rt.Default) > 0 && string(rt.Default) != "null" {
		def, err := profiles(rt.Default, "default", harnesses)
		if err != nil {
			return Table{}, err
		}
		t.Default = def
	}
	if len(t.Rules) == 0 && len(t.Default) == 0 {
		return Table{}, errors.New("no rules and no default")
	}
	return t, nil
}

// profiles reads one profile object or a non-empty array of them.
func profiles(raw json.RawMessage, where string, harnesses harness.Registry) ([]Profile, error) {
	var list []rawProfile
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		if err := strictUnmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("%s: %v", where, err)
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("%s: an empty profile array", where)
		}
	} else {
		var one rawProfile
		if err := strictUnmarshal(raw, &one); err != nil {
			return nil, fmt.Errorf("%s: %v", where, err)
		}
		list = []rawProfile{one}
	}
	out := make([]Profile, 0, len(list))
	for j, rp := range list {
		p, err := checkProfile(rp, harnesses)
		if err != nil {
			if len(list) > 1 {
				return nil, fmt.Errorf("%s profile %d: %v", where, j+1, err)
			}
			return nil, fmt.Errorf("%s: %v", where, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// checkProfile is every check a spawn would make, made at load: a known
// harness, one model name, and an effort that harness takes. A harness is
// known when it is registered.
func checkProfile(rp rawProfile, harnesses harness.Registry) (Profile, error) {
	if strings.TrimSpace(rp.Harness) == "" {
		return Profile{}, errors.New("`harness` is required")
	}
	kind, err := harnesses.Parse(strings.TrimSpace(rp.Harness))
	if err != nil {
		return Profile{}, err
	}
	model, err := harness.ParseModel(rp.Model)
	if err != nil {
		return Profile{}, err
	}
	effort, err := harness.ParseEffort(rp.Effort)
	if err != nil {
		return Profile{}, err
	}
	profile, err := harnesses.Lookup(kind)
	if err != nil {
		return Profile{}, err
	}
	if effort != "" && !profile.Info().SupportsEffort(effort) {
		return Profile{}, fmt.Errorf("%s does not take effort %s", kind, effort)
	}
	return Profile{Harness: kind, Model: model, Effort: effort}, nil
}

// strictUnmarshal refuses unknown fields: a misspelt key is a profile that
// silently loses an axis.
func strictUnmarshal(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after the JSON value")
	}
	return nil
}
