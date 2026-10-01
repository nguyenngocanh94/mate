package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

// Delivery is an explicit context-delivery strategy.
type Delivery string

const (
	// DeliveryAppendSystemPromptFile is the Claude default: pass the canonical
	// context path via --append-system-prompt-file. Claude 2.1.251 exits 1 on
	// a missing file.
	DeliveryAppendSystemPromptFile Delivery = "append_system_prompt_file"
	// DeliveryAppendSystemPrompt is the bounded Claude fallback. It inlines
	// file *contents* (never an @path token — Claude does not resolve those)
	// and exposes the entire context in the process table. That tradeoff is
	// why this is a fallback only.
	DeliveryAppendSystemPrompt Delivery = "append_system_prompt"
	// DeliveryInstructionFile is Codex 0.151.0 cwd discovery of
	// AGENTS.override.md (not tracked AGENTS.md).
	DeliveryInstructionFile Delivery = "instruction_file"
	// DeliveryCwdManual is "the harness already loads the manual from the
	// directory it runs in, so no flag carries it". Claude Code reads
	// `CLAUDE.md` in its cwd on its own, and mate's Mate cwd holds a
	// `CLAUDE.md` that is exactly `@AGENTS.md` (docs/mvp.md section 3), so
	// also passing that same manual as --append-system-prompt-file put it in
	// the model's context twice. A spec with this delivery carries no
	// context path and no context flag: the harness's Prepare wrote the
	// file the cwd loads, and its Build says so.
	DeliveryCwdManual Delivery = "cwd_manual"
)

// ParseDelivery rejects unknown strategies.
func ParseDelivery(s string) (Delivery, error) {
	switch Delivery(strings.TrimSpace(s)) {
	case DeliveryAppendSystemPromptFile, DeliveryAppendSystemPrompt, DeliveryInstructionFile:
		return Delivery(strings.TrimSpace(s)), nil
	case "":
		return "", fmt.Errorf("delivery: empty")
	default:
		return "", observability.NewError(observability.CodeUsage, fmt.Sprintf("unknown delivery %q", s))
	}
}

const (
	// DefaultMaxInlineBytes stays well under the Darwin ARG_MAX observed in
	// G1 (1048576; 900000-byte --append-system-prompt still exec'd).
	DefaultMaxInlineBytes = 100_000
)

var (
	// ErrContextRequired is returned when required context cannot be delivered.
	ErrContextRequired = errors.New("required context cannot be delivered")
	// ErrContextTooLarge is returned when context exceeds the delivery limit
	// or would be silently truncated.
	ErrContextTooLarge = errors.New("required context exceeds delivery limit")
)

// EnvVar is a harness-level environment assignment.
type EnvVar struct {
	Key   string
	Value string
}

// GeneratedFile is a file the launch spec depends on.
type GeneratedFile struct {
	Path string
	Role string
}

// LaunchSpec is a validated harness launch plan. Fields are unexported, so
// outside this package NewLaunchSpec is the only source of a startable spec,
// and it refuses an undeliverable required context. The zero value Go still
// allows is not startable, and ValidateRequiredContext refuses it.
type LaunchSpec struct {
	kind            string
	args            []string
	cwd             string
	env             []EnvVar
	contextFiles    []GeneratedFile
	delivery        Delivery
	contextPath     string
	contextRequired bool
	maxInlineBytes  int
	maxFileBytes    int
	taskPrompt      string
	model           string
	effort          Effort
	effortOmitted   bool
	notes           []string
	codexHome       string
	claudeConfigDir string
	unsetEnv        []string
	envKeys         []string
	checkContext    func(LaunchSpec) error
	screen          ScreenProfile
}

// Kind is the harness kind string (claude, codex).
func (s LaunchSpec) Kind() string { return s.kind }

// Screen is the profile the launched pane is read with.
func (s LaunchSpec) Screen() ScreenProfile { return s.screen }

// Args returns a copy of the extra harness argv (placed after `agent start --`).
func (s LaunchSpec) Args() []string {
	if s.args == nil {
		return nil
	}
	return append([]string(nil), s.args...)
}

// Cwd is the absolute directory the agent process must start in.
func (s LaunchSpec) Cwd() string { return s.cwd }

// Env returns a copy of harness env vars.
func (s LaunchSpec) Env() []EnvVar {
	if s.env == nil {
		return nil
	}
	return append([]EnvVar(nil), s.env...)
}

// ContextFiles returns a copy of generated files this spec depends on.
func (s LaunchSpec) ContextFiles() []GeneratedFile {
	if s.contextFiles == nil {
		return nil
	}
	return append([]GeneratedFile(nil), s.contextFiles...)
}

// Delivery is the explicit strategy.
func (s LaunchSpec) Delivery() Delivery { return s.delivery }

// ContextPath is the absolute required-context path.
func (s LaunchSpec) ContextPath() string { return s.contextPath }

// ContextRequired reports whether start must fail when context cannot be delivered.
func (s LaunchSpec) ContextRequired() bool { return s.contextRequired }

// MaxInlineBytes is the inline fallback budget (Claude).
func (s LaunchSpec) MaxInlineBytes() int { return s.maxInlineBytes }

// MaxFileBytes is the file/chain size budget (0 means strategy default).
func (s LaunchSpec) MaxFileBytes() int { return s.maxFileBytes }

// TaskPrompt is the Crew task to send through runtime.PromptAgent after the
// harness is ready. Empty means this launch has no first-task delivery (Mate).
func (s LaunchSpec) TaskPrompt() string { return s.taskPrompt }

// Model is the optional provider/model ref.
func (s LaunchSpec) Model() string { return s.model }

// Effort is the requested reasoning effort, passed or not.
func (s LaunchSpec) Effort() Effort { return s.effort }

// EffortOmitted reports that Effort was requested but left out of the argv
// because the harness does not take it.
func (s LaunchSpec) EffortOmitted() bool { return s.effortOmitted }

// CodexHome is the effective configured CODEX_HOME used while validating
// instruction discovery. Empty means the provider default is in effect.
func (s LaunchSpec) CodexHome() string { return s.codexHome }

// ClaudeConfigDir is the effective directory used to compute the Claude
// transcript search root. Whether CLAUDE_CONFIG_DIR is set in the pane is
// represented separately by Env and UnsetEnv.
func (s LaunchSpec) ClaudeConfigDir() string { return s.claudeConfigDir }

// EnvKeys are the variables, besides the identity keys, the launch was
// allowed to carry: the runtime's pane allowlist for this launch.
func (s LaunchSpec) EnvKeys() []string { return append([]string(nil), s.envKeys...) }

// UnsetEnv lists environment variables the runtime must remove from the
// pane's shell before starting the harness. This is distinct from setting an
// empty value: Claude treats its default account configuration as the
// variable being absent, and an inherited Herdr server may still carry an
// old value. Every launch also removes NestedSessionEnv, so a harness never
// mistakes itself for a child of whichever agent started the Herdr server.
func (s LaunchSpec) UnsetEnv() []string {
	out := append([]string(nil), s.unsetEnv...)
	for _, k := range NestedSessionEnv {
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// Notes are recorded tradeoffs (inline process-table exposure, unproven flags).
func (s LaunchSpec) Notes() []string {
	if s.notes == nil {
		return nil
	}
	return append([]string(nil), s.notes...)
}

// Startable is a shape check: kind, delivery, and cwd are set. It does not
// prove constructor provenance or that required context is deliverable -
// same-package code can assemble a startable-shaped spec, and a context file
// can disappear after construction. Consuming boundaries call
// ValidateRequiredContext, which is the actual delivery check.
func (s LaunchSpec) Startable() bool {
	return s.kind != "" && s.delivery != "" && s.cwd != ""
}

func (s LaunchSpec) codedRequired(msg string) error {
	return observability.WrapError(observability.CodeUsage, msg, ErrContextRequired)
}

func (s LaunchSpec) codedTooLarge(msg string) error {
	return observability.WrapError(observability.CodeUsage, msg, ErrContextTooLarge)
}

type deliveryLimit struct {
	max     int64
	note    string
	inArgv  bool
	argvRef string
}

func (s LaunchSpec) deliveryLimit() (deliveryLimit, error) {
	switch s.delivery {
	case DeliveryAppendSystemPrompt:
		max := int64(s.maxInlineBytes)
		if max <= 0 {
			max = DefaultMaxInlineBytes
		}
		return deliveryLimit{max: max, inArgv: true}, nil
	case DeliveryInstructionFile:
		max := int64(s.maxFileBytes)
		if max <= 0 {
			max = CodexDefaultMaxBytes
		}
		return deliveryLimit{
			max:  max,
			note: " (Codex 0.151.0 project_doc_max_bytes silently truncates the concatenated chain)",
		}, nil
	case DeliveryAppendSystemPromptFile:
		max := int64(s.maxFileBytes)
		if max <= 0 {
			max = math.MaxInt64
		}
		return deliveryLimit{max: max, argvRef: "--append-system-prompt-file"}, nil
	default:
		return deliveryLimit{}, s.codedRequired(fmt.Sprintf("unknown delivery %q", s.delivery))
	}
}

// ValidateRequiredContext is defence in depth. NewLaunchSpec already runs it;
// an undeliverable spec is not returned. Same-package tests may assemble a
// struct literal to prove the rule still holds if a spec is mutated by hand.
// A spec that is not startable (the zero value, or a hand-assembled one) is
// refused outright: nil from this validator must never bless a value the
// harness cannot start.
func (s LaunchSpec) ValidateRequiredContext() error {
	if !s.Startable() {
		return s.codedRequired("launch spec is not startable; only a constructor-produced spec carries a validated delivery")
	}
	if !filepath.IsAbs(s.cwd) {
		return s.codedRequired(fmt.Sprintf("cwd %s is relative; the agent's cwd must be absolute, not resolved against the Mate process cwd", s.cwd))
	}
	if !s.contextRequired {
		return nil
	}
	limit, err := s.deliveryLimit()
	if err != nil {
		return err
	}
	if s.contextPath == "" {
		return s.codedRequired("empty path")
	}
	if !filepath.IsAbs(s.contextPath) {
		return s.codedRequired(fmt.Sprintf("context path %s is relative and would resolve against the Mate process cwd, not the cwd the agent runs in", s.contextPath))
	}
	info, err := os.Stat(s.contextPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s.codedRequired(fmt.Sprintf("%s not found", s.contextPath))
		}
		return observability.WrapError(observability.CodeUsage, fmt.Sprintf("stat %s", s.contextPath), fmt.Errorf("%w: %w", ErrContextRequired, err))
	}
	if info.IsDir() {
		return s.codedRequired(fmt.Sprintf("%s is a directory", s.contextPath))
	}
	if !info.Mode().IsRegular() {
		return s.codedRequired(fmt.Sprintf("%s is not a regular file", s.contextPath))
	}
	size := info.Size()
	if size == 0 {
		return s.codedRequired(fmt.Sprintf("%s is empty", s.contextPath))
	}
	if size > limit.max {
		return s.codedTooLarge(fmt.Sprintf("%s is %d bytes, %s limit %d%s", s.contextPath, size, s.delivery, limit.max, limit.note))
	}
	if s.checkContext != nil {
		if err := s.checkContext(s); err != nil {
			return err
		}
	}
	if limit.argvRef != "" {
		ref, err := s.argvValue(limit.argvRef)
		if err != nil {
			return err
		}
		if ref != s.contextPath {
			return s.codedRequired(fmt.Sprintf("argv %s references %s, not the validated %s", limit.argvRef, ref, s.contextPath))
		}
	}
	if limit.inArgv {
		payload, err := s.argvValue("--append-system-prompt")
		if err != nil {
			return err
		}
		want, err := os.ReadFile(s.contextPath)
		if err != nil {
			return observability.WrapError(observability.CodeUsage, fmt.Sprintf("read %s", s.contextPath), fmt.Errorf("%w: %w", ErrContextRequired, err))
		}
		if payload != string(want) {
			return s.codedRequired(fmt.Sprintf("inline argv does not carry the contents of %s (argv %d bytes, file %d bytes)", s.contextPath, len(payload), len(want)))
		}
	}
	return nil
}

// argvValue returns the unique value of flag in the option region of Args
// (before `--`). Claude 2.1.251 ignores options after `--` and uses the last
// duplicate; validating the first occurrence would certify a context the CLI
// will not use. Duplicates and flags after the terminator are therefore
// rejected rather than guessed.
func (s LaunchSpec) argvValue(flag string) (string, error) {
	seenAfterTerminator := false
	inOptions := true
	var found []string
	for i, a := range s.args {
		if a == "--" {
			inOptions = false
			continue
		}
		if a != flag {
			continue
		}
		if !inOptions {
			seenAfterTerminator = true
			continue
		}
		if i+1 >= len(s.args) || s.args[i+1] == "--" {
			return "", s.codedRequired(fmt.Sprintf("%s is missing a value", flag))
		}
		found = append(found, s.args[i+1])
	}
	if seenAfterTerminator {
		return "", s.codedRequired(fmt.Sprintf("%s appears after -- and the target CLI ignores it", flag))
	}
	if len(found) == 0 {
		return "", s.codedRequired(fmt.Sprintf("argv carries no %s value", flag))
	}
	if len(found) > 1 {
		return "", s.codedRequired(fmt.Sprintf("duplicate %s (target CLI last-wins; mate rejects rather than guessing)", flag))
	}
	return found[0], nil
}

func filterLaunchEnv(vars []EnvVar, providerKeys []string) ([]EnvVar, error) {
	keys := append(config.IdentityEnvKeys(), providerKeys...)
	allow := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		allow[k] = struct{}{}
	}
	seen := make(map[string]struct{}, len(vars))
	out := make([]EnvVar, 0, len(vars))
	for _, v := range vars {
		key := strings.TrimSpace(v.Key)
		if key == "" {
			return nil, observability.NewError(observability.CodeUsage, "env key is empty")
		}
		if _, ok := allow[key]; !ok {
			return nil, observability.NewError(
				observability.CodeUsage,
				fmt.Sprintf("env key %q is not allowlisted; unknown keys are refused rather than dropped onto LaunchSpec.Env", key),
			)
		}
		if v.Value == "" {
			return nil, observability.NewError(observability.CodeUsage, fmt.Sprintf("env %s has an empty value; omit the key instead of injecting an empty assignment", key))
		}
		if _, dup := seen[key]; dup {
			return nil, observability.NewError(observability.CodeUsage, fmt.Sprintf("duplicate env key %s", key))
		}
		seen[key] = struct{}{}
		out = append(out, EnvVar{Key: key, Value: v.Value})
	}
	return out, nil
}
