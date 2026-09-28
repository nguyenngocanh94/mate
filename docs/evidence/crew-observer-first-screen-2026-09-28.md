# Crew observer first screen — evidence 2026-09-28

Design: `docs/evidence/crew-observer-first-screen-2026-09-28/crew-observer-design-20260928.html` (approved by the captain).
Plan: `docs/plans/2026-09-28-crew-observer-first-screen.md`.
Contract: `docs/dashboard.md`, "Crew performance projection" (`overview`, `top_output_segment_ids`, `top_output_call_ids`, `loops`, work-vocabulary segment kinds).

## What changed

- The projection gains a crew-level `overview` (the prompt-overview builder run over every prompt), two rankings by output + thinking, and a `loops` summary.
- Segments now use the work vocabulary of `executionWork` (`research`, `instructions`, `write_code`, `edit_code`, `review`, `test`, `coordination`, `wait`, `response`, `mixed`, `unknown`), so segments, prompt overviews, the crew overview and the UI filters share one taxonomy.
- Codex 0.157 JavaScript wrappers are decoded: `tools.apply_patch(…)` becomes an edit or write with the patched file names as target, `cat >> file <<'EOF'` becomes a write, `echo … >> "$MATE_STATUS"` becomes coordination, `xcrun xcresulttool …` becomes research; scripts (`python3 -`) stay unknown but keep their command in the label.
- The Crew page opens with a four-card strip (tokens, tool calls by work, repeats, most expensive step), kind chips and colours everywhere, output bars and poll brackets on the timeline, a tool-mix table per prompt, kind filters and an output + thinking sort on the segment table, and an "unclassified segments" coverage line.

## Measured on the real recording

Source: `newWorkspace/hellovietnam`, Crew `ios7` (Codex 0.157.1, gpt-6-sol, effort medium), served from an isolated copy of the review database on `127.0.0.1:7779` with the build of this tree (`go build ./cmd/mate` at 14:07).
The console's own dashboard on 7777 still runs the older build and shows none of this until the captain does the rollout (D6 in the design).

| Measure | Before (build 11:49) | After (this tree) |
| --- | ---: | ---: |
| Model calls / tokens (ledger) | 363 / 40,409,527 | 363 / 40,409,527 |
| Σ overview categories (calls / tokens) | not exposed at crew level | 363 / 40,409,527 |
| Σ segments (calls / tokens) | 363 / 40,409,527 | 363 / 40,409,527 |
| Segments | 184 | 206 (mixed segments now carry a target; wait chains unchanged at 30) |
| Unclassified segments (`wrapper` + `mixed` + `unknown`) | 71 | 29 (`mixed` 2, `unknown` 27) |
| Segment labels containing wrapper JavaScript | 35 | 0 |
| `edit_code` segments / calls | 0 (all "Tool wrapper") | 41 / 42 |
| Per-call kind: overview vs segment disagreements | n/a | 0 of 363 |
| `loops` | not exposed | measured · 17 chains · 153 polls · 123 no new output · 30 with new output · 30 processes · 183 poll calls · 20,616,649 poll tokens · 122 segment repeats · 0 repeated reads · 0 retries |
| `top_output_call_ids[0]` | not exposed | `…#turn#1753` (ordinal 301, 1,949 output + 790 thinking) |
| `top_segment_ids[0]` | `…#turn#1971` | `…#turn#1971` (process 85006, 2,222,317 tokens) |

Segment kinds after: research 61 · edit_code 41 · wait 30 · test 30 · unknown 27 · coordination 10 · write_code 4 · mixed 2 · review 1.
The 29 unclassified are dynamic wrappers (`[…].map(cmd => tools.exec_command({cmd}))`, `${path}` templates), `python3 -` heredoc scripts, three calls with no operation recorded, and housekeeping (`rm`, `find -delete`, `cp`).

Commands used (JSON saved as `ios7-final.json` in the session scratchpad):

```bash
curl -s http://127.0.0.1:7779/api/projects/hellovietnam/tasks/ios7 > ios7-final.json
jq '.performance | {calls: .model_calls, total: .tokens.total, ov_calls: ([.overview.categories[].model_calls]|add), ov_tokens: ([.overview.categories[].tokens.total // 0]|add)}' ios7-final.json
jq '.performance.loops' ios7-final.json
jq -r '.performance.top_output_call_ids[0], .performance.top_segment_ids[0]' ios7-final.json
jq -r '.performance.segments[].kind' ios7-final.json | sort | uniq -c
jq '[.performance.segments[] | select(.kind=="mixed" or .kind=="unknown")] | length' ios7-final.json
jq -r '.performance.segments[] | select(.label|test("tools\\.|Begin Patch")) | .label' ios7-final.json | wc -l
```

## Browser acceptance

Tool-launched Chrome against the 7779 build, page `#/p/hellovietnam/t/ios7`.

- 1440×1000: four answer cards render with the numbers above; `scrollWidth == clientWidth == 1440`; no console errors.
- 500×900: cards stack in one column; `scrollWidth == clientWidth == 500`; no console errors.
- Screenshots beside the design: `docs/evidence/crew-observer-first-screen-2026-09-28/crew-observer-after-1440.png`, `-1440-answers.png`, `-1440-timeline.png`, `-1440-toolmix.png`, `-500.png`, `-500-answers.png`.

## Review round

Two reviewers read the diff against a pristine copy of the tree taken before the team started.

- UI (code-reviewer): no Critical issue; one Important (`.crew-head` lacked `flex-wrap`), two Minor (duplicated spawn→close arithmetic, silent empty work card). All three fixed; the work card also stops printing "0 cmds" for kinds that have no native command (apply_patch edits, polls).
- Go (go-reviewer): no Critical issue; one Important (the relative-worktree match rewrote any `/…/`-bounded substring, so `cat /data/w/config.json` with worktree `/w` became `cat config.json` and could conflate two commands in the repeat-detection key), four Minor (phantom targets from `cmd:` text inside a patch body, UTF-16 surrogate pairs in decoded JavaScript literals, a tie test for the rankings, a comment on the git-chaining heuristic). All fixed: path tokens are anchored (a path is rewritten only when it starts with the worktree, or, for the relative name the task table stores, when the name is its last directory-boundary occurrence in an absolute path); a patch wrapper's targets and kinds come from the patch and its own tool invocations, never from `cmd:` text in the patch body; surrogate pairs combine into one rune.
- Deviation kept on purpose: the dashboard does not join the workspace root onto the relative worktree before projecting, because the fixture transcript and the review copy both record paths under a different root than the served workspace (symlinks), so an absolute-only identity would stop normalising exactly where it is checked. The anchored relative match is the identity; `TestWorktreeAnchoringRewritesOnlyWholePathTokens` covers the reviewer's example.
- Added after the review: `harness` on the task ledger (`actor.harness`, the Mate card's source) so the crew header can name the harness of a recording whose telemetry carries no version; contract in `docs/dashboard.md` section 3.

Re-measured after the fixes: identical numbers (29 unclassified of 206 segments, 363 calls and 40,409,527 tokens conserved across segments and overview, every native cwd normalised to `.`).

## Verification

- `gofmt -l .` prints nothing; `go vet ./...` clean.
- `make check` passed before and after the review round with the 60 declared live skips (`=== NOT PROVEN: 60 expected skip(s) ===`); as before, a green suite that skipped the live tests is not evidence those features work.
- New Go tests: `internal/diagnostics/summary_test.go` (crew overview conservation, output rankings, loops measured/unmeasured, ranking ties) and `internal/diagnostics/classify_test.go` (18 tests: vocabulary, apply_patch targets, heredoc writes, status echo, command rules, quote decoding, surrogate pairs, worktree anchoring, native `wait` polls, patch-body isolation); `internal/dashboard` tests assert the ledger's `harness` and worktree-relative native paths. Both packages pass under `-race`.
- Node tests: `node --test internal/dashboard/ui_observability_test.cjs` → 19 pass (six new: harness chip, four answers, unmeasured polling, kind filter and output sort, tool mix sums, poll brackets).

## Rollout (D6, D7) on 2026-09-28 14:56–14:59

- `make build` rebuilt `bin/mate` (version `6a046e6-dirty`); `~/.local/bin/mate` is a symlink to it.
- The console (pid 30199, started 09:53 on the old build) was quit with its own `q` key sent into its WezTerm pane; it exited within a second, released `.mate/mate.db.lock`, and its stage helper exited with it. `mate` was relaunched in the same pane (`/Users/anh/newWorkspace`) and now runs the new binary (pid 41381 holds the lock).
- The live database migrated to schema 3 on open. Within the first minute the observer had 41 telemetry cursors and 23,770 `telemetry.*` events backfilled from the existing transcripts.
- `pricing.yaml` gained a `gpt-6-sol` row (standard short-context OpenAI list prices: $2 input, $0.20 cache read, $2.50 cache write, $10 output per million; context window 1,050,000; the long-context tier above 272K is noted in the file but not modelled). The previous file is kept as `pricing.yaml.bak-20260928`.
- The 7777 dashboard was restarted on the new build. Live `ios7`: cost $9.18, context 18.4 %, harness `codex`, `performance` with native capabilities, overview 363 calls, loops measured (17 chains, 153 polls), 29 unclassified of 206 segments.

## Not done in this batch

- Claude Crews: polling chains are reported as not measurable (no native process identities); repeated reads and retries still work.
- No commit was made: the tree also carries another session's uncommitted send/paste work, and the captain commits.
