package store_test

import (
	"os"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestMigrateLockIsExclusiveAndReadWithoutCreating: a second migrate is
// refused while the first holds the lock, and asking whether one is running
// writes nothing to a workspace that never migrated.
func TestMigrateLockIsExclusiveAndReadWithoutCreating(t *testing.T) {
	w := newWorkspace(t)
	if held, err := w.MigrateLocked(); err != nil || held {
		t.Fatalf("MigrateLocked on a fresh workspace = %v, %v; want false", held, err)
	}
	if _, err := os.Stat(w.MigrateLockFile()); !os.IsNotExist(err) {
		t.Fatalf("MigrateLocked created %s: %v", w.MigrateLockFile(), err)
	}
	unlock, ok, err := w.LockMigrate()
	if err != nil || !ok {
		t.Fatalf("LockMigrate = %v, %v; want acquired", ok, err)
	}
	if _, ok2, err := w.LockMigrate(); err != nil || ok2 {
		t.Fatalf("second LockMigrate = %v, %v; want refused while held", ok2, err)
	}
	if held, err := w.MigrateLocked(); err != nil || !held {
		t.Fatalf("MigrateLocked while held = %v, %v; want true", held, err)
	}
	unlock()
	if held, err := w.MigrateLocked(); err != nil || held {
		t.Fatalf("MigrateLocked after unlock = %v, %v; want false", held, err)
	}
}

func TestMigrateLogRoundTrip(t *testing.T) {
	w := newWorkspace(t)
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	entries := []store.MigrateEntry{
		{Time: at, Project: "shop", Old: "/w/shop", New: "/w/shop/shop", Step: store.MigrateStaged + " /w/shop.migrating-1"},
		{Time: at, Project: "shop", Old: "/w/shop", New: "/w/shop/shop", Step: store.MigrateRenamed},
		{Time: at, Project: "shop", Old: "/w/shop", New: "/w/shop/shop"},
	}
	for _, e := range entries {
		if err := w.AppendMigrateLog(e); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(w.MigrateLog())
	if err != nil {
		t.Fatal(err)
	}
	if want := "2026-10-08T09:00:00Z\tshop\t/w/shop\t/w/shop/shop\n"; len(data) < len(want) || string(data[len(data)-len(want):]) != want {
		t.Fatalf("the finished line is not `RFC3339 \\t project \\t old \\t new`:\n%s", data)
	}
	got, err := w.ReadMigrateLog()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Step != entries[0].Step || got[1].Step != store.MigrateRenamed || !got[2].Done() || got[2].Time != at {
		t.Fatalf("ReadMigrateLog = %+v", got)
	}
	if err := w.AppendMigrateLog(store.MigrateEntry{Time: at, Project: "shop", Old: "a\tb", New: "c"}); err == nil {
		t.Fatal("a field with a tab was appended")
	}
}

func TestSetLayoutProjectDirs(t *testing.T) {
	w := newWorkspace(t)
	writeOldLayout(t, w)
	old, err := store.Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	if !old.LayoutOld() {
		t.Fatal("fixture is not the old layout")
	}
	if err := old.SetLayoutProjectDirs(); err != nil {
		t.Fatal(err)
	}
	again, err := store.Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	if again.Layout() != 2 || len(again.Projects()) != 1 {
		t.Fatalf("after SetLayoutProjectDirs: layout %d, projects %v", again.Layout(), again.Projects())
	}
}
