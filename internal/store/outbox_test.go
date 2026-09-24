package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
)

var outboxNow = time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)

func writeOutbox(t *testing.T, w *store.Workspace, items []store.OutboxItem) {
	t.Helper()
	if err := w.UpdateOutbox("shop", outboxNow, func([]store.OutboxItem) ([]store.OutboxItem, bool, error) {
		return items, true, nil
	}); err != nil {
		t.Fatalf("UpdateOutbox: %v", err)
	}
}

// A project nothing was ever queued for has an empty outbox, not an error.
func TestOutboxMissingFileIsEmpty(t *testing.T) {
	w := autoCursorWorkspace(t)
	items, err := w.ReadOutbox("shop")
	if err != nil || len(items) != 0 {
		t.Fatalf("ReadOutbox = %+v, %v; want empty", items, err)
	}
}

// The file is mate/.outbox, one JSON object per line, and it round-trips
// every field a sender, the daemon and the inbox row read.
func TestOutboxRoundTripsOneJSONLinePerItem(t *testing.T) {
	w := autoCursorWorkspace(t)
	want := []store.OutboxItem{
		{ID: 1, At: outboxNow, Source: store.OutboxSourceAssign, Key: "crews/k3.status@0",
			Text: "resolve: k3 asked", State: store.OutboxSent, SentAt: outboxNow.Add(time.Minute), Attempts: 3},
		{ID: 2, At: outboxNow, Source: store.OutboxSourceDigest, Key: "digest:ab", Text: "digest: 1 item(s)",
			State: store.OutboxQueued, Attempts: 1, LastRefusal: "agent is mid-turn", SentLogFrom: 42,
			Cursor: map[string]int64{w.CrewStatus("shop", "k3"): 0}},
	}
	writeOutbox(t, w, want)

	data, err := os.ReadFile(filepath.Join(w.MateDir("shop"), ".outbox"))
	if err != nil {
		t.Fatalf("read .outbox: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], `{"id":1,`) || !strings.Contains(lines[1], `"state":"queued"`) {
		t.Fatalf(".outbox =\n%s\nwant one JSON object per item", data)
	}

	got, err := w.ReadOutbox("shop")
	if err != nil {
		t.Fatalf("ReadOutbox: %v", err)
	}
	if len(got) != 2 || got[1].Cursor[w.CrewStatus("shop", "k3")] != 0 || got[1].SentLogFrom != 42 ||
		got[0].Attempts != 3 || !got[0].SentAt.Equal(want[0].SentAt) {
		t.Fatalf("ReadOutbox = %+v, want %+v", got, want)
	}
}

// Compaction keeps every queued item however old, drops settled items past
// the retention window, and keeps at most OutboxKeepSettled of the rest,
// newest first.
func TestOutboxCompactionKeepsQueuedAndBoundsSettled(t *testing.T) {
	w := autoCursorWorkspace(t)
	var items []store.OutboxItem
	old := outboxNow.Add(-store.OutboxRetention - time.Hour)
	items = append(items,
		store.OutboxItem{ID: 1, At: old, Source: store.OutboxSourceAssign, Key: "old-queued", Text: "x", State: store.OutboxQueued},
		store.OutboxItem{ID: 2, At: old, Source: store.OutboxSourceAssign, Key: "old-sent", Text: "x", State: store.OutboxSent, SentAt: old},
	)
	for i := 0; i < store.OutboxKeepSettled+10; i++ {
		at := outboxNow.Add(-time.Duration(store.OutboxKeepSettled+10-i) * time.Second)
		items = append(items, store.OutboxItem{ID: int64(3 + i), At: at, Source: store.OutboxSourceAssign,
			Key: "recent", Text: "x", State: store.OutboxSent, SentAt: at})
	}
	writeOutbox(t, w, items)

	got, err := w.ReadOutbox("shop")
	if err != nil {
		t.Fatalf("ReadOutbox: %v", err)
	}
	if len(got) != 1+store.OutboxKeepSettled {
		t.Fatalf("kept %d items, want the queued one plus %d settled", len(got), store.OutboxKeepSettled)
	}
	if got[0].Key != "old-queued" {
		t.Fatalf("first item = %+v, want the old queued item kept", got[0])
	}
	for _, item := range got {
		if item.Key == "old-sent" {
			t.Fatal("a settled item past the retention window was kept")
		}
	}
	if last := got[len(got)-1]; last.ID != int64(3+store.OutboxKeepSettled+9) {
		t.Fatalf("last item = %+v, want the newest settled one kept", last)
	}
	if got[1].ID != 13 {
		t.Fatalf("oldest settled kept = %d, want the ten oldest dropped", got[1].ID)
	}
}

// A change that reports nothing changed writes nothing, and one that fails
// writes nothing either.
func TestOutboxUpdateWritesOnlyAReportedChange(t *testing.T) {
	w := autoCursorWorkspace(t)
	if err := w.UpdateOutbox("shop", outboxNow, func(items []store.OutboxItem) ([]store.OutboxItem, bool, error) {
		return append(items, store.OutboxItem{ID: 1, State: store.OutboxQueued}), false, nil
	}); err != nil {
		t.Fatalf("UpdateOutbox: %v", err)
	}
	boom := errors.New("boom")
	if err := w.UpdateOutbox("shop", outboxNow, func(items []store.OutboxItem) ([]store.OutboxItem, bool, error) {
		return append(items, store.OutboxItem{ID: 1, State: store.OutboxQueued}), true, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("UpdateOutbox error = %v, want the change's own", err)
	}
	if _, err := os.Stat(w.OutboxFile("shop")); !os.IsNotExist(err) {
		t.Fatalf("stat .outbox = %v, want no file written", err)
	}
}

// The outbox is inside the workspace boundary like every other write: a mate
// directory that is a symlink out of the workspace is refused.
func TestOutboxRefusesSymlinkEscape(t *testing.T) {
	w := autoCursorWorkspace(t)
	outside := t.TempDir()
	if err := os.MkdirAll(w.ProjectDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, w.MateDir("shop")); err != nil {
		t.Fatal(err)
	}
	err := w.UpdateOutbox("shop", outboxNow, func(items []store.OutboxItem) ([]store.OutboxItem, bool, error) {
		return append(items, store.OutboxItem{ID: 1, State: store.OutboxQueued}), true, nil
	})
	var boundary *store.BoundaryError
	if !errors.As(err, &boundary) {
		t.Fatalf("UpdateOutbox = %v, want a *store.BoundaryError", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote %d file(s) outside the workspace", len(entries))
	}
}
