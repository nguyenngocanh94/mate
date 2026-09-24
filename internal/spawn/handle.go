package spawn

import (
	"context"
	"fmt"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Resolving a recorded `.meta` into the live Herdr handle a pane operation
// needs. StopMate, StopCrew and MateStatus each did this inline for their
// own step; the console's message box (mvp.md task 15) and `mate send`
// need exactly the same resolution for a pane they only want to type into,
// so it is spelled once here.
//
// A handle is not a liveness claim. It is the recorded identity plus the
// session Herdr currently answers for; whether an agent is behind it is
// what the caller's own next call establishes (mvp.md decision 8).

// MateHandle resolves the Mate pane of one project.
func MateHandle(ctx context.Context, w *store.Workspace, deps Deps, project string) (runtime.AgentHandle, harness.Kind, error) {
	if err := store.ValidateProjectName(project); err != nil {
		return runtime.AgentHandle{}, "", err
	}
	meta, err := w.ReadMateMeta(project)
	if err != nil {
		return runtime.AgentHandle{}, "", err
	}
	return agentHandle(ctx, w, deps, meta, project, MateTabLabel,
		fmt.Sprintf("the Mate of %s is not running; %s names no agent", project, w.MateMeta(project)))
}

// CrewHandle resolves one crew's pane.
func CrewHandle(ctx context.Context, w *store.Workspace, deps Deps, project, crew string) (runtime.AgentHandle, harness.Kind, error) {
	if err := store.ValidateProjectName(project); err != nil {
		return runtime.AgentHandle{}, "", err
	}
	if err := store.ValidateCrewID(crew); err != nil {
		return runtime.AgentHandle{}, "", err
	}
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return runtime.AgentHandle{}, "", err
	}
	return agentHandle(ctx, w, deps, meta, crew, CrewTabLabelPrefix+crew,
		fmt.Sprintf("crew %s of %s is not running; %s names no agent", crew, project, w.CrewMeta(project, crew)))
}

func agentHandle(ctx context.Context, w *store.Workspace, deps Deps, meta map[string]string, rawID, tabLabel, stopped string) (runtime.AgentHandle, harness.Kind, error) {
	if w == nil {
		return runtime.AgentHandle{}, "", errUsage("spawn: a workspace is required")
	}
	if deps.Runtime == nil {
		return runtime.AgentHandle{}, "", errUsage("spawn: a runtime adapter is required")
	}
	if meta[MetaAgent] == "" || meta[MetaPane] == "" {
		// One without the other is a half-written record, and neither is
		// something to type into: StopMate/StopCrew drop both keys together.
		return runtime.AgentHandle{}, "", observability.NewError(observability.CodeStateConflict, stopped)
	}
	spec, err := deps.sessionSpec(w)
	if err != nil {
		return runtime.AgentHandle{}, "", err
	}
	session, running, err := deps.Runtime.LookupSession(ctx, spec)
	if err != nil {
		return runtime.AgentHandle{}, "", err
	}
	if !running {
		return runtime.AgentHandle{}, "", observability.NewError(observability.CodeRuntimeUnavailable,
			fmt.Sprintf("the Herdr session %s is not running; nothing can be typed into %s", spec.Name, meta[MetaAgent]))
	}
	kind, _ := harness.ParseKind(meta[MetaHarness])
	return runtime.AgentHandle{
		Session: session,
		Name:    meta[MetaAgent],
		RawID:   rawID,
		Kind:    kind,
		Tab: runtime.TabHandle{
			Session:     session,
			WorkspaceID: meta[MetaWorkspace],
			TabID:       meta[MetaTab],
			PaneID:      meta[MetaPane],
			Label:       tabLabel,
		},
	}, kind, nil
}
