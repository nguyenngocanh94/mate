package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure Go SQLite driver; no cgo, so `make check` cross-compiles

	"github.com/nguyenngocanh94/matev2/internal/store"
)

// FileName is the database file inside `.matev2/`, and LockName the advisory
// lock beside it. Both are spelled once, here.
const (
	FileName = "matev2.db"
	LockName = "matev2.db.lock"
)

// ErrLocked is the refusal a second writer gets. It names the file rather
// than the process, because the process holding it may be a console on
// another terminal and the only thing this one can prove is that the lock is
// taken.
var ErrLocked = errors.New("db: another matev2 process is already writing this workspace's timeline")

// TimeFormat is how every timestamp is stored: RFC3339 with nanoseconds, in
// UTC. It is fixed width up to the fractional part and lexicographically
// ordered, so `ORDER BY at` is chronological and two rebuilds of the same
// files print byte-identical rows.
const TimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

// FormatTime renders t for storage. A zero time stores as the empty string,
// which sorts before every real timestamp and reads back as zero.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(TimeFormat)
}

// ParseTime reverses FormatTime, tolerating a plain RFC3339 value so a row
// written by hand or by an older build still reads.
func ParseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(TimeFormat, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return time.Time{}
}

// DB is an open timeline database. A writer holds the advisory lock for as
// long as it is open; a reader holds nothing.
type DB struct {
	sql    *sql.DB
	path   string
	lock   *os.File
	writer bool
}

// Path is `<workspace>/.matev2/matev2.db`, resolved through the workspace
// boundary so a symlink at that name cannot move the database out of the
// workspace.
func Path(ws *store.Workspace) (string, error) {
	return ws.Resolve(filepath.Join(ws.StateDir(), FileName))
}

// Open opens the workspace's database for writing: it takes the advisory
// lock, applies every outstanding migration and returns a handle. A second
// writer gets ErrLocked and nothing is changed.
func Open(ws *store.Workspace) (*DB, error) {
	path, err := Path(ws)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return OpenPath(path)
}

// OpenPath is Open for a database named directly. Tests use it; the CLI and
// the observer go through Open so the boundary check runs.
func OpenPath(path string) (*DB, error) {
	lock, err := takeLock(filepath.Join(filepath.Dir(path), LockName))
	if err != nil {
		return nil, err
	}
	sqlDB, err := openSQL(path, true)
	if err != nil {
		releaseLock(lock)
		return nil, err
	}
	d := &DB{sql: sqlDB, path: path, lock: lock, writer: true}
	if err := d.migrate(); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

// OpenRead opens the database read-only and takes no lock, so any number of
// readers can follow the timeline while the observer ingests. It never
// migrates: a reader that finds an older schema says so rather than
// rewriting a file it does not own.
func OpenRead(ws *store.Workspace) (*DB, error) {
	path, err := Path(ws)
	if err != nil {
		return nil, err
	}
	return OpenReadPath(path)
}

// OpenReadPath is OpenRead for a database named directly.
func OpenReadPath(path string) (*DB, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("db: no timeline at %s yet; open the workspace console once, or run `matev2 reindex`", path)
		}
		return nil, err
	}
	sqlDB, err := openSQL(path, false)
	if err != nil {
		return nil, err
	}
	d := &DB{sql: sqlDB, path: path}
	version, err := d.Version()
	if err != nil {
		_ = d.Close()
		return nil, err
	}
	if version != SchemaVersion {
		_ = d.Close()
		return nil, fmt.Errorf("db: %s is at schema version %d, this build reads %d; run `matev2 reindex`",
			path, version, SchemaVersion)
	}
	return d, nil
}

func openSQL(path string, write bool) (*sql.DB, error) {
	// WAL so a reader never blocks the ingest and the ingest never blocks a
	// reader; busy_timeout so the one case they do collide - a checkpoint -
	// waits instead of failing. foreign_keys is on because the ids here are
	// the whole point of the schema.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if write {
		dsn += "&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	} else {
		dsn += "&mode=ro"
	}
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection. modernc's driver is safe for concurrent use, but the
	// ingest is a single goroutine writing one transaction at a time and a
	// pool would only buy the chance of two of them.
	sqlDB.SetMaxOpenConns(1)
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return sqlDB, nil
}

// SQL is the handle for queries. It is exported because `internal/timeline`
// and the read-only CLI build their own statements: this package owns the
// schema and the lock, not every query over it.
func (d *DB) SQL() *sql.DB { return d.sql }

// Path is where the database lives.
func (d *DB) Path() string { return d.path }

// Writable reports whether this handle holds the writer lock.
func (d *DB) Writable() bool { return d.writer }

// Close releases the lock and the connection.
func (d *DB) Close() error {
	var errs []error
	if d.sql != nil {
		errs = append(errs, d.sql.Close())
		d.sql = nil
	}
	if d.lock != nil {
		releaseLock(d.lock)
		d.lock = nil
	}
	return errors.Join(errs...)
}

// Version is the highest applied schema version, or 0 for a database that has
// never been migrated.
func (d *DB) Version() (int, error) {
	var name string
	err := d.sql.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var version sql.NullInt64
	if err := d.sql.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		return 0, err
	}
	if !version.Valid {
		return 0, nil
	}
	return int(version.Int64), nil
}

// migrate applies every migration whose version is above the recorded one.
// Each step and the row that records it share one transaction, so a database
// is never half-migrated.
func (d *DB) migrate() error {
	current, err := d.Version()
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := d.applyMigration(m); err != nil {
			return fmt.Errorf("db: migration %d: %w", m.version, err)
		}
	}
	return nil
}

func (d *DB) applyMigration(m migration) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range m.stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("%w (in %s)", err, firstLine(stmt))
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_version(version, applied_at) VALUES (?, ?)`,
		m.version, FormatTime(time.Now())); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetDerived empties every derived table inside one transaction: it is what
// `matev2 reindex` runs before rebuilding from the files. The schema and its
// version are left alone.
//
// AUTOINCREMENT keeps its high-water mark in `sqlite_sequence`, so the
// sequence is reset too - a rebuild of the same files must produce the same
// `event.id`s, which is what makes two reindexes byte-identical.
func (d *DB) ResetDerived(ctx context.Context) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := ResetDerivedTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetDerivedTx is ResetDerived inside a transaction the caller owns, so a
// rebuild can empty the tables and refill them in one atomic step: a reindex
// that fails halfway leaves the timeline it started with rather than an empty
// one.
func ResetDerivedTx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	for _, table := range derivedTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return fmt.Errorf("db: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sqlite_sequence WHERE name = 'event'`); err != nil {
		return fmt.Errorf("db: reset the event sequence: %w", err)
	}
	return nil
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
