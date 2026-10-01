package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// Task is one crew's row of the comparison: what docs/mvp.md task 34 asks
// to be measured per task, read from the timeline database.
type Task struct {
	Label   string
	Project string
	Crew    string
	// Kind is ship or scout, read off the brief the Crew was given (a scout
	// brief has `## Deliverable`); "?" when the brief is gone.
	Kind string
	// Questions is how many `needs-decision` lines the Crew wrote.
	Questions int
	// Rework is how many lines the Mate typed into the Crew's pane after the
	// Crew's first `wait-mate`: corrections, plus any "rebase" or "commit
	// that" nudges. Zero when the Crew never handed back.
	Rework int
	// Calls is the Crew's model calls (`turn` rows); Turns is its harness
	// turns (distinct prompts it was given).
	Calls, Turns int
	// The four token buckets, which never overlap (docs/mvp.md section 7).
	In, CacheRead, CacheWrite, Out int64
	// ToWaitMate is spawn to the first `wait-mate`; zero when there was none.
	ToWaitMate time.Duration
	// Handback says whether `handback.md` exists and every `verify:` line of
	// `## Acceptance` has a pass/fail row.
	Handback string
}

// Mate is one project's Mate over the whole run: it is not a task's cost,
// but it is where a longer manual and a longer brief are paid for.
type Mate struct {
	Label, Project                 string
	Calls, Turns                   int
	In, CacheRead, CacheWrite, Out int64
	// CaptainLines is what the captain typed to the Mate; ToCrew is what the
	// Mate typed into any Crew's pane.
	CaptainLines, ToCrew int
}

// Measure reads one run's timeline. path is the database file or the
// workspace directory holding `.mate/mate.db`; the run's own files are
// read only for the two facts the database does not hold: a brief's shape
// and a hand-back's table.
func Measure(ctx context.Context, label, path string) ([]Task, []Mate, error) {
	dbPath, root, err := locate(path)
	if err != nil {
		return nil, nil, err
	}
	w, err := store.Open(root)
	if err != nil {
		return nil, nil, err
	}
	handle, err := db.OpenReadPath(dbPath)
	if err != nil {
		return nil, nil, err
	}
	defer handle.Close()
	q := handle.SQL()

	rows, err := q.QueryContext(ctx, `
		SELECT t.crew_actor_id, t.project, a.name, COALESCE(t.spawned_at, '')
		  FROM task t JOIN actor a ON a.id = t.crew_actor_id
		 ORDER BY t.project, t.spawned_at`)
	if err != nil {
		return nil, nil, err
	}
	type taskRow struct{ actor, project, crew, spawned string }
	var found []taskRow
	for rows.Next() {
		var r taskRow
		if err := rows.Scan(&r.actor, &r.project, &r.crew, &r.spawned); err != nil {
			rows.Close()
			return nil, nil, err
		}
		found = append(found, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	var tasks []Task
	projects := map[string]bool{}
	for _, r := range found {
		projects[r.project] = true
		t := Task{Label: label, Project: r.project, Crew: r.crew}
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM question WHERE crew_actor_id = ?`, r.actor).
			Scan(&t.Questions); err != nil {
			return nil, nil, err
		}
		if err := sumTurns(ctx, q, r.actor, &t.Calls, &t.Turns, &t.In, &t.CacheRead, &t.CacheWrite, &t.Out); err != nil {
			return nil, nil, err
		}
		var firstWait sql.NullString
		if err := q.QueryRowContext(ctx, `
			SELECT MIN(at) FROM event
			 WHERE actor_id = ? AND kind = ? AND json_extract(payload, '$.verb') = 'wait-mate'`,
			r.actor, timeline.KindStatusAppend).Scan(&firstWait); err != nil {
			return nil, nil, err
		}
		if firstWait.Valid && firstWait.String != "" {
			if err := q.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM message m JOIN event e ON e.id = m.event_id
				 WHERE m.from_actor_id = ? AND m.to_actor_id = ? AND e.at > ?`,
				timeline.MateActorID(r.project), r.actor, firstWait.String).Scan(&t.Rework); err != nil {
				return nil, nil, err
			}
			t.ToWaitMate = between(r.spawned, firstWait.String)
		}
		t.Kind, t.Handback = readBriefAndHandback(w, r.project, r.crew)
		tasks = append(tasks, t)
	}

	var mates []Mate
	mateRows, err := q.QueryContext(ctx, `SELECT project FROM actor WHERE kind = 'mate' ORDER BY project`)
	if err != nil {
		return nil, nil, err
	}
	for mateRows.Next() {
		var p string
		if err := mateRows.Scan(&p); err != nil {
			mateRows.Close()
			return nil, nil, err
		}
		projects[p] = true
	}
	mateRows.Close()
	for _, p := range sortedKeys(projects) {
		m := Mate{Label: label, Project: p}
		if err := sumTurns(ctx, q, timeline.MateActorID(p), &m.Calls, &m.Turns, &m.In, &m.CacheRead, &m.CacheWrite, &m.Out); err != nil {
			return nil, nil, err
		}
		if err := q.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM message WHERE from_actor_id = ? AND to_actor_id = ?`,
			timeline.UserActorID(p), timeline.MateActorID(p)).Scan(&m.CaptainLines); err != nil {
			return nil, nil, err
		}
		if err := q.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM message WHERE from_actor_id = ? AND to_actor_id LIKE ?`,
			timeline.MateActorID(p), timeline.ActorCrew+":"+p+":%").Scan(&m.ToCrew); err != nil {
			return nil, nil, err
		}
		mates = append(mates, m)
	}
	return tasks, mates, nil
}

func sumTurns(ctx context.Context, q *sql.DB, actor string, calls, turns *int, in, cr, cw, out *int64) error {
	return q.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT NULLIF(harness_turn_ref, '')),
		       COALESCE(SUM(input_tokens),0), COALESCE(SUM(cache_read_tokens),0),
		       COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(output_tokens),0)
		  FROM turn WHERE actor_id = ?`, actor).Scan(calls, turns, in, cr, cw, out)
}

// Reindex rebuilds one run's timeline from its files and transcripts, the
// way `mate reindex` does when no Herdr answers: a Codex crew's rollout is
// then found by its cwd and launch time.
func Reindex(ctx context.Context, path string) error {
	_, root, err := locate(path)
	if err != nil {
		return err
	}
	w, err := store.Open(root)
	if err != nil {
		return err
	}
	handle, err := db.Open(w)
	if err != nil {
		return err
	}
	defer handle.Close()
	return timeline.New(w, handle, timeline.Deps{Harnesses: catalog.Default()}).Reindex(ctx)
}

// locate accepts a database file or a workspace directory.
func locate(path string) (dbPath, root string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		return filepath.Join(path, ".mate", "mate.db"), path, nil
	}
	// <root>/.mate/mate.db
	return path, filepath.Dir(filepath.Dir(path)), nil
}

func between(from, to string) time.Duration {
	a, errA := time.Parse(time.RFC3339Nano, from)
	b, errB := time.Parse(time.RFC3339Nano, to)
	if errA != nil || errB != nil || b.Before(a) {
		return 0
	}
	return b.Sub(a)
}

// readBriefAndHandback is the task's shape and whether its hand-back covers
// every acceptance line.
func readBriefAndHandback(w *store.Workspace, project, crew string) (kind, handback string) {
	data, err := os.ReadFile(w.CrewBrief(project, crew))
	if err != nil {
		return "?", "no brief"
	}
	text := string(data)
	// Before M7 a brief had no `## Deliverable`; a scout is then known by
	// the report it left.
	_, scout := brief.SectionText(text, brief.Deliverable)
	if _, err := os.Stat(w.CrewReport(project, crew)); err == nil {
		scout = true
	}
	if scout {
		return "scout", "n/a"
	}
	hb, err := os.ReadFile(w.CrewHandback(project, crew))
	if err != nil {
		return "ship", "no handback.md"
	}
	criteria := 0
	if items, ok := brief.SectionItems(text, brief.Acceptance); ok {
		for _, it := range items {
			if strings.Contains(strings.ToLower(it), brief.VerifyMarker) {
				criteria++
			}
		}
	}
	rows := passFailRows(string(hb))
	if criteria > 0 && rows >= criteria {
		return "ship", fmt.Sprintf("yes %d/%d", rows, criteria)
	}
	return "ship", fmt.Sprintf("no %d/%d", rows, criteria)
}

// passFailRows counts the rows of a hand-back's table whose second column
// is `pass` or `fail`, the shape the Crew template asks for.
func passFailRows(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 2 {
			continue
		}
		switch strings.ToLower(strings.Trim(strings.TrimSpace(cells[1]), "*`")) {
		case "pass", "fail":
			n++
		}
	}
	return n
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
