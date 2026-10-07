// Package autopilot is the auto-mode daemon of docs/mvp.md section 5: it
// decides what the Mate is told without a human pressing a key.
//
// It lives in the console process, beside the observer (internal/watch), and
// like the observer it is a plain polling loop over the workspace's files
// with an injectable clock. Since task 30 it types nothing itself: every
// digest goes into the Mate's outbox (internal/outbox, `mate/.outbox`), the
// one sender into a Mate's composer, which `[assign]` uses too. This package
// holds no runtime at all.
//
// # What one tick does, per project whose `mate/.auto` exists
//
//  1. Merge the project's communication (internal/box) and take what is new
//     since the recorded cursor: the unresolved inbox (a crew's
//     `needs-decision`, an open incident) plus each open crew's latest status
//     line when that line is `wait-mate`. A `wait-mate` is not an inbox item
//     - the crews table already carries it and nobody has to answer it - but
//     in auto mode there is no reader watching that table, so the Mate is
//     the one who has to review it.
//  2. Nothing new means nothing is sent. The daemon is not a heartbeat.
//  3. Everything new becomes one line (see Line), queued in the outbox under
//     a key naming exactly those items (Key), and tried once at once so an
//     idle Mate gets it with no delay. A Busy or Pending composer leaves it
//     queued; the outbox's own loop retries it every two seconds. At most
//     one digest is queued per project: a later tick with a different set of
//     items rewrites the queued line in place, and one with nothing left to
//     say withdraws it.
//  4. Only a verified send advances the cursor, so a refused digest is
//     re-offered whole rather than silently dropped. The queued digest
//     carries the cursor it would record, and the outbox writes it when it
//     marks the digest sent, under the same lock this package gathers
//     under.
//
// # The two ways a project stops being digested
//
// The `.auto` flag disappearing is the only stop condition, and it has two
// writers: the console's `m` key toggles it, and a user can remove the file
// by hand. The captain typing to the Mate no longer turns it off (docs/mvp.md
// M18; it used to, and a crew's `wait-mate` landing in that window was told
// to nobody). A manual project's crews wait in the captain's inbox instead
// (internal/box). The daemon re-reads
// the flag at the top of each project's turn, and the outbox reads it again
// immediately before it types a digest (and withdraws the digest instead), so
// a captain who takes over is not answered by a machine a moment later.
//
// # Idempotence across restarts
//
// The cursor is `mate/.auto-cursor` (store.ReadAutoCursor), a byte offset per
// source file. A console that is killed and reopened, or a project whose
// auto flag went off and on again, resumes after the last digested line
// instead of re-sending a week of questions into the Mate's context. See
// internal/store/auto.go for why it is its own file rather than content
// inside `.auto`.
//
// # Wedged
//
// A digest that does not reach the Mate for longer than DefaultWedgedAfter
// opens a `wedged` incident (mvp.md section 4b) whose crew field is `mate`,
// and a verified send resolves it. Since task 30 that rule is the outbox's,
// for every queued line and not only digests, and its clock is the queued
// item's own `at` on disk rather than a stopwatch in this process: a console
// restart neither resets it nor opens a second incident. The incident text
// always names which half failed - a composer somebody else is typing into,
// a harness mid-turn, a screen the classifier cannot name, or a Mate that is
// not running at all.
package autopilot
