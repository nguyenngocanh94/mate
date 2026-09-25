package main

import (
	"context"
	"fmt"

	"github.com/nguyenngocanh94/mate/internal/host"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// consoleStage is the host-pane attach seam (docs/mvp.md M10). A nil Host
// yields a nil StageFunc, which the Console reads as "no next pane".
func consoleStage(ws *store.Workspace, h host.Host) console.StageFunc {
	if h == nil {
		return nil
	}
	return func(ctx context.Context, target console.StageTarget) error {
		ref, err := stageRef(ws, target)
		if err != nil {
			return err
		}
		_, err = h.Stage(ctx, host.StageTarget{
			Kind:      string(target.Kind),
			ID:        target.ID,
			Session:   ref.HerdrSession,
			AgentName: ref.AgentName,
		})
		return err
	}
}

// stageRef resolves the Herdr identity of the agent a target names out
// of its `.meta` - `mate.meta` for a Mate, `crews/<id>.meta` for a crew -
// which is the only record there is (internal/spawn/doc.go). The meta is
// re-read on every open rather than captured from the Console's snapshot:
// the snapshot can be a refresh old, and attaching a pane to an agent
// nobody owns is exactly the failure that record is a hint about, not
// proof of.
func stageRef(ws *store.Workspace, target console.StageTarget) (runtime.AgentSessionRef, error) {
	if target.ProjectID == "" {
		return runtime.AgentSessionRef{}, observability.NewError(observability.CodeUsage,
			"the stage target names no Project")
	}
	var (
		meta    map[string]string
		err     error
		stopped error
	)
	switch target.Kind {
	case console.StageMate:
		meta, err = ws.ReadMateMeta(target.ProjectID)
		stopped = errMateStopped(target.ProjectID)
	case console.StageCrew:
		if target.ID == "" {
			return runtime.AgentSessionRef{}, observability.NewError(observability.CodeUsage,
				"the stage target names no crew")
		}
		meta, err = ws.ReadCrewMeta(target.ProjectID, target.ID)
		stopped = errCrewStopped(target.ProjectID, target.ID)
	default:
		return runtime.AgentSessionRef{}, observability.NewError(observability.CodeUsage,
			fmt.Sprintf("there is no stage target of kind %q", target.Kind))
	}
	if err != nil {
		return runtime.AgentSessionRef{}, err
	}
	if meta[spawn.MetaAgent] == "" || meta[spawn.MetaPane] == "" {
		// An agent with no pane is the same stopped record seen from the
		// other side: StopMate and StopCrew drop both keys together, so one
		// without the other is a half-written meta, and neither is
		// something to attach a pane to.
		return runtime.AgentSessionRef{}, stopped
	}
	session := meta[spawn.MetaSession]
	if session == "" {
		session = ws.Session()
	}
	return runtime.AgentSessionRef{HerdrSession: session, AgentName: meta[spawn.MetaAgent]}, nil
}

// errMateStopped is the stopped state, not a failure: a Project whose Mate
// has never been started (or has been stopped) has nothing to show, and
// the reader's next move is the 's' key. It is coded CodeStateConflict
// rather than CodeUnknown so nothing downstream treats it as a transport
// fault.
func errMateStopped(project string) error {
	return observability.NewError(observability.CodeStateConflict,
		fmt.Sprintf("the Mate of %s is stopped; press s to start it", project))
}

// errCrewStopped is the crew counterpart. A stopped crew is not restarted
// from the Console - the Mate spawns a new one - so there is no key to
// offer, only the record that remains.
func errCrewStopped(project, crew string) error {
	return observability.NewError(observability.CodeStateConflict,
		fmt.Sprintf("crew %s of %s is stopped; its record stays in crews/%s", crew, project, crew))
}
