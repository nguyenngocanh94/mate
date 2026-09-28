package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// errNotFound is a 404 with a sentence: an unknown project or an unknown
// crew is a reader's mistake, not a broken server.
type errNotFound struct{ reason string }

func (e errNotFound) Error() string { return e.reason }

func notFound(format string, a ...any) error {
	return errNotFound{reason: fmt.Sprintf(format, a...)}
}

// lastEventID is the generation every response is cached against:
// `MAX(event.id)` over the whole workspace, because a dashboard that
// refreshed one project's page when another project's event landed would be
// wrong in the cheap direction, and one that missed it would be wrong in
// the expensive one.
func (s *Server) lastEventID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	if err := s.db.SQL().QueryRowContext(ctx, `SELECT MAX(id) FROM event`).Scan(&id); err != nil {
		return 0, err
	}
	return id.Int64, nil
}

// sceneRows reads `v_now` for one project, keyed by actor id.
//
// It does not go through scene.Now: that helper is `mate events --scene`'s
// and selects the eight columns the snapshot line prints, which do not
// include `context_pct` (added to the view by schema v2 for task 27). The
// page needs it on every row, so this reads the view directly and joins
// `actor` for the target's display name the same way scene.Now does.
func (s *Server) sceneRows(ctx context.Context, project string) (map[string]SceneRow, error) {
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT n.actor_id, n.actor_name, n.actor_kind, n.project, n.state, n.since,
		       t.name, n.detail, n.tokens_today,
                   (SELECT CASE WHEN p.context_window > 0 THEN 100.0 * u.context_tokens_after / p.context_window END
                    FROM turn u LEFT JOIN pricing p ON p.model=u.model WHERE u.actor_id=n.actor_id ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1),
		       (SELECT u.context_tokens_after FROM turn u WHERE u.actor_id = n.actor_id ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1)
		  FROM v_now n LEFT JOIN actor t ON t.id = n.target_actor_id
		 WHERE n.project = ?`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SceneRow{}
	for rows.Next() {
		var r SceneRow
		var state, since, target, detail sql.NullString
		var pct sql.NullFloat64
		var ctxTokens sql.NullInt64
		if err := rows.Scan(&r.ActorID, &r.Actor, &r.ActorKind, &r.Project,
			&state, &since, &target, &detail, &r.TokensToday, &pct, &ctxTokens); err != nil {
			return nil, err
		}
		r.State, r.Since, r.Target, r.Detail = state.String, since.String, target.String, detail.String
		r.ContextPct = nullFloat(pct)
		if ctxTokens.Valid {
			n := ctxTokens.Int64
			r.ContextTokens = &n
		}
		out[r.ActorID] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if meta, err := s.ws.ReadMateMeta(project); err == nil && meta["session_id"] != "" {
		id := timeline.MateActorID(project)
		if row, ok := out[id]; ok {
			row.ContextTokens, row.ContextPct = nil, nil
			if usage, known, err := s.db.LatestContext(ctx, id, meta["session_id"]); err == nil && known {
				row.ContextTokens, row.ContextPct = &usage.Tokens, usage.Pct
			}
			out[id] = row
		}
	}
	return out, nil
}

// actorFacts are the two things `v_now` has no column for and a card needs:
// which harness the actor runs, and whether it has gone. Both live on
// `actor`, which the ingest fills from `mate.meta` and `crews/<id>.meta`
// (internal/timeline/meta.go): `gone_at` is the Mate's `stopped_at` and a
// crew's teardown time.
type actorFacts struct {
	harness   string
	firstSeen string
	goneAt    string
}

// running is "started and not stopped". An actor with no first_seen has
// never run at all, which is not the same as having stopped.
func (a actorFacts) running() bool { return a.firstSeen != "" && a.goneAt == "" }

func (s *Server) actorFacts(ctx context.Context, actorID string) (actorFacts, bool, error) {
	var out actorFacts
	var harness, firstSeen, goneAt sql.NullString
	err := s.db.SQL().QueryRowContext(ctx,
		`SELECT harness, first_seen, gone_at FROM actor WHERE id = ?`, actorID).
		Scan(&harness, &firstSeen, &goneAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out.harness, out.firstSeen, out.goneAt = harness.String, firstSeen.String, goneAt.String
	return out, true, nil
}

// actorTotals is one actor's turn count, token buckets and cost, computed
// the way `v_task_ledger` computes a task's - including the guard that a
// pricing row of all zeroes is not a price (docs/timeline.md section 10).
// There is no task row for a Mate, so `v_task_ledger` cannot answer for
// one and this does, from `turn` directly. It is the same SQL
// cmd/mate/usage.go's mateLedgerRow runs, and the unit tests compare the
// two number for number.
func (s *Server) actorTotals(ctx context.Context, actorID string) (int64, Tokens, *float64, error) {
	var turns int64
	var tk Tokens
	if err := s.db.SQL().QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(cache_read_tokens),0),
		       COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(thinking_tokens),0)
		  FROM turn WHERE actor_id = ?`, actorID).
		Scan(&turns, &tk.Input, &tk.CacheRead, &tk.CacheWrite, &tk.Output, &tk.Thinking); err != nil {
		return 0, tk, nil, err
	}
	tk.Total = tk.Input + tk.CacheRead + tk.CacheWrite + tk.Output

	var cost sql.NullFloat64
	if err := s.db.SQL().QueryRowContext(ctx, `
		SELECT SUM(
			u.input_tokens       * COALESCE(p.input_per_m, 0) / 1000000.0 +
			u.cache_read_tokens  * COALESCE(p.cache_read_per_m, 0) / 1000000.0 +
			u.cache_write_tokens * COALESCE(p.cache_write_per_m, 0) / 1000000.0 +
			u.output_tokens      * COALESCE(p.output_per_m, 0) / 1000000.0)
		  FROM turn u JOIN pricing p ON p.model = u.model
		 WHERE u.actor_id = ?
		   AND (p.input_per_m > 0 OR p.cache_read_per_m > 0 OR p.cache_write_per_m > 0 OR p.output_per_m > 0)`,
		actorID).Scan(&cost); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, tk, nil, err
	}
	return turns, tk, nullFloat(cost), nil
}

// toolCount sums `turn.tool_count` for an actor. `v_task_ledger` has no
// such column and the tier-3 ledger of docs/mvp.md M6 asks for it ("số tool
// call"), so it is read off `turn`, the same table the view's own token
// sums come from.
func (s *Server) toolCount(ctx context.Context, actorID string) (int64, error) {
	var n int64
	err := s.db.SQL().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(tool_count),0) FROM turn WHERE actor_id = ?`, actorID).Scan(&n)
	return n, err
}

// tasks reads `v_task_ledger` for a project, oldest spawn first, and hangs
// each crew's current scene state on it.
func (s *Server) tasks(ctx context.Context, project string, now time.Time) ([]Task, error) {
	scenes, err := s.sceneRows(ctx, project)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT crew_actor_id, crew, text, branch, spawned_at, closed_at, close_state,
		       question_count, handback_count, turns,
		       input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, thinking_tokens,
		       context_tokens_last, last_model, context_pct, waited_ms, cost, merged_at
		  FROM v_task_ledger WHERE project = ? ORDER BY spawned_at, crew`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Task{}
	var ids []string
	for rows.Next() {
		var t Task
		var actorID string
		var branch, spawnedAt, closedAt, closeState, lastModel, mergedAt sql.NullString
		var ctxLast sql.NullInt64
		var pct, cost sql.NullFloat64
		if err := rows.Scan(&actorID, &t.Crew, &t.Text, &branch, &spawnedAt, &closedAt, &closeState,
			&t.QuestionCount, &t.HandbackCount, &t.Turns,
			&t.Tokens.Input, &t.Tokens.CacheRead, &t.Tokens.CacheWrite, &t.Tokens.Output, &t.Tokens.Thinking,
			&ctxLast, &lastModel, &pct, &t.WaitedMs, &cost, &mergedAt); err != nil {
			return nil, err
		}
		t.Branch, t.SpawnedAt, t.ClosedAt = branch.String, spawnedAt.String, closedAt.String
		t.CloseState, t.LastModel, t.MergedAt = closeState.String, lastModel.String, mergedAt.String
		t.Closed = t.CloseState != "" || t.ClosedAt != ""
		t.ContextTokensLast = ctxLast.Int64
		t.ContextPct, t.Cost = nullFloat(pct), nullFloat(cost)
		t.Tokens.Total = t.Tokens.Input + t.Tokens.CacheRead + t.Tokens.CacheWrite + t.Tokens.Output
		t.AgeMs = ageMs(t.SpawnedAt, t.ClosedAt, now)
		if sc, ok := scenes[actorID]; ok {
			t.State, t.Since, t.Target, t.Detail = sc.State, sc.Since, sc.Target, sc.Detail
		}
		out = append(out, t)
		ids = append(ids, actorID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, id := range ids {
		n, err := s.toolCount(ctx, id)
		if err != nil {
			return nil, err
		}
		out[i].ToolCount = n
		facts, _, err := s.actorFacts(ctx, id)
		if err != nil {
			return nil, err
		}
		out[i].Harness = facts.harness
	}
	return out, nil
}

// task is one crew's ledger row, or a 404 naming the crew.
func (s *Server) task(ctx context.Context, project, crew string, now time.Time) (Task, error) {
	all, err := s.tasks(ctx, project, now)
	if err != nil {
		return Task{}, err
	}
	for _, t := range all {
		if t.Crew == crew {
			return t, nil
		}
	}
	return Task{}, notFound("no crew %q in project %q; `mate crew list %s --all` names the ones there are", crew, project, project)
}

// turns reads an actor's turns in the order they happened, with the kind of
// the event that triggered each. The trigger's kind is a join onto `event`:
// `turn` records only `trigger_event_id`, and a page that showed a bare id
// would be showing the reader a number they cannot read.
func (s *Server) turns(ctx context.Context, actorID string) ([]Turn, error) {
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT u.id, u.session_id, u.harness_turn_ref, u.ordinal,
		       u.started_at, u.ended_at, u.trigger_event_id, e.kind,
		       u.outcome, u.model, u.input_tokens, u.cache_read_tokens, u.cache_write_tokens,
		       u.output_tokens, u.thinking_tokens, u.context_tokens_after, u.tool_count,
		       u.ref_path, u.ref_offset,
		       CASE WHEN p.context_window > 0
		            THEN 100.0 * u.context_tokens_after / p.context_window ELSE NULL END
		  FROM turn u
		  LEFT JOIN event e ON e.id = u.trigger_event_id
		  LEFT JOIN pricing p ON p.model = u.model
		 WHERE u.actor_id = ?
		 ORDER BY u.started_at, u.ordinal`, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Turn{}
	for rows.Next() {
		var t Turn
		var startedAt, endedAt, triggerKind, outcome, model sql.NullString
		var trigger sql.NullInt64
		var pct sql.NullFloat64
		if err := rows.Scan(&t.ID, &t.SessionID, &t.HarnessTurnRef, &t.Ordinal,
			&startedAt, &endedAt, &trigger, &triggerKind,
			&outcome, &model, &t.Tokens.Input, &t.Tokens.CacheRead, &t.Tokens.CacheWrite,
			&t.Tokens.Output, &t.Tokens.Thinking, &t.ContextAfter, &t.ToolCount,
			&t.Ref.Path, &t.Ref.Offset, &pct); err != nil {
			return nil, err
		}
		t.StartedAt, t.EndedAt = startedAt.String, endedAt.String
		t.TriggerEventID, t.TriggerKind = trigger.Int64, triggerKind.String
		t.Outcome, t.Model = outcome.String, model.String
		t.Tokens.Total = t.Tokens.Input + t.Tokens.CacheRead + t.Tokens.CacheWrite + t.Tokens.Output
		t.DurationMs = spanMs(t.StartedAt, t.EndedAt)
		t.ContextPct = nullFloat(pct)
		out = append(out, t)
	}
	return out, rows.Err()
}

// turnByID is one turn of one actor. The actor is part of the lookup, not
// just the id: a turn id from another crew's page must 404 rather than
// render under this crew's heading.
func (s *Server) turnByID(ctx context.Context, actorID, turnID string) (Turn, error) {
	all, err := s.turns(ctx, actorID)
	if err != nil {
		return Turn{}, err
	}
	for _, t := range all {
		if t.ID == turnID {
			return t, nil
		}
	}
	return Turn{}, notFound("no turn %q recorded for this actor", turnID)
}

// actions reads the tool calls of one turn.
//
// `action` carries no `ref_path`/`ref_offset` of its own - the schema puts
// the locator on `event` - so the ref comes from the `tool.called` event the
// row was written beside, through `action.event_id`.
func (s *Server) actions(ctx context.Context, turnID string) ([]Action, error) {
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT a.id, a.at, a.ended_at, a.tool, a.target, a.summary, a.duration_ms, a.ok,
		       a.event_id, COALESCE(e.ref_path,''), COALESCE(e.ref_offset,0)
		  FROM action a LEFT JOIN event e ON e.id = a.event_id
		 WHERE a.turn_id = ? ORDER BY a.at, a.id`, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Action{}
	for rows.Next() {
		var a Action
		var endedAt sql.NullString
		var dur, eventID sql.NullInt64
		var ok sql.NullBool
		if err := rows.Scan(&a.ID, &a.At, &endedAt, &a.Tool, &a.Target, &a.Summary,
			&dur, &ok, &eventID, &a.Ref.Path, &a.Ref.Offset); err != nil {
			return nil, err
		}
		a.EndedAt, a.EventID = endedAt.String, eventID.Int64
		if dur.Valid {
			v := dur.Int64
			a.DurationMs = &v
		}
		if ok.Valid {
			v := ok.Bool
			a.OK = &v
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// turnEvents are the `v_story` rows recorded inside one turn - the same
// rows, in the same shape and the same field order, that `mate events`
// prints as JSON lines.
func (s *Server) turnEvents(ctx context.Context, project, turnID string) ([]timeline.StoryEvent, error) {
	out, err := timeline.Story(ctx, s.db.SQL(), timeline.StoryQuery{Project: project, TurnID: turnID})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []timeline.StoryEvent{}
	}
	return out, nil
}

// statusLines are the `status.appended` events of one crew: the lines the
// crew itself wrote, with the verb and the text the ingest already split
// out of the payload (docs/timeline.md section 4).
func (s *Server) statusLines(ctx context.Context, actorID string) ([]StatusLine, error) {
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT id, at, turn_id, payload, ref_path, ref_offset
		  FROM event WHERE actor_id = ? AND kind = ? ORDER BY at, id`,
		actorID, timeline.KindStatusAppend)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []StatusLine{}
	for rows.Next() {
		var line StatusLine
		var turnID, payload sql.NullString
		if err := rows.Scan(&line.EventID, &line.At, &turnID, &payload,
			&line.Ref.Path, &line.Ref.Offset); err != nil {
			return nil, err
		}
		line.TurnID = turnID.String
		ev := timeline.StoryEvent{Payload: []byte(payloadOrEmpty(payload))}
		line.Verb, line.Text, line.Line = ev.Field("verb"), ev.Field("text"), ev.Field("line")
		out = append(out, line)
	}
	return out, rows.Err()
}

// questions are what the crew stopped and asked, with the answer joined on.
//
// The answer's text is not on `question` - the table records which event
// answered it, not what the answer said - so it is read from the answering
// event itself, and the answerer's display name from `actor`.
//
// Two sources, in that order, because the answering event is a
// `question.answered` and the line that carried the words is the
// `message.sent` beside it: `message.text` when a `message` row exists for
// the event, otherwise the event's own payload, whose `text` is the answer
// verbatim (docs/timeline.md's question.answered example). Preferring
// `message` keeps the page showing the line as sent even if the payload
// were ever trimmed.
func (s *Server) questions(ctx context.Context, actorID string) ([]Question, error) {
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT q.id, q.asked_event_id, q.asked_at, q.text, q.answered_event_id, q.answered_at,
		       a.name, m.text, e.payload, q.waited_ms
		  FROM question q
		  LEFT JOIN actor a ON a.id = q.answered_by_actor_id
		  LEFT JOIN message m ON m.event_id = q.answered_event_id
		  LEFT JOIN event e ON e.id = q.answered_event_id
		 WHERE q.crew_actor_id = ? ORDER BY q.asked_at, q.id`, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Question{}
	for rows.Next() {
		var q Question
		var answeredEvent, waited sql.NullInt64
		var answeredAt, answeredBy, answer, payload sql.NullString
		if err := rows.Scan(&q.ID, &q.AskedEventID, &q.AskedAt, &q.Text,
			&answeredEvent, &answeredAt, &answeredBy, &answer, &payload, &waited); err != nil {
			return nil, err
		}
		q.AnsweredEventID, q.AnsweredAt = answeredEvent.Int64, answeredAt.String
		q.AnsweredBy, q.Answer = answeredBy.String, answer.String
		if q.Answer == "" && answeredEvent.Valid {
			q.Answer = timeline.StoryEvent{Payload: []byte(payloadOrEmpty(payload))}.Field("text")
		}
		if waited.Valid {
			v := waited.Int64
			q.WaitedMs = &v
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// inbox is the project's waiting decisions, through query.LoadBox - the
// exact loader cmd/mate/console_session.go hands the console's rail, so
// the page and the TUI cannot disagree about what is waiting. It reads
// files and writes none.
func (s *Server) inbox(project string) ([]InboxItem, string) {
	field := query.LoadBox(s.ws, project)
	if !field.IsKnown() {
		return []InboxItem{}, field.Reason
	}
	view := field.Value
	out := make([]InboxItem, 0, len(view.Inbox))
	for _, e := range view.Inbox {
		out = append(out, InboxItem{
			Seq:       e.Seq,
			At:        db.FormatTime(e.At),
			Kind:      string(e.Kind),
			Source:    e.Source,
			Target:    e.Target,
			Crew:      e.Crew,
			Verb:      e.Verb,
			Text:      e.Text,
			Attention: e.Attention,
		})
	}
	return out, ""
}

// mateBlock is the Mate of one project: `v_now` for where it stands,
// `actor` for its harness and whether it has gone, `turn` for what it has
// spent and what it last did.
func (s *Server) mateBlock(ctx context.Context, project string, scenes map[string]SceneRow) (Mate, error) {
	actorID := timeline.MateActorID(project)
	facts, _, err := s.actorFacts(ctx, actorID)
	if err != nil {
		return Mate{}, err
	}
	out := Mate{Harness: facts.harness, Running: facts.running()}
	if sc, ok := scenes[actorID]; ok {
		out.State, out.Since, out.Target, out.Detail = sc.State, sc.Since, sc.Target, sc.Detail
		out.TokensToday, out.ContextPct, out.ContextTokens = sc.TokensToday, sc.ContextPct, sc.ContextTokens
	}
	turns, tokens, cost, err := s.actorTotals(ctx, actorID)
	if err != nil {
		return Mate{}, err
	}
	out.Turns, out.Tokens, out.Cost = turns, tokens, cost
	all, err := s.turns(ctx, actorID)
	if err != nil {
		return Mate{}, err
	}
	if len(all) > 0 {
		last := all[len(all)-1]
		out.LastTurn = &last
	}
	return out, nil
}

// nullFloat renders a nullable REAL as a pointer: a missing cost or an
// unknown context window is `null` on the wire and `?` in the CLI, never a
// zero the reader would believe.
func nullFloat(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	out := v.Float64
	return &out
}

func payloadOrEmpty(v sql.NullString) string {
	if !v.Valid || v.String == "" {
		return "{}"
	}
	return v.String
}

// spanMs is ended-started in milliseconds, 0 when either end is missing -
// a turn that has not ended has no duration yet, which is not a duration of
// zero, but a page showing a live turn wants a running row and not a null.
func spanMs(startedAt, endedAt string) int64 {
	if startedAt == "" || endedAt == "" {
		return 0
	}
	start, end := db.ParseTime(startedAt), db.ParseTime(endedAt)
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func ageMs(spawnedAt, closedAt string, now time.Time) int64 {
	if spawnedAt == "" {
		return 0
	}
	start := db.ParseTime(spawnedAt)
	if start.IsZero() {
		return 0
	}
	end := now
	if closedAt != "" {
		if t := db.ParseTime(closedAt); !t.IsZero() {
			end = t
		}
	}
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}
