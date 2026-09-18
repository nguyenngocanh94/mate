// Package autopilot is the auto-mode daemon of docs/mvp.md section 5: the
// one thing in matev2 that may type into the Mate's own composer without a
// human pressing a key.
//
// It lives in the console process, beside the observer (internal/watch), and
// like the observer it is a plain polling loop over the workspace's files
// with an injectable clock. It is a separate package from the observer
// because the two are opposites: the observer only ever reads panes and
// appends findings, while this one sends - so the narrow Runtime interface
// here is the one that can type, and the observer's structurally cannot.
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
//  3. Everything new becomes one line (see Line), sent with send.Send, which
//     types only into an empty composer and proves the line was submitted.
//     A Busy or Pending composer is a refusal, not a retry loop: the captain
//     shares that composer, and the next tick asks again.
//  4. Only a verified send advances the cursor, so a refused digest is
//     re-offered whole rather than silently dropped.
//
// # The three ways a project stops being digested
//
// The `.auto` flag disappearing is the only stop condition, and it has three
// writers: the Mate's own UserPromptSubmit hook deletes it the moment the
// captain types an unmarked prompt (internal/hook), the console's `m` key
// toggles it, and a user can remove the file by hand. The daemon re-reads
// the flag at the top of each project's turn and again immediately before it
// types, so a captain who takes over mid-tick is not answered by a machine a
// moment later.
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
// A digest that does not reach the Mate for longer than WedgedAfter opens a
// `wedged` incident (mvp.md section 4b) whose crew field is `mate`, and a
// verified send resolves it. The incident is what makes the failure durable:
// a footer line dies with the console, while the inbox shows an open incident
// to whoever opens it next.
//
// The clock starts on any tick that had something to say and could not say
// it - a composer somebody else is typing into, a harness mid-turn, a screen
// the classifier cannot name, or a Mate that is not running at all. The last
// of those is a wider reading of the kind's definition ("gửi vào pane không
// kiểm chứng được quá lâu") than its letter, and it is deliberate: auto mode
// quietly delivering nothing for an hour because the Mate died is exactly the
// state the incident exists to surface, and the incident text always names
// which of the two it was.
package autopilot
