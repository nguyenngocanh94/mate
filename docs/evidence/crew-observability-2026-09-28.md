# Crew observability acceptance — 2026-09-28

Design: `docs/plans/crew-harness-observability-2026-09-28.md`.

## Real transcript and dashboard

Source workspace: `/Users/anh/newWorkspace`, Crew `hellovietnam/ios7`.
Validation uses an isolated SQLite backup and a read-only source workspace;
the running console continues to own its original database writer lock.

Recorded API results:

| Measure | Result |
| --- | ---: |
| Prompt turns | 3 |
| Model calls | 363 |
| Native command executions | 162 |
| Native command failures | 10 |
| Process polls | 153 |
| Fresh input tokens | 233,931 |
| Cache read tokens | 40,106,880 |
| Cache write tokens | 0 |
| Output tokens | 68,716 |
| Total tokens | 40,409,527 |

All six token fields (including the informational thinking bucket) sum exactly
from segments and prompt turns to the existing task ledger. Thinking is already
inside output and is not added again to total.

The first screen exposes the ten command failures and the largest polling
chains. One chain has 11 polls: 10 with no new output and one with new output;
its associated model calls use 3,136 fresh input, 2,034,048 cache read and 471
output tokens. This is polling overhead evidence, not a claim the test stalled.
Clicking the failure finding opens native commands and exit codes, including
exit 65, even though the outer `exec` wrapper succeeded.

Time lanes use native intervals: prompt elapsed 3,299,710 ms, union of tool
intervals 1,282,762 ms, sum of invocation intervals 1,282,776 ms, recorded
decision wait 62,046 ms. These overlapping lanes are not additive. Synthetic
legacy `thinking` actions are excluded from tool time.

Chrome acceptance at 1440 × 1000 and 500 × 900 confirms the first-screen
findings, exact failure-evidence navigation and no document overflow. A mobile
click opens exit-65 evidence directly. Sorting by total tokens preserves that
evidence. Primary work/finding labels contain no unresolved wrapper JavaScript.

## Coverage limits

- Historical runs have no launch snapshot. New Crew launches persist hashes,
  sizes, model/effort and code revision before starting the harness; prior
  snapshots survive relaunch and reindex.
- Some native executions have no confirmed model-call parent. Their commands,
  timings and errors remain visible; individual token attribution is unknown.
- Observed input bytes are evidence of content, not exact per-file token cost.
- Standard tests may skip opt-in live harness tests. Replaying real transcript
  records through observer, database and browser is a separate proof and does
  not claim a newly spawned live Crew was exercised.

## Observer overhead

Measured against 35 real transcripts totaling approximately 191 MB. CPU
profiling identified two material costs: a correlated event lookup lacked an
index, and growing Codex files forced legacy history to be decoded/written
again. Schema 3 adds `event(turn_id, kind)`; the cache uses the normalized
parser's committed `NextState` to parse appended data and rebases source
offsets. New rows and late results retain their original ledger identity.

After these changes, the isolated profiler measured 6.246 s on initial ingest
and 1.733 s on the following ingest with growing sources. The earlier build
took tens of seconds per update. These are local measurements, not an SLA;
source volume and concurrent machine load affect elapsed time.

A fresh schema-2 backup migrated through the final build also verified all
three instruction inputs on `ios7`: base instructions 18,998 bytes, AGENTS
12,623 bytes and host skills 15,900 bytes (UTF-8 bytes, not characters/tokens).
Observed runtime is Codex 0.157.1, model `gpt-6-sol`, effort `medium`.

During the final concurrent-machine run, initial backfill took 23.929 s and
updates ranged from roughly 6 to 26 s. The machine had ten logical CPUs with
load averages above 120, so the earlier 1.733 s sample must not be presented
as its current guaranteed refresh time. Cursor errors were zero; the UI shows
the actual observation/ingest age. The following API checks still returned
200 for Crew diagnostics and 115 Mate prompt exchanges.

## Verification

- `make check`: passed; 60 expected opt-in live skips remain explicitly unproven.
- Thirteen frontend interaction tests pass with `node --test
  internal/dashboard/ui_observability_test.cjs`.
- Launch snapshot tests verify later edits do not change the baseline, different
  task text does not change the configuration fingerprint, shared template
  changes do change it, and earlier launches survive relaunch/reindex.
- Incremental parser tests compare appended and full parsing, including partial
  records and cumulative usage; replay/rollback/reindex and late result tests
  cover cursor/fact transaction behavior.
- An independent read of the real Crew API completed in 394 ms; a later request
  after the concurrent test suite completed took 99 ms.

## Rollout state

The review server is at `http://127.0.0.1:7778/#/p/hellovietnam/t/ios7`, using
an isolated derived database while reading the real transcript sources. The
original console and port-7777 server remain on their existing build/schema.
Replacing the installed binary and migrating the main database requires the
user to close the active console first; its single-writer lock is respected.
The user explicitly chose to keep the current console and review port 7778;
main installation/migration is deferred by that choice. The candidate CLI is
built and all temporary Go helper sources were removed
from the repository. No commit, push or main database mutation was performed.

## Follow-up: work overview on every Mate and Crew prompt

Each collapsed prompt now shows a summary and work-type labels. Expanded
prompts show the observed stages, category token buckets, measured execution
time and command/file/source evidence. Categories include research, writing,
editing, review, tests/builds, coordination, instructions, waiting, mixed and
unclassified activity. They come from observed operations; quoted commands in
a brief do not establish completed work.

The real preview API contains 115 Mate exchanges (432 model calls) and three
`ios7` Crew prompts (363 calls). Every overview conserves all six token fields
and model-call count per prompt, with no call ID shared across categories or
prompts. Three Mate prompts with no reported usage remain visible. One API
sample took 330 ms for Mate and 294 ms for Crew.

Five API regressions cover Mate/Crew conservation, exact native prompt evidence,
activity before usage arrives, native alias resolution and clocks advancing
without new events. Eleven overview regressions additionally cover mixed
poll/test calls, instruction/quoted-command false positives, separate sessions,
parallel interval unions, repeated test → edit → test stages and historical
execution timing. Thirteen frontend interactions include both overview views,
evidence navigation, retained detail state and Mate refresh after a quiet poll.

This follow-up changes read-only projections and UI; no additional schema or
main workspace migration is needed. Preview deployment remains at port 7778
as requested.
