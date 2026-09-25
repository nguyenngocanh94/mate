package harness

import (
	"fmt"
	"strings"
)

// A launch profile is the two axes besides the harness a Crew is spawned
// with: the model and the reasoning effort. Both are concrete values the
// caller already chose - the Mate reads .mate/crew-dispatch.json with its
// own judgment, as firstmate does - and both are optional: empty means the
// harness's own default, and no flag is passed.

// Effort is a reasoning-effort level.
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// Efforts are every level any harness takes, lowest first.
var Efforts = []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}

// ParseEffort accepts "" (the harness default) or one of Efforts.
func ParseEffort(s string) (Effort, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}
	for _, e := range Efforts {
		if string(e) == s {
			return e, nil
		}
	}
	return "", fmt.Errorf("effort %q: want one of low, medium, high, xhigh, max", s)
}

// SupportsEffort reports whether the harness takes e as a launch flag.
// Claude takes all five (claude 2.1.282 --help). Codex's
// model_reasoning_effort advertises low through xhigh for its catalogue
// models and max for some only (firstmate's codex record, codex-cli 0.142.1
// and 0.153.4), so max is not passed to Codex.
func (k Kind) SupportsEffort(e Effort) bool {
	switch k {
	case KindClaude:
		return e != ""
	case KindCodex:
		return e != "" && e != EffortMax
	}
	return false
}

// ParseModel accepts "" (the harness default) or one model name as the
// harness spells it - an alias like "opus" or a full id like "gpt-5.5". It
// becomes one argv element, so anything that could be read as a flag or a
// config override is refused.
func ParseModel(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if strings.HasPrefix(s, "-") || strings.ContainsAny(s, " \t\n\"'=\\") {
		return "", fmt.Errorf("model %q: want one model name, like opus or gpt-5.5", s)
	}
	return s, nil
}

// profileArgs are a launch's model and effort flags, and whether a
// requested effort was left out because the harness does not take it.
func profileArgs(k Kind, model string, effort Effort) (args []string, effortOmitted bool) {
	switch k {
	case KindClaude:
		if model != "" {
			args = append(args, "--model", model)
		}
		if k.SupportsEffort(effort) {
			args = append(args, "--effort", string(effort))
		}
	case KindCodex:
		if model != "" {
			args = append(args, "-m", model)
		}
		if k.SupportsEffort(effort) {
			args = append(args, "-c", `model_reasoning_effort="`+string(effort)+`"`)
		}
	}
	return args, effort != "" && !k.SupportsEffort(effort)
}
