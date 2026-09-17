// Package query is the read boundary between matev2's state and its UI: a
// set of DTOs plus the pure functions that derive capability and attention
// from them. It opens nothing. There is no database connection, no Herdr
// session and no file handle in here - load.go is the one file that reads
// `.matev2/` through internal/store, and everything else in the package is
// types and derivation.
//
// That split is what keeps internal/ui/console honest: the Console imports
// this package's types and nothing else (its boundary_test.go enforces
// that), so a snapshot on screen can only have arrived through a
// caller-supplied LoadFunc.
//
// Two rules shape every type in here:
//
// Nothing is live. Every status is what a backend recorded, and
// Snapshot.AsOf is the moment the read finished, so a UI can say how stale
// the picture is instead of implying it is current. Herdr's screen-scraped
// agent state is a secondary signal and never a conclusion (mvp.md section
// 2, decision 8); nothing here approximates it.
//
// Nothing is guessed. Any value whose read can fail is a Field[T] carrying
// Known/Absent/Unknown plus the reason - authored here, not in the UI, so
// `matev2` command output and the Console say the same thing about the same
// row. A read failure is Unknown, never Absent, and every Unknown field in
// a snapshot is also listed in Snapshot.Warnings so a footer can report
// them without walking the tree itself.
package query
