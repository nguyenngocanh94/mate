package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ContextUsage is the prompt carried by the latest observed model call.
// Tokens is meaningful even when the model's context window is unknown.
type ContextUsage struct {
	Tokens    int64
	Model     string
	SessionID string
	At        time.Time
	Pct       *float64
}

// LatestContext reads usage without requiring a price. An optional harness
// session ID prevents a newly refreshed Mate borrowing its old context.
func (d *DB) LatestContext(ctx context.Context, actor, session string) (ContextUsage, bool, error) {
	var out ContextUsage
	var at sql.NullString
	var window sql.NullInt64
	err := d.sql.QueryRowContext(ctx, `
		SELECT u.context_tokens_after, u.model, s.harness_session_id, u.started_at, p.context_window
		FROM turn u JOIN session s ON s.id = u.session_id
		LEFT JOIN pricing p ON p.model = u.model
		WHERE u.actor_id = ? AND (? = '' OR s.harness_session_id = ?)
		ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1`, actor, session, session).
		Scan(&out.Tokens, &out.Model, &out.SessionID, &at, &window)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out.At = ParseTime(at.String)
	if window.Valid && window.Int64 > 0 {
		pct := 100 * float64(out.Tokens) / float64(window.Int64)
		out.Pct = &pct
	}
	return out, true, nil
}
