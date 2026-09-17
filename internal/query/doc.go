// Package query exposes read-only DTOs for Console and future metrics UI.
// UI must not read SQLite or Herdr directly: every read here composes
// internal/application's use cases and its StateStore port. Nothing in this
// package opens a database connection or a Herdr session on its own behalf.
//
// Two rules shape every type in here:
//
// Nothing is live. Every status is durable, recorded database state, and
// Snapshot.AsOf is the moment the read finished, so a UI can say how stale
// the picture is instead of implying it is current. Live liveness belongs
// to the G7-04 crew health observer (ADR 0019), which does not exist, and
// nothing here approximates it.
//
// Nothing is guessed. Any value whose read can fail is a Field[T] carrying
// Known/Absent/Unknown plus the reason - authored here, not in the UI, so
// `mate` output and the Console say the same thing about the same row. A
// read failure is Unknown, never Absent, and every Unknown field in a
// snapshot is also listed in Snapshot.Warnings so a footer can report them
// without walking the tree itself.
package query
