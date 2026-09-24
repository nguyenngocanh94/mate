// Package timeline reads the files mate already writes - `crews/<id>.status`,
// `sent.log`, `incidents.log`, the `.meta` files, the harness transcripts and
// the crews' git branches - and records what they say into `internal/db` as
// one causally linked story (docs/mvp.md section M5, task 25).
//
// It writes nothing back. Every rule here is a reading rule, and
// `mate reindex` proves it: dropping every derived table and running the
// same ingest over the same files rebuilds the same rows, with the same
// `event.id`s, so nothing in the database is a fact the files do not carry.
// The one exception is written down rather than hidden - see `mode.changed`
// and `health.changed` in docs/timeline.md, which are observations of a
// moment that leaves no file behind.
//
// The ingest is incremental and idempotent. Append-only logs are read from
// `cursor`; a transcript is re-read whole on every pass, because its trailing
// message group may still grow and `harness.ParseTranscript` deliberately
// withholds it (see internal/harness/transcript.go). Every fact carries a
// natural key, so re-reading a source can only produce the row it already
// produced: `event.dedup` is unique, and every other table is keyed by what
// produced it.
//
// The observer inside the console calls Ingest at the end of each poll, and
// is the only writer: `internal/db` takes an advisory lock, so a second one
// is refused rather than interleaved.
package timeline
