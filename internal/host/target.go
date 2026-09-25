package host

import (
	"context"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// StageTarget is the agent a host pane should attach to.
type StageTarget struct {
	Kind      string
	ID        string
	Session   string
	AgentName string
}

// Validate requires a Herdr session and agent name, each a single token so
// they can be argv and AppleScript string contents without quoting.
func (t StageTarget) Validate() error {
	if strings.TrimSpace(t.Session) == "" {
		return observability.NewError(observability.CodeUsage, "stage needs a herdr session")
	}
	if strings.TrimSpace(t.AgentName) == "" {
		return observability.NewError(observability.CodeUsage, "stage needs an agent name")
	}
	if strings.ContainsAny(t.Session, " \t\n\"'") || strings.ContainsAny(t.AgentName, " \t\n\"'") {
		return observability.NewError(observability.CodeUsage, "session and agent names must be a single token")
	}
	return nil
}

// StageHandle is the host pane this process created. Only this handle is
// eligible to be replaced on a later Stage call.
type StageHandle struct {
	PaneID string
}

// Host shows an agent in the captain's sibling pane.
type Host interface {
	// EnsureSplit creates an empty pane to the right of the console when
	// none exists, and remembers it as the stage. A foreign pane is left
	// alone. A split this process already created is a no-op.
	EnsureSplit(ctx context.Context) (StageHandle, error)
	Stage(ctx context.Context, target StageTarget) (StageHandle, error)
}

// errForeignPane is the refusal when a right-hand pane exists and is not
// the handle this process last created. The captain's nvim or shell stays.
func errForeignPane() error {
	return observability.NewError(
		observability.CodeStateConflict,
		"the pane to the right is not mate's stage; close it or leave it empty",
	)
}
