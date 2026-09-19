# M4 task 24: two projects, two modes, end to end

Task 24 of `docs/mvp.md`, measured 2026-09-19 on Herdr 0.8.2, Claude Code 2.1.278 (Mate) and codex-cli 0.154.0 (Crew).

Two claims are proved here.
The first is the debt task 24 carried: `[assign]` works on a Mate that has not had a turn yet.
The second is the MVP capstone: one workspace, two registered repositories, one Mate each, `shop` in manual mode and `blog` in auto mode with `yolo` on, driven from the captain's own gestures to a merge on both default branches.

## How it was run

```sh
LAB=fm-lab-matev2-w24-r1
(env -u CLAUDECODE herdr --session "$LAB" server >/dev/null 2>&1 &)
sleep 3
TMPDIR=$(realpath /private/tmp) MATEV2_LIVE=1 MATEV2_HERDR_LIVE_SESSION=$LAB \
  go test -count=1 -v -timeout 25m \
  -run 'TestLiveAssignWorksOnAColdMate|TestLiveAcceptanceTwoProjects' ./cmd/matev2/
herdr session stop $LAB; sleep 1; herdr session delete $LAB
```

`env -u CLAUDECODE` and a symlink-free `TMPDIR` are the section 7 lessons.
The two required runs were `fm-lab-matev2-w24-r1` and `fm-lab-matev2-w24-r2`, each on a lab session created for it and deleted after it, with no code change between them.

| Run | `TestLiveAcceptanceTwoProjects` | `TestLiveAssignWorksOnAColdMate` | Package |
| --- | --- | --- | --- |
| 1 | PASS 353.31s | PASS 40.08s | ok 393.589s, exit 0 |
| 2 | PASS 350.92s | PASS 51.92s | ok 403.317s, exit 0 |

Both acceptance runs finished inside the task's fifteen-minute budget with about nine minutes to spare.
`make check` exits 0 on the same tree.

## Part A: `[assign]` on a cold Mate

### What was wrong

Measured before the fix: clicking `[assign]` on a Claude Mate that had not had a turn yet was refused with `state_conflict: agent mate-shop is showing a screen mate cannot name`.

The cause is not the welcome box.
It is the pane's width.
`streamTerminalSize` gives a Mate inside a 120x36 console **65 columns**, because the 54-column rail and its divider come off the width, and at 65 columns Claude draws its composer rule exactly as wide as the pane.
A row that fills the pane is a wrapped row as far as `herdr agent read --source recent-unwrapped` is concerned, so the rule above the composer, the composer itself and the rule below it arrive joined into one line:

```text
             ─────────────────────────────────────────────────────────────────❯                                                                ─────────────────────────────────────────────────────────────────
```

`locateClaudeComposer` looks for a line that starts with `❯` and has a rule on the line above and the line below, and after that join there is no such line.
The box is still drawn; only the line breaks are gone.

The captures are committed verbatim as `internal/send/testdata/screens/claude_startup_splash.txt` (65x33, empty), `claude_startup_splash_pending.txt` (the same pane with `half typed` in it) and `claude_startup_splash_80x24.txt` (the same cold Mate at 80x24, where the rules happened to survive on their own lines).
They were taken through the Console's own session stream, and the first two classified `unknown` against the classifier as it stood, which is the reproduction.

### The fix

`splitAtClaudeRules` puts every run of at least ten `─` back on a line of its own before the composer is located, dropping the whitespace either side of a rule because that is the padding of the row the rule was drawn on rather than a row of its own.
A rule that already had a line to itself comes back unchanged, so every existing screen fixture classifies exactly as before.
The split is local to `locateClaudeComposer`, so the busy scan keeps reading the rows as the harness drew them.

### Proof

`TestLiveAssignWorksOnAColdMate`, 40.08s and 51.92s.
A real Claude Mate is started through `consoleAction`, a Codex crew appends `needs-decision`, the Console's stream is opened at `console.StreamSize(SessionTargetMate, 120, 36)`, and the very first action taken on that Mate is `ActionResolve`.

```text
inbox item: k3 needs-decision pick A or B
a 120x36 console gives the Mate a 65x33 pane
cold Mate composer: state=empty evidence="❯"
resolve action: mate-shop asked to decide in 1 enter(s); the Mate answers the crew with matev2 send
sent.log app → mate: resolve: k3 asked: "pick A or B" — read <workspace>/.matev2/projects/shop/crews/k3.status, decide, and answer with matev2 send shop k3 "<one line>"
sent.log app → mate: resolve: k3 asked: "pick A or B" — read <workspace>/.matev2/projects/shop/crews/k3.status, decide, and answer with matev2 send shop k3 "<one line>"
```

The test asserts `sent.log` is empty before the action, so the Mate really has had no turn.
The two identical `Source: app` lines are the evidence section 7 asks for: the first is written by the action once the composer cleared, the second by the Mate's own `UserPromptSubmit` hook when the model read the line.

The width is asserted rather than assumed.
`console.StreamSize` was exported for that: a live proof that opens the stream at the console's own 120 columns measures a pane no reader ever has, which is exactly how this debt stayed hidden through three earlier live tests that all passed.

## Part A': the faint suggestion, found by the acceptance

The acceptance run found a second classifier defect of the same family, and deadlocked on it twice before it was fixed.

After a turn that asks the captain a question, Claude Code 2.1.278 offers an answer **inside the composer**, drawn faint:

```text
❯ \x1b[0m\x1b[2mUse checkout-express.html\x1b[0m
```

`--format text` renders that as `❯ Use checkout-express.html`, which is indistinguishable from a half-typed human line, so `send.Send` refused to type over it - correctly, on what it could see.
`[clear composer]`'s Ctrl+U did not help, because there is nothing there to clear.
The whole difference is SGR 2.
Observed strings across runs: `Use checkout-express.html`, `use the express checkout page`, `Show me the current README.md`.

The fix is `runtime.Adapter.ReadAgentStyled`, the same snapshot in `--format ansi`, used by `internal/send`, `internal/watch` and `matev2 state` - the three callers that classify rather than display.
`ClassifyComposer` strips the attributes for every structural rule it already had, and uses them for one question only: whether every visible rune of the composer's content was drawn faint.
It is fail-closed - a screen with no attributes, a content string it cannot find, or one non-faint rune all leave the verdict at `pending`, because typing over somebody's unsubmitted line is the mistake the state exists to prevent.

The capture is committed as `internal/send/testdata/screens/claude_ghost_suggestion.ansi`, and the test asserts both halves: the styled screen classifies `empty`, and the plain rendering of the same screen still classifies `pending`.

One trap is worth naming, because a test caught it and a reader will not.
Claude draws its own composer text with `38;2;255;255;255`, whose second parameter is the `2` that selects direct RGB, not the `2` that means faint.
A scanner that read SGR parameters independently would call a person's typing a suggestion and type over it.
`applySGR` consumes the extended-colour forms.

## Part B: the two-project acceptance

`TestLiveAcceptanceTwoProjects`.
Two real git repositories under one workspace.
`shop` holds a README, two checkout pages that nothing marks as canonical, and three documentation files of which two mention ESP32.
`blog` holds a README.
`matev2 project yolo blog on` runs through the command itself before either Mate starts, because the flag is read when the manual is rendered.

The observer (`internal/watch`) and the auto daemon (`internal/autopilot`) are started exactly as `cmd/matev2/console.go` starts them, and every console gesture goes through `consoleAction`.
Nothing in the test spawns a Crew: spawning is the Mate's job.

### shop, manual mode: the ship task

The captain types one line and nothing else until the merge:

```text
Add a Buy button to the end of README.md in project shop, linking to our checkout page. Use a crew.
```

Run 1, with the Mate's own words as `sent.log` recorded them:

```text
10:43:32 | user  → mate     | Add a Buy button to the end of README.md in project shop, linking to our checkout page. Use a crew.
10:45:23 | mate  → user     | The Buy button work is started but paused on a decision only you can make. […] Tell me which page the Buy button should link to […]
10:45:26 | app   → mate     | resolve: buybtn asked: "what is the checkout page URL for the Buy button?" — read <workspace>/.matev2/projects/shop/crews/buybtn.status, decide, and answer with matev2 send shop buybtn "<one line>"
10:45:26 | app   → mate     | resolve: buybtn asked: "what is the checkout page URL for the Buy button?" — read <workspace>/.matev2/projects/shop/crews/buybtn.status, decide, and answer with matev2 send shop buybtn "<one line>"
10:46:00 | mate  → crew:buybtn | Decision: link to whichever checkout page the site itself treats as the default (linked from the index page or nav); if neither is favoured, use pages/checkout-classic.html as a relative link. Add the button, commit, and hand back with wait-mate.
10:47:05 | mate  → user     | The Buy button is in place and ready for you in branch `matev2/buybtn`. […] the button links to the classic checkout page (`pages/checkout-classic.html`). […] merge the branch when you're ready and I'll close out the task.
```

Every crew status line of that task:

```text
working: verifying isolated worktree and task brief
needs-decision: what is the checkout page URL for the Buy button?
wait-mate: ready in branch matev2/buybtn
```

The merge line, from `ActionMerge` through `consoleAction`:

```text
shop/buybtn: merged 1 commit(s) into main (aa900f3..6b8ee07); crew finished, worktree and branch removed
```

`main` afterwards:

```text
6b8ee07 docs: add Buy link to classic checkout fallback

# shop

A tiny shop.

**[Buy](pages/checkout-classic.html)**
```

Run 2 took the same path with different words and a different choice:

```text
working: verifying isolated worktree and task brief
needs-decision: repo has two checkout routes, /pages/checkout-express.html and /pages/checkout-classic.html; choose one
wait-mate: ready in branch matev2/buybtn; used /pages/checkout-express.html because no main entry point links a checkout
```

```text
shop/buybtn: merged 1 commit(s) into main (dd1762e..0405ddb); crew finished, worktree and branch removed
0405ddb docs: add buy button
[**Buy**](/pages/checkout-express.html)
```

Asserted from files at each step, never from the pane: `crews/<id>.meta` reads `state=spawned` when the record appears; a `working` status line arrives; a `needs-decision` line arrives and `query.LoadBox` shows it as the project's single inbox item; the `resolve:` line is in `sent.log` as `Source: app`; the Mate's answer is in `sent.log` as `Source: mate` to `crew:buybtn`; the crew's `wait-mate` line arrives; the Mate's report to the captain is a line written **after** the hand-back; `main` is byte-for-byte where it started at that moment and the crew is not `finished`; and only then does the merge run.

### shop, manual mode: the scout task

```text
Find out which files mention ESP32 and write me a short report.
```

Run 1's crew status lines and report:

```text
working: verifying isolation and locating ESP32 references
wait-mate: report at <workspace>/.matev2/projects/shop/crews/esp32/report.md
```

```markdown
# ESP32 reference audit

- `docs/hardware.md` — 1 match. Describes a bench rig containing an ESP32 devkit and a relay board.
- `firmware/sensor.md` — 1 match. Identifies the sensor board's module as an ESP32-WROOM.

ESP32 is referenced only in concise hardware/firmware documentation, not in application source or
configuration. It appears to be the microcontroller platform for the project's sensor/relay hardware setup.
```

The Mate relayed the findings as findings, then the captain typed `close it`, the Mate ran `crew stop`, the crew's meta read `finished`, and `crews/esp32/report.md` was still on disk afterwards.
`main` did not move during the scout task, which the test asserts: a scout commits nothing and merges nothing.

### blog, auto mode with yolo on

The captain types one line that needs no decision and then touches nothing:

```text
Add the line "Published with matev2" to the end of README.md in project blog. Use a crew.
```

Run 1:

```text
10:43:29 | user  → mate     | Add the line "Published with matev2" to the end of README.md in project blog. Use a crew.
10:45:10 | mate  → user     | Done. README.md in project blog now ends with the line "Published with matev2", and the change is merged into `main`. […]
10:49:02 | user  → mate     | Thanks - that is all for today.
10:49:02 | app   → mate     | auto mode off: user prompt
```

```text
working: verifying isolated worktree and applying README update
wait-mate: ready in branch matev2/rd1
834cd6a Publish README
```

Run 2's were `working: verifying isolated worktree`, `wait-mate: ready in branch matev2/rd1`, `4f64bbe Append publishing marker`.

Asserted: the crew's meta reads `finished`, `main` advanced and carries the requested line, and `sent.log` holds **exactly one** `Source: user` entry in the whole project - the original request - which is checked before the last line is typed.
Then the captain types an unmarked line and `.auto` is gone within one poll, recorded by the Mate's own hook as `auto mode off: user prompt`.

One ordering detail is load-bearing and was a bug in the first run of this test.
`.auto` is turned on **after** the captain's request has reached the model, not after `send.Send` returned.
The hook that deletes `.auto` runs when Claude reads the prompt; a flag set in between is deleted a second later by the captain's own request, and the project silently runs in manual mode for the rest of the test.
The event to wait for is the hook's own `Source: user` line in `sent.log`.

## What the Mate's behaviour forced into the manual

Every change below was made because a real run did something the manual had not prepared it for, and every one of them was followed by a rerun.

1. **`matev2 diff` was documented as not yet available.**
   Section 4's "Not yet available" still said `matev2 diff <crew>` lands in a later task, three tasks after it landed, and section 9 told the Mate to review with raw `git -C <worktree>`.
   Section 4 now documents the command, and section 9's review step calls it.

2. **A scout's report had no home.**
   Section 5 said a scout "ends in a report file in the Crew's own directory" and named no path, and the Crew template's definition of done was ship-shaped only ("complete only when committed on this branch").
   A Crew told to investigate had nothing to write to and a rule telling it to commit.
   Section 5 and section 6 now name `{{.CrewsDir}}/<id>/report.md` and say the Mate must put that absolute path in the brief, because it is the one part of the task shape the Crew cannot work out from its own worktree.
   The Crew template now has two shapes of done, code and knowledge.

3. **The Crew shipped a guess instead of asking.**
   First run: the Crew found two checkout pages, picked "the natural target", committed, and mentioned the assumption afterwards.
   Template rule 5 now says the quiet part: *do not settle it by taking whichever option looks most natural and mentioning it afterwards; once you have shipped a guess it is indistinguishable from an instruction you were given, and the only moment anyone could have corrected it has passed.*
   Both required runs ended in `needs-decision` at that point.

4. **The Crew wrote no `working:` line.**
   "Report sparingly" was read as "report nothing until you are done", which leaves a Crew that started work and one whose harness never came up looking identical.
   Template rule 3 now asks for one `working:` line as the Crew's very first action, and keeps "sparingly" for everything after it.

5. **The Mate read the project and then asked the captain.**
   Second run: the Mate ran `ls`, `cat` and `find` over `shop/` to work out which checkout page was canonical, decided it could not, and ended its turn on Claude Code's interactive question widget - which holds the composer open, so the captain's next line, `[assign]` and the daemon were all locked out.
   The task did not fail; it stopped.
   Three rules came out of that one screen:
   - section 1 rule 2 now covers reading as well as writing: what the Mate knows about the project comes from `PROJECT.md`, a Crew's report and `matev2 diff`, never a look of its own;
   - section 5 now says an unanswered question in the request is not a reason to stop - name the gap in the brief, dispatch, and let the Crew reach it and ask, because a question in the captain's box costs them nothing while they are not looking whereas a Mate that stops to ask costs them the whole task;
   - section 13 now says everything the Mate says to the captain is plain text and then the turn ends, and never a widget that waits for a selection, because *a pane held open by a widget cannot receive that line - not from the captain, not from `[assign]`, not from the daemon.*

6. **Sections 9 and 10 contradicted each other again.**
   Section 9 told the Mate to poll a Crew in manual mode and act on what it found; section 10 said "in manual mode, never act on a Crew event on your own".
   This is the second time these two sections have disagreed (the first is recorded in section 7 under task 20).
   Section 10 now draws the line where it actually falls: a Crew the Mate dispatched and is supervising is the Mate's to act on, and what manual mode forbids is reaching for anything else - another Crew's question, an incident, the captain's box.

## What this establishes, and what it does not

Established:

- The whole of section 5's manual mode, driven from the console's own seams on a real project: intake, brief, spawn, a Crew question reaching the inbox, `[assign]`, the Mate answering the Crew, the hand-back, the report, and a merge that closes the Crew.
- A ship Crew and a scout Crew of the same Mate, in the same session, ending in the two different ways section 4b prescribes: a merge, and a `crew stop` on the captain's word with the report kept.
- Auto mode's flag lifecycle end to end on a second project: on by keystroke, a Crew taken to `finished` with no further captain input, and off again within one poll of an unmarked prompt.
- `yolo` delegating the merge: `blog`'s `main` advanced and its Crew reads `finished` with no merge command in the test.
- `[assign]` on a Mate that has never had a turn, at the pane width the console actually gives it.

Not established by these runs:

- **A digest delivered by the daemon.**
  Neither passing run has an `app → mate` `digest:` line in `blog`'s `sent.log`.
  The Mate took the captain's request, spawned the Crew, supervised it and merged inside one turn of about ninety seconds, so there was never a moment when the daemon had something new and the Mate was idle.
  Section 7 step 4 was changed during this task to tell a Mate in auto mode to stop after spawning and let the daemon wake it, and the Mate did not do so in either run.
  The daemon itself is proved by task 19's `TestLiveAutoDigestReachesTheMate` and task 20's `TestLiveAutoPolicyMateAnswersADigest`; what is not proved is that a Mate in auto mode yields its turn to it after a captain-initiated task.
- **`[assign]` landing on the first press.**
  It was refused four or five times in a row in both runs, always with `target_blocked: agent is mid-turn`, because a Mate in manual mode supervising a Crew is inside a tool call for most of every twenty-second cycle.
  The test presses again, which is what the console's outcome line asks a reader to do, but a captain's `[assign]` routinely meeting a busy composer is a real finding and not a comfortable one.
- **`needs-rebase`, `blocked`, and a Crew that fails.**
  Nothing in these runs took the unhappy branches; the recovery ladder and the rebase loop are covered by their own unit tests and by task 18's live proof, not here.
- **A workspace `WORKSPACE.md`.**
  `store.Init` does not create one, so every Mate's bootstrap begins with `cat: … No such file or directory` on a file its own manual told it to read.
  Harmless, and visible in every pane in this record.

## Lessons

They are recorded in `docs/mvp.md` section 7.
The short form: a live proof must open a pane at the geometry the product opens it at, not at the console's own numbers; and a composer classifier that reads `--format text` is reading a rendering with the one attribute it needs already thrown away.
