package main

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/watch"
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
		Session: consoleSession(ws, deps),
	}), nil
}

// consoleWatcherWithTimeline is consoleWatcher plus the M5 timeline: the
// observer becomes the single writer of `.mate/mate.db`, recording at the
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
		watcher = watch.New(ws, watch.Deps{
			Runtime: deps.Runtime,
			Handle:  consoleCrewHandle(ws, deps),
			Session: consoleSession(ws, deps),
		})
		return watcher, nil, nil
	}
	ingest := timeline.New(ws, handle, timeline.Deps{
		SessionRef: consoleSessionRef(ws, deps),
		Harnesses:  deps.Harnesses,
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
		Session:  consoleSession(ws, deps),
		Timeline: ingest,
		// The same *timeline.Ingester also implements watch.BudgetChecker
		// (mvp.md M5 task 27): it already holds the writable db.DB and the
		// workspace handle a budget check needs, and it is the same single
		// writer the rest of M5 keeps to one.
		Budget: ingest,
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
// for, with the screen profile of the harness it records. The observer takes
// it as a function so it imports nothing that could start or stop an agent.
func consoleCrewHandle(ws *store.Workspace, deps spawn.Deps) watch.HandleFunc {
	return func(ctx context.Context, project, crew string) (runtime.AgentHandle, harness.ScreenProfile, error) {
		handle, kind, err := spawn.CrewHandle(ctx, ws, deps, project, crew)
		if err != nil {
			return runtime.AgentHandle{}, nil, err
		}
		screens, err := screenOf(deps, kind)
		if err != nil {
			return runtime.AgentHandle{}, nil, err
		}
		return handle, screens, nil
	}
}

// consoleSession is watch.SessionFunc over the same session check the stage
// preflight uses: the observer hears that Herdr is gone whether or not any
// crew is open to resolve a handle for.
func consoleSession(ws *store.Workspace, deps spawn.Deps) watch.SessionFunc {
	return func(ctx context.Context) error {
		return herdrSession(ctx, ws, deps)
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

// withRuntimeNotice puts the observer's standing word about the terminal
// runtime into a snapshot query.Load built out of files alone. The Console
// cannot ask Herdr itself (its boundary test), so an empty notice is the
// only way it can tell "nothing was observed" apart from "nothing could be
// observed"; without it every file-read row keeps looking fresh while Herdr
// is down.
func withRuntimeNotice(snap query.Snapshot, watcher *watch.Watcher) query.Snapshot {
	if watcher == nil {
		return snap
	}
	notice, at := watcher.RuntimeNotice()
	if notice == "" {
		return snap
	}
	snap.Runtime = query.RuntimeStatus{Notice: notice, At: at}
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
// The composer word travels as crewstate's, the spelling `mate state` and
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

// withTokens puts each row's token usage into a snapshot query.Load built
// out of files alone (mvp.md M5 task 27): it opens `.mate/mate.db`
// read-only - the console never needs the writer's lock to read a ledger
// the observer already committed - and fills CrewNode.Tokens from
// `v_task_ledger` and MateNode.Tokens from `v_now`.
//
// A workspace with no database yet (no console has ever ingested it) is not
// an error: every row simply keeps the Absent Load already gave it, the
// same as a Crew health's snapshot pattern of Console.withCrewHealth.
func withTokens(snap query.Snapshot, ws *store.Workspace) query.Snapshot {
	handle, err := db.OpenRead(ws)
	if err != nil {
		return snap
	}
	defer handle.Close()

	for p := range snap.Projects {
		project := &snap.Projects[p]
		if tok, ok := mateTokens(handle, project.ProjectID); ok {
			meta, err := ws.ReadMateMeta(project.ProjectID)
			if err == nil && meta[spawn.MetaSessionID] != "" {
				tok.ContextTokens, tok.ContextPct = nil, nil
				if usage, known, err := handle.LatestContext(context.Background(), timeline.MateActorID(project.ProjectID), meta[spawn.MetaSessionID]); err == nil && known {
					tok.ContextTokens, tok.ContextPct = &usage.Tokens, usage.Pct
				}
			}
			project.Mate.Tokens = query.KnownField(tok)
		}
		for c := range project.Crews {
			crew := &project.Crews[c]
			if tok, ok := crewTokens(handle, project.ProjectID, crew.CrewID); ok {
				crew.Tokens = query.KnownField(tok)
			}
		}
	}
	return snap
}

// crewTokens reads one crew's row of `v_task_ledger`. No row (the crew has
// no turn recorded yet, or the timeline has not ingested it) reports ok=
// false, which leaves the Absent field Load gave it rather than a Known
// zero that would render as "no tokens spent".
func crewTokens(handle *db.DB, project, crew string) (query.TokenValue, bool) {
	actorID := timeline.CrewActorID(project, crew)
	var total int64
	var cost, contextPct sql.NullFloat64
	err := handle.SQL().QueryRow(`
		SELECT COALESCE(input_tokens,0)+COALESCE(cache_read_tokens,0)+COALESCE(cache_write_tokens,0)+COALESCE(output_tokens,0),
		       cost, context_pct
		  FROM v_task_ledger WHERE crew_actor_id = ?`, actorID).Scan(&total, &cost, &contextPct)
	if err != nil {
		return query.TokenValue{}, false
	}
	return tokenContext(handle, actorID, tokenValue(total, cost, contextPct)), true
}

// mateTokens reads the Mate's row of `v_now`: tokens_today and
// context_pct. There is no per-task ledger for a Mate - its turns belong to
// the project rather than to any one task (docs/timeline.md) - so "today"
// is the best whole-task-shaped number `v_now` offers, and cost is left nil
// rather than approximated from it: a day boundary is not a task boundary,
// and a wrong-looking dollar figure is worse than none.
func mateTokens(handle *db.DB, project string) (query.TokenValue, bool) {
	actorID := timeline.MateActorID(project)
	var total int64
	var contextPct sql.NullFloat64
	err := handle.SQL().QueryRow(`
		SELECT COALESCE(tokens_today,0), context_pct FROM v_now WHERE actor_id = ?`,
		actorID).Scan(&total, &contextPct)
	if err != nil {
		return query.TokenValue{}, false
	}
	return tokenContext(handle, actorID, tokenValue(total, sql.NullFloat64{}, contextPct)), true
}

func tokenValue(total int64, cost, contextPct sql.NullFloat64) query.TokenValue {
	v := query.TokenValue{Total: total}
	if cost.Valid {
		c := cost.Float64
		v.Cost = &c
	}
	if contextPct.Valid {
		p := contextPct.Float64
		v.ContextPct = &p
	}
	return v
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

func tokenContext(handle *db.DB, actorID string, value query.TokenValue) query.TokenValue {
	if c, ok, err := handle.LatestContext(context.Background(), actorID, ""); err == nil && ok {
		value.ContextTokens, value.ContextPct = &c.Tokens, c.Pct
	}
	return value
}
