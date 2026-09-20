package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/db"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/timeline"
)

// followInterval is how often `--follow` asks for events after the last id it
// printed. One second is what docs/mvp.md M5 names for a live reader, and it
// is a read of an indexed integer column, not a scan.
const followInterval = time.Second

// cmdEvents is `matev2 events <project> [--follow] [--since ...] [--narrate]`
// (docs/mvp.md task 25): the timeline as JSON lines from `v_story`, or as one
// sentence per event.
//
// It is read-only and takes no lock: the console's observer is the writer,
// and SQLite's WAL lets this read the last committed story while an ingest is
// in flight.
func cmdEvents(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory (default: MATEV2_WORKSPACE or the nearest ancestor with .matev2/)")
	follow := fs.Bool("follow", false, "keep printing events as they are recorded")
	since := fs.String("since", "", "start after an event id, or at an RFC3339 time")
	narrate := fs.Bool("narrate", false, "print one human sentence per event instead of JSON")
	limit := fs.Int("limit", 0, "print at most this many events (0 means all)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		return newUsageError("usage: matev2 events <project> [--follow] [--since <RFC3339|event id>] [--narrate] [--limit N]")
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

// cmdReindex is `matev2 reindex [<workspace>]`: drop every derived table and
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
			return newUsageError("usage: matev2 reindex [<workspace>]")
		}
		dir = fs.Arg(0)
	default:
		return newUsageError("usage: matev2 reindex [<workspace>]")
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
	if err := timeline.New(w, handle, timeline.Deps{}).Reindex(ctx); err != nil {
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
