package main

import (
	"context"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/watch"
)

// The observer's wiring (mvp.md task 18). It lives here, beside the
// Console's other seams, for the same reason they do: internal/query is
// file-only and internal/ui/console may reach neither Herdr nor
// internal/watch (its boundary test), so the one place that can hold both a
// live runtime and a snapshot is this package.

// consoleWatcher builds the observer for an open workspace.
//
// It opens its own *store.Workspace over the same directory rather than
// sharing the Console's. A Workspace caches workspace.yaml and rewrites that
// cache on every LoadConfig - which query.Load does on every refresh, on the
// UI goroutine - while the observer polls on its own. Two handles over the
// same files cost one more yaml read per poll and remove the shared mutable
// state entirely; the files themselves are read and appended under flock.
func consoleWatcher(dir string, deps spawn.Deps) (*watch.Watcher, error) {
	ws, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	return watch.New(ws, watch.Deps{
		Runtime: deps.Runtime,
		Handle:  consoleCrewHandle(ws, deps),
	}), nil
}

// consoleCrewHandle is watch.HandleFunc over spawn.CrewHandle: the recorded
// `crews/<id>.meta` resolved against the session Herdr currently answers
// for. The observer takes it as a function so it imports nothing that could
// start or stop an agent.
func consoleCrewHandle(ws *store.Workspace, deps spawn.Deps) watch.HandleFunc {
	return func(ctx context.Context, project, crew string) (runtime.AgentHandle, harness.Kind, error) {
		return spawn.CrewHandle(ctx, ws, deps, project, crew)
	}
}

// withCrewHealth puts the observer's latest readings into a snapshot
// query.Load built out of files alone.
//
// A crew with no reading keeps the Absent field Load gave it: "the observer
// has not looked at this crew" is a different sentence from any observation,
// and the row must not borrow a neighbour's or fall back to a zero value
// that would render as "agent gone".
func withCrewHealth(snap query.Snapshot, health map[watch.CrewRef]watch.Health) query.Snapshot {
	if len(health) == 0 {
		return snap
	}
	for p := range snap.Projects {
		project := &snap.Projects[p]
		for c := range project.Crews {
			crew := &project.Crews[c]
			h, ok := health[watch.CrewRef{Project: project.ProjectID, Crew: crew.CrewID}]
			if !ok {
				continue
			}
			crew.Health = query.KnownField(crewHealth(h))
		}
	}
	return snap
}

// crewHealth converts one observation into the Console's DTO. The composer
// word travels as itself: internal/ui/console may not import internal/send,
// and an unmeasured harness's state must render as whatever it was called
// rather than as a blank.
func crewHealth(h watch.Health) query.CrewHealth {
	return query.CrewHealth{
		AgentPresent: h.AgentPresent,
		Composer:     composerDTO(h.Composer),
		QuietFor:     h.QuietFor,
		ComposerFor:  h.ComposerFor,
		ObservedAt:   h.ObservedAt,
	}
}

func composerDTO(state send.ComposerState) query.CrewComposer {
	switch state {
	case send.StateEmpty:
		return query.ComposerEmpty
	case send.StatePending:
		return query.ComposerPending
	case send.StateBusy:
		return query.ComposerBusy
	default:
		return query.ComposerUnknown
	}
}
