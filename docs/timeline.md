# Timeline

Schema version: **1**.

This is the contract of `.matev2/matev2.db` and of `internal/timeline`.
The database is derived: every row here is read out of `crews/<id>.status`, `sent.log`, `incidents.log`, the `.meta` files, the harness transcripts or git, and `matev2 reindex <workspace>` rebuilds all of it from those sources.
Losing the file loses no work (docs/mvp.md decision 6).
Two rebuilds of the same sources produce byte-identical `v_story` output, `event.id` included, which is what lets a reader quote an id.

The observer inside the console is the single writer: it calls `Ingest` at the end of every poll, and `internal/db` holds an advisory lock on `matev2.db.lock` so a second writer is refused rather than interleaved.
Readers take no lock at all.

## 1. What the schema adds to the spec

The tables are the ones docs/mvp.md M5 lists, with one addition and one column worth naming.

`event.dedup` is the natural key of a fact and is `UNIQUE`.
It exists because a transcript's trailing message group may still grow, so the ingest re-reads it on every pass; without a key the same fact would become a second row with a second id, and a dashboard following `event.id > ?` would replay history as news.

`turn.harness_turn_ref` carries the harness's own larger unit - Claude's `promptId`, Codex's `task_started` turn id - beside the per-call turn.
A turn row is one model call in both harnesses; the harness turn is one prompt and everything it caused, and it is what groups a crew's calls into "the work the Mate's answer set off".

## 2. Locator rules

An agent's transcript is found by the first rule that answers, and the rule that fired is recorded on the `session` row's path and in the `ingest.unresolved` payload when none does.

1. `meta.transcript` - `.meta` already names the file.
   A Mate running Claude Code has it from the Stop hook's `transcript_path` (docs/mvp.md decision 9), which is exact.
2. `claude.projects` - `session_id=` plus Claude's own naming rule, `~/.claude/projects/<slug of cwd>/<session-id>.jsonl`.
   This is what finds a Claude agent before its first Stop hook has run.
3. `herdr.agent_session` - the runtime's `agent_session.value`, which for Codex is the rollout's session uuid, and Codex puts that uuid in the rollout's file name.
   Measured 2026-09-20 on Herdr 0.8.2: `agent_session` is present and correct for every Codex agent and **null for every Claude agent**, so this is a Codex rule and only a Codex rule.
4. `codex.adopt` - `harness.AdoptCodexRollout` over the rollout directory, matching on the canonical cwd and a `session_meta` timestamp at or after the recorded launch.
   This is the fallback for a crew whose agent Herdr no longer has, and it is deliberately conservative: two crews launched in the same worktree are genuinely ambiguous, and an ambiguous match is no match.

A crew's binding never moves - a crew is spawned once and is never resumed (docs/mvp.md section 4b) - so once one of those rules has answered, a later pass reuses the path the `session` row already carries (`session.recorded`) instead of asking the runtime again.
Without that, every Codex crew would cost two more `herdr` calls every five seconds on top of the observer's three.
A rebuild starts without the cache and runs the rules again.

A transcript nothing finds is an `ingest.unresolved` event, written once per `(actor, reason)`.
It is an event rather than a log line because the consequence is otherwise invisible: every turn, token and tool call of that agent is simply missing, and a timeline with a hole that says nothing about itself is worse than one with a hole that does.

## 3. How a status line gets a time

A `crews/<id>.status` line carries no timestamp of its own.
Two rules, in order, and the `status.appended` payload records which one fired in `dated_by`.

1. `transcript.shell` - the crew wrote the line with `echo "state: one line" >> $MATEV2_STATUS`, and that shell command is in the crew's own transcript with a timestamp on it, so the line is dated by the earliest action whose command contains it verbatim.
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
{"branch":"matev2/buybtn","crew":"buybtn","harness":"codex","task":"Add a Buy button to README.md","worktree":".worktrees/shop-buybtn"}
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

The first is `git log <default>..<branch>` of an open crew - two dots, the commits that belong to this branch alone, the same spelling `matev2 diff` uses for its commit list.

The second is the crew's own transcript.
A branch is short-lived: `matev2 merge` deletes it on its way out, so a crew that commits a few seconds before somebody merges can leave no window in which a five-second poll could read `git log` at all.
Measured 2026-09-20 in a live run: six seconds between the commit and the merge, no poll inside it, and the merge could not be proved.
But git echoes the sha back at the crew - `[matev2/buybtn 6b8ee07] docs: add …` - and that line is in the crew's transcript for ever, so a shell action that ran `git commit` and got that echo names a commit, which is then read out of git by its sha.

```json
{"branch":"matev2/buybtn","files":["README.md"],"sha":"0d2d20d…","short":"0d2d20d","subject":"docs: add Buy link"}
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
`marked` records that matev2 typed the line itself.
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
Nothing else records a merge - `matev2 merge` types into no pane, so `sent.log` is silent about it (docs/mvp.md section 7) - which is also why this survives a rebuild.

`matev2 merge` deletes the branch on its way out, so by the next poll there is no branch to ask about.
The tip that is tested is then the last `git.committed` this ingest recorded for the crew - which is why a crew's commits are read from its transcript as well as from `git log`, so the merge does not depend on a poll having fallen inside the window between the commit and the merge.

```json
{"branch":"matev2/buybtn","by":"captain","cause_rule":"crew.handback","crew":"buybtn","into":"main","sha":"df7e0de…","short":"df7e0de"}
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
The kind is in M5's vocabulary and in the narrate table because the Mate's `reviewing(crew)` scene is task 26's, and the event that opens it is the one the scene machine will need; a reader of this document should know it is a name with no producer rather than assume a gap in the ingest.

### `ingest.unresolved`

```json
{"harness":"codex","reason":"rollout_not_adopted"}
```

The reasons are `no_session_id`, `transcript_not_found`, `rollout_not_adopted`, `unknown_harness` and `no_worktree_recorded`.

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
| `question.answered` | the Mate turn that sent it - the turn holding the `matev2 send <project> <crew>` the answer came out of, within ten minutes. An answer the captain typed has no such turn and keeps `NULL`. |
| `turn.started` of a crew | the `question.answered` addressed to it, when there is one: a crew takes an answer as an ordinary new prompt. |
| `crew.spawned` | the Mate turn that ran `matev2 crew spawn` for that crew id, within thirty minutes. |
| `merge.done` | the Mate turn that ran `matev2 merge <project> <crew>` (`cause_rule: mate.turn.ran.merge`, `by: mate`); failing that, the crew's last `wait-mate` status line before the merge (`cause_rule: crew.handback`, `by: captain`). |

The second merge rule is weaker on purpose and says so in its payload.
A merge run from the Console writes to no file at all, so no event exists that can be pointed at as "the gesture that ran it"; the handback is what the captain acted on, and it is the most the files can say.

`turn.trigger_event_id` is the same fact from the turn table's side: it is the `turn.started` event's cause.

## 6. The narrate phrases

`matev2 events <project> --narrate` prints one sentence per event, prefixed with the local time to the second.
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
| `status.appended` (wait-mate) | `k3 hands back: ready in branch matev2/k3` |
| `status.appended` (needs-decision) | `k3 writes a question into its status file` |
| `message.sent` (captain → Mate) | `the captain tells the Mate: "add a Buy button"` |
| `message.sent` (Mate → captain) | `the Mate reports to the captain: "k3 is ready"` |
| `message.sent` (Mate → crew) | `the Mate sends k3: "A"` |
| `message.sent` (the hook's echo) | `the Mate reads it` |
| `question.asked` | `crew k3 asks the Mate: "pick A or B"` |
| `question.answered` | `the Mate answers k3: "A" (it waited 28.7s)` |
| `digest.sent` | `matev2 walks a digest into the Mate's office: "digest: 1 item(s) — …"` |
| `assign.clicked` | `the captain hands the Mate a question to resolve: "resolve: k3 asked: …"` |
| `incident.opened` | `the observer flags k3: stale` |
| `incident.resolved` | `the observer clears k3's stale` |
| `review.started` | `the Mate starts reviewing k3` |
| `merge.done` | `the captain merges k3 into main` |
| `context.compacted` | `the Mate's context is compacted` |
| `health.changed` | `k3's composer goes busy` |
| `ingest.unresolved` | `matev2 cannot find k3's transcript (rollout_not_adopted)` |

## 7. Views

`v_story` is one row per event with the names a reader would say out loud: the actor's and subject's names, the task's name, and the kind of the cause.
`matev2 events` prints it as JSON lines in a fixed field order, one event per line, so a consumer can diff two runs and `--follow` can stream.

`v_task_ledger` is one row per task: tokens by bucket, the number of turns, how often the crew had to come back and ask, how long it stood at the CEO's door, and the cost when `pricing` has a row for the model.
Cost is `NULL` until then, because a missing price is not a price of zero.

`v_now` is one row per actor: the scene state, since when, who it faces, and what it has spent today.
Its scene columns read `transition`, which task 26's projection writes; until then `state`, `since`, `target_actor_id` and `detail` are `NULL`, which is the honest answer for "no projection has run".

## 8. Known costs

A transcript is re-read whole on every pass rather than tailed from `cursor`.
The reason is `harness.ParseTranscript`: it withholds the trailing message group because a later write may still extend it, so a tail would have to carry the parser's own state across passes, and a resume that carried it wrongly would charge one turn's tokens to another permanently.
The cost is one JSON pass over each live transcript every five seconds, which is a few hundred kilobytes; `cursor` still records how far the parser trusted the file, so a withheld group is visible rather than silent.
A session long enough for that to hurt is the place to measure again.

`matev2 reindex` empties the derived tables and refills them in one transaction, so a rebuild that fails halfway leaves the timeline it started with.
An ordinary poll commits one transaction per project instead, for the opposite reason: a project whose `.meta` is half-written must not hold back the rest of the workspace.
