# M3 task 18b: the crew state vocabulary of section 4b, live

Task 18b of `docs/mvp.md`, measured 2026-09-18 on Herdr 0.8.2, Claude Code (Mate) and Codex (Crew).

The claim being proved: after replacing the five-verb status vocabulary with section 4b's seven states, the M2 acceptance loop still runs end to end - one plain sentence to the Mate produces a real Crew that reports `wait-mate:` rather than `done:`, and the branch carries the change.
Nothing in the test tells the Crew which verb to use; the verb comes from the rendered `brief.md` alone, and the Mate's reading of it from the rendered `AGENTS.md` alone.

## How it was run

```sh
LAB=fm-lab-matev2-w18b-66799
(env -u CLAUDECODE herdr --session "$LAB" server >/dev/null 2>&1 &)
sleep 4
TMPDIR=$(realpath /private/tmp) MATEV2_LIVE=1 MATEV2_HERDR_LIVE_SESSION=$LAB \
  go test -count=1 -v -run 'TestLiveAcceptanceMateRunsATask' ./cmd/matev2/
herdr session stop $LAB; sleep 1; herdr session delete $LAB
```

`env -u CLAUDECODE` is the nested-session lesson of section 7: a Herdr server started from inside a running Claude Code exports `CLAUDECODE=1` to every pane it later opens.

## Result

`--- PASS: TestLiveAcceptanceMateRunsATask (86.07s)`, exit 0.

The captain's one line, and everything that followed from it:

```text
captain → mate: Add a line "Built with matev2" to the end of README.md in project shop. Use a crew.

the mate spawned crew "readme1"
crew readme1: branch matev2/readme1, task "Add 'Built with matev2' line to README.md"

crew status file:
    working: updating README and preparing local commit
    wait-mate: ready in branch matev2/readme1

commits on matev2/readme1:
    8c0c5272db0a3366cf035c19cdeddf7ba5af2bb5 Add matev2 attribution to README

README.md on matev2/readme1:
    # shop

    A tiny shop.
    Built with matev2

mate → user: The diff is exactly what was asked: one line, "Built with matev2",
appended to README.md, nothing else touched. The README update is committed and
ready in branch `matev2/readme1`. It's not merged — that's your call whenever
you'd like to land it.
```

## What this establishes, and what it does not

Established:

- A real Codex Crew, given the new brief, writes `working:` and then `wait-mate:` and never reaches for `done:`, `blocked:` or `failed:`. The two verbs it used are the two the brief names.
- `wait-mate:` does not end the task. The Crew stayed open - in the tree, in `crew list`, out of the inbox - and the Mate reported the branch as ready and stopped, which is the behaviour section 4b's "closing is the user's or the Mate's decision" asks for.
- The Mate read the new manual and reported in outcome language without being told the vocabulary changed.

Not established by this run:

- `blocked`. It is the observer's state, opened from `incidents.log` by `internal/watch` (task 18), and this test does not stall a Crew. The rule that an open incident outranks the Crew's own last verb is covered by unit tests over a real `incidents.log` (`internal/query.TestLoadReadsBlockedFromTheObserversOpenIncidents`), and task 18's own live test covers the observer producing one.
- `finished` and `failed`. `crew stop` was not run here: the acceptance test ends where the Mate reports, and closing is the captain's decision. The teardown paths are covered by `internal/spawn`'s unit tests, including the refusal-before-the-kill rule, and by `TestLiveCrewTeardown`.

## The one thing this run made visible

Adding the seven-state table to `AGENTS.md` pushed the rendered manual past Codex's `project_doc_max_bytes` (32768) and every Codex `mate start` refused with `required context exceeds delivery limit`.
That is the correct refusal - matev2 will not let Codex silently truncate the tail of the manual - but it means the manual has almost no room left.
`internal/mateassets/budget_test.go` now fails at the template instead, with the arithmetic in its comment; section 7 of `docs/mvp.md` records the measurement.
