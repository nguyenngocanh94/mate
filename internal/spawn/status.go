package spawn

import (
	"context"
	"fmt"

	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// State is what MateStatus concluded about a project's Mate.
type State string

const (
	// StateRunning is a recorded agent Herdr still has.
	StateRunning State = "running"
	// StateStopped is a project whose meta names no agent.
	StateStopped State = "stopped"
	// StateStale is a recorded agent Herdr does not have. The record is
	// wrong, not the runtime: a start may proceed over it.
	StateStale State = "stale"
)

// Status is one line about a project's Mate.
type Status struct {
	Project string
	State   State
	Agent   string
	Session string
	Pane    string
	Harness string
	// Observed is the Herdr agent status behind StateRunning.
	Observed runtime.AgentStatus
	// Detail is the Herdr observation in words, for the one-line report.
	Detail string
}

// Line is the single line `matev2 mate status` prints.
func (s Status) Line() string {
	return fmt.Sprintf("%s: %s (%s)", s.Project, s.State, s.Detail)
}

// MateStatus reads `mate.meta` and asks Herdr whether the agent it names is
// real. The meta is never the answer on its own.
func MateStatus(ctx context.Context, w *store.Workspace, deps Deps, project string) (Status, error) {
	if w == nil {
		return Status{}, errUsage("spawn: a workspace is required")
	}
	if deps.Runtime == nil {
		return Status{}, errUsage("spawn: a runtime adapter is required")
	}
	if err := store.ValidateProjectName(project); err != nil {
		return Status{}, err
	}
	meta, err := w.ReadMateMeta(project)
	if err != nil {
		return Status{}, err
	}
	out := Status{
		Project: project,
		Agent:   meta[MetaAgent],
		Session: meta[MetaSession],
		Pane:    meta[MetaPane],
		Harness: meta[MetaHarness],
	}
	if out.Agent == "" {
		out.State = StateStopped
		out.Detail = "no agent recorded"
		return out, nil
	}
	spec, err := deps.sessionSpec(w)
	if err != nil {
		return Status{}, err
	}
	session, running, err := deps.Runtime.LookupSession(ctx, spec)
	if err != nil {
		return Status{}, err
	}
	if !running {
		out.State = StateStale
		out.Detail = fmt.Sprintf("herdr session %s is not running, meta names agent %s", session.Name, out.Agent)
		return out, nil
	}
	observed, err := deps.Runtime.InspectAgent(ctx, runtime.AgentHandle{Session: session, Name: out.Agent})
	if err != nil {
		if runtime.IsAgentNotFound(err) {
			out.State = StateStale
			out.Detail = fmt.Sprintf("herdr session %s has no agent %s", session.Name, out.Agent)
			return out, nil
		}
		return Status{}, err
	}
	out.State = StateRunning
	out.Observed = observed.Status
	out.Detail = fmt.Sprintf("herdr agent %s is %s in pane %s", out.Agent, observed.Status, observed.Handle.Tab.PaneID)
	if observed.Handle.Tab.PaneID == "" {
		out.Detail = fmt.Sprintf("herdr agent %s is %s", out.Agent, observed.Status)
	}
	return out, nil
}
