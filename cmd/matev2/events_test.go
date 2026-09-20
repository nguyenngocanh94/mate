package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/db"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/timeline"
)

// eventsWorkspace is a workspace with one project, one crew that asked and
// was answered, and nothing else: enough for the CLI to have a story to
// print, and small enough that the assertions can name every line.
func eventsWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := liveCrewWorkspace(t, "shop")
	if err := w.WriteMateMeta("shop", map[string]string{
		"harness":    "claude",
		"started_at": time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("WriteMateMeta: %v", err)
	}
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{
		"harness":    "codex",
		"task":       "add a Buy button",
		"branch":     "matev2/k3",
		"worktree":   ".worktrees/shop-k3",
		"started_at": time.Date(2026, 9, 19, 10, 1, 0, 0, time.UTC).Format(time.RFC3339),
		"state":      "spawned",
	}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	if err := w.AppendStatus("shop", "k3", "needs-decision: pick A or B"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	if err := w.AppendSent("shop", store.SentEntry{
		Time: time.Now(), Source: store.SourceMate, Target: store.CrewTarget("k3"), Text: "A",
	}); err != nil {
		t.Fatalf("AppendSent: %v", err)
	}
	return w
}

func runCLI(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("matev2 %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String()
}

// `matev2 reindex` builds the whole timeline from the files with no console
// running, and says what it found.
func TestReindexBuildsTheTimelineFromTheFilesAlone(t *testing.T) {
	w := eventsWorkspace(t)
	out := runCLI(t, "reindex", w.Root())
	if !strings.Contains(out, "shop: ") || !strings.Contains(out, "event(s)") {
		t.Fatalf("reindex printed:\n%s", out)
	}
	if !strings.Contains(out, "timeline rebuilt at ") {
		t.Fatalf("reindex did not say where the timeline is:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(w.StateDir(), "matev2.db")); err != nil {
		t.Fatalf("no database after reindex: %v", err)
	}
}

// `matev2 events <project>` prints one JSON object per line, in story order,
// with the field order the timeline package fixes.
func TestEventsPrintsOneJSONObjectPerLine(t *testing.T) {
	w := eventsWorkspace(t)
	runCLI(t, "reindex", w.Root())

	out := runCLI(t, "events", "shop", "--workspace", w.Root())
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("events printed %d line(s):\n%s", len(lines), out)
	}
	var lastID int64
	kinds := map[string]bool{}
	for i, line := range lines {
		var event timeline.StoryEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line %d is not one JSON object: %v\n%s", i, err, line)
		}
		if event.ID <= lastID {
			t.Fatalf("line %d has id %d after %d; the story is printed in order", i, event.ID, lastID)
		}
		lastID = event.ID
		kinds[event.Kind] = true
		if !strings.HasPrefix(line, `{"id":`) {
			t.Fatalf("line %d does not start with the id field; the field order is part of the contract:\n%s", i, line)
		}
	}
	for _, want := range []string{
		timeline.KindCrewSpawned, timeline.KindStatusAppend,
		timeline.KindQuestionAsked, timeline.KindQuestionAnsw,
	} {
		if !kinds[want] {
			t.Fatalf("the story has no %s event; it has %v", want, keysOf(kinds))
		}
	}
}

// `--narrate` tells the same story in sentences, one per event.
func TestEventsNarrateTellsTheStoryInSentences(t *testing.T) {
	w := eventsWorkspace(t)
	runCLI(t, "reindex", w.Root())

	out := runCLI(t, "events", "shop", "--narrate", "--workspace", w.Root())
	if !strings.Contains(out, `crew k3 asks the Mate: "pick A or B"`) {
		t.Fatalf("the narrated story does not carry the question:\n%s", out)
	}
	if !strings.Contains(out, `the Mate answers k3: "A"`) {
		t.Fatalf("the narrated story does not carry the answer:\n%s", out)
	}
	if !strings.Contains(out, `the Mate hires k3 for "add a Buy button"`) {
		t.Fatalf("the narrated story does not carry the hiring:\n%s", out)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(line) < 9 || line[2] != ':' || line[5] != ':' {
			t.Fatalf("a narrated line does not start with a clock time: %q", line)
		}
	}
}

// `--since` takes either an event id or an RFC3339 time, because the first
// is what `--follow` prints and the second is what a person has.
func TestEventsSinceAcceptsAnIDAndATime(t *testing.T) {
	w := eventsWorkspace(t)
	runCLI(t, "reindex", w.Root())

	all := strings.Split(strings.TrimRight(runCLI(t, "events", "shop", "--workspace", w.Root()), "\n"), "\n")
	var first timeline.StoryEvent
	if err := json.Unmarshal([]byte(all[0]), &first); err != nil {
		t.Fatalf("decode the first event: %v", err)
	}
	rest := strings.Split(strings.TrimRight(
		runCLI(t, "events", "shop", "--since", "1", "--workspace", w.Root()), "\n"), "\n")
	if len(rest) != len(all)-1 {
		t.Fatalf("--since 1 printed %d line(s), want %d", len(rest), len(all)-1)
	}

	byTime := runCLI(t, "events", "shop", "--since", first.Time().UTC().Format(time.RFC3339), "--workspace", w.Root())
	if strings.TrimSpace(byTime) == "" {
		t.Fatal("--since with a time printed nothing")
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"events", "shop", "--since", "not-a-time", "--workspace", w.Root()}, &stdout, &stderr); err == nil {
		t.Fatal("--since accepted a value that is neither an id nor a time")
	}
}

// Reading the timeline takes no lock, so `matev2 events` works while a
// console is writing. Writing takes one, so a second writer is refused.
func TestEventsReadsWhileTheWriterHoldsTheLock(t *testing.T) {
	w := eventsWorkspace(t)
	runCLI(t, "reindex", w.Root())

	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("open the writer: %v", err)
	}
	defer handle.Close()

	if out := runCLI(t, "events", "shop", "--workspace", w.Root()); strings.TrimSpace(out) == "" {
		t.Fatal("events printed nothing while the writer held the lock")
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"reindex", w.Root()}, &stdout, &stderr); err == nil {
		t.Fatal("a second writer was allowed to reindex while the first held the lock")
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
