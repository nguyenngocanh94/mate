// Package watch is the observer of docs/mvp.md section 4b: the one thing in
// matev2 that notices a crew which can no longer speak for itself.
//
// Every poll, for every open crew of every project, it asks three questions
// and nothing more:
//
//   - Is the agent the crew's `.meta` records still in Herdr's inventory
//     (runtime.Adapter.InspectAgent, runtime.IsAgentNotFound)?
//   - What is the pane showing (runtime.Adapter.ReadAgent plus
//     send.ClassifyComposer), and has that screen changed since last time?
//   - Has the crew appended anything to `crews/<id>.status`?
//
// From those it opens and resolves incidents in `incidents.log`. It writes
// nowhere else: a crew's `.status` belongs to the crew (section 4b - the
// observer never puts words in its mouth), and its `.meta` belongs to spawn
// and to `crew stop`. `blocked` is not a state anybody stores; it is what
// internal/box derives from an incident this package left open.
//
// Two kinds exist today:
//
//   - `stale`: the status file and the pane have both been unchanged for
//     longer than the threshold, while the composer is not busy and the
//     crew's last status verb is not one of the waiting verbs. A crew that
//     asked a question (`needs-decision`), handed back (`wait-mate`) or
//     reported it was done is waiting for a human, not stuck, and silence
//     is the correct behaviour for it.
//   - `runtime_lost`: Herdr answered that it does not have that agent.
//
// Both resolve when the condition clears - the pane changed, the composer
// went busy again, the crew wrote a status line, the agent came back - and a
// resolve is a new `resolved` line, never an edit.
//
// What it refuses to conclude matters as much as what it detects. A read
// that fails - Herdr unreachable, the pane unreadable, the recorded handle
// unresolvable - produces no incident and no health, because "mate could not
// look" is not evidence that anything is wrong (mvp.md decision 8: a
// screen-scraped signal is never a conclusion). Only a positive
// `agent_not_found` from Herdr opens `runtime_lost`.
//
// The observer lives in the console process. Closing the console means
// nobody is watching and no new incidents appear; mvp.md section 4b accepts
// that for the MVP. What it already wrote stays in `incidents.log`, so a
// console that starts again re-reads which incidents are open rather than
// re-deciding them.
package watch
