package timeline

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/crewstate"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// Meta keys read here. internal/spawn writes them under exactly these names;
// this package spells them again rather than importing spawn, because
// internal/watch calls the ingest and must keep importing nothing that can
// start or stop an agent. TestMetaKeysMatchSpawn is what keeps the two
// spellings equal.
const (
	MetaHarness      = "harness"
	MetaAgent        = "agent"
	MetaSessionID    = "session_id"
	MetaTranscript   = "transcript"
	MetaStartedAt    = "started_at"
	MetaStoppedAt    = "stopped_at"
	MetaResumedFrom  = "resumed_from"
	MetaTask         = "task"
	MetaWorktree     = "worktree"
	MetaBranch       = "branch"
	MetaState        = crewstate.MetaState
	MetaFailedReason = "failed_reason"
)

// crewRecord is one `crews/<id>.meta`, read whole.
type crewRecord struct {
	ID      string
	ActorID string
	Meta    map[string]string
	Path    string
}

// mateRecord is `mate/mate.meta`.
type mateRecord struct {
	ActorID string
	Meta    map[string]string
	Path    string
	Present bool
}

func (c crewRecord) harness() string { return c.Meta[MetaHarness] }
func (c crewRecord) state() crewstate.State {
	return crewstate.Declare(crewstate.Declaration{Meta: c.Meta})
}

func metaTime(meta map[string]string, key string) time.Time {
	value := strings.TrimSpace(meta[key])
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ingestMeta reads the `.meta` files: who exists, what each crew was hired
// for, which harness session each agent is in, and the four life events the
// files can date - a Mate starting and stopping, a crew being spawned and
// being closed.
//
// None of these files is append-only, so none of them has a cursor. They are
// read whole every pass and turned into rows with natural keys, so a pass
// that sees nothing new writes nothing new.
func (p *pass) ingestMeta(ctx context.Context) error {
	ws := p.ing.ws
	project := p.project

	// The three actors that are not agents. They exist from the first pass
	// because every message has two ends and an end that is not recorded is
	// a join that silently drops the row.
	p.b.actor(pendingActor{ID: UserActorID(project), Project: project, Kind: ActorUser, Name: "captain"})
	p.b.actor(pendingActor{ID: AppActorID(project), Project: project, Kind: ActorApp, Name: "matev2"})
	p.b.actor(pendingActor{ID: ObserverActorID(project), Project: project, Kind: ActorObserver, Name: "observer"})

	mateMeta, err := ws.ReadMateMeta(project)
	if err != nil {
		return err
	}
	p.mate = mateRecord{
		ActorID: MateActorID(project),
		Meta:    mateMeta,
		Path:    ws.MateMeta(project),
		Present: len(mateMeta) > 0,
	}
	startedAt := metaTime(mateMeta, MetaStartedAt)
	stoppedAt := metaTime(mateMeta, MetaStoppedAt)
	p.b.actor(pendingActor{
		ID: p.mate.ActorID, Project: project, Kind: ActorMate, Name: "mate",
		Harness: mateMeta[MetaHarness], FirstSeen: startedAt, LastSeen: startedAt, GoneAt: stoppedAt,
	})
	if p.mate.Present {
		sessionID := strings.TrimSpace(mateMeta[MetaSessionID])
		if sessionID != "" {
			p.b.session(pendingSession{
				ID:             sessionRowID(p.mate.ActorID, sessionID),
				ActorID:        p.mate.ActorID,
				HarnessSession: sessionID,
				TranscriptPath: strings.TrimSpace(mateMeta[MetaTranscript]),
				StartedAt:      startedAt,
				EndedAt:        stoppedAt,
				ResumedFrom:    strings.TrimSpace(mateMeta[MetaResumedFrom]),
			})
		}
		if !startedAt.IsZero() {
			p.b.event(pendingEvent{
				Dedup:   dedup(KindMateStarted, p.mate.ActorID, startedAt.Format(time.RFC3339)),
				Project: project, At: startedAt, ActorID: p.mate.ActorID, Kind: KindMateStarted,
				Payload: map[string]any{
					"harness":      mateMeta[MetaHarness],
					"agent":        mateMeta[MetaAgent],
					"session_id":   mateMeta[MetaSessionID],
					"resumed_from": mateMeta[MetaResumedFrom],
				},
				RefPath: p.mate.Path,
			})
		}
		if !stoppedAt.IsZero() {
			p.b.event(pendingEvent{
				Dedup:   dedup(KindMateStopped, p.mate.ActorID, stoppedAt.Format(time.RFC3339)),
				Project: project, At: stoppedAt, ActorID: p.mate.ActorID, Kind: KindMateStopped,
				Payload: map[string]any{"harness": mateMeta[MetaHarness]},
				RefPath: p.mate.Path,
			})
		}
	}

	crews, err := listCrewMetas(ws, project)
	if err != nil {
		return err
	}
	p.crews = crews
	for _, crew := range crews {
		spawnedAt := metaTime(crew.Meta, MetaStartedAt)
		closedAt := metaTime(crew.Meta, MetaStoppedAt)
		state := crew.state()
		gone := time.Time{}
		if state.Closed() {
			gone = closedAt
			if gone.IsZero() {
				gone = spawnedAt
			}
		}
		p.b.actor(pendingActor{
			ID: crew.ActorID, Project: project, Kind: ActorCrew, Name: crew.ID,
			Harness: crew.harness(), FirstSeen: spawnedAt, LastSeen: spawnedAt, GoneAt: gone,
		})
		if sessionID := strings.TrimSpace(crew.Meta[MetaSessionID]); sessionID != "" {
			p.b.session(pendingSession{
				ID:             sessionRowID(crew.ActorID, sessionID),
				ActorID:        crew.ActorID,
				HarnessSession: sessionID,
				TranscriptPath: strings.TrimSpace(crew.Meta[MetaTranscript]),
				StartedAt:      spawnedAt,
				EndedAt:        closedAt,
			})
		}
		task := pendingTask{
			CrewActorID: crew.ActorID,
			Project:     project,
			Text:        crew.Meta[MetaTask],
			BriefPath:   ws.CrewBrief(project, crew.ID),
			Branch:      crew.Meta[MetaBranch],
			Worktree:    crew.Meta[MetaWorktree],
			SpawnedAt:   spawnedAt,
		}
		if state.Closed() {
			task.ClosedAt = closedAt
			if task.ClosedAt.IsZero() {
				task.ClosedAt = spawnedAt
			}
			task.CloseState = string(state)
		}
		p.b.task(task)

		if !spawnedAt.IsZero() {
			p.b.event(pendingEvent{
				Dedup:   dedup(KindCrewSpawned, crew.ActorID),
				Project: project, At: spawnedAt, ActorID: p.mate.ActorID, Subject: crew.ActorID,
				Kind: KindCrewSpawned, TaskActor: crew.ActorID,
				Payload: map[string]any{
					"crew": crew.ID, "task": crew.Meta[MetaTask],
					"harness": crew.harness(), "branch": crew.Meta[MetaBranch],
					"worktree": crew.Meta[MetaWorktree],
				},
				RefPath: crew.Path,
			})
		}
		if state.Closed() && !task.ClosedAt.IsZero() {
			kind := KindCrewFinished
			if state == crewstate.StateFailed {
				kind = KindCrewFailed
			}
			payload := map[string]any{"crew": crew.ID, "state": string(state)}
			if reason := crew.Meta[MetaFailedReason]; reason != "" {
				payload["reason"] = reason
			}
			p.b.event(pendingEvent{
				Dedup:   dedup(kind, crew.ActorID),
				Project: project, At: task.ClosedAt, ActorID: crew.ActorID, Kind: kind,
				TaskActor: crew.ActorID, Payload: payload, RefPath: crew.Path,
			})
		}
	}

	return p.ingestMode(ctx)
}

// ingestMode records the two modes of docs/mvp.md section 5.
//
// `mate/.auto` is a flag, not a log: its presence is the whole record, and
// its removal leaves nothing at all behind (three different things delete it
// - the Mate's own hook, the `m` key and a human). So this is an observation,
// and the only one in the package besides `health.changed`: a pass compares
// the flag with the last `mode.changed` it recorded and writes one event when
// they differ. `auto` is dated by the flag file's mtime, which is exact;
// `manual` is dated by the pass that noticed, which is a lower bound and says
// so in its payload. A rebuild therefore records only the mode the workspace
// is in now, and docs/timeline.md says so plainly.
func (p *pass) ingestMode(ctx context.Context) error {
	auto := p.ing.ws.Auto(p.project)
	mode := "manual"
	at := p.now
	dated := "observed"
	if auto {
		mode = "auto"
		if fi, err := os.Stat(p.ing.ws.AutoFlag(p.project)); err == nil {
			at = fi.ModTime()
			dated = "flag_mtime"
		}
	}

	var last string
	err := p.tx.QueryRowContext(ctx,
		`SELECT json_extract(payload, '$.to') FROM event
		  WHERE project = ? AND kind = ? ORDER BY at DESC, id DESC LIMIT 1`,
		p.project, KindModeChanged).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if last == mode {
		return nil
	}
	if last == "" && mode == "manual" {
		// Manual is where a project starts (section 5), so a first pass over
		// a manual project has seen no change and invents none.
		return nil
	}
	from := last
	if from == "" {
		from = "manual"
	}
	p.b.event(pendingEvent{
		Dedup:   dedup(KindModeChanged, p.project, mode, at.Format(time.RFC3339Nano)),
		Project: p.project, At: at, ActorID: AppActorID(p.project), Kind: KindModeChanged,
		Payload: map[string]any{"from": from, "to": mode, "dated_by": dated},
		RefPath: p.ing.ws.AutoFlag(p.project),
	})
	return nil
}

// listCrewMetas reads every `crews/<id>.meta` of a project, closed ones
// included: a finished crew is most of the story, and a timeline that dropped
// it would answer "who did this" with silence.
func listCrewMetas(ws *store.Workspace, project string) ([]crewRecord, error) {
	entries, err := os.ReadDir(ws.CrewsDir(project))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []crewRecord
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, ok := strings.CutSuffix(e.Name(), ".meta")
		if !ok || store.ValidateCrewID(id) != nil {
			continue
		}
		meta, err := ws.ReadCrewMeta(project, id)
		if err != nil {
			// A half-written meta is not evidence of anything; the next pass
			// reads it again.
			continue
		}
		out = append(out, crewRecord{
			ID: id, ActorID: CrewActorID(project, id), Meta: meta,
			Path: filepath.Join(ws.CrewsDir(project), e.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// sessionRowID keys a session by the actor and the harness session id, so a
// Mate resumed into the same session keeps one row and a fresh session gets
// its own.
func sessionRowID(actorID, harnessSessionID string) string {
	return actorID + "#" + harnessSessionID
}

// dedup builds a fact's natural key out of its parts. The separator is a
// character none of the parts can contain (a crew id, an actor id, a path or
// an RFC3339 stamp), so two different facts cannot collide on one key.
func dedup(parts ...string) string { return strings.Join(parts, "\x1f") }
