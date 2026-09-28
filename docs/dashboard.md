# Dashboard

This is the contract of `mate dashboard` and of `internal/dashboard` (docs/mvp.md M6, tasks 28 and 29).
Task 28 owns the server and this document; task 29 builds the three-tier UI against it.

The dashboard is read-only and local.
It opens `.mate/mate.db` with `db.OpenRead`, which takes no lock, so it runs beside an open console rather than instead of it.
There is no endpoint that writes: every action stays in the console TUI, which is where a reader who can act already is.

```
mate dashboard [<workspace>] [--addr 127.0.0.1:7777] [--open] [--allow-remote]
```

`--addr` must be a loopback address unless `--allow-remote` is passed.
A workspace's timeline quotes every line the captain typed and names every path a crew touched, so putting it on a routable address has to be a decision rather than a default.
`--open` runs the platform opener (`open`, `xdg-open`, `rundll32`) on the URL; a machine with none of them says so and keeps serving.
The server prints its URL and runs until Ctrl+C.

## 1. Conventions

Every response carries two fields before anything else.

```json
{"generated_at": "2026-09-19T10:47:00.000000000Z", "last_event_id": 412}
```

`last_event_id` is `MAX(event.id)` over the whole workspace: the cursor for newly recorded events.
It is the number to hand back to `/api/events?since=`, so a page always polls from the exact id the data it is showing was computed at.

`generated_at` is when the snapshot behind the bytes was computed, not when they were served.
Workspace, project, model-call and diff responses are cached per `last_event_id`: while the id stands still they return the bytes already built, and when it moves every cached answer is dropped at once.
Crew task and Mate conversation responses are rebuilt on each request: running execution clocks and recording-heartbeat age can change without a new event. Usage is never extrapolated between harness records. The events endpoint is also uncached.

A timestamp is the database's own string: RFC3339 with nanoseconds, in UTC (`db.TimeFormat`).
It is passed through and never reformatted, so a value on the page and a row in the database compare byte for byte.

A nullable number is `null`, never `0`.
A cost with no `pricing` row and a context percentage with no known context window are unknown, and docs/timeline.md's "a missing price is not a price of zero" is the same rule `mate usage` prints as `?`.

Token buckets are always this object.

```json
{"input": 32, "cache_read": 57690, "cache_write": 739, "output": 911, "thinking": 73, "total": 59372}
```

`total` is `input + cache_read + cache_write + output` and excludes `thinking`, which is exactly what `mate usage`'s TOTAL column and `v_now.tokens_today` sum.

A `ref` is a transcript locator - the file a fact was read out of and the byte offset inside it - and it is what makes every number on the page traceable back to the harness's own record.

```json
{"path": "/Users/x/.codex/sessions/2026/09/19/rollout-01a0b944.jsonl", "offset": 18422}
```

Errors are JSON with the same envelope.
An unknown project or crew is `404` and the reason names what was missing and what there is instead.

```json
{"generated_at": "…", "last_event_id": 412, "error": "no project \"nosuch\" in this workspace; it has shop, site"}
```

There is no `Access-Control-Allow-Origin` header of any kind.
The only page that reads this API is the one this server serves.

## 2. `GET /api/workspace`

Tier 1: one card per registered project.

`mate.harness` and `mate.running` come from the `actor` row (`harness`, `first_seen`, `gone_at`), which `v_now` has no column for; running means started and not stopped.
`mate.state`, `mate.since`, `mate.tokens_today` and `mate.context_pct` are `v_now`.
`crews_by_state` counts this project's crew actors by their `v_now` scene state, and a crew no projection has placed counts under `"unknown"` rather than being dropped.
`inbox_waiting` is the length of `box.Inbox` through `query.LoadBox`, the same loader and the same number the console's rail header shows.
`mode` is `store.Auto`: `"auto"` or `"manual"`.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "root": "/Users/x/work",
  "projects": [
    {
      "name": "shop",
      "mode": "manual",
      "mate": {
        "harness": "claude",
        "running": true,
        "state": "idle",
        "since": "2026-09-19T10:46:40.000000000Z",
        "tokens_today": 57683,
        "context_pct": 29.2
      },
      "crews_by_state": {"waiting_review": 1},
      "inbox_waiting": 1
    }
  ]
}
```

A project whose box could not be read still gets a card, with `"error"` saying why: one broken project must not blank the workspace.

## 3. `GET /api/projects/{project}`

Tier 2: the Mate above, the task table below, the inbox beside it.

`mate` is the card's block plus what the Mate has spent and what it last did.
There is no `task` row for a Mate - its turns belong to the project and not to any one task (docs/timeline.md) - so `turns`, `tokens` and `cost` are computed from `turn` directly, with the same all-zeroes-is-not-a-price guard `v_task_ledger` uses.
This is the same SQL `cmd/mate/usage.go`'s `mateLedgerRow` runs, and the unit tests compare the two number for number.
`last_turn` is `null` for a Mate that has taken no model call yet, and otherwise the full model-call object of section 4.
The project page links to `GET /api/projects/{project}/mate`. Its `exchanges`
are prompt-level interactions: one captain message, crew digest, or app prompt
and the Mate's response. Calls are grouped by `(session_id,
harness_turn_ref)`, so a prompt spanning several model calls appears once.
An inbound message with no call still appears with `model_calls: 0` and an
empty response. The prompt and response come from `sent.log` through the
`message` table; hook echoes are excluded as duplicate prompts. Selected
events and messages to Crew appear as human-readable activities. `turns` in
the Mate summary remains the count of model calls used for token accounting.
The UI shows newest exchanges first and reveals older ones in batches of 20.

`tasks` is every crew the project has ever recorded, open and closed, oldest spawn first: one row of `v_task_ledger` each, with the crew's current `v_now` state, target and detail hung on it, plus two things no view carries.
`tool_count` is `SUM(turn.tool_count)` for the crew, which the tier-3 ledger asks for and `v_task_ledger` has no column for.
`age_ms` is `closed_at - spawned_at` for a closed task and `now - spawned_at` for an open one.
`closed` is true when the task has a `close_state` or a `closed_at`.

`inbox` is `box.Inbox` flattened through `query.LoadBox`, oldest first, the same rows in the same order the console's rail draws.
A failed box read leaves `inbox` empty and puts the reason in `inbox_error` rather than failing the page.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "project": "shop",
  "mode": "manual",
  "mate": {
    "harness": "claude",
    "running": true,
    "state": "idle",
    "since": "2026-09-19T10:46:40.000000000Z",
    "target": "",
    "detail": "",
    "tokens_today": 57683,
    "context_pct": 29.2,
    "turns": 6,
    "tokens": {"input": 212, "cache_read": 55112, "cache_write": 1840, "output": 519, "thinking": 84, "total": 57683},
    "cost": null,
    "last_turn": {"…": "a turn object, section 4"}
  },
  "tasks": [
    {
      "crew": "buybtn",
      "text": "Add a Buy button to README.md linking to the checkout page",
      "branch": "mate/buybtn",
      "state": "waiting_review",
      "since": "2026-09-19T10:46:12.000000000Z",
      "target": "mate",
      "detail": "",
      "close_state": "",
      "closed": false,
      "spawned_at": "2026-09-19T10:44:09.000000000Z",
      "closed_at": "",
      "merged_at": "",
      "age_ms": 171000,
      "turns": 9,
      "tokens": {"input": 23400, "cache_read": 32100, "cache_write": 1120, "output": 1154, "thinking": 351, "total": 57774},
      "cost": null,
      "last_model": "gpt-5-codex",
      "context_tokens_last": 41220,
      "context_pct": null,
      "question_count": 1,
      "handback_count": 1,
      "waited_ms": 86721,
      "tool_count": 14
    }
  ],
  "inbox": [
    {
      "seq": 3,
      "at": "2026-09-19T10:45:12.000000000Z",
      "kind": "status",
      "source": "crew",
      "target": "",
      "crew": "buybtn",
      "verb": "needs-decision",
      "text": "what is the checkout page URL for the Buy button?",
      "attention": true
    }
  ]
}
```

## 4. `GET /api/projects/{project}/tasks/{crew}`

Tier 3 leads with the current activity, up to three evidence-linked findings,
prompt-level timelines and the segments using the most tokens. Model calls,
status lines, questions and the branch remain available as supporting evidence.

`ledger` is the same task object as tier 2's row.

`turns` is one object per model call, oldest first.
`trigger_kind` is the `kind` of the event named by `turn.trigger_event_id`, joined on because a page showing a bare id would be showing the reader a number they cannot read.
`duration_ms` is `ended_at - started_at`, and `0` for a turn that has not ended.
`context_pct` is `100 * context_tokens_after / pricing.context_window` for that turn's model, `null` when the model has no priced window.
`ref` is the turn's own `ref_path`/`ref_offset`.

`status_lines` is the crew's `status.appended` events, with `verb`, `text` and `line` read out of the payload docs/timeline.md section 4 documents.

`questions` is the `question` table for this crew.
The answer's text is not on that table - it records which event answered, not what the answer said - so it is read from the answering event: `message.text` when a `message` row exists for it, otherwise that event's own payload, whose `text` is the answer verbatim.
`answered_by` is the answering actor's name.
`waited_ms` is `null` while a question is still waiting, which is not the same as having waited zero.

`branch` is the task's recorded branch and whether git still has it.
A branch that is gone is not an error: the crew was torn down and its work landed or was discarded, and `reason` says so.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "project": "shop",
  "crew": "buybtn",
  "ledger": {"…": "a task object, section 3"},
  "turns": [
    {
      "id": "crew:shop:buybtn#t7",
      "ordinal": 6,
      "started_at": "2026-09-19T10:45:58.000000000Z",
      "ended_at": "2026-09-19T10:46:04.000000000Z",
      "duration_ms": 6000,
      "trigger_event_id": 288,
      "trigger_kind": "message.sent",
      "outcome": "tool_use",
      "model": "gpt-5-codex",
      "tokens": {"input": 2140, "cache_read": 9120, "cache_write": 0, "output": 142, "thinking": 33, "total": 11402},
      "context_tokens_after": 41220,
      "context_pct": null,
      "tool_count": 3,
      "ref": {"path": "/Users/x/.codex/sessions/…/rollout-01a0b944.jsonl", "offset": 18422}
    }
  ],
  "status_lines": [
    {
      "event_id": 231,
      "at": "2026-09-19T10:44:31.000000000Z",
      "turn_id": "crew:shop:buybtn#t2",
      "verb": "working",
      "text": "verifying isolated worktree and task brief",
      "line": "working: verifying isolated worktree and task brief",
      "ref": {"path": "/Users/x/.codex/sessions/…/rollout-01a0b944.jsonl", "offset": 9004}
    }
  ],
  "questions": [
    {
      "id": "crew:shop:buybtn#q1",
      "asked_event_id": 252,
      "asked_at": "2026-09-19T10:45:12.000000000Z",
      "text": "what is the checkout page URL for the Buy button?",
      "answered_event_id": 288,
      "answered_at": "2026-09-19T10:46:00.000000000Z",
      "answered_by": "mate",
      "answer": "Use pages/checkout-express.html for the Buy button.",
      "waited_ms": 48000
    }
  ],
  "branch": {"name": "mate/buybtn", "exists": true}
}
```

### Crew performance projection

The task response also carries `performance` (`version: crew-observability-v1`).
The observer records `telemetry.*` facts in the derived event store; this read-only
projection uses their native prompt/execution identities and the existing ledger
for billed token buckets. It never adds native response usage to ledger usage.

| Field | Meaning |
| --- | --- |
| `tokens`, `model_calls` | Exactly the Crew ledger's bucket totals and model-call count. |
| `prompt_turns` | Native prompt ID scoped to its session, actual prompt and source locator, prompt timing, bucket totals, call count and `segment_ids`. A missing prompt ID leaves that call isolated. |
| `segments` | Contiguous activity in call order, with `kind`, concrete `label`/`target`, `call_ids`, `execution_ids`, buckets, nullable elapsed/tool-union/invocation time, repeat count and versioned classification rule. Each ledger call is assigned once. A running execution before usage arrives can have zero attributed calls. |
| `current_segment_id`, `top_segment_ids` | Current observed work when identifiable, and up to five segments ranked by total tokens. A completed run has no current segment. |
| `findings`, `top_finding_ids` | Versioned polling, repeated-read, repeated-error/repair, failed-command, long-execution, output/context and decision-wait observations. The first screen shows at most three. Findings include explanatory text, confidence, count, source evidence, related calls/segments/executions and a review suggestion. Their call sets can overlap; do not add their tokens together. |
| `executions` | Native commands and observed tools, with exact command/cwd, native process/wrapper links when known, start/end, nullable duration/exit/output measurements, output hash/preview, polling and wrapper flags, and source locator. Wrapper success never overrides a nested native failure. |
| `processes` | One process per session identity, actual job command when known, execution/poll IDs, unchanged/progress/unknown-output poll counts and tokens of the associated polling calls. Polls are not additional builds/tests. |
| `time` | Prompt elapsed, union of tool spans, sum of invocation durations, union of decision waits, and time not explained by measured tool/wait spans. Lanes overlap. None is called API latency or total thinking time. Missing measurements are `null`. Legacy synthesized `thinking` actions are excluded. |
| `recent_tokens`, `recent_window_ms` | Actual usage reported in the last five minutes, never an interpolated burn rate. |
| `freshness` | Separate last observed/ingested/usage stamps, age, stale flag, adapter capabilities and explicit missing-data reasons. `telemetry_cursor.observed_at` supplies the observer heartbeat even when no transcript bytes changed. |
| `profile` | Recorded launch snapshot, if available: repo revision/dirty state, requested model/effort, harness and configured document hashes/sizes. Historical runs without a snapshot remain unknown. |
| `runtime`, `observed_inputs`, `progress` | Actually reported model/effort/harness version, actually observed instruction-input hashes/bytes, and file-change evidence. Configured files and observed inputs are distinct; bytes are not exact billed token attribution. |

Finding/process `tokens` is `null` if no model-call link is confirmed. A native
execution with no confirmed wrapper parent remains visible in its prompt's
execution lane; nearby timestamps do not establish token attribution. Repeated
reads require identical query/range and output hash, and reset after a recorded
file change or changed output. Retries with intervening file changes are shown
as repair sequences; missing file state is never treated as unchanged code.
Detector thresholds live in `diagnostics.Options` and are exercised by numeric
and false-positive fixtures.

### Work overview for every prompt

Both `performance.prompt_turns[].overview` for Crew and
`exchanges[].overview` for Mate use the same projection. The collapsed prompt
shows a short summary and work-type labels; opening it shows the observed
sequence, usage by type, execution time and source evidence.

The types include research/inspection, writing files, editing files, review,
tests/builds, coordination, waiting/polling and reading instructions.
Classification uses actual tool operations, literal commands and native file
changes. A request to run tests does not establish that tests ran. An edit is
not automatically called a bug fix. Unclassified work remains explicit.

`overview.categories` contain `kind`, `label`, `model_calls`,
`execution_count`, nullable `tokens`/`elapsed_ms`, related call/segment/execution
IDs and self-contained command/source `evidence`. Each model call belongs to
exactly one category. A call covering several kinds belongs to `mixed`; its
usage is not divided among tools. The other categories can still show those
tools as evidence with unattributed usage. Category token buckets add up to
the prompt ledger. Execution intervals can overlap across categories.

`overview.sequence` retains successive observed types, including returns such
as test → edit → test. One operation containing several types becomes one
mixed step; it does not invent an order among parallel tools. `rule` identifies
the classifier and `coverage` explains the available evidence.

Mate grouping uses confirmed native response aliases when a legacy call has
no prompt ID. Otherwise that call stays isolated. Native prompts and their
work remain visible before the first usage record; message-only prompts show
an empty overview until activity arrives. The overview adds no model calls.

## 5. `GET /api/projects/{project}/tasks/{crew}/turns/{turn}`

One turn opened: the turn itself, its tool calls, and the story rows recorded inside it.

`actions` is the `action` rows of that turn, oldest first.
`action` carries no locator of its own - the schema puts `ref_path`/`ref_offset` on `event` - so each action's `ref` comes from the `tool.called` event it was written beside, through `action.event_id`.
`duration_ms` and `ok` are `null` for a call whose result the transcript did not carry.
`actions` can be longer than the turn's `tool_count`: the count is the calls the harness made, while `action` also holds the synthesised `thinking` rows the ingest writes for a busy stretch no call explains (docs/timeline.md section 4).
A UI that draws them should say which is which by the tool name.

`events` is `v_story` filtered to `turn_id`, in exactly the shape and field order `mate events` prints as JSON lines: `id`, `at`, `project`, `kind`, `actor`, `actor_kind`, `subject`, `task`, `turn`, `cause`, `cause_kind`, `payload`, `ref`, `ref_offset`.
It is produced by `timeline.Story`, the same function the CLI calls, so the two cannot drift.

A turn id belonging to another crew is a `404`: a link built from one page must not render under another page's heading.

Turn ids contain `#` (`crew:shop:buybtn#<session>#turn#20`), so the path segment must be percent-encoded - `encodeURIComponent` in the UI.
An unescaped `#` makes the browser send only the part before it and treat the rest as a fragment, which arrives here as a turn id that matches nothing.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "project": "shop",
  "crew": "buybtn",
  "turn": {"…": "a turn object, section 4"},
  "actions": [
    {
      "id": "crew:shop:buybtn#a21",
      "at": "2026-09-19T10:46:01.000000000Z",
      "ended_at": "2026-09-19T10:46:01.131000000Z",
      "tool": "exec",
      "target": "git add README.md && git commit -m \"docs: add Buy link\"",
      "summary": "",
      "duration_ms": 131,
      "ok": true,
      "event_id": 301,
      "ref": {"path": "/Users/x/.codex/sessions/…/rollout-01a0b944.jsonl", "offset": 19110}
    }
  ],
  "events": [
    {
      "id": 301,
      "at": "2026-09-19T10:46:01.000000000Z",
      "project": "shop",
      "kind": "tool.called",
      "actor": "buybtn",
      "actor_kind": "crew",
      "task": "buybtn",
      "turn": "crew:shop:buybtn#t7",
      "payload": {"class": "shell", "target": "git add README.md && …", "tool": "exec"},
      "ref": "/Users/x/.codex/sessions/…/rollout-01a0b944.jsonl",
      "ref_offset": 19110
    }
  ]
}
```

## 6. `GET /api/projects/{project}/tasks/{crew}/diff`

The bottom of the task page: `mate diff <project> <crew>`, verbatim.

The CLI's own `crewDiffText` produces it, so the page and the terminal can never disagree about what a branch contains.
A branch git no longer has returns `200` with an empty `text` and a `reason`, not an error, for the same reason the CLI says it plainly: a crew whose branch was deleted is a crew whose work landed or was discarded.
A diff that fails for any other reason answers the same way - the rest of the task page is still true, and a `500` here would take it down with the branch.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "project": "shop",
  "crew": "buybtn",
  "branch": "mate/buybtn",
  "exists": true,
  "text": "0d2d20d docs: add Buy link\n\ndiff --git a/README.md b/README.md\n…"
}
```

## 7. `GET /api/events?since=<id>&wait=<seconds>&project=<p>`

The long poll that keeps a page current without a refresh button.

It returns as soon as an event with `id > since` exists.
Otherwise it waits up to `wait` seconds, polling the database once a second, and then returns an empty list - an empty answer is the honest "still nothing", and the client polls again with the same cursor.
`wait` is clamped to 25 seconds, which is under every default proxy and browser idle timeout a local page can meet.
`since` defaults to 0, which returns the whole story.
`project` is optional; without it the story of every project is returned, merged and sorted by `(at, id)`.

`events` are `v_story` rows through `timeline.Story`, the same shape and field order `mate events` prints.

`now` are the `v_now` rows of the actors that moved, so one call refreshes both the story and the scene.
`v_now` has no "changed since" of its own, so which actors moved is read off `transition` - the table the view's scene columns already come from - as the distinct actors with a transition whose `event_id > since`.

This endpoint is not cached: it answers "what happened after the id you hold", which depends on the caller's cursor. Crew task snapshots are also uncached because heartbeat age and running durations advance without events; the remaining endpoints use the event generation cache.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "project": "shop",
  "since": 400,
  "events": [{"id": 401, "at": "…", "project": "shop", "kind": "turn.ended", "…": "…"}],
  "now": [
    {
      "actor_id": "crew:shop:buybtn",
      "actor": "buybtn",
      "actor_kind": "crew",
      "project": "shop",
      "state": "waiting_review",
      "since": "2026-09-19T10:46:12.000000000Z",
      "target": "mate",
      "detail": "",
      "tokens_today": 57774,
      "context_pct": null
    }
  ]
}
```

## 8. `GET /` - the UI

`/` serves an embedded `embed.FS` rooted at `internal/dashboard/ui/`: plain HTML and JS, no build step and no CDN, so the dashboard is a single binary that works with no network at all.
Task 28 ships a placeholder that lists these endpoints and fetches `/api/workspace` to prove the wiring; task 29 owns everything else under that directory.

## 9. What is read directly rather than through a view

The views are the contract, and these are the five places they do not reach.
Each is named here because a later change to a view should absorb them rather than leave two ways to ask the same question.

1. `actor.harness`, `actor.first_seen` and `actor.gone_at`, for a Mate card's harness and whether it is running. `v_now` carries neither.
2. `SUM(turn.tool_count)` per actor, for the tier-3 ledger's tool-call count. `v_task_ledger` has no such column.
3. The Mate's own turn count, token buckets and cost, from `turn`. `v_task_ledger` joins `task`, and a Mate has no task row.
4. `turn`, `action`, `question` and `message` themselves, for the turn timeline: the views summarise a task, they do not enumerate what happened inside it.
5. `transition`, for which actors moved since an event id, because `v_now` is a snapshot with no history in it.

`v_now`'s `context_pct` is read from the view directly rather than through `scene.Now`, which selects only the eight columns `mate events --scene` prints.

## 10. The UI

Task 29's page, under `internal/dashboard/ui/`: `index.html`, `app.css`, `app.js` and `humanize.js`.
Plain HTML, CSS and JavaScript loaded as classic scripts - no build step, no framework, no CDN and no off-origin request of any kind.
`internal/dashboard/ui_assets_test.go` walks every embedded file and fails the build on a `<script src=`, `<link href=`, `<img src=`, `fetch(`, `url(`, `@import` or `new WebSocket/EventSource/Worker` pointing anywhere but this origin, and on any bare `http://` or `https://` outside a comment.

### Routes

The three tiers are one document, routed on the hash, so a link to a task survives a copy-paste and the back button works.

| Hash | Tier |
| --- | --- |
| `#/` | workspace: one card per project - mode, the Mate's harness, running/stopped, scene state, since, tokens today, context %, crews counted by state, `N waiting` |
| `#/p/<project>` | project: the Mate panel, the task table (filter chips, default `open`), the inbox beside it |
| `#/p/<project>/mate` | Mate: prompt-level questions, replies, decisions and outcomes; filter by captain, crew or system |
| `#/p/<project>/t/<crew>` | task: the ledger header, the turn timeline, status lines, questions, the branch diff |

A project or crew name is `encodeURIComponent`-ed into the hash, and a turn id into the path of `/turns/{turn}` (section 5).

### Live update

One loop, one request at a time: `GET /api/events?since=<the last_event_id the page's data was built at>&wait=20`, plus `&project=<p>` on tiers 2 and 3.
On any `events` or `now` the page re-fetches only the endpoint the current tier reads and re-renders, keeping scroll position, the expanded turns and keyboard focus.
Because the API is cached per `last_event_id` and the page always polls from the id its own bytes carry, a re-fetch after an event that changed nothing it shows costs the server a cache hit.

The header carries the state as words: `live · HH:MM:SS` after every answered poll, `stale · <reason>` when one fails, where the reason is the API's own `reason` or, for a transport failure, "the dashboard is not answering".
A failed poll retries every 3 seconds rather than spinning.

One thing the `since` contract does not cover: `mate reindex` deletes and re-inserts every event, so `MAX(event.id)` can go *backwards*.
A page holding a cursor from before a rebuild would poll a dead id for ever while reporting `live`, so the page treats a `last_event_id` lower than its own cursor as a rebuild, takes the server's number and re-fetches.

### Numbers

`humanize.js` re-implements `internal/query`'s `HumanizeTokens` and `HumanizeCost` - `523`, `96.3k`, `1.2M`, `$0.12`, `$1.2k` - including Go's round-half-to-even, which JavaScript's own `toFixed` does not do (`$0.125` is `$0.12` in Go and `$0.13` in `toFixed`).
`TestUIHumanizeMatchesGo` builds a table of ~900 cases from the Go functions, sweeping every boundary plus a deterministic spread, and replays it through the page's own file in node when the machine has one.
Durations (`840ms`, `6.2s`, `1m7s`, `2h04m`, `3d 4h`) are the page's own: the console's `shortDuration` answers a different question and rounds a minute and seven seconds down to `1m`.

A `null` cost or context percentage renders `?`, never `0` - the same answer `mate usage` prints.
A timestamp is shown as a local clock time with the database's own RFC3339 string on hover, so a value on the page and a row in the database stay comparable.
Every turn, action, status line and event carries its `ref` as a `path:offset` tooltip.

### Vocabulary and colour

The state words are `internal/timeline/scene`'s own (`at_desk_working`, `waiting_review`, `walking_to_ceo`, …) and the inbox's phrases are the console's (`needs an answer`, `stuck, quiet too long`, `agent gone`, `over budget`, `send wedged`), so the TUI and the browser describe the same workspace in the same words.
Every state word is a glyph plus the word: colour is never the only difference between a crew working and a crew blocked.

Colour is defined once as tokens on `:root` and redefined under `prefers-color-scheme: dark`; the dark series steps are chosen for the dark surface rather than flipped.
The four token buckets are a stacked meter in a fixed slot order - input, cache read, cache write, output - assigned to the bucket and not to its size, with a 2px surface gap between segments and a legend that always shows the numbers.
Context % is a single-hue meter, because it is one magnitude against one window.
Below 720px the tables collapse to one card per row and the page does not scroll sideways.
