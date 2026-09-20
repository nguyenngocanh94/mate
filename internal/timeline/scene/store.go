package scene

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/db"
)

// Querier is the part of *sql.DB and *sql.Tx this package uses, so the
// projection can run inside the ingest's transaction and the CLI can read the
// result without one.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Run is the projection: read one project's events, run the machine over all
// of them, and replace the project's `transition` rows with the result.
//
// It replaces rather than appends because the machine is recomputed whole
// (see the package comment), and because that is what makes the table a
// function of the events: two reindexes of the same files produce the same
// rows byte for byte, including the ids.
func Run(ctx context.Context, q Querier, project string) error {
	actors, events, err := Load(ctx, q, project)
	if err != nil {
		return err
	}
	return Save(ctx, q, project, Project(project, actors, events))
}

// Load reads the actors and the events of one project, in story order.
func Load(ctx context.Context, q Querier, project string) ([]Actor, []Event, error) {
	actorRows, err := q.QueryContext(ctx,
		`SELECT id, kind, name, project FROM actor WHERE project = ? ORDER BY id`, project)
	if err != nil {
		return nil, nil, err
	}
	var actors []Actor
	for actorRows.Next() {
		var a Actor
		if err := actorRows.Scan(&a.ID, &a.Kind, &a.Name, &a.Project); err != nil {
			actorRows.Close()
			return nil, nil, err
		}
		actors = append(actors, a)
	}
	actorRows.Close()
	if err := actorRows.Err(); err != nil {
		return nil, nil, err
	}

	eventRows, err := q.QueryContext(ctx,
		`SELECT id, at, kind, actor_id, subject_actor_id, task_actor_id, cause_event_id, payload
		   FROM event WHERE project = ? ORDER BY at, id`, project)
	if err != nil {
		return nil, nil, err
	}
	defer eventRows.Close()
	var events []Event
	for eventRows.Next() {
		var e Event
		var at, payload string
		var subject, task sql.NullString
		var cause sql.NullInt64
		if err := eventRows.Scan(&e.ID, &at, &e.Kind, &e.ActorID, &subject, &task, &cause, &payload); err != nil {
			return nil, nil, err
		}
		e.At = db.ParseTime(at)
		e.SubjectID, e.TaskActor, e.CauseID = subject.String, task.String, cause.Int64
		if payload != "" {
			_ = json.Unmarshal([]byte(payload), &e.Payload)
		}
		events = append(events, e)
	}
	return actors, events, eventRows.Err()
}

// Save replaces one project's transitions.
func Save(ctx context.Context, q Querier, project string, rows []Transition) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM transition WHERE project = ?`, project); err != nil {
		return err
	}
	for _, t := range rows {
		var target any
		if t.Target != "" {
			target = t.Target
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO transition(id, actor_id, project, from_state, to_state, at, event_id, target_actor_id, detail)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.ActorID, t.Project, string(t.From), string(t.To),
			db.FormatTime(t.At), t.EventID, target, t.Detail); err != nil {
			return err
		}
	}
	return nil
}

// NowQuery selects a snapshot.
type NowQuery struct {
	Project string
	// Actor limits the snapshot to one actor id.
	Actor string
}

// Now reads `v_now`: one row per actor, the Mate first, because the scene is
// its office.
func Now(ctx context.Context, q Querier, query NowQuery) ([]NowRow, error) {
	sqlText := `SELECT n.actor_id, n.actor_kind, n.actor_name, n.project, n.state, n.since,
	                   n.target_actor_id, t.name, n.detail, n.tokens_today
	              FROM v_now n LEFT JOIN actor t ON t.id = n.target_actor_id
	             WHERE n.project = ?`
	args := []any{query.Project}
	if query.Actor != "" {
		sqlText += ` AND n.actor_id = ?`
		args = append(args, query.Actor)
	}
	sqlText += ` ORDER BY CASE n.actor_kind WHEN 'mate' THEN 0 ELSE 1 END, n.actor_name, n.actor_id`
	rows, err := q.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NowRow
	for rows.Next() {
		var n NowRow
		var state, since, target, targetName, detail sql.NullString
		if err := rows.Scan(&n.ActorID, &n.ActorKind, &n.ActorName, &n.Project,
			&state, &since, &target, &targetName, &detail, &n.TokensToday); err != nil {
			return nil, err
		}
		n.State = State(state.String)
		n.Since = db.ParseTime(since.String)
		n.TargetID, n.TargetName, n.Detail = target.String, targetName.String, detail.String
		out = append(out, n)
	}
	return out, rows.Err()
}

// TransitionQuery selects a slice of the scene's history.
type TransitionQuery struct {
	// Project is required: a scene is one project's.
	Project string
	// AfterID returns only transitions after a known transition id, which is
	// what `--follow` polls with. The id sorts in story order, so this is a
	// cursor and not a scan.
	AfterID string
	// SinceEventID returns only transitions caused by an event after an id.
	SinceEventID int64
	// SinceTime returns only transitions at or after a moment.
	SinceTime time.Time
	// Limit bounds the result; zero means no bound.
	Limit int
}

// Transitions reads the scene's history in story order, with the names a
// reader would say out loud.
func Transitions(ctx context.Context, q Querier, query TransitionQuery) ([]Row, error) {
	sqlText := `SELECT r.id, r.project, r.actor_id, r.from_state, r.to_state, r.at, r.event_id,
	                   r.target_actor_id, r.detail, a.kind, a.name, t.name
	              FROM transition r
	              JOIN actor a ON a.id = r.actor_id
	              LEFT JOIN actor t ON t.id = r.target_actor_id
	             WHERE r.project = ?`
	args := []any{query.Project}
	if query.AfterID != "" {
		sqlText += ` AND r.id > ?`
		args = append(args, query.AfterID)
	}
	if query.SinceEventID > 0 {
		sqlText += ` AND r.event_id > ?`
		args = append(args, query.SinceEventID)
	}
	if !query.SinceTime.IsZero() {
		sqlText += ` AND r.at >= ?`
		args = append(args, db.FormatTime(query.SinceTime))
	}
	sqlText += ` ORDER BY r.at, r.id`
	if query.Limit > 0 {
		sqlText += ` LIMIT ?`
		args = append(args, query.Limit)
	}
	rows, err := q.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var at, from, to string
		var target, targetName sql.NullString
		if err := rows.Scan(&r.ID, &r.Project, &r.ActorID, &from, &to, &at, &r.EventID,
			&target, &r.Detail, &r.ActorKind, &r.ActorName, &targetName); err != nil {
			return nil, err
		}
		r.From, r.To, r.At = State(from), State(to), db.ParseTime(at)
		r.Target, r.TargetName = target.String, targetName.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastID is the newest transition of a project, which is where `--follow`
// starts so it prints what happens next and not what has already happened.
func LastID(ctx context.Context, q Querier, project string) (string, error) {
	var id sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT MAX(id) FROM transition WHERE project = ?`, project).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	return id.String, nil
}
