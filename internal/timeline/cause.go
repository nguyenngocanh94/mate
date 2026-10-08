package timeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
)

// linkCauses fills `event.cause_event_id` for the kinds docs/timeline.md
// gives a rule to, and `turn.trigger_event_id` for every turn.
//
// It runs after the pass has flushed, because a cause is an event and an
// event has no id until it is inserted. It only ever fills a NULL: a cause
// once decided is never re-decided, so a later pass cannot move a link a
// reader has already seen.
//
// Every rule is "the latest qualifying event at or before this one". None of
// them is "the nearest event in time": M5's first question is answered by a
// chain that is derived from what each side actually did, not from two things
// having happened close together.
func (p *pass) linkCauses(ctx context.Context) error {
	for _, link := range []func(context.Context) error{
		p.causeQuestionAsked,
		p.causeCarriedQuestions,
		p.causeMateTurnStarted,
		p.causeQuestionAnswered,
		p.causeCrewTurnAfterAnswer,
		p.causeCrewSpawned,
		p.causeMergeDone,
		p.fillTriggers,
	} {
		if err := link(ctx); err != nil {
			return err
		}
	}
	return nil
}

// causeQuestionAsked: a crew's question is caused by the end of the turn it
// asked in. The crew wrote the status line with a shell command inside a
// turn, so the turn that was running is the cause; when the line is dated by
// the file's mtime instead, the latest turn that had already ended is the
// closest thing the files can prove.
func (p *pass) causeQuestionAsked(ctx context.Context) error {
	_, err := p.tx.ExecContext(ctx, `
		UPDATE event SET cause_event_id = (
			SELECT t.id FROM event t
			 WHERE t.actor_id = event.actor_id AND t.kind = ? AND t.at <= event.at
			 ORDER BY t.at DESC, t.id DESC LIMIT 1)
		 WHERE project = ? AND kind = ? AND cause_event_id IS NULL`,
		KindTurnEnded, p.project, KindQuestionAsked)
	return err
}

// causeCarriedQuestions: a digest and an `[assign]` line are caused by the
// question(s) they carry. The first one is the cause - a cause is one edge -
// and the payload of the question event says which crew, so the whole set is
// recoverable from the line itself.
func (p *pass) causeCarriedQuestions(ctx context.Context) error {
	rows, err := p.tx.QueryContext(ctx,
		`SELECT id, at, payload FROM event
		  WHERE project = ? AND kind IN (?, ?) AND cause_event_id IS NULL`,
		p.project, KindDigestSent, KindAssignClicked)
	if err != nil {
		return err
	}
	type carried struct {
		id      int64
		at      time.Time
		payload string
	}
	var lines []carried
	for rows.Next() {
		var c carried
		var at string
		if err := rows.Scan(&c.id, &at, &c.payload); err != nil {
			rows.Close()
			return err
		}
		c.at = db.ParseTime(at)
		lines = append(lines, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, line := range lines {
		text := payloadString(line.payload, "text")
		crews := crewsNamedIn(text, p.crewIDs())
		if len(crews) == 0 {
			continue
		}
		var first int64
		for _, crew := range crews {
			id, ok, err := p.latestQuestionAsked(ctx, CrewActorID(p.project, crew), line.at)
			if err != nil {
				return err
			}
			if ok && (first == 0 || id < first) {
				first = id
			}
		}
		if first == 0 {
			continue
		}
		if _, err := p.tx.ExecContext(ctx,
			`UPDATE event SET cause_event_id = ? WHERE id = ? AND cause_event_id IS NULL`,
			first, line.id); err != nil {
			return err
		}
	}
	return nil
}

func (p *pass) latestQuestionAsked(ctx context.Context, crewActor string, before time.Time) (int64, bool, error) {
	var id int64
	err := p.tx.QueryRowContext(ctx,
		`SELECT id FROM event WHERE actor_id = ? AND kind = ? AND at <= ?
		  ORDER BY at DESC, id DESC LIMIT 1`,
		crewActor, KindQuestionAsked, db.FormatTime(before)).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// causeMateTurnStarted: a Mate turn is caused by the line that reached its
// composer - the captain's own prompt, a daemon digest, or an `[assign]`.
// "At or before, latest first", over the three kinds a line into the Mate's
// pane can have.
func (p *pass) causeMateTurnStarted(ctx context.Context) error {
	_, err := p.tx.ExecContext(ctx, `
		UPDATE event SET cause_event_id = (
			SELECT m.id FROM event m JOIN message g ON g.event_id = m.id
			 WHERE g.to_actor_id = event.actor_id AND m.at <= event.at
			 ORDER BY m.at DESC, m.id DESC LIMIT 1)
		 WHERE project = ? AND kind = ? AND actor_id = ? AND cause_event_id IS NULL`,
		p.project, KindTurnStarted, p.mate.ActorID)
	return err
}

// causeQuestionAnswered: the Mate's answer is caused by the turn that sent
// it - the turn holding the `mate send <project> <crew>` the answer came
// out of. An answer the captain typed has no such turn and keeps a NULL
// cause, which is the honest record: nothing in any file says what the
// captain was doing first.
func (p *pass) causeQuestionAnswered(ctx context.Context) error {
	rows, err := p.tx.QueryContext(ctx,
		`SELECT id, at, subject_actor_id, actor_id FROM event
		  WHERE project = ? AND kind = ? AND cause_event_id IS NULL`,
		p.project, KindQuestionAnsw)
	if err != nil {
		return err
	}
	type answer struct {
		id      int64
		at      time.Time
		crew    string
		byActor string
	}
	var answers []answer
	for rows.Next() {
		var a answer
		var at string
		var crew sql.NullString
		if err := rows.Scan(&a.id, &at, &crew, &a.byActor); err != nil {
			rows.Close()
			return err
		}
		a.at, a.crew = db.ParseTime(at), crew.String
		answers = append(answers, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, a := range answers {
		if a.byActor != p.mate.ActorID {
			continue
		}
		crew := crewFromActorID(a.crew)
		cause, ok, err := p.turnThatRan(ctx, p.mate.ActorID, a.at,
			[]string{"mate send", crew}, 10*time.Minute)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if _, err := p.tx.ExecContext(ctx,
			`UPDATE event SET cause_event_id = ? WHERE id = ? AND cause_event_id IS NULL`,
			cause, a.id); err != nil {
			return err
		}
	}
	return nil
}

// causeCrewTurnAfterAnswer: the crew's next turn is caused by the answer. A
// crew treats an answer as an ordinary new prompt (docs/mvp.md section 4), so
// the first turn it starts after one is the work that answer caused.
func (p *pass) causeCrewTurnAfterAnswer(ctx context.Context) error {
	_, err := p.tx.ExecContext(ctx, `
		UPDATE event SET cause_event_id = (
			SELECT a.id FROM event a
			 WHERE a.kind = ? AND a.subject_actor_id = event.actor_id AND a.at <= event.at
			 ORDER BY a.at DESC, a.id DESC LIMIT 1)
		 WHERE project = ? AND kind = ? AND cause_event_id IS NULL
		   AND actor_id LIKE 'crew:%'
		   AND EXISTS (SELECT 1 FROM event a
		                WHERE a.kind = ? AND a.subject_actor_id = event.actor_id AND a.at <= event.at)`,
		KindQuestionAnsw, p.project, KindTurnStarted, KindQuestionAnsw)
	return err
}

// causeCrewSpawned: the Mate turn that ran `mate crew spawn`.
func (p *pass) causeCrewSpawned(ctx context.Context) error {
	rows, err := p.tx.QueryContext(ctx,
		`SELECT id, at, subject_actor_id FROM event
		  WHERE project = ? AND kind = ? AND cause_event_id IS NULL`,
		p.project, KindCrewSpawned)
	if err != nil {
		return err
	}
	type spawn struct {
		id   int64
		at   time.Time
		crew string
	}
	var spawns []spawn
	for rows.Next() {
		var s spawn
		var at string
		var crew sql.NullString
		if err := rows.Scan(&s.id, &at, &crew); err != nil {
			rows.Close()
			return err
		}
		s.at, s.crew = db.ParseTime(at), crewFromActorID(crew.String)
		spawns = append(spawns, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, s := range spawns {
		cause, ok, err := p.turnThatRan(ctx, p.mate.ActorID, s.at.Add(time.Minute),
			[]string{"crew spawn", s.crew}, 30*time.Minute)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if _, err := p.tx.ExecContext(ctx,
			`UPDATE event SET cause_event_id = ? WHERE id = ? AND cause_event_id IS NULL`,
			cause, s.id); err != nil {
			return err
		}
	}
	return nil
}

// causeMergeDone has two rules, tried in order, and the payload records which
// one fired.
//
//  1. The Mate turn that ran `mate merge <project> <crew>`. Exact, and the
//     one that fires when the Mate landed the branch itself after a review.
//  2. The crew's handback - its last `wait-mate` status line before the
//     merge. This is the captain's merge, and it is a weaker rule on purpose:
//     `mate merge` types into no pane, so a merge run from the Console
//     leaves nothing at all in `sent.log` (docs/mvp.md section 7) and no
//     event exists to point at. The handback is what the captain acted on,
//     which is the most the files can say about why the merge happened.
func (p *pass) causeMergeDone(ctx context.Context) error {
	rows, err := p.tx.QueryContext(ctx,
		`SELECT id, at, subject_actor_id FROM event
		  WHERE project = ? AND kind = ? AND cause_event_id IS NULL`,
		p.project, KindMergeDone)
	if err != nil {
		return err
	}
	type merge struct {
		id        int64
		at        time.Time
		crewActor string
	}
	var merges []merge
	for rows.Next() {
		var m merge
		var at string
		var crew sql.NullString
		if err := rows.Scan(&m.id, &at, &crew); err != nil {
			rows.Close()
			return err
		}
		m.at, m.crewActor = db.ParseTime(at), crew.String
		merges = append(merges, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, m := range merges {
		crew := crewFromActorID(m.crewActor)
		cause, ok, err := p.turnThatRan(ctx, p.mate.ActorID, m.at.Add(2*time.Minute),
			[]string{"mate merge", crew}, 30*time.Minute)
		if err != nil {
			return err
		}
		by := "mate"
		if !ok {
			by = "captain"
			cause, ok, err = p.lastHandback(ctx, m.crewActor, m.at)
			if err != nil {
				return err
			}
		}
		if !ok {
			continue
		}
		if _, err := p.tx.ExecContext(ctx,
			`UPDATE event
			    SET cause_event_id = ?,
			        payload = json_set(payload, '$.by', ?, '$.cause_rule', ?)
			  WHERE id = ? AND cause_event_id IS NULL`,
			cause, by, mergeCauseRule(by), m.id); err != nil {
			return err
		}
	}
	return nil
}

func mergeCauseRule(by string) string {
	if by == "mate" {
		return "mate.turn.ran.merge"
	}
	return "crew.handback"
}

func (p *pass) lastHandback(ctx context.Context, crewActor string, before time.Time) (int64, bool, error) {
	var id int64
	err := p.tx.QueryRowContext(ctx,
		`SELECT id FROM event
		  WHERE actor_id = ? AND kind = ? AND at <= ?
		    AND json_extract(payload, '$.verb') = 'wait-mate'
		  ORDER BY at DESC, id DESC LIMIT 1`,
		crewActor, KindStatusAppend, db.FormatTime(before)).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// turnThatRan finds the `turn.started` event of the latest turn of an actor
// that ran a command containing every one of the given fragments, within a
// window before `before`. The window exists so a merge is never attributed to
// a command the Mate ran an hour earlier for a different crew.
func (p *pass) turnThatRan(ctx context.Context, actorID string, before time.Time,
	fragments []string, window time.Duration) (int64, bool, error) {

	rows, err := p.tx.QueryContext(ctx,
		`SELECT a.turn_id, a.summary FROM action a
		  WHERE a.actor_id = ? AND a.at <= ? AND a.at >= ? AND a.turn_id IS NOT NULL
		  ORDER BY a.at DESC`,
		actorID, db.FormatTime(before), db.FormatTime(before.Add(-window)))
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var turnID, summary string
		if err := rows.Scan(&turnID, &summary); err != nil {
			return 0, false, err
		}
		if !containsAll(summary, fragments) {
			continue
		}
		var id int64
		err := p.tx.QueryRowContext(ctx,
			`SELECT id FROM event WHERE kind = ? AND turn_id = ? LIMIT 1`,
			KindTurnStarted, turnID).Scan(&id)
		if err != nil {
			continue
		}
		return id, true, nil
	}
	return 0, false, rows.Err()
}

func containsAll(haystack string, fragments []string) bool {
	for _, fragment := range fragments {
		if fragment == "" {
			continue
		}
		if !strings.Contains(haystack, fragment) {
			return false
		}
	}
	return true
}

// fillTriggers copies a `turn.started` event's cause onto the turn row, which
// is where the schema keeps it (`turn.trigger_event_id`). The two are the
// same fact seen from the two tables.
func (p *pass) fillTriggers(ctx context.Context) error {
	_, err := p.tx.ExecContext(ctx, `
		UPDATE turn SET trigger_event_id = (
			SELECT e.cause_event_id FROM event e
			 WHERE e.kind = ? AND e.turn_id = turn.id LIMIT 1)
		 WHERE trigger_event_id IS NULL
		   AND EXISTS (SELECT 1 FROM event e
		                WHERE e.kind = ? AND e.turn_id = turn.id AND e.cause_event_id IS NOT NULL)
		   AND actor_id IN (SELECT id FROM actor WHERE project = ?)`,
		KindTurnStarted, KindTurnStarted, p.project)
	return err
}

// updateCounters keeps `task.question_count` and `task.handback_count` equal
// to what the events say, so a ledger never has to count them again.
func (p *pass) updateCounters(ctx context.Context) error {
	_, err := p.tx.ExecContext(ctx, `
		UPDATE task SET
		  question_count = (SELECT COUNT(*) FROM event e
		                     WHERE e.kind = ? AND e.actor_id = task.crew_actor_id),
		  handback_count = (SELECT COUNT(*) FROM event e
		                     WHERE e.kind = ? AND e.actor_id = task.crew_actor_id
		                       AND json_extract(e.payload, '$.verb') = 'wait-mate')
		 WHERE project = ?`,
		KindQuestionAsked, KindStatusAppend, p.project)
	if err != nil {
		return err
	}
	_, err = p.tx.ExecContext(ctx, `
		UPDATE task SET close_cause_event_id = (
			SELECT e.id FROM event e
			 WHERE e.task_actor_id = task.crew_actor_id AND e.kind IN (?, ?)
			 ORDER BY e.at DESC, e.id DESC LIMIT 1)
		 WHERE project = ? AND closed_at IS NOT NULL AND close_cause_event_id IS NULL`,
		KindCrewFinished, KindCrewFailed, p.project)
	return err
}

// crewIDs is every crew of the project, so a digest line can be scanned for
// the names it mentions without guessing what a crew id looks like.
func (p *pass) crewIDs() []string {
	out := make([]string, 0, len(p.crews))
	for _, crew := range p.crews {
		out = append(out, crew.ID)
	}
	return out
}

// crewsNamedIn finds the crews a digest or assign line is about. A digest
// spells each item `<crew> needs-decision: "…"` and an assign line
// `resolve: <crew> asked: "…"` (docs/mvp.md section 5), so a crew is named
// when its id appears followed by a space in the line.
func crewsNamedIn(text string, crews []string) []string {
	var out []string
	for _, crew := range crews {
		if crew == "" {
			continue
		}
		if strings.Contains(text, crew+" ") || strings.Contains(text, " "+crew) {
			out = append(out, crew)
		}
	}
	return out
}

func payloadString(payload, key string) string {
	var values map[string]any
	if json.Unmarshal([]byte(payload), &values) != nil {
		return ""
	}
	if s, ok := values[key].(string); ok {
		return s
	}
	return ""
}
