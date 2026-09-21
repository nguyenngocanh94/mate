// Package dashboard is the read-only HTTP face of the timeline
// (docs/mvp.md M6, docs/dashboard.md).
//
// Everything it answers comes out of `.matev2/matev2.db` through
// db.OpenRead, which takes no lock, so the dashboard runs while the console
// holds the writer. The views are the contract: `v_now` for where an actor
// stands, `v_task_ledger` for what a task cost, `v_story` for what happened.
// Where a view has no column for something a page needs - an actor's
// harness, a turn's actions, a question's answer - this package queries the
// table behind it, and docs/dashboard.md names every one of those.
//
// Three things are deliberate:
//
// Nothing writes. There is no handler that takes a POST, no column this
// package updates, and no file it opens for writing. Every action stays in
// the console TUI, which is where a reader who can act already is.
//
// The bind address must be loopback unless the caller passes AllowRemote. A
// workspace's timeline names branches, file paths, the commands a crew ran
// and the text of everything the captain typed; that is not something to put
// on a LAN by forgetting a flag.
//
// A response is computed once per `event.id`. The last event id is the whole
// freshness signal of this database - the ingest only ever appends - so a
// page that refreshes every second while nothing happens costs one integer
// read, and `generated_at` is when the snapshot behind the bytes was
// computed rather than when they were served.
package dashboard
