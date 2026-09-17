# M2 acceptance: the Mate runs a task end to end

Task 17 of `docs/mvp.md`, measured 2026-09-17 on Herdr 0.8.2, Claude Code (Mate) and Codex (Crew).

The claim being proved: one plain sentence typed into the Mate's pane, by a human, with no scripted commands and no test-side nudging, ends with a Crew's commit on a branch and the Mate reporting it back.
Everything between those two points comes from the rendered `AGENTS.md` alone.

## How it was run

```sh
LAB=fm-lab-matev2-17-r1
(herdr --session "$LAB" server >/dev/null 2>&1 &)
sleep 3
TMPDIR=$(realpath /private/tmp) MATEV2_LIVE=1 MATEV2_HERDR_LIVE_SESSION=$LAB \
  go test -count=1 -v -timeout 15m -run TestLiveAcceptance ./cmd/matev2/
herdr session stop $LAB; sleep 1; herdr session delete $LAB
```

The test is `TestLiveAcceptanceMateRunsATask` in `cmd/matev2/acceptance_live_test.go`.
It builds a temp workspace with a real `git init` repo containing `README.md`, starts a Claude Mate, types one line into its pane through `send.Send` as the user (no sentinel), and then only watches files:

1. a `crews/*.meta` appears - the Mate spawned a Crew rather than doing the work itself;
2. that crew's `.status` reaches `done:`;
3. the crew's branch carries a commit that puts the line in `README.md`;
4. the Mate's own Stop hook wrote a `mate → user` line in `sent.log` naming the branch or saying the work is ready.

The captain's line, verbatim:

```text
Add a line "Built with matev2" to the end of README.md in project shop. Use a crew.
```

## Result

Two runs, both green, on separate lab sessions. No manual change was forced by a live failure.

| Run | Lab session | Wall clock | Outcome |
| --- | --- | --- | --- |
| 1 | `fm-lab-matev2-17-r1` | 100.98s | PASS |
| 2 | `fm-lab-matev2-17-r2` | 85.95s | PASS |

### Run 1 transcript

```text
=== RUN   TestLiveAcceptanceMateRunsATask
    mate mate-shop in pane w1:p1, cwd /private/tmp/TestLiveAcceptanceMateRunsATask2151919661/001/.matev2/projects/shop/mate
    captain → mate: Add a line "Built with matev2" to the end of README.md in project shop. Use a crew. (empty → enter ×1 → busy)
    the mate spawned crew "readme1"
    crew readme1: branch matev2/readme1, worktree /private/tmp/TestLiveAcceptanceMateRunsATask2151919661/001/.worktrees/shop-readme1, task "Add 'Built with matev2' line to README.md"
    crew status file:
        working: updating README and preparing local commit
        done: ready in branch matev2/readme1
    commits on matev2/readme1:
        6b72260ae61d5171f953a4dd6ae12f55ebaefecd docs: add matev2 attribution
    README.md on matev2/readme1:
        # shop

        A tiny shop.
        Built with matev2
    mate → user: The line is added and ready in branch `matev2/readme1` — README now ends with "Built with matev2". It's waiting for you to merge.
    sent.log user → mate: Add a line "Built with matev2" to the end of README.md in project shop. Use a crew.
    sent.log mate → user: The line is added and ready in branch `matev2/readme1` — README now ends with "Built with matev2". It's waiting for you to merge.
--- PASS: TestLiveAcceptanceMateRunsATask (100.98s)
```

### Run 2 transcript

```text
=== RUN   TestLiveAcceptanceMateRunsATask
    mate mate-shop in pane w1:p1, cwd /private/tmp/TestLiveAcceptanceMateRunsATask4257103377/001/.matev2/projects/shop/mate
    captain → mate: Add a line "Built with matev2" to the end of README.md in project shop. Use a crew. (empty → enter ×1 → busy)
    the mate spawned crew "readme1"
    crew readme1: branch matev2/readme1, worktree /private/tmp/TestLiveAcceptanceMateRunsATask4257103377/001/.worktrees/shop-readme1, task "Add 'Built with matev2' line to README.md"
    crew status file:
        working: verified isolated worktree
        done: ready in branch matev2/readme1
    commits on matev2/readme1:
        1f934f406385c07b95a6e4231a5f98c24dd7c319 docs: add Mate attribution
    README.md on matev2/readme1:
        # shop

        A tiny shop.
        Built with matev2
    mate → user: The README now ends with the line "Built with matev2" and is ready in branch `matev2/readme1`. I've checked the diff and it's exactly that one line added, nothing else touched. It's waiting for you to merge.
--- PASS: TestLiveAcceptanceMateRunsATask (85.95s)
```

## What the evidence shows

**The Mate delegated.**
It never touched `shop/README.md` itself; the only commit is on `matev2/readme1`, authored inside the Crew's worktree, and the primary checkout's `main` is untouched.

**The status protocol carried the whole conversation.**
Two lines, no more: one `working:` phase change and one `done: ready in branch matev2/readme1`.
That is the brief's own instruction (`assets/crew/brief.md.tmpl`) being followed literally, and it is what `matev2 state` then read as `done`.

**The Mate reviewed before reporting.**
Run 2's report - "I've checked the diff and it's exactly that one line added, nothing else touched" - is the section 9 review step (read-only `git -C <worktree> log/diff`) showing up in the captain-facing sentence.

**The Mate stopped at the branch.**
Both reports end by handing the merge back to the captain, which is what the manual requires while `matev2 merge` does not exist (tasks 21-22).

**The Stop hook closed the loop.**
The `mate → user` lines above are `sent.log` entries written by `matev2 hook mate-stop`, not by the test reading a pane.

## Manual changes this forced

No change was forced by a failed live run; both runs passed as written.
One change was made before the first run, from reading the code rather than from a failure, and it is the one the acceptance would otherwise have deadlocked on:

- **Section 9, "Waiting is your job, not someone else's."**
  Nothing wakes a Mate today: the console observer is task 18 and the auto daemon is task 19, so in a supervised project with no console attached a Mate that ends its turn after spawning is never called again.
  The manual now tells the Mate to stay in the loop itself with `sleep 20; matev2 state <project> <id>`, repeated while the line says `working`.
  Section 7 backs it up by ending the spawn checklist with "do not stop here".
  This is a manual workaround for a missing mechanism, and it should be revisited when task 18 lands.

Two other manual decisions are worth recording because the acceptance depends on them:

- The manual writes every command with the binary's **absolute** path (`{{.MatevBin}}`), because a Mate's pane has no guarantee `matev2` is on `PATH`; the live Mate used the absolute path every time.
- Section 7 warns that `crew spawn` takes a minute or more and that a caller asking for a timeout should be given at least five minutes, because a killed half-spawn leaves a record to clean up.

## Debt paid in the same task

`harness.Claude.BuildLaunchSpec` no longer requires a `ContextPath` when the caller sets `AgentSpec.ManualInCwd`.
The Mate's launch now passes no `--append-system-prompt-file` at all: `<mate>/CLAUDE.md` is `@AGENTS.md` and Claude loads it from its cwd, so the manual reaches the model exactly once instead of twice.
Codex was checked and carries no equivalent debt: it has no cwd auto-load of `CLAUDE.md`, its one delivery mechanism *is* a file in the cwd, and it prefers `AGENTS.override.md` over `AGENTS.md`, so the manual is loaded once there too.
`spawn` keeps writing `AGENTS.override.md` for a Codex Mate because that is the name Codex reads, and because the same override rule is load-bearing for a Crew worktree, where a tracked `AGENTS.md` must be shadowed.
