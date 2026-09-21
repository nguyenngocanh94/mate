# M6 task 29 - the dashboard UI on a real workspace

2026-09-21.
The three-tier UI of `docs/mvp.md` M6 row 29, driven through Playwright against `matev2 dashboard` on the captain's own workspace `~/work-matev2` - project `shop`, one Mate on codex and six crews, of which one is closed.
The workspace was read only: the dashboard opens the database with `db.OpenRead`, nothing on the page writes, and nothing in this run touched anything under `~/work-matev2`.

```
matev2 dashboard ~/work-matev2 --addr 127.0.0.1:7791
matev2 dashboard on http://127.0.0.1:7791/ (read-only; Ctrl+C to stop)
```

## Screenshots

| Tier | Light | Dark |
| --- | --- | --- |
| 1. Workspace (`#/`) | `m6-dashboard-2026-09-21/tier1-workspace-light.png` | `m6-dashboard-2026-09-21/tier1-workspace-dark.png` |
| 2. Project (`#/p/shop`) | `m6-dashboard-2026-09-21/tier2-project-light.png` | `m6-dashboard-2026-09-21/tier2-project-dark.png` |
| 3. Task (`#/p/shop/t/buyesp32`) | `m6-dashboard-2026-09-21/tier3-task-light.png` | `m6-dashboard-2026-09-21/tier3-task-dark.png` |

Light and dark are the same page under `prefers-color-scheme`, emulated through Playwright; the dark series steps are the dataviz palette's own dark column, not a flip of the light one.
The tier-3 shot has turn `#2` expanded, showing its actions table, the `tool.called`/`tool.finished` events inside it, the crew's status lines, the unanswered question, and the branch diff loaded from `/tasks/{crew}/diff`.

A seventh shot, `m6-dashboard-2026-09-21/tier2-project-phone-light.png`, is the project page at 375 CSS pixels: the task table has collapsed to one card per crew and `document.documentElement.scrollWidth` equals `clientWidth` (360 = 360), so there is no horizontal page scroll.

## Every request stayed on the loopback origin

`browser_network_requests` after loading all three tiers, expanding a turn and loading a diff:

```
[GET] http://127.0.0.1:7791/                       => 200
[GET] http://127.0.0.1:7791/app.css                => 200
[GET] http://127.0.0.1:7791/app.js                 => 200
[GET] http://127.0.0.1:7791/humanize.js            => 200
[GET] http://127.0.0.1:7791/api/workspace          => 200
[GET] http://127.0.0.1:7791/api/projects/shop      => 200
[GET] http://127.0.0.1:7791/api/projects/shop/tasks/esp32research          => 200
[GET] http://127.0.0.1:7791/api/projects/shop/tasks/esp32research/diff     => 200
[GET] http://127.0.0.1:7791/api/projects/shop/tasks/esp32research/turns/crew%3Ashop%3Aesp32research%2301a0afe0-7d5a-74c0-a59d-4fe0daaa9914%23turn%23197 => 200
[GET] http://127.0.0.1:7791/api/events?since=987&wait=20&project=shop
```

Ten requests, ten to `127.0.0.1:7791`, none anywhere else.
The turn id's `#` characters arrive percent-encoded as `%23`, which is what `docs/dashboard.md` section 5 requires.
`internal/dashboard/ui_assets_test.go` makes the same claim at the source, over every embedded file, so a `<script src="https://…">` introduced later fails the build rather than this document.

## The page updates itself

Proved on a throwaway copy of the workspace, because the proof needs the database to move and `~/work-matev2` is not ours to write to.

1. `cp -R ~/work-matev2 <scratch>/ws-copy`, `matev2 dashboard <scratch>/ws-copy --addr 127.0.0.1:7792`, open `#/p/shop`: the inbox shows 1 waiting.
2. Append `needs-decision: second live probe for task 29` to `power12.status` and `matev2 reindex <scratch>/ws-copy` (389 events).
3. With no refresh and no click, the open page's inbox became two rows - `rpi35 needs an answer` and `power12 needs an answer` - inside the first 500 ms poll tick, and the header stayed `live · HH:MM:SS`.

The stale path was checked too: with the server stopped, the header reads `stale · the dashboard is not answering` within one poll, and returns to `live` on its own once the server is back.

## What this run found in the data

These are findings about tasks 25-27 and the ingest, not about the UI, which renders what the API returns.

1. **No price anywhere.** `cost` is `null` and `context_pct` is `null` for the Mate and for all six crews, because `pricing.yaml` has no row for `gpt-5.6-terra`, the model every turn in this workspace ran on. The whole COST column and both context meters read `?`. That is the honest rendering of an unknown price, but it means M5 task 27's economics are invisible on real data until the model is priced.
2. **A full `matev2 reindex` resets `event.id`.** The rebuild deletes and re-inserts every row, so `MAX(event.id)` jumps backwards - in this run 987 before the first reindex of the copy, 388 after it. Any open page polling `/api/events?since=987` then waits for an id that will never come round again, and shows a frozen workspace while saying `live`. The UI now notices a `last_event_id` smaller than its own cursor and re-syncs, but the API's `since` contract assumes a monotonic id that `reindex` does not provide. Worth a line in `docs/dashboard.md` section 7 or a generation counter that survives a rebuild.
3. **Turn ordinals start at 0.** `docs/dashboard.md`'s example shows `"ordinal": 6` for the seventh turn; the real rows number the first turn `0`. The page prints the ordinal as recorded (`#0` … `#6`).
4. **`tokens_today` is 0 for a Mate that has spent 12.7M.** Correct as written - the last turn in this workspace is from 2026-09-20 and "today" is the 21st - but a card that says `0 tokens today` beside `156 turns` reads as a bug until you check the dates.
5. **`waited_ms` on a task row is 0, not null, for a question still waiting.** `buyesp32` has one unanswered question; `Question.waited_ms` is correctly `null`, but the ledger's `Task.waited_ms` is a plain `int64` and reports 0. The page draws `–` rather than `0s` for that reason. The two fields disagree about the same fact.
6. **The embedded assets are served with no `Last-Modified` and no `ETag`** (an `embed.FS` file has a zero modification time), so a browser that has the page open across an upgrade of the binary can keep serving the old `app.js` from its own cache. Every screenshot in this run needed an explicit cache-busting reload after a rebuild. A validator or a version query string belongs in the server, which is task 28's file.

## Reproducing

```
matev2 reindex ~/work-matev2          # only if .matev2/matev2.db is missing
matev2 dashboard ~/work-matev2 --addr 127.0.0.1:7791 --open
go test ./internal/dashboard/ -run TestUI -v
```
