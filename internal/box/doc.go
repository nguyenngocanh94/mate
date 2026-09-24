// Package box merges a project's communication - crew status lines,
// sent.log, and observer incidents - into one time-ordered view. It is pure:
// it reads through internal/store and returns values, makes no Herdr calls,
// and writes nothing. The console (task 15) renders a View for the human;
// the observer (task 18) polls LoadSince for the same data.
//
// Ordering and its one documented limitation: `crews/<id>.status` lines carry
// no timestamp, because a crew appends them with a bare
// `echo "state: one line" >> $MATE_STATUS`. The only time signal available
// is the status file's mtime, which reflects when its last line was written.
// Every status line currently in a file is therefore stamped with that file's
// current mtime, and lines from the same file are ordered relative to each
// other by their byte offset, not by wall-clock time. This means a status
// file with several unread lines shows them all at the same instant - the
// time of the most recent append - even though they were written
// sequentially over some span. Callers that need finer-grained crew timing
// must look elsewhere (there is none in the MVP); this package only orders
// what the file format can support.
//
// sent.log entries carry a real RFC3339 timestamp and need no such
// approximation, and neither do the incidents: `incidents.log` is written by
// the observer (internal/watch) with a timestamp per line. This package only
// reads that file; it never detects an incident and never writes one.
package box
