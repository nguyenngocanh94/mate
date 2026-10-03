# Timeline

Schema version: **3**.

This is the contract of `.mate/mate.db` and of `internal/timeline`.
The database is derived: every row here is read out of `crews/<id>.status`, `sent.log`, `incidents.log`, the `.meta` files, the harness transcripts or git, and `mate reindex <workspace>` rebuilds all of it from those sources.
Losing the file loses no work (docs/mvp.md decision 6).
Two rebuilds of the same sources produce byte-identical `v_story` output, `event.id` included, which is what lets a reader quote an id.

The observer inside the console is the single writer: it calls `Ingest` at the end of every poll, and `internal/db` holds an advisory lock on `mate.db.lock` so a second writer is refused rather than interleaved.
Readers take no lock at all.

## 1. What the schema adds to the spec

The tables are the ones docs/mvp.md M5 lists, with one addition and one column worth naming.

`event.dedup` is the natural key of a fact and is `UNIQUE`.
It exists because a transcript's trailing message group may still grow, so the ingest re-reads it on every pass; without a key the same fact would become a second row with a second id, and a dashboard following `event.id > ?` would replay history as news.

`turn.harness_turn_ref` carries the harness's own larger unit - Claude's `promptId`, Codex's `task_started` turn id - beside the per-call turn.
A turn row is one model call in both harnesses; the harness turn is one prompt and everything it caused, and it is what groups a crew's calls into "the work the Mate's answer set off".

`telemetry_cursor` stores the native observer adapter's byte offset and JSON
correlation state in the same transaction as its facts. It also records the
actor/session, source size/mtime, last observer heartbeat, and parser error.
`mate reindex` clears this table along with the derived facts. A prefix hash in
the state detects source replacement (including a larger replacement); a
replacement or truncation emits a visible gap instead of silently treating the
remaining bytes as an append.

Unchanged transcripts reuse a normalized parser cache; growing Codex sources
append a parsed tail using the adapter's cumulative-usage and prompt state.
Committed file metadata
and the ledger cursor allow ingest to skip rewriting historical turns/actions;
status timestamps and commit sightings are still reconstructed from the cached
commands. A restart can rebuild the cache without duplicating facts. Native
Codex telemetry tails only complete appended JSONL records; partial or malformed
lines keep their byte position for a later pass. Late results retain their
wrapper/process correlations across restarts.

### Crew diagnostic evidence

Events named `telemetry.*` carry the versioned `internal/telemetry.Fact` shape:
session and prompt identity where observed, source byte reference, occurrence
and observation times, and whether a measurement came from native records or
the normalized fallback. They are diagnostic evidence and are excluded from
the coordination story and scene; `StoryQuery.IncludeTelemetry` exposes them
in the raw story when explicitly requested.

Kinds include `prompt`, `turn_started`, `turn_completed`, `response`,
`execution`, `tool_call`, `tool_result`, `progress`, `context`, `activity`,
`capability`, and `gap`. `telemetry.profile` contains the source launch profile
from `store.HarnessProfile`, including archived launch snapshots. It records
configured documents, while `context`/`instruction_input` records instruction
bytes/hash actually observed in the transcript. Neither is an exact token
measurement of an individual file. Private reasoning text is never copied.

Native Codex responses retain response IDs and cache-separated usage. A later
`token_count` with the identical cumulative counters adds an alias observation
with `ledger_ref_offset`; consumers collapse observations by response ID and
prefer the linked one. **`turn` remains the only additive token ledger.**
Summing response evidence alongside it would double-count. Unmatched response
evidence remains visible; pending alias state is bounded with an explicit gap.

Native command observations preserve execution/process IDs, command/cwd,
known start/end/duration, status and nullable exit code. Wrapper success never
overrides a failed native command. Polls are linked only when a literal process
ID is recoverable; nested structured command output supplies exact new-output
bytes. Unknown output progress, truncation, timestamps, wrapper ancestry and
child usage remain unknown. Output hashes and bounded excerpts provide evidence
without copying a second full transcript. Claude uses existing normalized
response/tool facts and explicitly reports unavailable native process/timing
coverage.

Read-only-source measurement on 2026-09-28: a real workspace with 35 located
transcripts (about 191 MB when first measured) ingested into an isolated database
in 6.25 seconds with a cold parser cache and 1.73 seconds on the next pass while
sources were still growing. This measures observer ingest, not harness latency.
The native extractor separately consumed a 9.5 MB Crew rollout in 404 ms and
recovered all 363 response aliases, 162 commands including 10 failures, 153 polls
with measured new-output bytes, and 42 file changes. These are local measurements,
not a universal overhead bound. The turn-event index in schema 3 prevents the
causality projection from scanning all native evidence once per model call.

## 2. Locator rules

An agent's transcript is found by the first rule that answers, and the rule that fired is recorded on the `session` row's path and in the `ingest.unresolved` payload when none does.
Rule 1 and the recorded path of an earlier pass (`session.recorded`) are the timeline's own; rules 2 to 4 belong to the harness, behind its `Transcript` capability (`harness.TranscriptSource.Locate`), and the timeline asks the harness the agent runs on.
A harness whose `Transcript` capability is not verified has no rules and no reader: the agent is recorded as `transcript_unobservable` and has no turns and no tokens, never guessed ones.

1. `meta.transcript` - `.meta` already names the file.
   A Mate running Claude Code has it from the Stop hook's `transcript_path` (docs/mvp.md decision 9), which is exact.
2. `claude.projects` - `session_id=` plus Claude's own naming rule, `~/.claude/projects/<slug of cwd>/<session-id>.jsonl`.
   This is what finds a Claude agent before its first Stop hook has run.
3. `herdr.agent_session` - the runtime's `agent_session.value`, which for Codex is the rollout's session uuid, and Codex puts that uuid in the rollout's file name.
   Measured 2026-09-20 on Herdr 0.8.2: `agent_session` is present and correct for every Codex agent and **null for every Claude agent**, so this is a Codex rule and only a Codex rule.
4. `codex.adopt` - `harness.AdoptCodexRollout` over the rollout directory, matching on the canonical cwd and a `session_meta` timestamp at or after the recorded launch.
   This is the fallback for a crew whose agent Herdr no longer has, and it is deliberately conservative: two crews launched in the same worktree are genuinely ambiguous, and an ambiguous match is no match.
   The recorded launch is `launched_at=`, which `crew spawn` and `mate start` take just before `agent start`.
   It is not `started_at=`: that is written once the agent is ready and has its first prompt, and Codex has opened its rollout by then (measured 2026-09-24, task 34: a rollout's first record 0.2s before `started_at`, so every rebuild after the crew was gone lost its transcript, and whether a crew was affected was a sub-second race).
   A record written before `launched_at` existed uses `started_at` less five minutes, which spawn's own timeouts bound; the exact cwd still has to match, and a crew's worktree path is its own.

A crew's binding does not move while one agent runs - a crew is never resumed (docs/mvp.md section 4b) - so once one of those rules has answered, a later pass reuses the path the `session` row already carries (`session.recorded`) instead of asking the runtime again.
`mate crew relaunch` starts a fresh session under the same actor, so a recorded path is reused only when it belongs to the current launch: its session id matches the meta's `session_id=` when both are known, and the file was written at or after `launched_at`.
A dead agent's transcript fails one of the two and the rules are tried again from the top.
Without that, every Codex crew would cost two more `herdr` calls every five seconds on top of the observer's three.
A rebuild starts without the cache and runs the rules again.

A transcript nothing finds is an `ingest.unresolved` event, written once per `(actor, reason)`.
It is an event rather than a log line because the consequence is otherwise invisible: every turn, token and tool call of that agent is simply missing, and a timeline with a hole that says nothing about itself is worse than one with a hole that does.

## 3. How a status line gets a time

A `crews/<id>.status` line carries no timestamp of its own.
Two rules, in order, and the `status.appended` payload records which one fired in `dated_by`.

1. `transcript.shell` - the crew wrote the line with `echo "state: one line" >> $MATE_STATUS`, and that shell command is in the crew's own transcript with a timestamp on it, so the line is dated by the earliest action whose command contains it verbatim.
   This rule is exact and it survives a rebuild.
2. `status.mtime` - the file's modification time, clamped so a file's lines never go backwards.
   It is a fallback and not the primary rule because mtime is the time of the file's *last* line: dating every line by it would put a question after the answer to it the moment the crew wrote anything else, and `question.waited_ms` would go negative on the first reindex.

## 4. The event vocabulary

Every kind's payload is a JSON object, and every event also carries `ref_path` and `ref_offset` - the byte of the source it was read from - so any fact can be traced back.

### `mate.started`, `mate.stopped`

From `mate/mate.meta`.

```json
{"agent":"mate-shop","harness":"claude","resumed_from":"","session_id":"8414030c-5d90-4925-94cc-c94e12aae4a9"}
```

### `crew.spawned`

From `crews/<id>.meta`, dated by `started_at`.
The actor is the Mate, the subject is the crew: hiring is something the Mate did to somebody.

```json
{"branch":"mate/buybtn","crew":"buybtn","harness":"codex","task":"Add a Buy button to README.md","worktree":".worktrees/shop-buybtn"}
```

### `crew.finished`, `crew.failed`

From `state=finished|failed` in the same file, dated by `stopped_at`.

```json
{"crew":"buybtn","state":"finished"}
```

### `mode.changed`

From the presence of `mate/.auto`.

```json
{"dated_by":"flag_mtime","from":"manual","to":"auto"}
```

This is an observation, not a reading of a file that records history.
`.auto` is a flag with three different things allowed to delete it, and its removal leaves nothing behind, so a pass compares the flag with the last `mode.changed` it recorded and writes one event when they differ.
Turning auto on is dated by the flag's mtime (`dated_by: flag_mtime`) and is exact; turning it off is dated by the pass that noticed (`dated_by: observed`) and is a lower bound.
A rebuild therefore recovers only the mode the workspace is in now, which is the honest consequence of the flag being a flag.

### `turn.started`, `turn.ended`

One pair per model call.
For Claude that is one assistant message group (one `message.id`), which is the unit Claude restates usage on; for Codex it is the group between two `token_count` records, which is the same unit seen from the other side.

```json
{"cache_read_tokens":57690,"cache_write_tokens":739,"context_tokens_after":58461,"harness_turn":"e6114c69-5e7a-4d34-b35d-ef6c74677ed2","input_tokens":32,"model":"claude-fable-5-1","outcome":"tool_use","output_tokens":911,"thinking_tokens":73,"tool_count":1}
```

`turn.ended` carries the same payload plus `duration_ms`.

`context_tokens_after` is what the context held when the call returned.
For Claude it is the message's `input_tokens + cache_read_input_tokens + cache_creation_input_tokens`.
For Codex it is the `token_count` record's `last_token_usage.input_tokens + cache_write_input_tokens`, because Codex's `input_tokens` already includes the cached part and is therefore the prompt size; the cumulative delta is the fallback when the record carries no `last_token_usage`.

The trailing message group of a Claude transcript is never a turn while the file may still grow: `harness.ParseTranscript` withholds it deliberately, and this ingest does not flush it (see `internal/harness/transcript.go`).
A live session's last turn is therefore pending, not lost.

### `tool.called`, `tool.finished`

One pair per tool call, with the `action` row beside them.

```json
{"class":"shell","target":"git add README.md && git commit -m \"docs: add Buy link\"","tool":"exec"}
```

```json
{"duration_ms":131,"ok":true,"output_bytes":812,"target":"git add README.md && git commit -m \"docs: add Buy link\"","tool":"exec"}
```

`target` is the file path for a read or an edit, and the first 80 runes of the command for a shell call.
A path too long for that is shortened from the front (`…/shop-buybtn/README.md`), because a crew's files all live under the same long worktree path and truncating from the start names none of them.

Two shapes had to be unwrapped, both measured 2026-09-20 on codex-cli 0.154.
Codex's `exec` tool is handed a JavaScript program, stored in the rollout as a JSON string: `"const r = await tools.exec_command({\"cmd\":\"…\"});"`.
And a Codex edit goes through that same `exec` tool as a script that builds a patch, whose `*** Update File:` header is inside a JavaScript string literal where the newlines are the two characters `\` and `n` rather than line breaks.
An ingest that read either naively records the escaping instead of the command, and every Codex edit as an unnamed "patch"; the `class` of such a call is corrected to `edit` so the story says "edits README.md" rather than "runs: …".

The tool `thinking` is a synthesised action, not a call the harness made.
It fills a stretch where an agent was demonstrably working and no tool call explains it, so a busy window is never unexplained.
Two rules produce one: a gap of at least five seconds inside a turn, which is derived from the transcript and survives a rebuild; and a stretch between a `health.changed` to `busy` and the next change with no action inside it, which is observational.

### `git.committed`

From two sources, keyed by the full sha so a commit both find is one event.

The first is `git log <default>..<branch>` of an open crew - two dots, the commits that belong to this branch alone, the same spelling `mate diff` uses for its commit list.

The second is the crew's own transcript.
A branch is short-lived: `mate merge` deletes it on its way out, so a crew that commits a few seconds before somebody merges can leave no window in which a five-second poll could read `git log` at all.
Measured 2026-09-20 in a live run: six seconds between the commit and the merge, no poll inside it, and the merge could not be proved.
But git echoes the sha back at the crew - `[mate/buybtn 6b8ee07] docs: add …` - and that line is in the crew's transcript for ever, so a shell action that ran `git commit` and got that echo names a commit, which is then read out of git by its sha.

```json
{"branch":"mate/buybtn","files":["README.md"],"sha":"0d2d20d…","short":"0d2d20d","subject":"docs: add Buy link"}
```

### `status.appended`

One per line of `crews/<id>.status`.

```json
{"dated_by":"transcript.shell","line":"needs-decision: what is the checkout page URL?","text":"what is the checkout page URL?","verb":"needs-decision"}
```

### `question.asked`, `question.answered`

`question.asked` accompanies a `needs-decision` status line (and the legacy `blocked:` spelling, which `internal/box` already reads as one).

```json
{"crew":"buybtn","text":"what is the checkout page URL for the Buy button?"}
```

```json
{"by":"mate","crew":"buybtn","question":"what is the checkout page URL for the Buy button?","text":"Use pages/checkout-express.html.","waited_ms":86721}
```

A question is answered by a `sent.log` line addressed to `crew:<id>` at or after it, which is `internal/box`'s inbox rule.
The comparison truncates the question's time to the second, because `sent.log` writes RFC3339 with no fractional part and an answer typed in the same second would otherwise read as older than the question.
A crew that asked twice without being answered is answered in the order it asked.

### `message.sent`, `digest.sent`, `assign.clicked`

One event per line of `sent.log`, and its kind is the channel: a daemon digest is `digest.sent`, a console `[assign]` is `assign.clicked`, and everything else is `message.sent`.
Three kinds rather than one event plus two duplicates, because a line is one thing that happened and a story that counted it twice would answer "how often did the captain hand work over" with double.

```json
{"channel":"pane","crew":"buybtn","from":"mate","marked":false,"text":"Use pages/checkout-express.html.","to":"crew:buybtn"}
```

The channel is read off the line's shape, not off the sentinel: `sent.log` holds the text with `send.Marker` already stripped, both because the daemon writes the line it built and because the Mate's `UserPromptSubmit` hook strips the marker before appending.
`marked` records that mate typed the line itself.
An app line into the Mate's pane whose text is neither a digest nor a resolve is `hook` - the hook's own note that an unmarked prompt ended auto mode, which was typed into no pane at all.

A digest and an `[assign]` line reach `sent.log` **twice**: once written by the sender after the composer cleared, and once by the Mate's own hook when the model read it.
That pair is the evidence the sentinel survived to the model, and docs/mvp.md section 7 names it as the proof to look for - but it is one handover, so the second copy is recorded as the reading of the line rather than as a second handover: its channel is `hook`, its payload carries `"confirms": true`, and it narrates as `the Mate reads it`.
The rule is that an app line to the Mate repeating the previous app line to the Mate verbatim, within ten minutes, is the hook's.
A rejected digest is never written at all - the daemon writes `sent.log` only after a verified send - so a verbatim repeat is always the hook and never a re-offer.
Measured 2026-09-20 in `TestLiveTimelineExplainsTheAcceptance`: without this rule the story told the same `[assign]` twice, one second apart.

### `incident.opened`, `incident.resolved`

From `incidents.log`, whose writer is the observer alone.

```json
{"crew":"k9","incident":"stale","text":"no status line and no pane change for 3m0s; composer empty"}
```

A `resolved` line closes the newest still-open incident of the same `(crew, kind)`, which is the only pairing the file supports.
The daemon files its own `wedged` finding under the crew name `mate`, and that one is about the Mate.

### `merge.done`

From git: the crew is closed `finished` and its branch tip is now an ancestor of the default branch.
Nothing else records a merge - `mate merge` types into no pane, so `sent.log` is silent about it (docs/mvp.md section 7) - which is also why this survives a rebuild.

`mate merge` deletes the branch on its way out, so by the next poll there is no branch to ask about.
The tip that is tested is then the last `git.committed` this ingest recorded for the crew - which is why a crew's commits are read from its transcript as well as from `git log`, so the merge does not depend on a poll having fallen inside the window between the commit and the merge.

```json
{"branch":"mate/buybtn","by":"captain","cause_rule":"crew.handback","crew":"buybtn","into":"main","sha":"df7e0de…","short":"df7e0de"}
```

### `context.compacted`

The moment a harness threw the conversation away and replaced it with a summary, which is the one event that explains a context size falling instead of rising.

```json
{"harness":"claude","trigger":"claude.compact_summary"}
```

The triggers are `claude.compact_summary` (a user record carrying `isCompactSummary`), `claude.compact_boundary` (a `system` record with `subtype: compact_boundary`) and `codex.compacted` (a top-level `compacted` record).

### `health.changed`

Only when the observer's composer classification for an agent changes, never once per poll.

```json
{"agent_present":true,"from":"empty","to":"busy"}
```

Like `mode.changed` this is an observation and nothing on disk keeps it, so a rebuild recovers no health history.
That is why the `thinking` actions that matter are derived from the transcript as well.

It covers crews and not the Mate, because `internal/watch` polls the open crews of a project and not its Mate (docs/mvp.md task 18).
A Mate's busy stretches are therefore explained by its transcript alone, which is the source that survives a rebuild anyway.

### `review.started`

Nothing emits it yet.
The kind is in M5's vocabulary and in the narrate table because the Mate's `reviewing(crew)` scene needs an event to open it; a reader of this document should know it is a name with no producer rather than assume a gap in the ingest.
The scene machine has the edge for it (`mate.reviews`) and, until something emits it, reaches `reviewing` from the command the Mate actually runs to review a crew - `mate diff <project> <crew>`, which is a `tool.called` in its own transcript (section 9.4, `mate.reviews.diff`).

### `ingest.unresolved`

```json
{"harness":"codex","reason":"rollout_not_adopted"}
```

The reasons are `no_session_id`, `transcript_not_found`, `rollout_not_adopted`, `unknown_harness`, `transcript_unobservable` and `no_worktree_recorded`.

## 5. Causality rules

`event.cause_event_id` is filled by these rules and by no others.
Every rule is "the latest qualifying event at or before this one" - never "the nearest event in time", because a chain inferred from two things happening close together is not a chain.
A cause once decided is never re-decided, so a link a reader has seen cannot move.
An event with no cause keeps `NULL`, which is a fact and not a gap to be filled by a guess.

| Event | Cause |
| --- | --- |
| `question.asked` | the crew's own `turn.ended`: the turn it asked in. |
| `digest.sent`, `assign.clicked` | the first `question.asked` the line carries; the crews it names are read out of its text, and the payload of each question event says which crew. |
| `turn.started` of the Mate | the `message.sent` / `digest.sent` / `assign.clicked` addressed to the Mate that reached its composer. |
| `question.answered` | the Mate turn that sent it - the turn holding the `mate send <project> <crew>` the answer came out of, within ten minutes. An answer the captain typed has no such turn and keeps `NULL`. |
| `turn.started` of a crew | the `question.answered` addressed to it, when there is one: a crew takes an answer as an ordinary new prompt. |
| `crew.spawned` | the Mate turn that ran `mate crew spawn` for that crew id, within thirty minutes. |
| `merge.done` | the Mate turn that ran `mate merge <project> <crew>` (`cause_rule: mate.turn.ran.merge`, `by: mate`); failing that, the crew's last `wait-mate` status line before the merge (`cause_rule: crew.handback`, `by: captain`). |

The second merge rule is weaker on purpose and says so in its payload.
A merge run from the Console writes to no file at all, so no event exists that can be pointed at as "the gesture that ran it"; the handback is what the captain acted on, and it is the most the files can say.

`turn.trigger_event_id` is the same fact from the turn table's side: it is the `turn.started` event's cause.

## 6. The narrate phrases

`mate events <project> --narrate` prints one sentence per event, prefixed with the local time to the second.
The Mate and the captain are named with an article because there is one of each; a crew is called by its name, which is what everybody in the story calls it.

| Kind | Sentence |
| --- | --- |
| `mate.started` | `the Mate starts up (claude)` |
| `mate.stopped` | `the Mate shuts down` |
| `crew.spawned` | `the Mate hires k3 for "add a Buy button"` |
| `crew.finished` | `k3 is closed as finished` |
| `crew.failed` | `k3 is closed as failed: trust dialog not recognised` |
| `mode.changed` | `the project switches to auto mode` |
| `turn.started` | `k3 starts a turn` |
| `turn.ended` | `k3 ends a turn after 5.2s (1 tool call(s), 188 output tokens)` |
| `tool.called` (read) | `k3 reads landing.html` |
| `tool.called` (edit) | `k3 edits landing.html` |
| `tool.called` (shell) | `k3 runs: git add README.md && git commit -m "…"` |
| `tool.called` (search) | `k3 searches for checkout in pages/` |
| `tool.finished` | `k3 finishes exec in 131ms`, or `k3's exec fails` |
| `git.committed` | `k3 commits 0d2d20d "docs: add Buy link"` |
| `status.appended` (working) | `k3 reports: verifying the worktree` |
| `status.appended` (wait-mate) | `k3 hands back: ready in branch mate/k3` |
| `status.appended` (needs-decision) | `k3 writes a question into its status file` |
| `message.sent` (captain → Mate) | `the captain tells the Mate: "add a Buy button"` |
| `message.sent` (Mate → captain) | `the Mate reports to the captain: "k3 is ready"` |
| `message.sent` (Mate → crew) | `the Mate sends k3: "A"` |
| `message.sent` (the hook's echo) | `the Mate reads it` |
| `question.asked` | `crew k3 asks the Mate: "pick A or B"` |
| `question.answered` | `the Mate answers k3: "A" (it waited 28.7s)` |
| `digest.sent` | `mate walks a digest into the Mate's office: "digest: 1 item(s) — …"` |
| `assign.clicked` | `the captain hands the Mate a question to resolve: "resolve: k3 asked: …"` |
| `incident.opened` | `the observer flags k3: stale` |
| `incident.resolved` | `the observer clears k3's stale` |
| `review.started` | `the Mate starts reviewing k3` |
| `merge.done` | `the captain merges k3 into main` |
| `context.compacted` | `the Mate's context is compacted` |
| `health.changed` | `k3's composer goes busy` |
| `ingest.unresolved` | `mate cannot find k3's transcript (rollout_not_adopted)` |

## 7. Views

`v_story` is one row per event with the names a reader would say out loud: the actor's and subject's names, the task's name, and the kind of the cause.
`mate events` prints it as JSON lines in a fixed field order, one event per line, so a consumer can diff two runs and `--follow` can stream.

`v_task_ledger` is one row per task: tokens by bucket, the number of turns, how often the crew had to come back and ask, how long it stood at the CEO's door, and the cost when `pricing` has a row for the model.
Cost is `NULL` until then, because a missing price is not a price of zero.

`v_now` is one row per actor: the scene state, since when, who it faces, and what it has spent today.
Its scene columns read `transition`, which the scene projection of section 9 writes on every pass that recorded anything.
An actor with no transition at all reads `NULL` in all four, which is the honest answer for "this actor is in no scene": the captain, mate and the observer are in the story and not in the office.

`docs/dashboard.md` is the JSON contract of the read-only HTTP API over these three views (`mate dashboard`, docs/mvp.md M6), including the five places it has to query a table directly because no view carries the field.

## 8. Known costs

A transcript is re-read whole on every pass rather than tailed from `cursor`.
The reason is `harness.ParseTranscript`: it withholds the trailing message group because a later write may still extend it, so a tail would have to carry the parser's own state across passes, and a resume that carried it wrongly would charge one turn's tokens to another permanently.
The cost is one JSON pass over each live transcript every five seconds, which is a few hundred kilobytes; `cursor` still records how far the parser trusted the file, so a withheld group is visible rather than silent.
A session long enough for that to hurt is the place to measure again.

`mate reindex` empties the derived tables and refills them in one transaction, so a rebuild that fails halfway leaves the timeline it started with.
An ordinary poll commits one transaction per project instead, for the opposite reason: a project whose `.meta` is half-written must not hold back the rest of the workspace.

## 9. The scene

`internal/timeline/scene` projects the events onto the office of docs/mvp.md M5: the Mate is a CEO in its room, a crew is an employee at a desk, and a crew with a question carries the note to the CEO's door and waits there.
The projection writes the `transition` table and is what fills `v_now`.

It is a state machine spelled out as data - a table of edges, one per row below - and nothing moves an actor except an edge.
An event that reaches an actor's machine and matches no edge is recorded as a transition to the state the actor is already in, with `unexplained: <kind>` in `detail`.
That is the rule the whole section exists for: a scene that quietly dropped a fact would read as if the fact had never happened, and the depth test of task 26 fails on any row carrying it.

### 9.1 The states

A parameterised state - `walking_to_ceo(question)`, `leaving(merged)`, `reading(crew)` - is stored as a state plus `detail` plus `target_actor_id`, so the vocabulary stays finite and `SELECT ... WHERE to_state = 'waiting_at_ceo'` can ask how long anybody stood at the door.

| State | Whose | What it means |
| --- | --- | --- |
| `arriving` | crew | hired, walking in, not yet seen working |
| `at_desk_working` | crew | at its desk; the default place for a crew that is doing anything |
| `walking_to_ceo` | crew | on its way to the CEO's office, `detail` `question` or `handback` |
| `waiting_at_ceo` | crew | standing at the door with a question nobody has answered |
| `waiting_review` | crew | standing at the door with finished work nobody has looked at |
| `leaving` | crew | on its way out, `detail` `merged`, `closed` or `failed` |
| `gone` | crew, Mate | out of the building |
| `idle` | Mate | alone in its office |
| `on_phone` | Mate | the captain typed into its pane |
| `receiving_digest` | Mate | mate walked a note in, `detail` `digest` or `assign`; it is on the desk, unread |
| `reading` | Mate | reading the note, `target_actor_id` the crew it is about |
| `deciding` | Mate | working at its desk |
| `answering` | Mate | answering the crew at its door |
| `reviewing` | Mate | reading a crew's work |
| `merging` | Mate | landing a crew's branch itself |
| `asleep` | both | an incident says nothing has moved |
| `blocked` | both | an incident says it cannot be reached |

`walking_to_ceo` and `leaving` are instantaneous: the edge that produces one records both it and the state after it with the same `at`, so a renderer can animate the walk while `waiting_at_ceo`'s duration is still measured from the moment the waiting began.

`asleep` and `blocked` are a pause and not a destination.
Entering one remembers where the actor was, and `incident.resolved` puts it back there rather than guessing.
An incident is also a report and not a cage: a crew the observer called asleep and which then writes a question into its status file is demonstrably awake and standing at the door, so the ordinary edges apply from `asleep` and `blocked` too, and the `resolved` line that follows finds the crew already moved and moves nobody.

### 9.2 Which machine an event reaches

An event is applied to the machine of its actor and to the machine of its subject.
`crew.spawned` is something the Mate did to a crew, and it is the crew that walks in; `question.asked` is a crew's, and its subject is the Mate.
The captain, mate and the observer have no machine: nobody draws the captain.

Two events recorded at the same instant are applied in `(at, rank, id)` order, and only one kind needs a rank: `mate merge` closes the crew it merged, so `merge.done` and `crew.finished` share a timestamp, and the merge is applied first so a crew that landed its branch leaves `merged` rather than merely `closed`.

### 9.3 The crew's table

Rows are tried in order and the first match wins.
"anywhere" is the last row for a kind and catches what the rows above it did not; "in the building" is every state but `gone`; "at work" is every state but `gone`, `asleep` and `blocked`.

| id | From | Event | When | To | Facing, or why it moves nobody |
| --- | --- | --- | --- | --- | --- |
| `crew.hired` | in the building | `crew.spawned` | | `arriving` | the Mate |
| `crew.hired.gone` | anywhere | `crew.spawned` | | - | |
| `crew.merged` | in the building | `merge.done` | | `leaving(merged)` → `gone` | |
| `crew.merged.gone` | anywhere | `merge.done` | | - | |
| `crew.closed` | in the building | `crew.finished` | | `leaving(closed)` → `gone` | |
| `crew.closed.gone` | anywhere | `crew.finished` | | - | |
| `crew.failed` | in the building | `crew.failed` | | `leaving(failed)` → `gone` | |
| `crew.failed.gone` | anywhere | `crew.failed` | | - | |
| `crew.asleep` | at work | `incident.opened` | `stale` | `asleep(stale)` | |
| `crew.blocked` | at work | `incident.opened` | `runtime_lost`, `wedged` | `blocked(<kind>)` | |
| `crew.incident.other` | anywhere | `incident.opened` | | - | `budget` is an inbox item and not a state (docs/mvp.md section 4b) |
| `crew.awake` | `asleep`, `blocked` | `incident.resolved` | | back where the incident found it | |
| `crew.awake.other` | anywhere | `incident.resolved` | | - | |
| `crew.asks` | in the building | `question.asked` | | `walking_to_ceo(question)` → `waiting_at_ceo` | the Mate |
| `crew.asks.away` | anywhere | `question.asked` | | - | |
| `crew.answered` | in the building | `question.answered` | | `at_desk_working` | |
| `crew.answered.away` | anywhere | `question.answered` | | - | |
| `crew.handback` | in the building, not `waiting_review` | `status.appended` | `verb: wait-mate` | `walking_to_ceo(handback)` → `waiting_review` | the Mate |
| `crew.at.desk` | `arriving`, unknown | `status.appended` | | `at_desk_working` | |
| `crew.status` | anywhere | `status.appended` | | - | a `working:` line is the crew saying what it is doing, not moving |
| `crew.back.to.work` | `waiting_review` | `message.sent` | addressed to it | `at_desk_working` | the Mate sent it back with something to change |
| `crew.message` | anywhere | `message.sent` | | - | |
| `crew.turn.first` | `arriving`, unknown | `turn.started` | | `at_desk_working` | |
| `crew.turn` | anywhere | `turn.started` | | - | a crew waiting at the door still burns turns; the turn does not fetch it back |
| `crew.tool.first` | `arriving`, unknown | `tool.called` | | `at_desk_working` | |
| `crew.tool` | anywhere | `tool.called` | | - | |
| `crew.commit.first` | `arriving`, unknown | `git.committed` | | `at_desk_working` | |
| `crew.commit` | anywhere | `git.committed` | | - | |
| `crew.turn.ended` | anywhere | `turn.ended` | | - | |
| `crew.tool.finished` | anywhere | `tool.finished` | | - | |
| `crew.compacted` | anywhere | `context.compacted` | | - | its memory, not its position |
| `crew.health` | anywhere | `health.changed` | | - | an observation of a pane, which a rebuild cannot read back |
| `crew.unresolved` | anywhere | `ingest.unresolved` | | - | mate's problem, not a move |
| `crew.review` | anywhere | `review.started` | | - | being reviewed is where it already is |
| `crew.mate.started` | anywhere | `mate.started` | | - | |
| `crew.mate.stopped` | anywhere | `mate.stopped` | | - | |
| `crew.mode` | anywhere | `mode.changed` | | - | |
| `crew.digest` | anywhere | `digest.sent` | | - | addressed to the Mate |
| `crew.assign` | anywhere | `assign.clicked` | | - | addressed to the Mate |

### 9.4 The Mate's table

| id | From | Event | When | To | Facing, or why it moves nobody |
| --- | --- | --- | --- | --- | --- |
| `mate.opens` | anywhere | `mate.started` | | `idle` | |
| `mate.closes` | anywhere | `mate.stopped` | | `gone` | |
| `mate.asleep` | at work | `incident.opened` | `stale` | `asleep(stale)` | |
| `mate.blocked` | at work | `incident.opened` | `runtime_lost`, `wedged` | `blocked(<kind>)` | the daemon files `wedged` against the Mate (docs/mvp.md task 19) |
| `mate.incident.other` | anywhere | `incident.opened` | | - | |
| `mate.awake` | `asleep`, `blocked` | `incident.resolved` | | back where the incident found it | |
| `mate.awake.other` | anywhere | `incident.resolved` | | - | |
| `mate.phone` | in the building | `message.sent` | from the captain, channel `pane`, to the Mate | `on_phone` | the captain |
| `mate.reads.echo` | `receiving_digest` | `message.sent` | `confirms: true`, to the Mate | `reading(crew)` | the crew the note is about |
| `mate.message` | anywhere | `message.sent` | | - | its own line out, or an echo of a line already counted |
| `mate.digest` | in the building | `digest.sent` | | `receiving_digest(digest)` | the crew the digest is about |
| `mate.digest.away` | anywhere | `digest.sent` | | - | |
| `mate.assign` | in the building | `assign.clicked` | | `receiving_digest(assign)` | the crew the note is about |
| `mate.assign.away` | anywhere | `assign.clicked` | | - | |
| `mate.reads` | `receiving_digest` | `turn.started` | | `reading(crew)` → `deciding` | the crew the note is about |
| `mate.turn` | in the building | `turn.started` | | `deciding` | whoever it was already facing |
| `mate.turn.away` | anywhere | `turn.started` | | - | |
| `mate.turn.ended` | `reading`, `deciding`, `answering`, `reviewing`, `merging` | `turn.ended` | | `idle` | |
| `mate.turn.ended.holding` | anywhere | `turn.ended` | | - | a note that arrived mid-turn is still unread when the turn ends, and the phone is still ringing |
| `mate.answers` | in the building | `question.answered` | the Mate sent it | `answering(crew)` → `idle` | the crew |
| `mate.answers.other` | anywhere | `question.answered` | | - | the captain answered it, which is the captain's doing |
| `mate.reviews` | in the building | `review.started` | | `reviewing(crew)` | the crew |
| `mate.reviews.away` | anywhere | `review.started` | | - | |
| `mate.reviews.diff` | in the building | `tool.called` | the command is `mate diff <project> <crew>` | `reviewing(crew)` | the crew |
| `mate.tool` | anywhere | `tool.called` | | - | |
| `mate.merges` | in the building | `merge.done` | `by: mate` | `merging(crew)` → `idle` | the crew |
| `mate.merges.captain` | anywhere | `merge.done` | | - | the captain merged from the console: the crew leaves, the Mate did nothing |
| `mate.hires` | anywhere | `crew.spawned` | | - | hiring happens inside a turn it is already in |
| `mate.status` | anywhere | `status.appended` | | - | |
| `mate.asked` | anywhere | `question.asked` | | - | a crew at the door does not move the Mate; the note reaching its pane does |
| `mate.crew.finished` | anywhere | `crew.finished` | | - | |
| `mate.crew.failed` | anywhere | `crew.failed` | | - | |
| `mate.commit` | anywhere | `git.committed` | | - | |
| `mate.tool.finished` | anywhere | `tool.finished` | | - | |
| `mate.compacted` | anywhere | `context.compacted` | | - | |
| `mate.health` | anywhere | `health.changed` | | - | |
| `mate.unresolved` | anywhere | `ingest.unresolved` | | - | |
| `mate.mode` | anywhere | `mode.changed` | | - | |

Four of these edges deserve their reason spelled out.

`mate.reads` fires only from `receiving_digest`, and it does not look at what caused the turn.
Both halves of that are measurements rather than taste.
The causality rule of section 5 gives *every* Mate turn the last line that reached its composer as its cause, so a Mate that works through six model calls after one `[assign]` would read the same note six times if the cause were the trigger; the note is instead picked up once, by the first turn that starts after it landed on the desk.
And the line the cause rule finds is usually not the `[assign]` at all: a Claude Mate's `UserPromptSubmit` hook writes the same line back to `sent.log` when the model reads it, and that echo is the newest line to the Mate when the turn starts.
Measured 2026-09-20 in `TestLiveTimelineExplainsTheAcceptance`: the `[assign]` and its echo both at 16:47:57, the turn at 16:48:05, and a `mate.reads` that demanded an `[assign]` as the cause never fired at all, so the Mate never read anything in a run where it plainly did.

`mate.reads.echo` is that echo used for what it is.
The hook's line is the one fact in `sent.log` that proves the model read what mate typed (section 4), and "the Mate reads it" is already how the event narrates, so on a Claude Mate the note leaves the desk at the echo and the turn that follows finds the Mate already reading.
A Codex Mate has no such hook, and `mate.reads` picks the note up at its next turn instead.

`mate.turn.ended` deliberately does not fire from `receiving_digest` or `on_phone`.
A line that arrives while the Mate is mid-turn has not been read when that turn ends, and a scene that returned the Mate to `idle` would lose the fact that something is waiting on its desk.

`mate.reviews.diff` is the one edge that reads a command rather than an event kind.
`review.started` has no producer (section 4), and the thing a Mate actually does to review a crew is run `mate diff <project> <crew>`, which is a `tool.called` in its own transcript and survives a rebuild.
The `review.started` edge stays in the table for the day something emits it.

### 9.5 The scene phrases

`mate events <project> --scene --narrate` prints one sentence per transition, prefixed with the local time to the second, the same shape `--narrate` prints an event in.

| To | Sentence |
| --- | --- |
| `arriving` | `k3 arrives at the office` |
| `at_desk_working` (from `arriving`) | `k3 sits down at its desk` |
| `at_desk_working` (from `waiting_at_ceo`) | `k3 goes back to its desk` |
| `at_desk_working` (from `waiting_review`) | `k3 goes back to its desk with more to do` |
| `walking_to_ceo(question)` | `k3 walks to the CEO's office with a question` |
| `walking_to_ceo(handback)` | `k3 walks to the CEO's office with the finished work` |
| `waiting_at_ceo` | `k3 waits at the CEO's door` |
| `waiting_review` | `k3 waits for the Mate to review the work` |
| `leaving(merged)` | `k3 leaves, merged` |
| `leaving(closed)` | `k3 leaves, its task closed` |
| `leaving(failed)` | `k3 leaves, its task failed` |
| `gone` | `k3 is out of the building`, or `the Mate shuts the office` |
| `asleep` | `k3 falls asleep (stale)` |
| `blocked` | `k3 cannot be reached (runtime_lost)` |
| out of `asleep` | `k3 wakes up, back waiting at the CEO's door` |
| out of `blocked` | `k3 is back in touch, at its desk` |
| `idle` (first) | `the Mate takes the office` |
| `idle` | `the Mate is alone in its office again` |
| `on_phone` | `the captain calls the Mate` |
| `receiving_digest(digest)` | `mate walks a digest into the Mate's office` |
| `receiving_digest(assign)` | `mate walks the captain's note into the Mate's office` |
| `reading` | `the Mate reads k3's note` |
| `deciding` | `the Mate thinks it over` |
| `answering` | `the Mate answers k3` |
| `reviewing` | `the Mate reviews k3's work` |
| `merging` | `the Mate lands k3's work` |
| unexplained | `k3: nothing in the scene explains <kind>` |

The snapshot is the same vocabulary in the present tense, one line per actor, with the moment the actor arrived in that state: `now      k3 is waiting at the CEO's door (since 10:44:33)`.
The captain, mate and the observer have no scene, so the narrated snapshot leaves them out; the JSON snapshot still carries their rows, with a `state` of `""`, because "this actor is in no scene" is an answer and not a gap.

### 9.6 `mate events <project> --scene`

`--scene` prints the `v_now` snapshot, one JSON line per actor, and stops.
`--since <RFC3339|event id>` adds the transitions from that point, oldest first; `--since 0` is the whole history.
`--follow` then keeps printing transitions as they are recorded, and `--narrate` turns both halves into the sentences above.

The transition's id is its cursor: `<project>|<at>|<n>`, where `at` is the fixed-width timestamp `internal/db` stores and `n` counts the transitions inside that instant.
It therefore sorts lexicographically into story order, which is what lets `v_now` break a tie inside one instant with `ORDER BY at DESC, id DESC` and read the state the machine actually ended in, and what lets `--follow` poll `id > <last>` instead of scanning.

### 9.7 How the projection runs

It runs inside `Ingester.Ingest` and `Reindex`, in the same transaction as the events of that pass and after the causality rules, because the crew a note walked into the office is about is found by following `cause_event_id`.
The events and the transitions therefore commit together: a reader never sees a story the scene has not caught up with.

The projection is recomputed whole, every time, and the project's `transition` rows are replaced.
It is not incremental on purpose.
An ingest pass can insert an event whose time is older than events it has already written - a commit read out of a transcript, a status line dated by the shell command that wrote it - and a machine fed that event out of order would be wrong from then on.
Recomputing also makes the table a function of the events by construction: two rebuilds of the same files produce the same rows byte for byte, ids included.

The cost is one ordered read of the project's events per pass that recorded anything, which is the same read `mate events` does; a pass that inserted no event skips the projection entirely.
A workspace whose event count makes that read expensive is the place to measure again, and the answer then is a projection that resumes from the oldest event the pass touched rather than from the first.

## 10. Economics (schema v2, task 27)

### `pricing.yaml`

`store.Init` seeds `.mate/pricing.yaml` once, at `PricingModel` rows for every model id mate has actually seen in a transcript, every price at 0 and a comment that the captain owns the numbers.
`timeline.Ingester.ingestPricing` loads the file and upserts it into the `pricing` table on every pass, before any project's own pass, because `v_task_ledger` and `v_now` read `pricing` by model on every query and pricing has no project of its own to be ordered by.
A missing or empty file is not an error - every model stays unpriced, which the views already render as a `NULL` cost rather than a free one - and `mate reindex` clears `pricing` along with everything else it rebuilds, so a model the captain removed from the file does not linger as a stale row.

A price of exactly 0 across all four columns does not count as priced: `v_task_ledger.cost` and `mate usage`'s cost columns only join a `pricing` row into the sum when at least one of `input_per_m`, `cache_read_per_m`, `cache_write_per_m`, `output_per_m` is greater than zero.
Without that guard the seeded placeholder rows would make every crew's cost `$0.00` from the moment `store.Init` runs, which is the exact falsehood "cost is NULL until priced" exists to prevent.

### `context_window` and `context_pct`

`pricing.context_window` is a technical fact, not a price, so the seed fills in a real value for each seeded model rather than leaving it at 0.
`v_task_ledger.context_pct` and `v_now.context_pct` are both `100.0 * <the actor's most recent turn's context_tokens_after> / <that turn's model's context_window>`, `NULL` when the model has no known window.
`v_now`'s scene columns (`state`, `since`, `target_actor_id`, `detail`) are unchanged from schema v1 and read by task 26's projection; `context_pct` is added at the end of the column list so nothing that reads those columns by name has to change.

### Codex's `input_tokens` is cache-inclusive; Claude's is not

Measured 2026-09-20 on a real rollout (`TestLiveUsageMatchesTheHarness`): the last `token_count` record is `{"input_tokens":232424,"cached_input_tokens":209152,"cache_write_input_tokens":0,"output_tokens":1544,"reasoning_output_tokens":351,"total_tokens":233968}`, and `232424 + 1544 = 233968` exactly - Codex's own `input_tokens` already counts every cached token, and `total_tokens` is simply input plus output. `cached_input_tokens` is a descriptive subset, not an addend.

Claude's turn is the opposite: section 4's `turn.started` example (`input_tokens: 32, cache_read_tokens: 57690, cache_write_tokens: 739`) sums to exactly `context_tokens_after: 58461` - all four buckets are disjoint, and none is a subset of another.

Before this was noticed, `codexTurns` stored Codex's raw `input_tokens` delta as `turn.input_tokens` unchanged, so every sum across the four buckets (`v_task_ledger`'s token columns and cost, `v_now.tokens_today`, the budget check, `mate usage`) double-counted the cached portion of a Codex crew's usage, and would have double-billed it too for a captain who priced both `input_per_m` and `cache_read_per_m`.
The fix is in `codexTurns` itself: it subtracts the cache-read delta from the input delta before either is stored, so `turn.input_tokens` means "billed at the input rate, not a cache rate" for both harnesses, and every view built on top of `turn` sums correctly without asking which harness a turn came from.
`context_tokens_after` is untouched by this - it is derived straight from the raw `last_token_usage` block, not from the corrected `turn.input_tokens`.

### `mate usage`

`mate usage <project>` prints `v_task_ledger` as a table, the Mate's own row first (computed the same way but read straight off `turn` rather than `task`, since a Mate's turns belong to the project and not to any one task), then one row per task, oldest spawn first, then a totals footer.
`mate usage <project> <crew>` instead prints that crew's own `turn` rows, one per model call, in the shape `mate events --narrate` calls "ends a turn".
`mate usage <project> --top <n>` keeps the Mate's row and the n largest tasks by TOTAL, largest first; the totals footer still sums every task, and a last line says how many were left out.
`mate usage <project> <crew> --why` prints the dashboard Task page's `performance` projection (docs/dashboard.md) as one bounded page of text: token buckets, calls, prompts and peak context, work by kind, the loop summary, the first six findings with one evidence line and their `review` hint, the instruction files the harness loaded, and the three costliest prompts, then every `freshness.missing` line under `not measured`.
It reads through `dashboard.Server.CrewPerformance`, the same code path as `/api/projects/<p>/tasks/<crew>`, and measures nothing itself.
Both flags exist for the Mate's `token-review` skill, whose reader pays for every line it reads.
Every number is humanised (`query.HumanizeTokens`, `query.HumanizeCost`: `96.3k`, `$0.12`), and a `NULL` cost or context percentage prints as `?`, never as `0` or `0%` - the same rule the console's TOKENS column and `mate state`'s `tokens:`/`ctx:` suffix follow, all three built on the same two functions so a number reads the same everywhere it appears.

### The console's TOKENS column and `mate state`

`query.CrewNode.Tokens` and `query.MateNode.Tokens` are `Field[TokenValue]`, filled the same way `Health` is: `query.Load` reads only `.mate/`'s files and leaves them `Absent`, and the Console's wiring (`cmd/mate/console_watch.go`'s `withTokens`, beside `withCrewHealth`) opens `.mate/mate.db` read-only afterwards and fills them from `v_task_ledger` and `v_now`.
`internal/ui/console/list.go` draws a TOKENS column on the Mate row and every Crew row, at the same width breakpoint `colUpdated` already uses (`wideList`), so a narrow pane drops it before it drops anything a reader is more likely to need.
`mate state <project> <crew>` appends ` · tokens: 96k` and, once the crew's last turn has a priced context window, ` · ctx: 62%` after `crewstate.Result.Line()` - built in `cmd/mate/state.go`, not in `internal/crewstate`, because that package is a deliberate leaf that imports nothing else in this module.

### Budget (`project.yaml`'s `budget:` block)

`store.BudgetConfig` is optional and per-dimension optional: `crew_tokens`, `crew_usd` (checked per crew) and `project_usd` (checked against every crew of the project summed, filed under the crew name `mate` since no one crew is at fault).
`internal/timeline.Ingester.CheckBudgets` runs at the end of every observer poll, after that poll's `Ingest` has written the turns a crossing would be based on, and is wired in as `watch.Deps.Budget` - a seam, the same shape as `Ingest`, so `internal/watch` still imports nothing that can read `pricing.yaml` or open the database itself.
It opens a `budget` incident (`box.IncidentBudget`) on a crossing crew exactly once: a crew already carrying an open one is left alone even if it has since spent more, because the incident is a fact about spend that already happened, not a condition that clears - it is never resolved.
The incident's text is `"<humanised total> tokens of <humanised limit> tokens"` or `"<humanised cost> of <humanised limit>"`, which a digest's fourth item shape reads verbatim as `<crew> over budget: <text>`.

Per the 2026-09-20 decision (mvp.md section 4b), a `budget` incident is deliberately excluded from what makes a crew `blocked`: `box.BlockingIncidents` narrows `box.OpenIncidents` to `stale` and `runtime_lost` only, and every caller that used to feed `OpenIncidents` into `crewstate.Declaration.OpenIncident` (`query.CrewStateOf`, `spawn.crew_stop`'s open-crew resolution, the Console's session metadata, `mate state`) now goes through `BlockingIncidents` instead.
`box.OpenIncidents` itself is unchanged and still puts a `budget` incident in the inbox, where the phrase table (`internal/ui/console/box.go`'s `boxNeedPhrase`) reads it as `over budget`.
`internal/autopilot.Gather` gives a `budget` incident its own `ItemBudget` kind rather than `ItemBlocked`, so the digest vocabulary itself cannot claim a budget crossing changed the crew's state; `Line`'s fourth item shape is `<crew> over budget: <total> of <limit>`, spelled once in `internal/autopilot/digest.go`'s `itemText` and in the Mate manual's section 10.
