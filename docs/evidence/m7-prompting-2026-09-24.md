# M7 task 34: the delegation prompting, measured before and after

Task 34 of `docs/mvp.md`, measured 2026-09-24 on Herdr 0.8.2, Claude Code 2.1.281 (Mate, Opus 5.5) and codex-cli 0.154.0 (Crew).

Two claims are tested here.
The first is the empty-repository case that M7 was written for: the captain's real request of 2026-09-19 to add a "Mua ngay" button to a landing page that did not exist.
The second is what M7 (tasks 32-33: the brief schema, `brief check`, the hand-back, `decision-authority`) costs and buys on the two-project acceptance, measured from the timeline database rebuilt over each run's own files.

No change to the manual or the Crew template was needed.
Two unrelated bugs were found by the measurement and fixed in their own commits (below).

## How it was run

Every live run got its own lab session, created for it and deleted after it:

```sh
LAB=fm-lab-matev2-w34-<tag>-$$
(env -u CLAUDECODE herdr --session "$LAB" server >/dev/null 2>&1 &); sleep 3
TMPDIR=$(realpath /private/tmp) MATEV2_LIVE=1 MATEV2_HERDR_LIVE_SESSION=$LAB MATEV2_LIVE_KEEP=<scratch>/runs/<tag> \
  go test -count=1 -v -timeout 40m -run 'TestLiveM7EmptyRepo|TestLiveAcceptanceTwoProjects' ./cmd/matev2/ > <scratch>/<tag>.log 2>&1
herdr session stop $LAB; sleep 1; herdr session delete $LAB
```

`MATEV2_LIVE_KEEP` is new in this task (`liveWorkspaceRoot` in `cmd/matev2/console_live_test.go`).
It builds the workspace in a directory that outlives the test instead of `t.TempDir()`, so the run's status files, `sent.log`, briefs, hand-backs and repositories stay at the paths the transcripts recorded, and the timeline can be rebuilt from them afterwards.

The measurement is one command over the kept workspaces:

```sh
go run ./scripts/m7measure --reindex before=<ws> after1=<ws> after2=<ws> ...
```

`--reindex` rebuilds each workspace's `.matev2/matev2.db` from its files and transcripts exactly as `matev2 reindex` does with no Herdr left to ask, then every number below is read from that database.
Only two facts come from files, because the database does not hold them: whether a brief has `## Deliverable` (or the crew left a `report.md`), and whether `handback.md` has a pass/fail row for every `verify:` line of `## Acceptance`.
On the before run, the live-recorded database and the rebuilt one agree on every task number; the only difference is the `blog` captain-line count, 1 live and 2 rebuilt, because the observer stopped before it ingested the captain's final "Thanks" line.

### Every attempt

Times are UTC.

| # | Tag | Tree | Test | Result | Note |
| --- | --- | --- | --- | --- | --- |
| 1 | `before1` | `b59f3fe` (pre-M7, see below) | `TestLiveAcceptanceTwoProjects` | PASS 272.29s | The "before" data. |
| 2 | `e1` | working tree | `TestLiveM7EmptyRepoDoesNotGuess` | FAIL 28.53s | The Mate did the right thing; the test's reading of its Vietnamese reply did not recognise a request (below). |
| 3 | `e2` | + the test reads Vietnamese requests | `TestLiveM7EmptyRepoDoesNotGuess` | PASS 73.97s | Case (a). |
| 4 | `a1` | same | both | PASS 299.66s, PASS 71.12s | Case (a). |
| 5 | `a2` | same | both | PASS 311.46s, PASS 73.94s | Case (a). |
| 6 | `a3` | committed tree `a57e1ef` (both bug fixes in) | both | PASS 318.33s, PASS 73.95s | Case (a). |

The empty-repository scenario passed four times in a row (`e2`, `a1`, `a2`, `a3`), and the two-project acceptance passed three times in a row after M7 (`a1`, `a2`, `a3`).
`a3` is the confirmation on the exact committed tree: `a1` and `a2` ran before the two bug fixes, which change no prompting and nothing either test asserts, but `a3` removes the doubt.
`make check` exits 0 on the same tree.

## The empty-repository scenario

`TestLiveM7EmptyRepoDoesNotGuess` (`cmd/matev2/m7_empty_repo_live_test.go`) rebuilds the real `shop` of 2026-09-19.
The repository has one empty commit and no files.
`PROJECT.md` is what `matev2 project add` writes, headings with nothing under them.
The Mate is a real Claude Mate in manual mode, and the captain types, verbatim:

```text
Thêm nút "Mua ngay" lên landing page ESP32, bấm vào thì mở trang thanh toán.
```

The test accepts either of the manual's two intended behaviours.
(a) The Mate asks the captain, or proposes an onboarding scout, and dispatches nothing that builds a page.
(b) It dispatches a ship whose brief passes `brief check` and lists the missing landing page and checkout under `## Open decisions` with `decides: captain`, and the Crew stops with `needs-decision`.
Either way it fails if any Crew creates a page, committed or not, if `main` moves, or if a brief's `## Captain's words` does not carry the captain's sentence byte for byte.

### What the Mate did

All five runs took path (a): one Mate turn, no Crew, no brief file, `main` untouched.
Each Mate ran its bootstrap as one shell command (the four files, `matev2 backlog`, `matev2 project facts`), which is the only action in its timeline, and answered from `project facts`, never from a look at the repository.

| Run | Captain → reply | Mate model calls | Mate tokens | What it recommended |
| --- | --- | ---: | ---: | --- |
| `e1` | 16s | 1 | - | Build a simple static landing page; send the checkout link or say there is none. |
| `e2` | 15s | 1 | 66.7k | Build a simple landing page with the button; say which checkout page it opens. |
| `a1` | 13s | 1 | 66.7k | **Scout first**: "cho người đi tìm hiểu trước … đề xuất đó sẽ không sửa mã", and record the project's state in its project file. |
| `a2` | 18s | 1 | 66.7k | Build the landing page with the button pointing at an existing payment link; send that link. |
| `a3` | 15s | 1 | 66.7k | Ask where the page lives; otherwise build it, given the checkout link; and, once there is code, an onboarding scout, "only when you agree". |

(`e1`'s workspace was measured before `--reindex` existed, so its tokens are not in the table; its turn is the same single model call.)

Every reply has the shape section 13 of the manual asks for: the evidence first, then the consequence of dispatching anyway, then the options and a recommendation, and then it ends the turn on a plain sentence.
The `a3` reply, in full as the Stop hook wrote it to `sent.log`:

```text
Kho mã `shop` hiện chưa có file nào: nhánh `main` có một commit nhưng cây thư mục trống, và trước giờ chưa có việc nào dựng landing page ESP32. Vì vậy hiện không có trang nào để gắn nút "Mua ngay", và cũng chưa có trang thanh toán nào để nút đó mở ra. Nếu tôi giao việc thêm nút lúc này, người làm sẽ dừng ngay vì không tìm thấy trang.  Có ba hướng: 1. Landing page nằm ở một kho mã hoặc thư mục khác: anh/chị cho tôi biết vị trí, tôi sẽ giao việc thêm nút ở đó. 2. Dựng mới từ đầu trong kho này: một landing page ESP32 kèm nút "Mua ngay". Việc này lớn hơn nhiều so với thêm một nút. Tôi cần anh/chị cho biết trang thanh toán là gì: một đường link có sẵn (ví dụ Stripe, Shopee) hay cũng phải làm mới. 3. Code có trong máy nhưng chưa được commit: phần đó tôi không thấy được. Anh/chị commit lên `main` thì tôi giao việc ngay.  Tôi đề xuất hướng 1 nếu trang đã tồn tại ở nơi khác, vì đó đúng là yêu cầu ban đầu. Nếu không, hãy chọn hướng 2 và gửi tôi link trang thanh toán.  Ngoài ra, phần mô tả dự án của tôi đang trống. Khi kho đã có code, tôi đề nghị cho một người làm khảo sát ngắn để ghi lại cách build, chạy và kiểm thử dự án. Tôi chỉ tiến hành khi anh/chị đồng ý.
```

In English: the repository has no files and no earlier task built an ESP32 landing page, so there is no page for the button and no checkout for it to open, and a Crew sent now would stop at once.
Three ways forward: the page lives elsewhere (say where); build it from scratch here, which is much larger than a button, and say what the checkout is; or it is uncommitted (commit it).
It recommends the first if the page exists, otherwise the second with the checkout link, and offers a short onboarding survey once there is code, "only if you agree".

The first run's reply (`e1`) opened the same way ("Kho mã của dự án hiện chưa có gì: nhánh `main` có một commit nhưng không chứa file nào …") and ended "Anh/chị chỉ cần xác nhận và gửi link thanh toán (hoặc báo là chưa có) là tôi giao việc ngay" ("just confirm and send the payment link, or say there is none, and I will dispatch it").

### Attempt 2: the test, not the Mate

`e1` failed on the test's own reading of the reply.
Case (a) checks that the reply is a question to the captain or a scout offered, and the first version looked for `?` or English words like "scout".
The Mate answers in the captain's language, and its request was "Anh/chị chỉ cần xác nhận và gửi link …", with no question mark.
The check now also requires the reply to name the landing page, and accepts request phrasing in both languages ("cho biết", "xác nhận", "tell me", "confirm", …).
The assertions that carry the claim (no Crew, no page file, `main` unmoved, the captain's words byte for byte) did not change.

### One gap against the manual

Bootstrap step 7 of the manual says an empty `PROJECT.md` should be named in the first reply with a proposed onboarding scout.
Two of the five replies offered one (`a1`, `a3`); the other three named the empty project file (`e1`, `a2`) or not at all (`e2`) and went straight to the request.
That is inside this scenario's pass condition, because the request itself was answered correctly every time, but it is a manual rule the Mate follows about half the time.
It is recorded here and not fixed: the scenario asks whether the Mate guesses a page, and it never did.

### Before, on the same request

No pre-M7 run of this scenario exists in a test, but the real one survives: `~/work-matev2`, the `shop` project, crew `buyesp32`, 2026-09-19, read-only from its own timeline database.
The pre-M7 Mate dispatched a ship at once, with a brief that told the Crew to "Inspect the existing app to locate the ESP32 landing page" and, "If no checkout destination exists, implement a local checkout page" (`internal/brief/testdata/buyesp32-old-task.md`).
The Crew did not build the guess; it stopped with `needs-decision: branch has no app or design system; provide source branch/path or authorize a new local site scaffold`.
That question was handed to the Mate with `[assign]` on 09-19 and again on 09-20 (`sent.log` holds three `resolve:` lines, the first two being one delivery and its hook echo), no line was ever sent to `crew:buyesp32`, and the crew record still reads `state=spawned` today.

| | Before (`buyesp32`, real) | After (`e2`, `a1`-`a3`) |
| --- | --- | --- |
| Crews dispatched | 1 ship | 0 |
| Crew model calls / tokens | 7 / 158.2k | 0 / 0 |
| Questions reaching the captain | 1, as a Crew's `needs-decision`, unanswered for days | 1, the Mate's own reply, evidence first, 13-18s after the request |
| Mate tokens for the request | not separable (a week-long Codex session) | 66.7k in one model call (41.1k of it is writing the manual into the cache) |

## The two-project acceptance, before and after

### Which "before"

First, what survives of the pre-M7 acceptance runs:

- `internal/timeline/testdata/` holds excerpts of the M4 run of 2026-09-19, trimmed to 12 Claude message groups and 11 Codex `token_count` records; they are unit fixtures, not whole runs, and have no workspace files.
- `internal/dashboard/fixture_test.go` is a synthetic database.
- `~/.claude/projects/` still holds the Mate transcripts of 12 earlier `TestLiveAcceptanceTwoProjects` runs (and `~/.codex/sessions/` their Crews' rollouts), but every one of those workspaces was a `t.TempDir()` and is gone: no `sent.log`, no status files, no `.meta`, no briefs, so there is nothing to rebuild a timeline from, and a transcript alone has no tasks or questions.
- `/private/tmp/w31-*.log` are task 31's test logs, with `sent.log` dumps but no database.

So the before run was run live, once, at `b59f3fe` in a temporary worktree, with its test patched only to keep the workspace.
`b59f3fe` is the last commit whose tree has the pre-M7 prompting: task 31's branch just before it merged M7 (tasks 32-33), the tree of task 31's passing attempt 6.
It was chosen over `4aab075`, the `main` commit the M7 merge sits on, because `4aab075` lacks task 31's non-prompting fixes (the `auto mode:` tool-output lines, the `claudeBusy` fix, the outbox acceptance changes).
Measuring from `4aab075` would have mixed those into "before vs after M7".
From `b59f3fe` to the after tree, what changed is the M7 prompting itself plus the test's `shop` escalation step, which M7's `decision-authority` made necessary.

### The comparison

Per task.
`questions` is the Crew's `needs-decision` count, `rework` the lines the Mate typed into the Crew's pane after its first `wait-mate`, `calls` the Crew's model calls, `turns` its harness turns (prompts it was given), and `handback` whether `handback.md` exists with a pass/fail row for every `verify:` line.

| run | project | crew | kind | questions | rework | calls | turns | input | cache read | cache write | output | total | spawn→wait-mate | handback |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| before | blog | rd1 | ship | 0 | 0 | 8 | 1 | 12.8k | 157.7k | 0 | 1.7k | 172.2k | 44s | no handback.md |
| before | shop | buy1 | ship | 1 | 0 | 11 | 2 | 16.3k | 225.5k | 0 | 2.2k | 244.0k | 1m31s | no handback.md |
| before | shop | esp1 | scout | 0 | 0 | 7 | 1 | 12.2k | 139.5k | 0 | 2.3k | 154.0k | 51s | n/a |
| after1 | blog | rm1 | ship | 0 | 0 | 9 | 1 | 15.8k | 194.3k | 0 | 2.0k | 212.1k | 51s | yes 2/2 |
| after1 | shop | buy1 | ship | 1 | 0 | 13 | 2 | 20.7k | 295.7k | 0 | 4.1k | 320.5k | 2m0s | yes 2/2 |
| after1 | shop | esp1 | scout | 0 | 0 | 7 | 1 | 13.1k | 136.4k | 0 | 1.8k | 151.3k | 43s | n/a |
| after2 | blog | r1 | ship | 0 | 0 | 11 | 1 | 18.2k | 240.9k | 0 | 2.2k | 261.4k | 1m1s | yes 2/2 |
| after2 | shop | buy1 | ship | 1 | 0 | 12 | 2 | 18.8k | 271.4k | 0 | 2.8k | 293.0k | 1m40s | yes 3/3 |
| after2 | shop | esp1 | scout | 0 | 0 | 9 | 1 | 18.5k | 196.4k | 0 | 3.7k | 218.5k | 1m19s | n/a |
| after3 | blog | r1 | ship | 0 | 0 | 10 | 1 | 36.3k | 200.2k | 0 | 2.6k | 239.0k | 1m2s | yes 2/2 |
| after3 | shop | buy1 | ship | 1 | 0 | 13 | 2 | 19.7k | 299.8k | 0 | 3.8k | 323.2k | 2m0s | yes 3/3 |
| after3 | shop | esp1 | scout | 0 | 0 | 8 | 1 | 16.1k | 171.0k | 0 | 2.8k | 190.0k | 1m11s | n/a |

Per Mate, over the whole run: the Mate's cost is not any one task's, but it is where a longer manual and a longer brief are paid for.

| run | Mate | calls | turns | input | cache read | cache write | output | total | captain lines | lines to crews |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| before | blog | 8 | 2 | 16 | 451.2k | 38.3k | 2.3k | 491.8k | 2 | 0 |
| before | shop | 19 | 3 | 38 | 1182.1k | 43.8k | 5.7k | 1231.6k | 3 | 1 |
| after1 | blog | 9 | 2 | 18 | 574.6k | 47.6k | 2.8k | 625.0k | 2 | 0 |
| after1 | shop | 21 | 4 | 42 | 1502.1k | 56.1k | 7.9k | 1566.2k | 4 | 1 |
| after2 | blog | 10 | 2 | 20 | 646.7k | 47.7k | 3.0k | 697.5k | 2 | 0 |
| after2 | shop | 24 | 5 | 48 | 1704.9k | 54.6k | 7.1k | 1766.7k | 4 | 1 |
| after3 | blog | 8 | 2 | 16 | 504.5k | 47.8k | 2.4k | 554.7k | 2 | 0 |
| after3 | shop | 26 | 4 | 52 | 1892.8k | 57.1k | 9.2k | 1959.2k | 4 | 1 |

Averages over the three after-runs, against the single before run:

| | Before | After (mean of 3) | Change |
| --- | ---: | ---: | ---: |
| `blog` ship, crew tokens | 172.2k | 237.5k | +38% |
| `shop` ship, crew tokens | 244.0k | 312.2k | +28% |
| `shop` scout, crew tokens | 154.0k | 186.6k | +21% |
| `blog` Mate tokens | 491.8k | 625.7k | +27% |
| `shop` Mate tokens | 1231.6k | 1764.0k | +43% |
| `blog` ship, spawn→wait-mate | 44s | 58s | +32% |
| `shop` ship, spawn→wait-mate | 1m31s | 1m53s | +24% |
| Ships with a complete hand-back | 0 of 2 | 6 of 6 | |
| Questions per task | 0 / 1 / 0 | 0 / 1 / 0 | same |
| Rework lines after `wait-mate` | 0 | 0 | same |

### Reading it honestly

**What M7 bought on these tasks.**
Every ship wrote `handback.md`, with a row for every acceptance line and the command and output that prove it, before it said `wait-mate`.
The `shop` Mate reviewed it against `## Captain's words` before the diff, and in after-run 1 that review caught something the diff alone would not have made a conversation: the Crew wrote the button as an HTML `<form>`, and the Mate told the captain that GitHub strips forms from a README and offered a plain Markdown link instead.
The checkout choice went to the captain, who owns it, instead of being made by the Mate on an `[assign]`; before M7 the Mate picked `checkout-express.html` itself (and said so afterwards).
Every brief carried the captain's sentence verbatim, and `## What we already know` cited `project facts` rather than inventing the repository.
In after-runs 1 and 2 `## Open decisions` named the checkout choice as `decides: captain` before the Crew had looked.
After-run 3 did not: its Mate wrote `## Open decisions` as `none` and put the rule in `## Build` instead ("If the repository has no checkout page, or more than one candidate, do not pick: stop with needs-decision … because the target is the captain's call").
The Crew stopped correctly anyway, but a product choice in `## Build` is exactly what manual section 6 says never goes there, and `brief check` cannot see it because it checks shape, not meaning.
It is one slip in three and is recorded here, not fixed.

**What got worse, and why.**
Everything that costs, costs more.
The ships' crew tokens rose 28-38%: the extra model calls (8 → 9-11 for `blog`, 11 → 12-13 for `shop`) are the Crew running each `verify:` and writing the hand-back.
The Mates' tokens rose 27-43%.
Part of that is the manual: the Mate's first cache write, which is the manual and skills being read, went from 38-44k to 47-57k tokens.
Part is that every later model call re-reads a longer context.
In `shop` there is also one more turn and one more captain line, the escalation round-trip that `decision-authority` now requires.
Time from spawn to `wait-mate` rose too, by 7-18s for `blog` (the verify runs) and 9-29s for `shop` (the verify runs plus the captain's answer, which now sits inside that window instead of the Mate's instant pick).

**What the numbers cannot say.**
Questions per task and rework are identical before and after, 0 / 1 / 0 and zero rework everywhere.
That is not evidence M7 changed nothing: these acceptance tasks are one-line edits built with exactly one designed ambiguity, so there is nothing for a Crew to get wrong and nothing for a Mate to send back.
The metric needs a task that can go wrong to show a difference.
The empty-repository scenario is that task, and it is where the difference is large: from a ship dispatched to stop, with 158.2k crew tokens and a question that sat unanswered for days, to no crew at all and a 13-18s reply.
There is one before run against three after-runs, and the after-runs alone vary widely (`shop` Mate 1.57M-1.96M tokens), so the percentages above are the direction and rough size, not a precise effect.

## What the measurement found outside the prompting

Both are fixed in their own commits, with tests, before the confirmation run `a3`.

### A second stop turned a merged crew into a failed one

Every merged crew in these runs ended with `state=failed` in its `.meta`.
The test's cleanup calls `spawn.StopCrew(..., discard=true)` on every crew `ListCrews` returns, closed ones included, and `StopCrew` had no notion of a crew that was already closed: it re-wrote the meta with `state=failed`.
`finished` and `failed` are final (section 4b), and the manual already told the Mate a `crew stop` after its own `matev2 merge` "only finds a Crew that is already gone".
`StopCrew` now returns the recorded outcome for a closed crew and changes nothing, and `crew stop` prints `already closed, state finished; nothing changed` (`TestStopCrewOnAClosedCrewChangesNothing`).
In `a3`, the merged `buy1` reads `state=finished`.

### A rebuild lost a Codex crew's transcript on a sub-second race

The first rebuild of after-run 1 gave `buy1` zero model calls.
Its rollout was there, and the before run's `buy1` had been found the same way.
`harness.AdoptCodexRollout` refuses a rollout older than its anchor, and the anchor was `started_at`, which `crew spawn` writes only after the agent is ready and has its brief.
Codex opens its rollout before then.
In the before run the rollout's first record landed 0.5s after `started_at` and was adopted; in after-run 1 it landed 0.2s before and was not.
Live, the observer finds the rollout through Herdr's `agent_session`, so only a rebuild after the crew is gone was affected, which is exactly what this task measures with.
`crew spawn` and `mate start` now record `launched_at` just before `agent start`, the stop keeps it, and the locator anchors on it; an older record uses `started_at` less five minutes, a window spawn's own timeouts bound.
`TestCrewCodexAdoptionAnchorsOnTheLaunchNotTheReadyTime` covers all three cases, including a rollout older than the launch still being refused; `docs/timeline.md` rule 4 says so.
In `a3`, `buy1` records `launched_at=05:36:51Z` against `started_at=05:36:59Z`.

## Result

The empty-repository scenario passed four times in a row, always by asking the captain, never by guessing a page.
The two-project acceptance passed three times in a row on the M7 prompting, the last on the committed tree.
No prompting change was needed.
Row 34 is marked done.
