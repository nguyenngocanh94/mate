package timeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/db"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// The two shapes an app line into the Mate's pane can have. `sent.log` holds
// them with the sentinel already stripped - the daemon writes the line it
// built and the Mate's own UserPromptSubmit hook strips the marker before
// appending (internal/hook) - so the channel is read from the verb, not from
// the marker, and `marked` records that the line was one matev2 typed itself.
const (
	digestPrefix = "digest: "
	assignPrefix = "resolve: "
	// hookAutoOffText is internal/hook's own note that an unmarked prompt
	// turned auto mode off. It is spelled here rather than imported for the
	// reason the meta keys are: internal/watch calls this ingest and must not
	// gain a dependency through it. TestHookTextsMatchHook keeps them equal.
	HookAutoOffText = "auto mode off: user prompt"
)

// hookEchoWindow bounds how long after a line matev2 typed the Mate's hook
// may still be recording that the model read it. It is generous because a
// Mate mid-turn can take a minute to reach the prompt (docs/mvp.md section 7),
// and short enough that a digest re-sent much later is a new handover.
const hookEchoWindow = 10 * time.Minute

// isHookEcho reports whether this line is the Mate's hook recording a line
// matev2 had already written, rather than a new one.
func isHookEcho(entry, previous store.SentEntry) bool {
	if entry.Source != store.SourceApp || entry.Target != store.TargetMate {
		return false
	}
	if previous.Text == "" || previous.Text != entry.Text {
		return false
	}
	if entry.Time.Before(previous.Time) {
		return false
	}
	return entry.Time.Sub(previous.Time) <= hookEchoWindow
}

// ingestStatus reads each crew's `crews/<id>.status` from its cursor.
//
// A status line has no timestamp. The crew wrote it with
// `echo "state: one line" >> $MATEV2_STATUS`, and that shell command is in
// the crew's own transcript with a timestamp on it, so the first rule is to
// find the line inside the command that echoed it. That rule is exact and it
// survives a rebuild, which matters more than it looks: the file's mtime -
// the only other clock available, and the one internal/box uses - is the time
// of the file's *last* line, so dating every line by it would put a question
// after the answer to it as soon as the crew wrote anything else.
func (p *pass) ingestStatus(ctx context.Context) error {
	for _, crew := range p.crews {
		path := p.ing.ws.CrewStatus(p.project, crew.ID)
		from, err := p.readCursor(ctx, path)
		if err != nil {
			return err
		}
		lines, next, err := p.ing.ws.ReadStatus(p.project, crew.ID, from)
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			continue
		}
		p.b.cursor(path, next)

		mtime := p.now
		if fi, statErr := os.Stat(path); statErr == nil {
			mtime = fi.ModTime()
		}
		previous, err := p.lastStatusTime(ctx, crew.ActorID)
		if err != nil {
			return err
		}
		for _, line := range lines {
			status := box.ParseStatus(line.Line)
			at, dated := p.dateStatusLine(crew.ActorID, line.Line, mtime, previous)
			previous = at
			statusDedup := dedup(KindStatusAppend, crew.ActorID, fmt.Sprint(line.Offset))
			p.b.event(pendingEvent{
				Dedup:   statusDedup,
				Project: p.project, At: at, ActorID: crew.ActorID, Subject: p.mate.ActorID,
				Kind: KindStatusAppend, TaskActor: crew.ActorID,
				Payload: map[string]any{
					"verb": string(status.State), "text": status.Text,
					"line": line.Line, "dated_by": dated,
				},
				RefPath: path, RefOffset: line.Offset,
			})
			if status.State != box.StateNeedsDecision {
				continue
			}
			askedDedup := dedup(KindQuestionAsked, crew.ActorID, fmt.Sprint(line.Offset))
			p.b.event(pendingEvent{
				Dedup:   askedDedup,
				Project: p.project, At: at, ActorID: crew.ActorID, Subject: p.mate.ActorID,
				Kind: KindQuestionAsked, TaskActor: crew.ActorID,
				Payload: map[string]any{"text": status.Text, "crew": crew.ID},
				RefPath: path, RefOffset: line.Offset,
			})
			p.b.question(&pendingQuestion{
				ID:          questionRowID(crew.ActorID, line.Offset),
				AskedDedup:  askedDedup,
				CrewActorID: crew.ActorID,
				Text:        status.Text,
				AskedAt:     at,
			})
		}
	}
	return nil
}

// dateStatusLine applies the two rules above and says which one fired, so a
// reader of a payload can tell an exact timestamp from an approximate one.
func (p *pass) dateStatusLine(actorID, line string, mtime, previous time.Time) (time.Time, string) {
	text := strings.TrimSpace(line)
	if text != "" {
		best := time.Time{}
		for _, cmd := range p.statusClock[actorID] {
			if !strings.Contains(cmd.command, text) {
				continue
			}
			if best.IsZero() || cmd.at.Before(best) {
				best = cmd.at
			}
		}
		if !best.IsZero() {
			return best, "transcript.shell"
		}
	}
	// The fallback, clamped so a file's lines never go backwards: mtime is
	// the newest line's time, so an older line can only be at or before it.
	at := mtime
	if !previous.IsZero() && at.Before(previous) {
		at = previous
	}
	return at, "status.mtime"
}

// lastAppLineToMate is the newest line matev2 typed into the Mate's pane that
// an earlier pass already recorded, so a hook echo split across two passes is
// still recognised.
func (p *pass) lastAppLineToMate(ctx context.Context) (store.SentEntry, error) {
	var at, text string
	err := p.tx.QueryRowContext(ctx,
		`SELECT e.at, json_extract(e.payload, '$.text') FROM event e
		  WHERE e.project = ? AND json_extract(e.payload, '$.from') = ?
		    AND json_extract(e.payload, '$.to') = ?
		  ORDER BY e.at DESC, e.id DESC LIMIT 1`,
		p.project, store.SourceApp, store.TargetMate).Scan(&at, &text)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.SentEntry{}, nil
		}
		return store.SentEntry{}, nil //nolint:nilerr // an unreadable hint is not a failure
	}
	return store.SentEntry{Time: db.ParseTime(at), Source: store.SourceApp, Target: store.TargetMate, Text: text}, nil
}

func (p *pass) lastStatusTime(ctx context.Context, actorID string) (time.Time, error) {
	var value sql.NullString
	err := p.tx.QueryRowContext(ctx,
		`SELECT at FROM event WHERE actor_id = ? AND kind = ? ORDER BY at DESC, id DESC LIMIT 1`,
		actorID, KindStatusAppend).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	return db.ParseTime(value.String), nil
}

// ingestSent reads `sent.log` from its cursor: every line matev2 or a human
// typed into a pane, with who typed it and where it went.
//
// One event per line, and its kind is the channel: a daemon digest is
// `digest.sent`, a console `[assign]` is `assign.clicked`, and everything
// else is `message.sent`. Three kinds rather than one event plus two
// duplicates, because a line is one thing that happened and a story that
// counted it twice would answer "how many times did the captain hand work
// over" with double.
func (p *pass) ingestSent(ctx context.Context) error {
	path := p.ing.ws.SentLog(p.project)
	from, err := p.readCursor(ctx, path)
	if err != nil {
		return err
	}
	entries, next, err := p.ing.ws.ReadSent(p.project, from)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	p.b.cursor(path, next)

	open, err := p.openQuestions(ctx)
	if err != nil {
		return err
	}
	previousToMate, err := p.lastAppLineToMate(ctx)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fromActor := p.actorForSource(entry.Source)
		toActor, crew := p.actorForTarget(entry.Target)
		channel, kind, marked := classifySent(entry)

		// A digest or an `[assign]` line reaches `sent.log` twice: once
		// written by the sender after the composer cleared, and once by the
		// Mate's own UserPromptSubmit hook when the model read it. That pair
		// is the evidence the sentinel survived to the model, and it is the
		// proof docs/mvp.md section 7 asks for - but it is one thing
		// happening, so the second copy is recorded as the reading rather
		// than as a second handover. A rejected digest is never written at
		// all (the daemon writes only after a verified send), so a verbatim
		// repeat of the line just before it is always the hook's.
		if isHookEcho(entry, previousToMate) {
			channel, kind = ChannelHook, KindMessageSent
		}
		if entry.Source == store.SourceApp && entry.Target == store.TargetMate {
			previousToMate = entry
		}

		eventDedup := dedup(kind, p.project, fmt.Sprint(entry.Offset))
		payload := map[string]any{
			"channel": channel, "from": entry.Source, "to": entry.Target,
			"text": entry.Text, "marked": marked,
		}
		if channel == ChannelHook && entry.Text != HookAutoOffText {
			payload["confirms"] = true
		}
		if crew != "" {
			payload["crew"] = crew
		}
		p.b.event(pendingEvent{
			Dedup:   eventDedup,
			Project: p.project, At: entry.Time, ActorID: fromActor, Subject: toActor,
			Kind: kind, TaskActor: taskActorFor(p.project, crew), Payload: payload,
			RefPath: path, RefOffset: entry.Offset,
		})
		p.b.message(pendingMessage{
			EventDedup: eventDedup, From: fromActor, To: toActor,
			Channel: channel, Text: entry.Text, Marked: marked,
		})

		if crew == "" || channel != ChannelPane {
			continue
		}
		if entry.Source != store.SourceMate && entry.Source != store.SourceUser {
			continue
		}
		p.answer(open, crew, entry, fromActor, path)
	}
	return nil
}

// answer applies the inbox rule of docs/mvp.md section 4: a line addressed to
// `crew:<id>` at or after a question of that crew's answers it. The
// comparison truncates the question to the second for the reason
// internal/box gives - `sent.log` writes RFC3339 with no fractional part, so
// an answer typed in the same second as the question would otherwise read as
// older than it.
//
// The oldest unanswered question is the one answered: a crew that asked twice
// without being answered is answered in the order it asked.
func (p *pass) answer(open map[string][]*pendingQuestion, crew string, entry store.SentEntry, fromActor, path string) {
	questions := open[crew]
	for _, q := range questions {
		if q.AnsweredDedup != "" {
			continue
		}
		if entry.Time.Before(q.AskedAt.Truncate(time.Second)) {
			continue
		}
		crewActor := CrewActorID(p.project, crew)
		answeredDedup := dedup(KindQuestionAnsw, q.ID, fmt.Sprint(entry.Offset))
		p.b.event(pendingEvent{
			Dedup:   answeredDedup,
			Project: p.project, At: entry.Time, ActorID: fromActor, Subject: crewActor,
			Kind: KindQuestionAnsw, TaskActor: crewActor,
			Payload: map[string]any{
				"text": entry.Text, "crew": crew, "by": entry.Source,
				"question":  q.Text,
				"waited_ms": entry.Time.Sub(q.AskedAt).Milliseconds(),
			},
			RefPath: path, RefOffset: entry.Offset,
		})
		q.AnsweredDedup = answeredDedup
		q.AnsweredBy = fromActor
		q.AnsweredAt = entry.Time
		if q.AskedDedup == "" {
			// The question was asked in an earlier pass, so it is not in
			// this batch yet: only its answer is new.
			p.b.question(q)
		}
		return
	}
}

// openQuestions is every unanswered question of the project, the ones this
// pass has just read included, keyed by crew and oldest first.
func (p *pass) openQuestions(ctx context.Context) (map[string][]*pendingQuestion, error) {
	out := map[string][]*pendingQuestion{}
	rows, err := p.tx.QueryContext(ctx,
		`SELECT q.id, q.crew_actor_id, q.text, q.asked_at, a.name
		   FROM question q JOIN actor a ON a.id = q.crew_actor_id
		  WHERE q.answered_event_id IS NULL AND a.project = ?`, p.project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, crewActor, text, askedAt, crew string
		if err := rows.Scan(&id, &crewActor, &text, &askedAt, &crew); err != nil {
			return nil, err
		}
		q := &pendingQuestion{
			ID: id, CrewActorID: crewActor, Text: text, AskedAt: db.ParseTime(askedAt),
			AskedDedup: "", // already recorded; upsertQuestion resolves it from the row
		}
		out[crew] = append(out[crew], q)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Questions this pass has just read are not in the table yet.
	for _, q := range p.b.questions {
		crew := crewFromActorID(q.CrewActorID)
		out[crew] = append(out[crew], q)
	}
	for crew := range out {
		list := out[crew]
		sort.SliceStable(list, func(i, j int) bool { return list[i].AskedAt.Before(list[j].AskedAt) })
		out[crew] = list
	}
	return out, nil
}

// classifySent reads a `sent.log` line's channel off its shape.
func classifySent(entry store.SentEntry) (channel, kind string, marked bool) {
	if entry.Source == store.SourceApp && entry.Target == store.TargetMate {
		marked = true
		switch {
		case strings.HasPrefix(entry.Text, digestPrefix):
			return ChannelDigest, KindDigestSent, true
		case strings.HasPrefix(entry.Text, assignPrefix):
			return ChannelAssign, KindAssignClicked, true
		default:
			// The hook's own note that an unmarked prompt turned auto mode
			// off: the app wrote it, but nothing was typed into any pane.
			return ChannelHook, KindMessageSent, true
		}
	}
	return ChannelPane, KindMessageSent, false
}

func (p *pass) actorForSource(source string) string {
	switch source {
	case store.SourceMate:
		return p.mate.ActorID
	case store.SourceApp:
		return AppActorID(p.project)
	default:
		return UserActorID(p.project)
	}
}

// actorForTarget maps a `sent.log` target onto an actor. `mate` is the Mate's
// pane, `crew:<id>` a crew's, and `user` is the Stop hook's record of what
// the Mate said back to the captain.
func (p *pass) actorForTarget(target string) (actorID, crew string) {
	if id, ok := strings.CutPrefix(target, "crew:"); ok {
		return p.ensureCrewActor(id), id
	}
	if target == store.TargetMate {
		return p.mate.ActorID, ""
	}
	return UserActorID(p.project), ""
}

// ingestIncidents reads `incidents.log` from its cursor: the observer's own
// findings, which are the only way a crew that cannot speak is heard.
func (p *pass) ingestIncidents(ctx context.Context) error {
	path := p.ing.ws.IncidentsLog(p.project)
	from, err := p.readCursor(ctx, path)
	if err != nil {
		return err
	}
	entries, next, err := p.ing.ws.ReadIncidents(p.project, from)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	p.b.cursor(path, next)

	observer := ObserverActorID(p.project)
	for _, entry := range entries {
		subject := p.incidentSubject(entry.Crew)
		kind := KindIncidentOpen
		if !entry.Open() {
			kind = KindIncidentResol
		}
		eventDedup := dedup(kind, p.project, fmt.Sprint(entry.Offset))
		p.b.event(pendingEvent{
			Dedup:   eventDedup,
			Project: p.project, At: entry.Time, ActorID: observer, Subject: subject,
			Kind: kind, TaskActor: taskActorFor(p.project, entry.Crew),
			Payload: map[string]any{
				"incident": entry.Kind, "crew": entry.Crew, "text": entry.Text,
			},
			RefPath: path, RefOffset: entry.Offset,
		})
		if entry.Open() {
			p.b.incident(&pendingIncident{
				ID:          incidentRowID(p.project, entry.Crew, entry.Kind, entry.Offset),
				Project:     p.project,
				ActorID:     subject,
				Kind:        entry.Kind,
				OpenedDedup: eventDedup,
				OpenedAt:    entry.Time,
			})
			continue
		}
		// A `resolved` line closes the newest still-open incident of the
		// same (crew, kind), which is the pairing internal/box already uses
		// and the only one the file supports: an incident has no identity
		// beyond that pair.
		if pending := p.openIncidentInBatch(subject, entry.Kind); pending != nil {
			pending.ResolvedDedup = eventDedup
			pending.ResolvedAt = entry.Time
			continue
		}
		id, at, ok := p.openIncidentRow(ctx, subject, entry.Kind)
		if !ok {
			continue
		}
		p.b.incident(&pendingIncident{
			ID: id, Project: p.project, ActorID: subject, Kind: entry.Kind,
			OpenedDedup: "", OpenedAt: at,
			ResolvedDedup: eventDedup, ResolvedAt: entry.Time,
		})
	}
	return nil
}

// openIncidentInBatch is the newest unresolved incident of an (actor, kind)
// this pass has itself just opened.
func (p *pass) openIncidentInBatch(actorID, kind string) *pendingIncident {
	for i := len(p.b.incidents) - 1; i >= 0; i-- {
		in := p.b.incidents[i]
		if in.Kind == kind && in.ActorID == actorID && in.ResolvedDedup == "" {
			return in
		}
	}
	return nil
}

// openIncidentRow is the newest unresolved incident of an (actor, kind) an
// earlier pass recorded.
func (p *pass) openIncidentRow(ctx context.Context, actorID, kind string) (string, time.Time, bool) {
	var id, openedAt string
	err := p.tx.QueryRowContext(ctx,
		`SELECT id, opened_at FROM incident
		  WHERE project = ? AND actor_id = ? AND kind = ? AND resolved_event_id IS NULL
		  ORDER BY opened_at DESC LIMIT 1`,
		p.project, actorID, kind).Scan(&id, &openedAt)
	if err != nil {
		return "", time.Time{}, false
	}
	return id, db.ParseTime(openedAt), true
}

// incidentSubject is who an incident is about. The daemon files its own
// `wedged` finding under the crew name `mate` (autopilot.MateCrew), which is
// the Mate; a crew of that name would be a crew, and there is no way to tell
// the two apart from the file - so the Mate wins, because that is the one
// the daemon actually writes.
func (p *pass) incidentSubject(crew string) string {
	switch crew {
	case "":
		return ObserverActorID(p.project)
	case "mate":
		return p.mate.ActorID
	default:
		return p.ensureCrewActor(crew)
	}
}

// ensureCrewActor names a crew the logs mention, whether or not it still has a
// `crews/<id>.meta`. A `sent.log` line addressed to a crew whose meta a human
// deleted is still a line that was sent, and a message with an end that is not
// in `actor` is a row the schema refuses and a join silently drops.
func (p *pass) ensureCrewActor(crew string) string {
	actorID := CrewActorID(p.project, crew)
	for _, known := range p.crews {
		if known.ID == crew {
			return actorID
		}
	}
	for _, pending := range p.b.actors {
		if pending.ID == actorID {
			return actorID
		}
	}
	p.b.actor(pendingActor{ID: actorID, Project: p.project, Kind: ActorCrew, Name: crew})
	return actorID
}

func taskActorFor(project, crew string) string {
	if crew == "" || crew == "mate" {
		return ""
	}
	return CrewActorID(project, crew)
}

func crewFromActorID(actorID string) string {
	parts := strings.SplitN(actorID, ":", 3)
	if len(parts) != 3 {
		return ""
	}
	return parts[2]
}

func questionRowID(crewActorID string, offset int64) string {
	return fmt.Sprintf("%s#q#%d", crewActorID, offset)
}

func incidentRowID(project, crew, kind string, offset int64) string {
	return fmt.Sprintf("%s#%s#%s#%d", project, crew, kind, offset)
}
