package harness

import (
	"errors"
	"fmt"
	"strings"
)

// These identity types lived in v1's internal/domain, which matev2 does not
// have. They are the only pieces of it the harness and runtime adapters
// used, so they live with the harness that gives them meaning.

var (
	// ErrEmptyValue is returned when a parsed enum is missing.
	ErrEmptyValue = errors.New("empty value")
	// ErrInvalidValue is returned when a parsed enum is not a known member.
	ErrInvalidValue = errors.New("invalid value")
)

// Kind is a supported agent harness. matev2 launches the two v1 probed:
// Claude Code and Codex CLI.
type Kind string

const (
	KindClaude Kind = "claude"
	KindCodex  Kind = "codex"
)

// ParseKind rejects empty and unknown kinds.
func ParseKind(s string) (Kind, error) {
	switch Kind(strings.ToLower(strings.TrimSpace(s))) {
	case KindClaude:
		return KindClaude, nil
	case KindCodex:
		return KindCodex, nil
	case "":
		return "", fmt.Errorf("harness kind: %w", ErrEmptyValue)
	default:
		return "", fmt.Errorf("harness kind %q: %w", s, ErrInvalidValue)
	}
}

func (k Kind) String() string { return string(k) }

// ModelRef names a provider/model pair. Empty is allowed and means "harness
// default". A provider without a model is rejected.
type ModelRef struct {
	Provider string
	Model    string
}

// ParseModelRef accepts an empty pair (harness default) or a provider+model.
func ParseModelRef(provider, model string) (ModelRef, error) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" && model == "" {
		return ModelRef{}, nil
	}
	if provider == "" || model == "" {
		return ModelRef{}, fmt.Errorf("model ref: provider and model must both be set or both empty")
	}
	return ModelRef{Provider: provider, Model: model}, nil
}

func (m ModelRef) String() string {
	if m.Provider == "" {
		return ""
	}
	return m.Provider + "/" + m.Model
}

// IsZero reports whether this is the harness-default (unset) ref.
func (m ModelRef) IsZero() bool {
	return m.Provider == "" && m.Model == ""
}

// AgentRole is the caller role. Agents never self-declare a different
// identity; the binary injects this via MATEV2_AGENT_ROLE.
type AgentRole string

const (
	RoleUser AgentRole = "user"
	RoleMate AgentRole = "mate"
	RoleCrew AgentRole = "crew"
)

// ParseAgentRole rejects empty and unknown roles.
func ParseAgentRole(s string) (AgentRole, error) {
	switch AgentRole(strings.ToLower(strings.TrimSpace(s))) {
	case RoleUser:
		return RoleUser, nil
	case RoleMate:
		return RoleMate, nil
	case RoleCrew:
		return RoleCrew, nil
	case "":
		return "", fmt.Errorf("agent role: %w", ErrEmptyValue)
	default:
		// Enum-shaped and caller-supplied (MATEV2_AGENT_ROLE): list what is
		// allowed rather than echoing a value that may be a secret.
		return "", fmt.Errorf("agent role must be one of user, mate, crew: %w", ErrInvalidValue)
	}
}

func (r AgentRole) String() string { return string(r) }
