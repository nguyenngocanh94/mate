# M6 debt, task 31: the auto-mode Mate ends its turn after spawning

Task 31 of `docs/mvp.md`, measured 2026-09-24 on Herdr 0.8.2, Claude Code 2.1.281 (Mate) and codex-cli (Crew).

The debt, recorded at task 24: in auto mode the Mate took the captain's request, spawned, supervised with `sleep 20; matev2 state` and merged inside one turn.
Its composer was Busy the whole time, so the daemon never delivered a `digest:` line, and auto mode ran as a slower manual mode.
The manual already told it to stop after spawning, and it did not in either task 24 run.

## What changed

### The instruction is in the tool output

When the project has `mate/.auto`, three commands end with one extra line (`cmd/matev2/auto_turn.go`):

| Command | Last line |
| --- | --- |
| `matev2 crew spawn <p> <crew>` | `auto mode: end your turn now; the console will wake you with a digest when <crew> speaks. Do not poll.` |
| `matev2 state <p> <crew>` | `auto mode: do not poll; end your turn and wait for the digest.` |
| `matev2 send <p> <crew> …` from the Mate (`Source: mate`) | `auto mode: end your turn; the digest will tell you when <crew> hands back.` |

In manual mode none of them print it, and neither does a `send` the captain runs from a shell.
The flag is read when the output is printed, not when the command started, because `crew spawn` can take minutes.
`cmd/matev2/auto_turn_test.go` checks both modes for all three commands.

### The manual says it once

- Section 4 names the extra line under each of the three commands.
- Section 7 step 4 splits on the mode, and says the spawn output tells the Mate which mode it is in.
  Manual: go to section 9 and supervise.
  Auto: write the captain one line that the work has started and end the turn; never run the `sleep 20` loop.
  Section 7 also shows the `--scout` spawn form beside the ship one.
- Section 9 now opens "Waiting is your job in manual mode, and only in manual mode".
  The old sentence about the watcher and daemon "both keep running regardless of mode" is gone.
  One sentence was added about running the loop in the foreground as the single command it is.
  The paragraph now ends by pointing auto mode at section 10.
- Section 10 has a new subsection, "Ending the turn in auto mode".
  It is the one place the rule is written down.
  It says why the rule exists: a composer is shut for the whole of a turn, `sleep` included.
  It says what to do: after spawning or answering, report in one line and end the turn.
  It lists what is forbidden: the `sleep 20` loop, running `state`/`peek` again, waiting for a hand-back.
  It names the only reason to stay in a turn: work already in hand, such as reviewing a `wait-mate` diff, merging it under `yolo`, or the rest of the same digest.
  It also says the `auto mode:` lines are this rule.
  "Acting on a digest" now points at it instead of repeating it.

`internal/mateassets` goldens were regenerated and the diff read; `digest_test.go` and the budget test stay green (rendered manual about 45 KB, well under budget).

### The proof

`TestLiveAcceptanceTwoProjects` now records the timeline, as `cmdConsole` does (`consoleWatcherWithTimeline`).
After the `blog` Crew is `finished`, `assertTheMateYieldedToTheDigest` checks four things:

1. `blog`'s `sent.log` holds at least one `app → mate` line starting `digest: `.
2. The Mate's turn rows are grouped into harness turns by `harness_turn_ref` (Claude's promptId).
   The harness turn that ran `crew spawn blog <crew>` has, as its `trigger_event_id`, the captain's line.
3. The harness turn that ran `matev2 merge blog <crew>` is a different turn, and its trigger is an `app` message starting `digest: `.
4. The merge turn started after the spawn turn ended.

Nothing but a digest can start a later `blog` turn, because the captain types once.
A digest can only get into a composer that is not Busy.
So point 3 is also the proof that the Mate ended the spawning turn.

On exit the test now also logs three things.
The daemon's last notice per project, and whether `.auto` is still set.
The `blog` Mate's composer, read the way `send.Send` reads it.
Each project's `incidents.log`.
Attempt 4 below could only be diagnosed because of these lines.

## Every attempt

All attempts used the same command, each on its own lab session, deleted afterwards:

```sh
LAB=fm-lab-matev2-w31-<tag>
(env -u CLAUDECODE herdr --session "$LAB" server >/dev/null 2>&1 &); sleep 3
TMPDIR=$(realpath /private/tmp) MATEV2_LIVE=1 MATEV2_HERDR_LIVE_SESSION=$LAB \
  go test -count=1 -v -timeout 40m -run 'TestLiveAcceptanceTwoProjects' ./cmd/matev2/
herdr session stop $LAB; sleep 1; herdr session delete $LAB
```

Times are UTC.

| # | Tree | Result | What the `blog` Mate did |
| --- | --- | --- | --- |
| 1 | tool-output lines and manual, first wording | PASS 211.78s | Reported and ended the spawn turn 26s after the request. Digest at 03:57:52, then a separate turn merged. |
| 2 | same | FAIL 730.57s | Reported and ended the spawn turn 33s after the request. Crew handed back. No digest in 11 minutes (cause found at #4). |
| 3 | + exit evidence in the test | PASS 230.25s | Reported and ended the spawn turn 25s after the request. Digest at 04:15:05, then a separate turn merged. |
| 4 | + main merged (task 30 outbox) | FAIL 679.92s | Reported and ended the spawn turn 26s after the request. Digest queued at 04:21:24, refused as mid-turn, `wedged` at 04:26:25. |
| 5 | + `claudeBusy` fix | FAIL 451.12s, in the `shop` half | Reported and ended the spawn turn 25s after the request. Digest at 04:34:07. The test failed earlier, in `shop`. |
| 6 | + a `resolve:` sentence in section 10 | PASS 239.25s | Reported and ended the spawn turn 26s after the request. Digest at 04:42:28, then a separate turn merged. |
| 7 | + main merged (M7, tasks 32-33); `resolve:` sentence reverted | FAIL 393.55s, in the `shop` half | Reported and ended the spawn turn 28s after the request. Digest at 04:47:55, merged 16s later. |
| 8 | + `shop` half waits for the escalation and types the captain's answer | FAIL 118.68s, in the `shop` half | Not reached. The `shop` Mate escalated, then relayed the answer through the brief rather than in the line. |
| 9 | + the relay may go through the brief | **PASS 254.52s** | Reported and ended the spawn turn 25s after the request. Digest at 05:01:08, then a separate turn merged. |
| 10 | same tree as 9 | **PASS 284.46s** | Reported and ended the spawn turn 27s after the request. Digest at 05:05:34, then a separate turn merged. |

The `blog` Mate ended its spawning turn in every run where it was reached.
None of those runs ran the supervision loop.
The tool-output wording was never changed, because it never needed to be.

### Attempts 2 and 4: an idle Mate read as busy

At the end of attempt 4 the `blog` Mate had plainly finished its turn (`✻ Sautéed for 26s · done 11:20 AM`), with a faint suggestion in its composer.
But the daemon said:

```text
daemon blog: auto=true sends=0 notice="auto digest for blog not delivered: target_blocked: agent mate-blog is mid-turn (⎿  • Working (2s • esc to interrupt)); nothing was typed: agent is mid-turn"
blog Mate composer: state=busy evidence="⎿  • Working (2s • esc to interrupt)"
```

That busy line is not the Mate's.
It is the Codex Crew's pane, printed by the Mate's own last tool call and quoted in the Mate's transcript:

```text
⏺ Bash(cat > backlog.md <<'EOF'
      # Backlog…)
  ⎿  • Working (2s • esc to interrupt)
     … +4 lines (ctrl+o to expand)
```

`claudeBusy` fell back to the shared seeds, and `esc to interrupt` matched the quoted line.
Attempt 2's pane shows the same shape: the Mate's last tool call printed `health: busy (• Working (6s • esc to interrupt))`.
So the same classifier went green in 1 and 3 and red in 2 and 4, depending only on what the Mate's last tool call printed.

The fix is in `internal/send`.
A Claude pane is busy only by its own signature: the spinner drawn at column 0, or the queued-message placeholder.
The shared seeds are no longer consulted for Claude.
Claude draws none of them itself, and everything it quotes is indented under `⏺` or `⎿`.
The capture is committed as `internal/send/testdata/screens/claude_idle_quoting_codex_busy.ansi`.
`TestClassifyComposerIgnoresAnotherHarnessBusyLineQuotedByClaude` fails on the old classifier with `state = "busy" (evidence "⎿  • Working (2s • esc to interrupt)")`.
It also checks the other direction: a quoted, indented Claude spinner is not busy, and the pane's own spinner at column 0 is.

### Attempts 5 and 7: the `shop` half

Neither failure involves auto mode or the digest.

In attempt 5 the `shop` Mate received the `[assign]`ed checkout question and sent the Crew a heuristic instead of an answer.
When the Crew asked again, it told the captain "I can't make this choice for you".
Attempt 6 added a sentence to section 10's `resolve:` steps: a `resolve:` line hands the Mate the choice, so it picks and says which.
Attempt 6 passed.

Then M7 (tasks 32-33) merged.
It made the `decision-authority` skill the single owner of this policy, and it escalates any `decides: captain` open decision "however obvious the answer looks".
The sentence from attempt 6 contradicted that skill, so it was reverted rather than left as a second policy.
In attempt 7 the Mate wrote the checkout choice into the brief as `decides: captain`, loaded `decision-authority` when the `[assign]` arrived, and escalated:

```text
04:48:06 | mate → user | I haven't answered it. Picking the checkout page is a product choice, I marked it as yours when I set the task up, and nothing I have points to one page over the other. […]
```

The test waits for the Mate to answer the Crew, so under M7's policy the `shop` half now fails every time at that step.
Decided 2026-09-24: `[assign]` hands the question to the Mate to handle, not to decide, and `decision-authority` stays.
`docs/mvp.md` section 5's sentence about `[assign]` now says so.
The test changed, not the policy.
After `[assign]` is delivered, the `shop` half waits for the Mate's escalation to the captain naming both pages.
It asserts the Mate sent the Crew no checkout choice.
Then it types the captain's answer into the Mate's pane unmarked; `shop` is in manual mode, so there is no `.auto` to clear.
Finally it waits for the Mate's `Source: mate` line to the Crew.
Attempt 8 failed on the relay: the Mate did not send the choice itself.
It ran `matev2 brief append` and sent the Crew a pointer: "The captain added to your task: read the words marked (added …) at the end of ## Captain's words in …/brief.md, then carry on with them."
That is the M7 way, so the test accepts the choice either in the relayed line or in the Crew's `brief.md`.
It also asserts that `main`'s README links `checkout-express.html`.

Attempt 9, `shop` `sent.log` from the question on:

```text
05:01:03 | mate → user      | I need you to pick which checkout page the Buy button links to.  The repository has two checkout pages, `pages/checkout-express.html` and `pages/checkout-classic.html` […]
05:01:04 | app  → mate      | resolve: buy1 asked: "checkout target is ambiguous: pages/checkout-express.html or pages/checkout-classic.html" — read …/crews/buy1.status, decide, and answer with matev2 send shop buy1 "<one line>"
05:01:05 | app  → mate      | (the hook's copy of the same line)
05:01:12 | mate → user      | I'm not answering this one, because it's yours to decide and I have nothing to base a choice on. […]
05:01:12 | user → mate      | Use the express checkout page, pages/checkout-express.html.
05:01:17 | mate → crew:buy1 | The captain added to your task: read the words marked (added 2026-09-24 12:01 +07) at the end of ## Captain's words in …/crews/buy1/brief.md, then carry on with them.
05:02:25 | mate → user      | The Buy button is in and ready for you to merge, in branch `matev2/buy1`.  README.md now ends with `[Buy](pages/checkout-express.html)`. […]
```

Then the console merge: `shop/buy1: merged 1 commit(s) into main (b72ae0c..8dd2d0c); crew finished, worktree and branch removed`.
Attempt 10 took the same path (`05:05:40` escalation, `05:05:41` the captain's line, `05:05:45` the relay, merge `ec6d712..7c06288`).

## The two final runs, in full

Attempts 9 and 10 ran back to back on the same tree, each on its own lab session.

Attempt 9, `blog` `sent.log`:

```text
04:59:56 | user → mate | Add the line "Published with matev2" to the end of README.md in project blog. Use a crew.
05:00:24 | mate → user | I've started the work to add "Published with matev2" as the last line of README.md, and it's under way now. I'll review it when it's done, and because merging is delegated on this project […]
05:01:08 | app  → mate | digest: 1 item(s) — rd1 wait-mate: "ready in branch matev2/rd1" — status files under <workspace>/.matev2/projects/blog/crews; act per AGENTS.md section 10
05:01:09 | app  → mate | digest: 1 item(s) — rd1 wait-mate: "ready in branch matev2/rd1" — status files under <workspace>/.matev2/projects/blog/crews; act per AGENTS.md section 10
05:01:26 | mate → user | The line is added and merged into main. README.md now ends with "Published with matev2", and nothing else in the file changed. […]
05:03:50 | user → mate | Thanks - that is all for today.
05:03:50 | app  → mate | auto mode off: user prompt
```

Attempt 9, the `blog` Mate's turn boundaries:

```text
#0 6896ca76-…  04:59:59.351 .. 05:00:24.503  4 model calls  trigger user:blog: Add the line "Published with matev2" …   (ran crew spawn blog rd1)
#1 ca0a1c4b-…  05:01:11.751 .. 05:01:24.228  5 model calls  trigger app:blog: digest: 1 item(s) — rd1 wait-mate: …     (ran matev2 merge blog rd1)
```

Attempt 10, `blog` `sent.log`:

```text
05:04:22 | user → mate | Add the line "Published with matev2" to the end of README.md in project blog. Use a crew.
05:04:52 | mate → user | I've started adding "Published with matev2" to the end of README.md, and it will go on branch `matev2/rm1`. Merging is delegated to me on this project, so I'll land it on main […]
05:05:34 | app  → mate | digest: 1 item(s) — rm1 wait-mate: "ready in branch matev2/rm1" — status files under <workspace>/.matev2/projects/blog/crews; act per AGENTS.md section 10
05:05:34 | app  → mate | digest: 1 item(s) — rm1 wait-mate: "ready in branch matev2/rm1" — status files under <workspace>/.matev2/projects/blog/crews; act per AGENTS.md section 10
05:05:46 | mate → user | README.md now ends with the line "Published with matev2", and the change is merged into main as one commit. I checked it before merging: that line is the only thing that changed.
05:08:46 | user → mate | Thanks - that is all for today.
05:08:46 | app  → mate | auto mode off: user prompt
```

Attempt 10, the `blog` Mate's turn boundaries:

```text
#0 60df9db9-…  05:04:25.533 .. 05:04:52.284  4 model calls  trigger user:blog: Add the line "Published with matev2" …   (ran crew spawn blog rm1)
#1 b31391d9-…  05:05:37.110 .. 05:05:44.339  3 model calls  trigger app:blog: digest: 1 item(s) — rm1 wait-mate: …     (ran matev2 merge blog rm1)
```

Each digest appears twice: once from the outbox, once from the Mate's own `UserPromptSubmit` hook, so it reached the model.
Both Crews finished through the Mate's `matev2 merge`.
`blog`'s `sent.log` holds exactly one captain line before the last one.
Earlier passing attempts 1, 3 and 6 had the same shape (spawn turn, digest, separate merge turn).

## Section 9's loop and Claude Code's `sleep` block

Task 30 measured that Claude Code refuses a foreground `sleep 45`.
In the transcripts the refusal reads `Blocked: standalone sleep 45. To wait for a condition, use Monitor with an until-loop …`, so only a `sleep` standing alone is refused.
In attempts 1-3 the `shop` Mate supervised in manual mode with `sleep 20; matev2 state …` and `for i in $(seq 1 12); do sleep 20; …; done`.
Every one of those ran in the foreground and returned its `state:` line after the wait.
The loop stays as written, with the one sentence added to section 9.

## Result

Two consecutive full passes on the final tree: attempts 9 (254.52s) and 10 (284.46s).
`make check` exits 0 on the same tree.
