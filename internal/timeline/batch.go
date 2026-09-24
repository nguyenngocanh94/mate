package timeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
)

// pendingEvent is one event a pass wants to record. It is collected rather
// than inserted immediately because `event.id` must be reproducible: a
// rebuild of the same files has to hand out the same ids, so a pass sorts
// everything it found into one deterministic order and inserts it in that
// order. Dedup is the fact's natural key and the handle every row that
// references an event uses.
type pendingEvent struct {
	Dedup      string
	Project    string
	At         time.Time
	ActorID    string
	Kind       string
	Subject    string
	TurnID     string
	TaskActor  string
	Payload    map[string]any
	RefPath    string
	RefOffset  int64
	CauseDedup string
}

type pendingTurn struct {
	ID             string
	ActorID        string
	SessionID      string
	Ordinal        int
	StartedAt      time.Time
	EndedAt        time.Time
	Outcome        string
	Model          string
	HarnessTurnRef string
	Input          int64
	CacheRead      int64
	CacheWrite     int64
	Output         int64
	Thinking       int64
	ContextAfter   int64
	ToolCount      int
	RefPath        string
	RefOffset      int64
}

type pendingAction struct {
	ID         string
	TurnID     string
	EventDedup string
	ActorID    string
	At         time.Time
	EndedAt    time.Time
	Tool       string
	Target     string
	Summary    string
	DurationMS *int64
	OK         *bool
}

type pendingMessage struct {
	EventDedup string
	From       string
	To         string
	Channel    string
	Text       string
	Marked     bool
}

type pendingQuestion struct {
	ID          string
	AskedDedup  string
	CrewActorID string
	Text        string
	AskedAt     time.Time
	// The answer half is filled in the same pass when the answering line is
	// already in `sent.log`, and by a later pass otherwise.
	AnsweredDedup string
	AnsweredBy    string
	AnsweredAt    time.Time
}

type pendingIncident struct {
	ID            string
	Project       string
	ActorID       string
	Kind          string
	OpenedDedup   string
	OpenedAt      time.Time
	ResolvedDedup string
	ResolvedAt    time.Time
}

type pendingUsage struct {
	ID         string
	SessionID  string
	At         time.Time
	Cumulative bool
	Input      int64
	CacheRead  int64
	CacheWrite int64
	Output     int64
	Thinking   int64
	RefPath    string
	RefOffset  int64
}

type pendingSession struct {
	ID             string
	ActorID        string
	HarnessSession string
	TranscriptPath string
	StartedAt      time.Time
	EndedAt        time.Time
	ResumedFrom    string
}

type pendingTask struct {
	CrewActorID string
	Project     string
	Text        string
	BriefPath   string
	Branch      string
	Worktree    string
	SpawnedAt   time.Time
	ClosedAt    time.Time
	CloseState  string
	MergedDedup string
}

type pendingActor struct {
	ID        string
	Project   string
	Kind      string
	Name      string
	Harness   string
	FirstSeen time.Time
	LastSeen  time.Time
	GoneAt    time.Time
}

// batch is everything one project's pass found, before any of it is written.
type batch struct {
	actors    []pendingActor
	sessions  []pendingSession
	tasks     []pendingTask
	events    []pendingEvent
	turns     []pendingTurn
	actions   []pendingAction
	messages  []pendingMessage
	questions []*pendingQuestion
	incidents []*pendingIncident
	usage     []pendingUsage
	cursors   map[string]int64
}

func newBatch() *batch { return &batch{cursors: map[string]int64{}} }

func (b *batch) actor(a pendingActor)         { b.actors = append(b.actors, a) }
func (b *batch) session(s pendingSession)     { b.sessions = append(b.sessions, s) }
func (b *batch) task(t pendingTask)           { b.tasks = append(b.tasks, t) }
func (b *batch) event(e pendingEvent)         { b.events = append(b.events, e) }
func (b *batch) turn(t pendingTurn)           { b.turns = append(b.turns, t) }
func (b *batch) action(a pendingAction)       { b.actions = append(b.actions, a) }
func (b *batch) message(m pendingMessage)     { b.messages = append(b.messages, m) }
func (b *batch) question(q *pendingQuestion)  { b.questions = append(b.questions, q) }
func (b *batch) incident(in *pendingIncident) { b.incidents = append(b.incidents, in) }
func (b *batch) usageSample(u pendingUsage)   { b.usage = append(b.usage, u) }
func (b *batch) cursor(path string, at int64) { b.cursors[path] = at }

// sortEvents puts a pass's events into the one order a rebuild will also
// produce. Time first, because that is the story's order; then the source
// byte the fact came from, which is exact and total within one file; then the
// kind and the natural key, which break the remaining ties between facts read
// from different files in the same instant.
func (b *batch) sortEvents() {
	sort.SliceStable(b.events, func(i, j int) bool {
		a, c := b.events[i], b.events[j]
		if !a.At.Equal(c.At) {
			return a.At.Before(c.At)
		}
		if a.RefPath != c.RefPath {
			return a.RefPath < c.RefPath
		}
		if a.RefOffset != c.RefOffset {
			return a.RefOffset < c.RefOffset
		}
		if rankA, rankC := kindRank(a.Kind), kindRank(c.Kind); rankA != rankC {
			return rankA < rankC
		}
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		return a.Dedup < c.Dedup
	})
}

// kindRank orders two facts read from the same byte of the same file. Only
// that case reaches it, and in that case alphabetical order would tell the
// story backwards: a crew's `question.asked` and the `status.appended` that
// carries it share a line, and the question is what the line says, not what
// happened before it was written.
func kindRank(kind string) int {
	switch kind {
	case KindTurnStarted:
		return 10
	case KindToolCalled:
		return 20
	case KindStatusAppend:
		return 30
	case KindQuestionAsked:
		return 31
	case KindMessageSent, KindDigestSent, KindAssignClicked:
		return 40
	case KindQuestionAnsw:
		return 41
	case KindToolFinished:
		return 50
	case KindTurnEnded:
		return 60
	default:
		return 45
	}
}

// writer is the batch's other half: it holds the transaction and the map from
// a natural key to the `event.id` the insert produced.
type writer struct {
	tx  *sql.Tx
	ids map[string]int64
	now time.Time
	// insertedEvents counts the events this pass actually wrote. The scene
	// projection reads it: a pass that found nothing new cannot have moved
	// anybody, and recomputing the projection for it would be work nobody
	// asked for.
	insertedEvents int
}

func newWriter(tx *sql.Tx, now time.Time) *writer {
	return &writer{tx: tx, ids: map[string]int64{}, now: now}
}

// eventID resolves a natural key to a row id, looking first at what this pass
// inserted and then at what an earlier pass did. An unknown key is not an
// error: it is a cause that has not been recorded (yet, or ever), and a NULL
// cause is the honest value for it.
func (w *writer) eventID(ctx context.Context, dedup string) (int64, bool) {
	if dedup == "" {
		return 0, false
	}
	if id, ok := w.ids[dedup]; ok {
		return id, true
	}
	var id int64
	err := w.tx.QueryRowContext(ctx, `SELECT id FROM event WHERE dedup = ?`, dedup).Scan(&id)
	if err != nil {
		return 0, false
	}
	w.ids[dedup] = id
	return id, true
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return db.FormatTime(t)
}

func payloadJSON(p map[string]any) string {
	if len(p) == 0 {
		return "{}"
	}
	// encoding/json sorts map keys, so the same payload always renders the
	// same bytes and a `v_story` dump is comparable between two rebuilds.
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Sprintf(`{"payload_error":%q}`, err.Error())
	}
	return string(raw)
}

// flush writes one project's pass. Actors and sessions first because every
// other row references them, then events in their sorted order, then the rows
// that hang off an event id.
func (w *writer) flush(ctx context.Context, b *batch) error {
	for _, a := range b.actors {
		if err := w.upsertActor(ctx, a); err != nil {
			return err
		}
	}
	for _, s := range b.sessions {
		if err := w.upsertSession(ctx, s); err != nil {
			return err
		}
	}
	for _, t := range b.tasks {
		if err := w.upsertTask(ctx, t); err != nil {
			return err
		}
	}
	b.sortEvents()
	for _, e := range b.events {
		if err := w.insertEvent(ctx, e); err != nil {
			return err
		}
	}
	for _, t := range b.turns {
		if err := w.upsertTurn(ctx, t); err != nil {
			return err
		}
	}
	for _, a := range b.actions {
		if err := w.upsertAction(ctx, a); err != nil {
			return err
		}
	}
	for _, m := range b.messages {
		if err := w.upsertMessage(ctx, m); err != nil {
			return err
		}
	}
	for _, q := range b.questions {
		if err := w.upsertQuestion(ctx, *q); err != nil {
			return err
		}
	}
	for _, in := range b.incidents {
		if err := w.upsertIncident(ctx, *in); err != nil {
			return err
		}
	}
	for _, u := range b.usage {
		if err := w.upsertUsage(ctx, u); err != nil {
			return err
		}
	}
	for _, t := range b.tasks {
		if t.MergedDedup == "" {
			continue
		}
		id, ok := w.eventID(ctx, t.MergedDedup)
		if !ok {
			continue
		}
		if _, err := w.tx.ExecContext(ctx,
			`UPDATE task SET merged_event_id = ? WHERE crew_actor_id = ?`, id, t.CrewActorID); err != nil {
			return err
		}
	}
	for path, offset := range b.cursors {
		if _, err := w.tx.ExecContext(ctx,
			`INSERT INTO cursor(source_path, byte_offset, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT(source_path) DO UPDATE SET byte_offset = excluded.byte_offset, updated_at = excluded.updated_at`,
			path, offset, db.FormatTime(w.now)); err != nil {
			return err
		}
	}
	return nil
}

func (w *writer) upsertActor(ctx context.Context, a pendingActor) error {
	_, err := w.tx.ExecContext(ctx,
		`INSERT INTO actor(id, project, kind, name, harness, first_seen, last_seen, gone_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   harness   = CASE WHEN excluded.harness <> '' THEN excluded.harness ELSE actor.harness END,
		   first_seen= COALESCE(actor.first_seen, excluded.first_seen),
		   last_seen = MAX(COALESCE(actor.last_seen, ''), COALESCE(excluded.last_seen, '')),
		   gone_at   = COALESCE(excluded.gone_at, actor.gone_at)`,
		a.ID, a.Project, a.Kind, a.Name, a.Harness,
		nullTime(a.FirstSeen), nullTime(a.LastSeen), nullTime(a.GoneAt))
	return err
}

func (w *writer) upsertSession(ctx context.Context, s pendingSession) error {
	_, err := w.tx.ExecContext(ctx,
		`INSERT INTO session(id, actor_id, harness_session_id, transcript_path, started_at, ended_at, resumed_from_session_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   transcript_path = CASE WHEN excluded.transcript_path <> '' THEN excluded.transcript_path ELSE session.transcript_path END,
		   started_at = COALESCE(session.started_at, excluded.started_at),
		   ended_at   = COALESCE(excluded.ended_at, session.ended_at)`,
		s.ID, s.ActorID, s.HarnessSession, s.TranscriptPath,
		nullTime(s.StartedAt), nullTime(s.EndedAt), nullString(s.ResumedFrom))
	return err
}

func (w *writer) upsertTask(ctx context.Context, t pendingTask) error {
	_, err := w.tx.ExecContext(ctx,
		`INSERT INTO task(crew_actor_id, project, text, brief_path, branch, worktree, spawned_at, closed_at, close_state)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(crew_actor_id) DO UPDATE SET
		   text        = CASE WHEN excluded.text <> '' THEN excluded.text ELSE task.text END,
		   brief_path  = CASE WHEN excluded.brief_path <> '' THEN excluded.brief_path ELSE task.brief_path END,
		   branch      = CASE WHEN excluded.branch <> '' THEN excluded.branch ELSE task.branch END,
		   worktree    = CASE WHEN excluded.worktree <> '' THEN excluded.worktree ELSE task.worktree END,
		   spawned_at  = COALESCE(task.spawned_at, excluded.spawned_at),
		   closed_at   = COALESCE(excluded.closed_at, task.closed_at),
		   close_state = COALESCE(excluded.close_state, task.close_state)`,
		t.CrewActorID, t.Project, t.Text, t.BriefPath, t.Branch, t.Worktree,
		nullTime(t.SpawnedAt), nullTime(t.ClosedAt), nullString(t.CloseState))
	return err
}

func (w *writer) insertEvent(ctx context.Context, e pendingEvent) error {
	var cause any
	if id, ok := w.eventID(ctx, e.CauseDedup); ok {
		cause = id
	}
	res, err := w.tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO event(dedup, project, at, actor_id, kind, subject_actor_id, turn_id, task_actor_id, cause_event_id, payload, ref_path, ref_offset)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Dedup, e.Project, db.FormatTime(e.At), e.ActorID, e.Kind,
		nullString(e.Subject), nullString(e.TurnID), nullString(e.TaskActor),
		cause, payloadJSON(e.Payload), e.RefPath, e.RefOffset)
	if err != nil {
		return fmt.Errorf("timeline: insert %s event: %w", e.Kind, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		w.ids[e.Dedup] = id
		w.insertedEvents++
		return nil
	}
	// Already recorded by an earlier pass: keep the id so this pass's rows
	// can still reference it.
	_, _ = w.eventID(ctx, e.Dedup)
	return nil
}

func (w *writer) upsertTurn(ctx context.Context, t pendingTurn) error {
	_, err := w.tx.ExecContext(ctx,
		`INSERT INTO turn(id, actor_id, session_id, ordinal, started_at, ended_at, outcome, model, harness_turn_ref,
		                  input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, thinking_tokens,
		                  context_tokens_after, tool_count, ref_path, ref_offset)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   ended_at             = COALESCE(excluded.ended_at, turn.ended_at),
		   outcome              = excluded.outcome,
		   model                = excluded.model,
		   input_tokens         = excluded.input_tokens,
		   cache_read_tokens    = excluded.cache_read_tokens,
		   cache_write_tokens   = excluded.cache_write_tokens,
		   output_tokens        = excluded.output_tokens,
		   thinking_tokens      = excluded.thinking_tokens,
		   context_tokens_after = excluded.context_tokens_after,
		   tool_count           = excluded.tool_count`,
		t.ID, t.ActorID, t.SessionID, t.Ordinal, nullTime(t.StartedAt), nullTime(t.EndedAt),
		t.Outcome, t.Model, t.HarnessTurnRef,
		t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Thinking,
		t.ContextAfter, t.ToolCount, t.RefPath, t.RefOffset)
	return err
}

func (w *writer) upsertAction(ctx context.Context, a pendingAction) error {
	var eventID any
	if id, ok := w.eventID(ctx, a.EventDedup); ok {
		eventID = id
	}
	var duration any
	if a.DurationMS != nil {
		duration = *a.DurationMS
	}
	var ok any
	if a.OK != nil {
		if *a.OK {
			ok = 1
		} else {
			ok = 0
		}
	}
	_, err := w.tx.ExecContext(ctx,
		`INSERT INTO action(id, turn_id, event_id, at, ended_at, actor_id, tool, target, summary, duration_ms, ok)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   turn_id     = COALESCE(excluded.turn_id, action.turn_id),
		   event_id    = COALESCE(excluded.event_id, action.event_id),
		   ended_at    = COALESCE(excluded.ended_at, action.ended_at),
		   duration_ms = COALESCE(excluded.duration_ms, action.duration_ms),
		   ok          = COALESCE(excluded.ok, action.ok)`,
		a.ID, nullString(a.TurnID), eventID, db.FormatTime(a.At), nullTime(a.EndedAt),
		a.ActorID, a.Tool, a.Target, a.Summary, duration, ok)
	return err
}

func (w *writer) upsertMessage(ctx context.Context, m pendingMessage) error {
	id, ok := w.eventID(ctx, m.EventDedup)
	if !ok {
		return nil
	}
	marked := 0
	if m.Marked {
		marked = 1
	}
	_, err := w.tx.ExecContext(ctx,
		`INSERT INTO message(event_id, from_actor_id, to_actor_id, channel, text, marked)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(event_id) DO NOTHING`,
		id, m.From, m.To, m.Channel, m.Text, marked)
	return err
}

func (w *writer) upsertQuestion(ctx context.Context, q pendingQuestion) error {
	var answered, waited any
	if id, found := w.eventID(ctx, q.AnsweredDedup); found {
		answered = id
		waited = q.AnsweredAt.Sub(q.AskedAt).Milliseconds()
	}
	asked, ok := w.eventID(ctx, q.AskedDedup)
	if !ok {
		// An older question, already in the table, that this pass has just
		// found the answer to: only the answer half is new.
		if answered == nil {
			return nil
		}
		_, err := w.tx.ExecContext(ctx,
			`UPDATE question SET answered_event_id = ?, answered_by_actor_id = ?, answered_at = ?, waited_ms = ?
			  WHERE id = ? AND answered_event_id IS NULL`,
			answered, q.AnsweredBy, db.FormatTime(q.AnsweredAt), waited, q.ID)
		return err
	}
	_, err := w.tx.ExecContext(ctx,
		`INSERT INTO question(id, asked_event_id, crew_actor_id, text, asked_at, answered_event_id, answered_by_actor_id, answered_at, waited_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   answered_event_id    = COALESCE(excluded.answered_event_id, question.answered_event_id),
		   answered_by_actor_id = COALESCE(excluded.answered_by_actor_id, question.answered_by_actor_id),
		   answered_at          = COALESCE(excluded.answered_at, question.answered_at),
		   waited_ms            = COALESCE(excluded.waited_ms, question.waited_ms)`,
		q.ID, asked, q.CrewActorID, q.Text, db.FormatTime(q.AskedAt),
		answered, nullString(q.AnsweredBy), nullTime(q.AnsweredAt), waited)
	return err
}

func (w *writer) upsertIncident(ctx context.Context, in pendingIncident) error {
	var resolved any
	if id, found := w.eventID(ctx, in.ResolvedDedup); found {
		resolved = id
	}
	opened, ok := w.eventID(ctx, in.OpenedDedup)
	if !ok {
		// An incident opened by an earlier pass that this one has just seen
		// closed: only the resolve half is new.
		if resolved == nil {
			return nil
		}
		_, err := w.tx.ExecContext(ctx,
			`UPDATE incident SET resolved_event_id = ?, resolved_at = ?
			  WHERE id = ? AND resolved_event_id IS NULL`,
			resolved, db.FormatTime(in.ResolvedAt), in.ID)
		return err
	}
	_, err := w.tx.ExecContext(ctx,
		`INSERT INTO incident(id, project, actor_id, kind, opened_event_id, opened_at, resolved_event_id, resolved_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   resolved_event_id = COALESCE(excluded.resolved_event_id, incident.resolved_event_id),
		   resolved_at       = COALESCE(excluded.resolved_at, incident.resolved_at)`,
		in.ID, in.Project, in.ActorID, in.Kind, opened, db.FormatTime(in.OpenedAt),
		resolved, nullTime(in.ResolvedAt))
	return err
}

func (w *writer) upsertUsage(ctx context.Context, u pendingUsage) error {
	cumulative := 0
	if u.Cumulative {
		cumulative = 1
	}
	_, err := w.tx.ExecContext(ctx,
		`INSERT INTO usage_sample(id, session_id, at, cumulative, input, cache_read, cache_write, output, thinking, ref_path, ref_offset)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		u.ID, u.SessionID, db.FormatTime(u.At), cumulative,
		u.Input, u.CacheRead, u.CacheWrite, u.Output, u.Thinking, u.RefPath, u.RefOffset)
	return err
}
