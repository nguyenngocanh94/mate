package harness

import (
	"errors"
	"fmt"
	"strings"
)

// These identity types lived in v1's internal/domain, which mate does not
// have. They are the only pieces of it the harness and runtime adapters
// used, so they live with the harness that gives them meaning.

var (
	// ErrEmptyValue is returned when a parsed enum is missing.
	ErrEmptyValue = errors.New("empty value")
	// ErrInvalidValue is returned when a parsed enum is not a known member.
	ErrInvalidValue = errors.New("invalid value")
)

// Kind names an agent harness. A kind is valid when a Registry holds a
// profile for it (registry.go); Registry.Parse reads one from text.
type Kind string

func (k Kind) String() string { return string(k) }

// AgentRole is the caller role. Agents never self-declare a different
// identity; the binary injects this via MATE_AGENT_ROLE.
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
		// Enum-shaped and caller-supplied (MATE_AGENT_ROLE): list what is
		// allowed rather than echoing a value that may be a secret.
		return "", fmt.Errorf("agent role must be one of user, mate, crew: %w", ErrInvalidValue)
	}
}

func (r AgentRole) String() string { return string(r) }
