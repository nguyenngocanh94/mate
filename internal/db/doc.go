// Package db owns `.matev2/matev2.db`, the derived timeline store of
// docs/mvp.md decision 6 and section M5.
//
// The database is derived and rebuildable. Nothing in it is the record of
// anything: `crews/<id>.status`, `sent.log`, `incidents.log`, the `.meta`
// files, the harness transcripts and git are the record, and
// `matev2 reindex` rebuilds every table here from them. Losing the file
// loses no work, which is why the flat files stay exactly as they were and
// why no writer in this repository was changed to feed the database.
//
// One writer. The observer inside the console is the process that ingests,
// so Open takes an advisory lock on `matev2.db.lock` beside the database and
// a second writer is refused with ErrLocked rather than allowed to interleave
// with the first. Readers - `matev2 events`, a dashboard, the console's own
// columns - use OpenRead, which takes no lock at all and never writes: SQLite
// in WAL mode lets them read the last committed snapshot while an ingest is
// in flight.
//
// Path boundary. The database path is resolved through the workspace the way
// every other path under `.matev2/` is (store.Workspace.Resolve), so a
// symlink planted at `matev2.db` cannot make an ingest write outside the
// workspace.
//
// Schema. Migrations are ordered, idempotent and recorded in
// `schema_version`; Open migrates up from an empty file and from any earlier
// version. The tables are the ones docs/mvp.md M5 lists, and docs/timeline.md
// carries the payload of every event kind. The one addition to that list is
// `event.dedup`, whose reason is written on the column.
package db
