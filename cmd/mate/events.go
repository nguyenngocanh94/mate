package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/timeline/scene"
)

// followInterval is how often `--follow` asks for events after the last id it
// printed. One second is what docs/mvp.md M5 names for a live reader, and it
// is a read of an indexed integer column, not a scan.
const followInterval = time.Second

// cmdEvents is `mate events <project> [--follow] [--since ...] [--narrate]
// [--scene]` (docs/mvp.md tasks 25 and 26): the timeline as JSON lines from
// `v_story`, or as one sentence per event, or - with `--scene` - the office
// itself: where everybody is standing now, and then every move.
//
// It is read-only and takes no lock: the console's observer is the writer,
// and SQLite's WAL lets this read the last committed story while an ingest is
// in flight.
func cmdEvents(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory (default: MATE_WORKSPACE or the nearest ancestor with .mate/)")
	follow := fs.Bool("follow", false, "keep printing events as they are recorded")
	since := fs.String("since", "", "start after an event id, or at an RFC3339 time")
	narrate := fs.Bool("narrate", false, "print one human sentence per event instead of JSON")
	limit := fs.Int("limit", 0, "print at most this many events (0 means all)")
	sceneMode := fs.Bool("scene", false, "print the office scene: where everybody is now, then every move")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		return newUsageError("usage: mate events <project> [--follow] [--since <RFC3339|event id>] [--narrate] [--scene] [--limit N]")
	}
	project := fs.Arg(0)

	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	if _, ok := w.Project(project); !ok {
		return newUsageErrorf("no project %q in this workspace", project)
	}
	query, err := storyQuery(project, *since)
	if err != nil {
		return err
	}
	query.Limit = *limit

	handle, err := db.OpenRead(w)
	if err != nil {
		return err
	}
	defer handle.Close()

	ctx := context.Background()
	if *sceneMode {
		return printScene(ctx, handle, query, sceneHistory{
			asked:   strings.TrimSpace(*since) != "",
			follow:  *follow,
			narrate: *narrate,
		}, stdout)
	}
	lastID, err := printEvents(ctx, handle, query, *narrate, stdout)
	if err != nil {
		return err
	}
	if !*follow {
		return nil
	}
	for {
		time.Sleep(followInterval)
		query = timeline.StoryQuery{Project: project, SinceID: lastID}
		next, err := printEvents(ctx, handle, query, *narrate, stdout)
		if err != nil {
			return err
		}
		if next > lastID {
			lastID = next
		}
	}
}

// printScene is `mate events <project> --scene` (docs/mvp.md task 26): the
// office as the projection last left it, and then every move.
//
// The snapshot comes first and always, because the question a reader arrives
// with is "who is doing what right now" and an empty stream is an answer to
// nothing. `--since` adds the moves since a moment or an event id, and
// `--follow` keeps printing the ones that happen next.
func printScene(ctx context.Context, handle *db.DB, query timeline.StoryQuery,
	how sceneHistory, stdout io.Writer) error {

	now, err := scene.Now(ctx, handle.SQL(), scene.NowQuery{Project: query.Project})
	if err != nil {
		return err
	}
	for _, row := range now {
		line := scene.NarrateNow(row)
		if how.narrate {
			// The captain, mate and the observer are in the story and not
			// in the office: they have no scene, and a narrated snapshot
			// that said so three times over would be noise. The JSON
			// snapshot still carries their rows.
			if row.State == scene.Unknown {
				continue
			}
		} else if line, err = row.JSONLine(); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}

	cursor, err := scene.LastID(ctx, handle.SQL(), query.Project)
	if err != nil {
		return err
	}
	if how.asked {
		past := scene.TransitionQuery{
			Project:      query.Project,
			SinceEventID: query.SinceID,
			SinceTime:    query.SinceTime,
			Limit:        query.Limit,
		}
		if _, err := printTransitions(ctx, handle, past, how.narrate, stdout); err != nil {
			return err
		}
	}
	if !how.follow {
		return nil
	}
	for {
		time.Sleep(followInterval)
		next, err := printTransitions(ctx, handle,
			scene.TransitionQuery{Project: query.Project, AfterID: cursor}, how.narrate, stdout)
		if err != nil {
			return err
		}
		if next != "" {
			cursor = next
		}
	}
}

// sceneHistory is how much of the scene `--scene` was asked for. `asked` is
// "the reader gave a --since", whatever it parsed to: `--since 0` is a
// request for the whole history and not for nothing.
type sceneHistory struct {
	asked   bool
	follow  bool
	narrate bool
}

func printTransitions(ctx context.Context, handle *db.DB, query scene.TransitionQuery,
	narrate bool, stdout io.Writer) (string, error) {

	rows, err := scene.Transitions(ctx, handle.SQL(), query)
	if err != nil {
		return "", err
	}
	last := ""
	for _, row := range rows {
		line := scene.Narrate(row)
		if !narrate {
			if line, err = row.JSONLine(); err != nil {
				return last, err
			}
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return last, err
		}
		last = row.ID
	}
	return last, nil
}

func printEvents(ctx context.Context, handle *db.DB, query timeline.StoryQuery,
	narrate bool, stdout io.Writer) (int64, error) {

	events, err := timeline.Story(ctx, handle.SQL(), query)
	if err != nil {
		return query.SinceID, err
	}
	last := query.SinceID
	for _, event := range events {
		if narrate {
			if _, err := fmt.Fprintln(stdout, timeline.Narrate(event)); err != nil {
				return last, err
			}
		} else {
			line, err := event.JSONLine()
			if err != nil {
				return last, err
			}
			if _, err := fmt.Fprintln(stdout, line); err != nil {
				return last, err
			}
		}
		last = event.ID
	}
	return last, nil
}

// storyQuery reads `--since` as either an event id or a timestamp. A bare
// number is an id, because that is what `--follow` prints and what a reader
// copies back; anything else has to parse as RFC3339.
func storyQuery(project, since string) (timeline.StoryQuery, error) {
	query := timeline.StoryQuery{Project: project}
	since = strings.TrimSpace(since)
	if since == "" {
		return query, nil
	}
	if id, err := strconv.ParseInt(since, 10, 64); err == nil {
		query.SinceID = id
		return query, nil
	}
	at, err := time.Parse(time.RFC3339, since)
	if err != nil {
		return query, newUsageErrorf("--since %q is neither an event id nor an RFC3339 time", since)
	}
	query.SinceTime = at
	return query, nil
}

// cmdReindex is `mate reindex [<workspace>]`: drop every derived table and
// rebuild the whole timeline from the files and the transcripts.
//
// It takes the writer's lock, so it refuses while a console is open on the
// same workspace - which is the honest answer, because two writers rebuilding
// the same database would interleave.
func cmdReindex(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("reindex", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	dir := *workspace
	switch fs.NArg() {
	case 0:
	case 1:
		if dir != "" {
			return newUsageError("usage: mate reindex [<workspace>]")
		}
		dir = fs.Arg(0)
	default:
		return newUsageError("usage: mate reindex [<workspace>]")
	}
	w, err := resolveWorkspace(dir)
	if err != nil {
		return err
	}

	handle, err := db.Open(w)
	if err != nil {
		return err
	}
	defer handle.Close()

	ctx := context.Background()
	// Ask Herdr for each live crew's session ref the way the console does:
	// a Codex crew spawned before its rollout could be adopted by cwd and
	// launch time is otherwise "rollout_not_adopted" forever, even while
	// its agent is still listed (measured 2026-09-20 on a real workspace).
	// A Herdr that is not running just leaves the fallback in charge.
	if err := timeline.New(w, handle, timeline.Deps{
		SessionRef: consoleSessionRef(w, liveDeps()),
		Harnesses:  harnesses,
	}).Reindex(ctx); err != nil {
		return err
	}
	return reportReindex(ctx, w, handle, stdout)
}

func reportReindex(ctx context.Context, w *store.Workspace, handle *db.DB, stdout io.Writer) error {
	for _, ref := range w.Projects() {
		var events, turns, actions int
		row := handle.SQL().QueryRowContext(ctx,
			`SELECT
			   (SELECT COUNT(*) FROM event WHERE project = ?),
			   (SELECT COUNT(*) FROM turn t JOIN actor a ON a.id = t.actor_id WHERE a.project = ?),
			   (SELECT COUNT(*) FROM action c JOIN actor a ON a.id = c.actor_id WHERE a.project = ?)`,
			ref.Name, ref.Name, ref.Name)
		if err := row.Scan(&events, &turns, &actions); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s: %d event(s), %d turn(s), %d action(s)\n", ref.Name, events, turns, actions)
	}
	fmt.Fprintf(stdout, "timeline rebuilt at %s\n", handle.Path())
	return nil
}
