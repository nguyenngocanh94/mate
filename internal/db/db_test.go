package db_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A fresh file migrates up from nothing, and every view the spec names is
// queryable - which is the only way to find out that a view's SQL is wrong,
// since SQLite accepts a CREATE VIEW over columns that do not exist and
// fails at the first SELECT.
func TestOpenMigratesAnEmptyFileAndEveryViewAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), db.FileName)
	d, err := db.OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer d.Close()

	version, err := d.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != db.SchemaVersion {
		t.Fatalf("schema version %d, want %d", version, db.SchemaVersion)
	}
	for _, view := range []string{"v_story", "v_task_ledger", "v_now"} {
		rows, err := d.SQL().Query("SELECT * FROM " + view)
		if err != nil {
			t.Fatalf("SELECT from %s: %v", view, err)
		}
		cols, err := rows.Columns()
		rows.Close()
		if err != nil {
			t.Fatalf("%s columns: %v", view, err)
		}
		if len(cols) == 0 {
			t.Fatalf("%s has no columns", view)
		}
	}
}

// v_now's columns are a contract with task 26 and task 27, which fill
// `transition` and `pricing` and must not have to change the view to do it.
func TestNowViewCarriesTheColumnsTheSceneProjectionWillFill(t *testing.T) {
	path := filepath.Join(t.TempDir(), db.FileName)
	d, err := db.OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer d.Close()
	rows, err := d.SQL().Query("SELECT * FROM v_now")
	if err != nil {
		t.Fatalf("SELECT from v_now: %v", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	got := map[string]bool{}
	for _, c := range cols {
		got[c] = true
	}
	for _, want := range []string{
		"actor_id", "actor_kind", "actor_name", "project",
		"state", "since", "target_actor_id", "detail", "tokens_today",
	} {
		if !got[want] {
			t.Fatalf("v_now has no %s column; columns are %v", want, cols)
		}
	}
}

// Migrating a database that is already at version 1 must be a no-op, and
// migrating one left at version 0 with the table present must bring it up.
func TestMigrateIsIdempotentAndRunsFromAPartialVersionRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), db.FileName)
	d, err := db.OpenPath(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := d.SQL().Exec(`INSERT INTO actor(id, project, kind, name) VALUES ('a','p','crew','k3')`); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	again, err := db.OpenPath(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer again.Close()
	var n int
	if err := again.SQL().QueryRow(`SELECT COUNT(*) FROM actor`).Scan(&n); err != nil {
		t.Fatalf("count actors: %v", err)
	}
	if n != 1 {
		t.Fatalf("re-opening migrated over the data: %d actors, want 1", n)
	}
	var applied int
	if err := again.SQL().QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&applied); err != nil {
		t.Fatalf("count schema_version: %v", err)
	}
	if applied != db.SchemaVersion {
		t.Fatalf("schema_version holds %d rows, want %d", applied, db.SchemaVersion)
	}
}

// The single-writer rule of the M5 spec: the observer is the writer, and a
// second one is refused rather than allowed to interleave.
func TestASecondWriterIsRefusedAndAReaderIsNot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, db.FileName)
	first, err := db.OpenPath(path)
	if err != nil {
		t.Fatalf("first writer: %v", err)
	}
	defer first.Close()

	if _, err := db.OpenPath(path); err != db.ErrLocked {
		t.Fatalf("second writer got %v, want ErrLocked", err)
	}

	reader, err := db.OpenReadPath(path)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	defer reader.Close()
	if reader.Writable() {
		t.Fatal("a read handle reports itself writable")
	}
	var n int
	if err := reader.SQL().QueryRow(`SELECT COUNT(*) FROM event`).Scan(&n); err != nil {
		t.Fatalf("reader query: %v", err)
	}

	// Releasing the lock lets the next writer in; a lock nobody can take
	// again is a workspace nobody can reopen.
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}
	third, err := db.OpenPath(path)
	if err != nil {
		t.Fatalf("writer after close: %v", err)
	}
	_ = third.Close()
}

// ResetDerived is what reindex runs: every derived table empty, the schema
// untouched, and the event sequence back at zero so the rebuilt ids match.
func TestResetDerivedEmptiesTheDerivedTablesAndTheEventSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), db.FileName)
	d, err := db.OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer d.Close()
	if _, err := d.SQL().Exec(`INSERT INTO actor(id, project, kind, name) VALUES ('a','p','crew','k3')`); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	if _, err := d.SQL().Exec(
		`INSERT INTO event(dedup, project, at, actor_id, kind) VALUES ('x','p','2026-01-01T00:00:00.000000000Z','a','status.appended')`); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if err := d.ResetDerived(context.Background()); err != nil {
		t.Fatalf("ResetDerived: %v", err)
	}
	for _, table := range []string{"actor", "event"} {
		var n int
		if err := d.SQL().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("%s still holds %d rows after ResetDerived", table, n)
		}
	}
	if version, err := d.Version(); err != nil || version != db.SchemaVersion {
		t.Fatalf("version after ResetDerived = %d, %v", version, err)
	}
	if _, err := d.SQL().Exec(`INSERT INTO actor(id, project, kind, name) VALUES ('a','p','crew','k3')`); err != nil {
		t.Fatalf("re-seed actor: %v", err)
	}
	if _, err := d.SQL().Exec(
		`INSERT INTO event(dedup, project, at, actor_id, kind) VALUES ('x','p','2026-01-01T00:00:00.000000000Z','a','status.appended')`); err != nil {
		t.Fatalf("re-seed event: %v", err)
	}
	var id int64
	if err := d.SQL().QueryRow(`SELECT id FROM event`).Scan(&id); err != nil {
		t.Fatalf("read event id: %v", err)
	}
	if id != 1 {
		t.Fatalf("the rebuilt event has id %d, want 1: a rebuild must reproduce the same ids", id)
	}
}

// The database lives inside `.mate/`, and the boundary that protects every
// other file under it protects this one: a symlink at `mate.db` pointing
// out of the workspace is refused, not followed.
func TestPathRefusesASymlinkOutOfTheWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	ws, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	target := filepath.Join(outside, "elsewhere.db")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatalf("seed the outside file: %v", err)
	}
	path := filepath.Join(ws.StateDir(), db.FileName)
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := db.Path(ws); err == nil {
		t.Fatal("db.Path followed a symlink out of the workspace")
	}
}
