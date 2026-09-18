# M3 task 20: the auto-mode policy in AGENTS.md, live

Task 20 of `docs/mvp.md`, measured 2026-09-18 on Herdr 0.8.2, Claude Code (Mate) and Codex (Crew).

The claim being proved: a real Mate, taught only by the rendered `AGENTS.md` section 10, acts on a `digest:` line the way the manual prescribes - reads the crew's own status file rather than the truncated excerpt, answers the crew's `needs-decision` itself with `matev2 send`, and does **not** run `crew stop` on the crew's `wait-mate`, because section 4b and section 9's "Closing a Crew" reserve that word for the captain.

## How it was run

```sh
LAB=fm-lab-matev2-w20-8072
(env -u CLAUDECODE herdr --session "$LAB" server >/dev/null 2>&1 &)
sleep 3
TMPDIR=/private/tmp MATEV2_LIVE=1 MATEV2_HERDR_LIVE_SESSION=$LAB \
  go test -count=1 -v -run 'TestLiveAutoPolicy' ./cmd/matev2/
herdr session stop $LAB; sleep 1; herdr session delete $LAB
```

`env -u CLAUDECODE` and a symlink-free `TMPDIR` are the section 7 lessons: a Herdr server started from inside a running Claude Code exports `CLAUDECODE=1` to every pane it later opens, and Herdr reports a pane's cwd already resolved through `/private`.

## Result

`--- PASS: TestLiveAutoPolicyMateAnswersADigest (75.75s)`, exit 0, first attempt - no manual-prose fix was needed after the fact.

```text
Mate mate-shop is running on claude in pane w1:p1
spawned crew crew-k3 in pane w1:p2 (branch matev2/k3)
inbox item: k3 needs-decision choose colour red or blue for the button
digest: digest: 1 item(s) — k3 needs-decision: "choose colour red or blue for the button" — status files under /private/tmp/TestLiveAutoPolicyMateAnswersADigest2463133098/001/.matev2/projects/shop/crews; act per AGENTS.md section 10
mate -> crew:k3 "Use blue for the button."
crew finished: wait-mate: chose blue
```

The crew's brief: `Append needs-decision: choose colour red or blue for the button to the status file and stop; when the Mate answers, append wait-mate: chose <answer> and stop`.

The digest the daemon delivered to the Mate's own pane, with the sentinel `⟦matev2⟧ ` prefix stripped for this record:

```text
digest: 1 item(s) — k3 needs-decision: "choose colour red or blue for the button" — status files under <workspace>/.matev2/projects/shop/crews; act per AGENTS.md section 10
```

The Mate's reply to the crew, recorded in `sent.log` as `Source: mate` to `crew:k3`:

```text
Use blue for the button.
```

The crew's own resulting status line:

```text
wait-mate: chose blue
```

`crew k3`'s meta after all of this was still `state=spawned` - the assertion the test makes explicitly - so the Mate reported the decision back through the digest's grammar without ever running `crew stop`.

## What this establishes, and what it does not

Established:

- A real Mate reads a `digest:` line's `needs-decision` item, decides the technical choice itself (the brief gave it nothing to escalate - picking a button colour is not a merge, a scope change, or money), and answers with exactly one `matev2 send`.
- The crew treats that answer as an ordinary new prompt and reaches `wait-mate:` on its own, never `done:` or any word outside the seven-state vocabulary.
- The Mate does not close the crew on its own initiative after a digest's `wait-mate`. Section 10's rewritten policy ("closing the Crew is the captain's word, not yours") held without prompting; the crew's meta stayed `spawned`.
- One tick of the daemon, one digest, one reply from the Mate to the crew, and the whole loop finished inside the auto-mode manual's own rules - no polling loop was needed or used by the test to get there.

Not established by this run:

- The `blocked` branch of the policy (peek, `stuck-crew-recovery`, the recovery ladder) and the `yolo`-gated merge exception on `wait-mate`. Both are new prose in this task; task 18's live test already proves the observer opens a `blocked` incident on a real stalled Codex crew, and `stuck-crew-recovery`'s own steps are unit-covered structurally through the rendered-skill golden tests, but no live run in this task drove a Mate through either path end to end.
- The `wedged`-incident skip and the "unmarked prompt ends auto mode" rule - both already proved live by task 19's `TestLiveAutoDigestReachesTheMate`, which this task's test does not repeat.
- Multi-item digests (the `· +N more` cap, the fixed blocked/needs-decision/wait-mate ordering across several crews in one line). The acceptance scenario specified for this task is a single crew with a single question; the ordering rule and the five-item cap are covered by `internal/autopilot`'s own unit tests and by `internal/mateassets.TestRenderAgentsSection10TeachesDigestGrammar`, not by a live multi-crew run.

## Lesson

None. The manual's prose as first written was sufficient for the Mate to answer correctly and to leave the crew open; no iteration on section 10's wording was required after seeing a real run, which is itself worth recording since the task's own instructions anticipated a fix-and-rerun cycle.
