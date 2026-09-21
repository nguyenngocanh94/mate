package timeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/db"
)

// StoryEvent is one row of `v_story`: an event with the names a reader would
// say out loud instead of the ids a join needs.
//
// The field order is the JSON field order, and it is part of the contract:
// `matev2 events` prints one of these per line, and a consumer diffing two
// runs compares bytes. A field is added at the end, never in the middle.
type StoryEvent struct {
	ID        int64           `json:"id"`
	At        string          `json:"at"`
	Project   string          `json:"project"`
	Kind      string          `json:"kind"`
	Actor     string          `json:"actor"`
	ActorKind string          `json:"actor_kind"`
	Subject   string          `json:"subject,omitempty"`
	Task      string          `json:"task,omitempty"`
	Turn      string          `json:"turn,omitempty"`
	Cause     int64           `json:"cause,omitempty"`
	CauseKind string          `json:"cause_kind,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	Ref       string          `json:"ref,omitempty"`
	RefOffset int64           `json:"ref_offset,omitempty"`
}

// Time is the event's timestamp, parsed.
func (e StoryEvent) Time() time.Time { return db.ParseTime(e.At) }

// Field reads one value out of the payload, as a string. A payload key that
// is a number or a bool renders the way a reader would write it.
func (e StoryEvent) Field(key string) string {
	var values map[string]any
	if json.Unmarshal(e.Payload, &values) != nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

// StoryQuery selects a slice of the story.
type StoryQuery struct {
	// Project is required: a story is one project's.
	Project string
	// SinceID returns only events after a known id, which is what a live
	// reader polls with.
	SinceID int64
	// SinceTime returns only events at or after a moment.
	SinceTime time.Time
	// Limit bounds the result; zero means no bound.
	Limit int
	// TurnID returns only the events recorded inside one turn, which is
	// what the dashboard asks for when a reader opens one. It is here
	// rather than in the caller so a page showing one turn does not read
	// the whole project's story to throw most of it away.
	TurnID string
}

// Story reads `v_story` in story order: oldest first, and by id within one
// instant, which is the order the events were recorded in.
func Story(ctx context.Context, sqlDB *sql.DB, q StoryQuery) ([]StoryEvent, error) {
	query := `SELECT id, at, project, kind, actor_name, actor_kind, subject_name,
	                 task_name, turn_id, cause_event_id, cause_kind, payload, ref_path, ref_offset
	            FROM v_story WHERE project = ?`
	args := []any{q.Project}
	if q.SinceID > 0 {
		query += ` AND id > ?`
		args = append(args, q.SinceID)
	}
	if !q.SinceTime.IsZero() {
		query += ` AND at >= ?`
		args = append(args, db.FormatTime(q.SinceTime))
	}
	if q.TurnID != "" {
		query += ` AND turn_id = ?`
		args = append(args, q.TurnID)
	}
	query += ` ORDER BY at, id`
	if q.Limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, q.Limit)
	}
	rows, err := sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StoryEvent
	for rows.Next() {
		var e StoryEvent
		var subject, task, turn, causeKind, payload sql.NullString
		var cause sql.NullInt64
		if err := rows.Scan(&e.ID, &e.At, &e.Project, &e.Kind, &e.Actor, &e.ActorKind,
			&subject, &task, &turn, &cause, &causeKind, &payload, &e.Ref, &e.RefOffset); err != nil {
			return nil, err
		}
		e.Subject, e.Task, e.Turn, e.CauseKind = subject.String, task.String, turn.String, causeKind.String
		e.Cause = cause.Int64
		e.Payload = json.RawMessage(payload.String)
		if len(e.Payload) == 0 {
			e.Payload = json.RawMessage("{}")
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// JSONLine renders one event as the line `matev2 events` prints. It is
// deliberately not indented: one event per line is what makes `--follow`
// streamable and what lets a reader `grep` the story.
func (e StoryEvent) JSONLine() (string, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// quote renders a piece of an agent's own text inside a narrated sentence:
// one line, bounded, and with its own quotes softened so the sentence's
// quotes still close.
func quote(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, `"`, `'`)), " ")
	return `"` + truncateRunes(text, limit) + `"`
}
