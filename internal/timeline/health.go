package timeline

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/db"
)

// ToolThinking is the tool name of a synthesised action: the stretch where an
// agent was demonstrably working and no tool call explains it. It is a real
// `action` row rather than a gap, because M5's first question is "what was it
// doing" and "nothing is recorded" is not an answer a reader can act on.
const ToolThinking = "thinking"

// ingestHealth records `health.changed`: one event when the observer's
// composer classification for an agent changes, and none at all while it
// stays the same.
//
// This is an observation, not a file. The observer polls every five seconds
// and nothing on disk keeps what it saw, so a rebuild recovers no health
// history at all - docs/timeline.md says so, and the `thinking` actions that
// matter most are derived from the transcript instead, which does survive.
func (p *pass) ingestHealth(ctx context.Context) error {
	for _, reading := range p.ing.deps.health() {
		if reading.Project != p.project {
			continue
		}
		actorID := p.mate.ActorID
		if reading.Crew != "" {
			actorID = CrewActorID(p.project, reading.Crew)
		}
		composer := string(reading.Composer)
		if composer == "" {
			composer = string(crewstate.ComposerUnknown)
		}
		last, err := p.lastComposer(ctx, actorID)
		if err != nil {
			return err
		}
		if last == composer {
			continue
		}
		at := reading.At
		if at.IsZero() {
			at = p.now
		}
		p.b.event(pendingEvent{
			Dedup:   dedup(KindHealthChanged, actorID, composer, at.Format(time.RFC3339Nano)),
			Project: p.project, At: at, ActorID: actorID, Kind: KindHealthChanged,
			TaskActor: p.taskActor(actorID),
			Payload: map[string]any{
				"from": last, "to": composer, "agent_present": reading.AgentPresent,
			},
		})
	}
	return nil
}

func (p *pass) lastComposer(ctx context.Context, actorID string) (string, error) {
	var value sql.NullString
	err := p.tx.QueryRowContext(ctx,
		`SELECT json_extract(payload, '$.to') FROM event
		  WHERE actor_id = ? AND kind = ? ORDER BY at DESC, id DESC LIMIT 1`,
		actorID, KindHealthChanged).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value.String, nil
}

// fillThinking closes the holes: a stretch where an agent was working and no
// tool call says what it was doing becomes an `action` with the tool
// `thinking`. Two rules, and both are needed because they cover different
// holes.
//
//  1. Inside a turn. A model that reasons for thirty seconds between two tool
//     calls leaves a gap in the transcript's own timestamps. This rule is
//     derived from the transcript, so it survives a rebuild, and it is the
//     one that makes a turn readable minute by minute.
//  2. Across a busy stretch the observer saw. Between a `health.changed` to
//     `busy` and the next change, an agent was demonstrably working; if no
//     action of any kind falls inside, the transcript has not caught up (or
//     never will, for an agent whose transcript the locator could not find)
//     and the stretch is recorded as thinking rather than left blank.
func (p *pass) fillThinking(ctx context.Context) error {
	if err := p.fillTurnGaps(ctx); err != nil {
		return err
	}
	return p.fillBusyStretches(ctx)
}

func (p *pass) fillTurnGaps(ctx context.Context) error {
	gap := p.ing.deps.thinkingGap()
	rows, err := p.tx.QueryContext(ctx,
		`SELECT t.id, t.actor_id, t.started_at, t.ended_at FROM turn t
		   JOIN actor a ON a.id = t.actor_id
		  WHERE a.project = ? AND t.started_at IS NOT NULL AND t.ended_at IS NOT NULL`,
		p.project)
	if err != nil {
		return err
	}
	type turnWindow struct {
		id      string
		actorID string
		from    time.Time
		to      time.Time
	}
	var windows []turnWindow
	for rows.Next() {
		var t turnWindow
		var from, to string
		if err := rows.Scan(&t.id, &t.actorID, &from, &to); err != nil {
			rows.Close()
			return err
		}
		t.from, t.to = db.ParseTime(from), db.ParseTime(to)
		windows = append(windows, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, window := range windows {
		covered, err := p.coverage(ctx, window.actorID, window.from, window.to)
		if err != nil {
			return err
		}
		for _, hole := range holes(window.from, window.to, covered, gap) {
			if err := p.insertThinking(ctx, window.actorID, window.id,
				"turn.gap", hole.from, hole.to); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *pass) fillBusyStretches(ctx context.Context) error {
	rows, err := p.tx.QueryContext(ctx,
		`SELECT actor_id, at, json_extract(payload, '$.to') FROM event
		  WHERE project = ? AND kind = ? ORDER BY actor_id, at, id`,
		p.project, KindHealthChanged)
	if err != nil {
		return err
	}
	type mark struct {
		actorID  string
		at       time.Time
		composer string
	}
	var marks []mark
	for rows.Next() {
		var m mark
		var at string
		if err := rows.Scan(&m.actorID, &at, &m.composer); err != nil {
			rows.Close()
			return err
		}
		m.at = db.ParseTime(at)
		marks = append(marks, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	gap := p.ing.deps.thinkingGap()
	for i, m := range marks {
		if m.composer != string(crewstate.ComposerBusy) {
			continue
		}
		if i+1 >= len(marks) || marks[i+1].actorID != m.actorID {
			// The stretch has not ended yet: an open window is not a hole,
			// and recording one would give it an end it does not have.
			continue
		}
		from, to := m.at, marks[i+1].at
		if to.Sub(from) < gap {
			continue
		}
		covered, err := p.coverage(ctx, m.actorID, from, to)
		if err != nil {
			return err
		}
		for _, hole := range holes(from, to, covered, gap) {
			if err := p.insertThinking(ctx, m.actorID, "", "busy.stretch", hole.from, hole.to); err != nil {
				return err
			}
		}
	}
	return nil
}

type span struct{ from, to time.Time }

// coverage is every action of an actor that overlaps a window, as spans. An
// action with no recorded end covers the instant it started: an in-flight
// call is not evidence of how long it ran.
func (p *pass) coverage(ctx context.Context, actorID string, from, to time.Time) ([]span, error) {
	rows, err := p.tx.QueryContext(ctx,
		`SELECT at, ended_at FROM action
		  WHERE actor_id = ? AND at <= ? AND COALESCE(ended_at, at) >= ?
		  ORDER BY at`,
		actorID, db.FormatTime(to), db.FormatTime(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []span
	for rows.Next() {
		var at string
		var ended sql.NullString
		if err := rows.Scan(&at, &ended); err != nil {
			return nil, err
		}
		s := span{from: db.ParseTime(at)}
		s.to = s.from
		if ended.Valid {
			if end := db.ParseTime(ended.String); end.After(s.from) {
				s.to = end
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// holes are the sub-intervals of [from, to] that no span covers and that last
// at least `least`. The spans arrive sorted by start.
func holes(from, to time.Time, covered []span, least time.Duration) []span {
	var out []span
	cursor := from
	for _, s := range covered {
		if s.from.After(cursor) && s.from.Sub(cursor) >= least {
			out = append(out, span{from: cursor, to: s.from})
		}
		if s.to.After(cursor) {
			cursor = s.to
		}
	}
	if to.After(cursor) && to.Sub(cursor) >= least {
		out = append(out, span{from: cursor, to: to})
	}
	return out
}

// insertThinking writes one synthesised action. Its id carries the actor and
// the start of the hole, so filling the same hole twice writes one row; it
// carries no event, because nothing happened that a story would narrate
// beyond the turn it already belongs to.
func (p *pass) insertThinking(ctx context.Context, actorID, turnID, rule string, from, to time.Time) error {
	id := fmt.Sprintf("%s#thinking#%d", actorID, from.UTC().UnixNano())
	duration := to.Sub(from).Milliseconds()
	_, err := p.tx.ExecContext(ctx,
		`INSERT INTO action(id, turn_id, event_id, at, ended_at, actor_id, tool, target, summary, duration_ms, ok)
		 VALUES (?, ?, NULL, ?, ?, ?, ?, '', ?, ?, NULL)
		 ON CONFLICT(id) DO UPDATE SET
		   ended_at    = MAX(action.ended_at, excluded.ended_at),
		   duration_ms = MAX(action.duration_ms, excluded.duration_ms)`,
		id, nullString(turnID), db.FormatTime(from), db.FormatTime(to), actorID, ToolThinking, rule, duration)
	return err
}
