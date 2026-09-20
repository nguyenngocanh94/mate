package main

import (
	"context"
	"errors"
	"sort"

	"github.com/nguyenngocanh94/matev2/internal/crewstate"
	"github.com/nguyenngocanh94/matev2/internal/db"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/timeline"
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
//
// It records no timeline: a database handle holds an advisory lock for as
// long as it is open, so the one place that opens it is the one place that
// can close it. cmdConsole uses consoleWatcherWithTimeline for that reason.
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

// consoleWatcherWithTimeline is consoleWatcher plus the M5 timeline: the
// observer becomes the single writer of `.matev2/matev2.db`, recording at the
// end of every poll what the files say.
//
// The returned handle owns the database and its advisory lock; the
// caller closes it when the workspace closes. A workspace whose database is
// already locked by another console is not an error here: that console is the
// writer, this one still observes, and the timeline is recorded once rather
// than twice (internal/db.ErrLocked).
func consoleWatcherWithTimeline(dir string, deps spawn.Deps) (*watch.Watcher, *db.DB, error) {
	ws, err := store.Open(dir)
	if err != nil {
		return nil, nil, err
	}
	var watcher *watch.Watcher
	handle, err := db.Open(ws)
	if err != nil {
		if !errors.Is(err, db.ErrLocked) {
			return nil, nil, err
		}
		watcher = watch.New(ws, watch.Deps{Runtime: deps.Runtime, Handle: consoleCrewHandle(ws, deps)})
		return watcher, nil, nil
	}
	ingest := timeline.New(ws, handle, timeline.Deps{
		SessionRef: consoleSessionRef(ws, deps),
		// The readings of the round that has just finished: Poll swaps the
		// health snapshot in before it calls the ingest, and the closure
		// reads `watcher` at call time, after it has been assigned.
		Health: func() []timeline.HealthReading {
			if watcher == nil {
				return nil
			}
			return healthReadings(watcher.Snapshot())
		},
	})
	watcher = watch.New(ws, watch.Deps{
		Runtime:  deps.Runtime,
		Handle:   consoleCrewHandle(ws, deps),
		Timeline: ingest,
	})
	return watcher, handle, nil
}

// consoleSessionRef is timeline.SessionRefFunc over the Herdr adapter: the
// `agent_session.value` Herdr records for a crew's agent, which for Codex is
// the rollout id and for Claude is nothing at all (measured 2026-09-20,
// Herdr 0.8.2). It lives here for the reason consoleCrewHandle does:
// internal/timeline may not reach Herdr.
func consoleSessionRef(ws *store.Workspace, deps spawn.Deps) timeline.SessionRefFunc {
	return func(ctx context.Context, project, crew string) (string, error) {
		handle, _, err := spawn.CrewHandle(ctx, ws, deps, project, crew)
		if err != nil {
			return "", err
		}
		observed, err := deps.Runtime.InspectAgent(ctx, handle)
		if err != nil {
			return "", err
		}
		return observed.SessionRef, nil
	}
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

// healthReadings turns the observer's snapshot into what the timeline ingest
// reads. It lives here rather than in internal/watch for the reason
// consoleCrewHandle does: the observer's own imports stay to what it needs to
// watch, and this package is the one place that already holds both halves.
//
// The composer word travels as crewstate's, the spelling `matev2 state` and
// the console already share, so internal/timeline needs neither internal/send
// nor the Herdr adapter it pulls in.
func healthReadings(snapshot map[watch.CrewRef]watch.Health) []timeline.HealthReading {
	out := make([]timeline.HealthReading, 0, len(snapshot))
	for ref, h := range snapshot {
		out = append(out, timeline.HealthReading{
			Project:      ref.Project,
			Crew:         ref.Crew,
			AgentPresent: h.AgentPresent,
			Composer:     composerWord(h.Composer),
			At:           h.ObservedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Crew < out[j].Crew
	})
	return out
}

func composerWord(state send.ComposerState) crewstate.Composer {
	switch state {
	case send.StateEmpty:
		return crewstate.ComposerEmpty
	case send.StatePending:
		return crewstate.ComposerPending
	case send.StateBusy:
		return crewstate.ComposerBusy
	default:
		return crewstate.ComposerUnknown
	}
}
