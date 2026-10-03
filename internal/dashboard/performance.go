package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/diagnostics"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// CrewPerformance is the Task page's ledger row, model calls and diagnostics
// for one crew, for a reader that is not the HTTP API (`mate usage --why`):
// the terminal and the page read through the same projection, so they cannot
// disagree about what a Crew spent its tokens on.
func (s *Server) CrewPerformance(ctx context.Context, project, crew string) (Task, []Turn, diagnostics.Performance, error) {
	ledger, turns, _, performance, err := s.taskPerformance(ctx, project, crew)
	return ledger, turns, performance, err
}

func (s *Server) taskPerformance(ctx context.Context, project, crew string) (Task, []Turn, []Question, diagnostics.Performance, error) {
	now := s.deps.now()
	ledger, err := s.task(ctx, project, crew, now)
	if err != nil {
		return Task{}, nil, nil, diagnostics.Performance{}, err
	}
	actorID := timeline.CrewActorID(project, crew)
	turns, err := s.turns(ctx, actorID)
	if err != nil {
		return Task{}, nil, nil, diagnostics.Performance{}, err
	}
	questions, err := s.questions(ctx, actorID)
	if err != nil {
		return Task{}, nil, nil, diagnostics.Performance{}, err
	}
	performance, err := s.crewPerformance(ctx, actorID, ledger, turns, questions, now)
	if err != nil {
		return Task{}, nil, nil, diagnostics.Performance{}, err
	}
	return ledger, turns, questions, performance, nil
}

func (s *Server) crewPerformance(ctx context.Context, actorID string, ledger Task, turns []Turn, questions []Question, now time.Time) (diagnostics.Performance, error) {
	return s.actorPerformance(ctx, actorID, ledger.Closed, turns, questions, now)
}

// Both Mate and Crew use the same recorded activities and accounting rules.
// An actor without a task row (Mate) simply has no crew worktree to normalize.
func (s *Server) actorPerformance(ctx context.Context, actorID string, closed bool, turns []Turn, questions []Question, now time.Time) (diagnostics.Performance, error) {
	in := diagnostics.Input{Calls: make([]diagnostics.Call, 0, len(turns))}
	for _, t := range turns {
		in.Calls = append(in.Calls, diagnostics.Call{ID: t.ID, SessionID: t.SessionID, HarnessTurnRef: t.HarnessTurnRef,
			StartedAt: t.StartedAt, EndedAt: t.EndedAt, Model: t.Model, Ordinal: t.Ordinal, Tokens: performanceTokens(t.Tokens),
			ContextAfter: t.ContextAfter, Ref: diagnostics.Ref{Path: t.Ref.Path, Offset: t.Ref.Offset}})
	}
	for _, q := range questions {
		in.Decisions = append(in.Decisions, diagnostics.Decision{ID: q.ID, Text: q.Text, AskedAt: q.AskedAt, AnsweredAt: q.AnsweredAt, EventID: q.AskedEventID})
	}
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT a.id,COALESCE(a.turn_id,''),COALESCE(t.session_id,''),a.tool,a.target,a.summary,
		       a.at,a.ended_at,a.duration_ms,a.ok,COALESCE(e.ref_path,''),COALESCE(e.ref_offset,0)
		  FROM action a LEFT JOIN turn t ON t.id=a.turn_id LEFT JOIN event e ON e.id=a.event_id
		 WHERE a.actor_id=? AND a.tool <> 'thinking' ORDER BY a.at,a.id`, actorID)
	if err != nil {
		return diagnostics.Performance{}, err
	}
	for rows.Next() {
		var a diagnostics.Action
		var ended sql.NullString
		var dur sql.NullInt64
		var ok sql.NullBool
		if err = rows.Scan(&a.ID, &a.CallID, &a.SessionID, &a.Tool, &a.Target, &a.Summary, &a.StartedAt, &ended, &dur, &ok, &a.Ref.Path, &a.Ref.Offset); err != nil {
			rows.Close()
			return diagnostics.Performance{}, err
		}
		a.EndedAt = ended.String
		if dur.Valid {
			n := dur.Int64
			a.DurationMs = &n
		}
		if ok.Valid {
			v := ok.Bool
			a.OK = &v
		}
		in.Actions = append(in.Actions, a)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return diagnostics.Performance{}, err
	}
	if err = rows.Close(); err != nil {
		return diagnostics.Performance{}, err
	}
	rows, err = s.db.SQL().QueryContext(ctx, `SELECT kind,payload,ref_path,ref_offset FROM event WHERE actor_id=? AND kind LIKE 'telemetry.%' ORDER BY id`, actorID)
	if err != nil {
		return diagnostics.Performance{}, err
	}
	for rows.Next() {
		var kind, raw, path string
		var offset int64
		if err = rows.Scan(&kind, &raw, &path, &offset); err != nil {
			rows.Close()
			return diagnostics.Performance{}, err
		}
		if kind == "telemetry.profile" {
			var profile map[string]any
			if err = json.Unmarshal([]byte(raw), &profile); err != nil {
				in.RecordingError = "Recorded launch profile could not be decoded."
			} else {
				in.Profile = profile
			}
			continue
		}
		var f telemetry.Fact
		if err = json.Unmarshal([]byte(raw), &f); err != nil {
			in.RecordingError = fmt.Sprintf("A recorded %s event could not be decoded.", kind)
			continue
		}
		if f.Kind == "" {
			f.Kind = strings.TrimPrefix(kind, "telemetry.")
		}
		if f.SourcePath == "" {
			f.SourcePath = path
			f.SourceOffset = offset
		}
		in.Facts = append(in.Facts, f)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return diagnostics.Performance{}, err
	}
	if err = rows.Close(); err != nil {
		return diagnostics.Performance{}, err
	}
	// Old read-only databases legitimately predate the telemetry cursor.
	// Their coverage stays unknown; opening the dashboard never migrates it.
	var table int
	if err = s.db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='telemetry_cursor'`).Scan(&table); err != nil {
		return diagnostics.Performance{}, err
	}
	if table > 0 {
		var observed, recordingErr sql.NullString
		err = s.db.SQL().QueryRowContext(ctx, `SELECT MAX(observed_at),MAX(NULLIF(error,'')) FROM telemetry_cursor WHERE actor_id=?`, actorID).Scan(&observed, &recordingErr)
		if err != nil {
			return diagnostics.Performance{}, err
		}
		in.LastIngestedAt = observed.String
		if recordingErr.Valid {
			in.RecordingError = recordingErr.String
		}
	}
	var worktree string
	if err = s.db.SQL().QueryRowContext(ctx, `SELECT worktree FROM task WHERE crew_actor_id=?`, actorID).Scan(&worktree); err != nil && err != sql.ErrNoRows {
		return diagnostics.Performance{}, err
	}
	return diagnostics.Project(in, diagnostics.Options{Now: now, Closed: closed, Worktree: worktree}), nil
}

func performanceTokens(t Tokens) diagnostics.Tokens {
	return diagnostics.Tokens{Input: t.Input, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Output: t.Output, Thinking: t.Thinking, Total: t.Total}
}
