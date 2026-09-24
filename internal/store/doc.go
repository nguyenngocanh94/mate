// Package store is the only package that reads and writes `.mate/` and
// `.worktrees/`. Every other package - project CRUD, spawn, box, watch,
// console - goes through it, so the on-disk layout of docs/mvp.md section 3 is
// described in exactly one place.
//
// Ownership rule. The console process is the only writer of state: it creates
// the workspace, registers projects, writes `workspace.yaml`, `project.yaml`,
// the `.meta` files and the `.auto` flag. Agents do not write state. The one
// exception is `crews/<id>.status`, which a crew appends to with a plain
// `echo "state: one line" >> $MATE_STATUS`; that is why the status log is
// append-only, line oriented and locked rather than rewritten. `sent.log` is
// append-only for the same reason: the console and the Mate hooks both add
// lines to it and nobody edits it. Anything that is not an append is an atomic
// temp-file-plus-rename write, so a reader never sees half a file.
//
// Path boundary. Every path this package writes is resolved through
// filepath.EvalSymlinks on its deepest existing ancestor and must land inside
// the workspace root. A symlink under `.mate/` that points outside the
// workspace makes the write fail with *BoundaryError and nothing is created.
// Registered repositories must be inside the workspace too; they need not be
// direct children of it.
package store
