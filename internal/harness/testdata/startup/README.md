# Startup screens

Captured 2026-09-14 through `herdr agent read <name> --source recent-unwrapped --lines 40`
against Herdr 0.8.2 panes in an isolated lab session, with isolated `CODEX_HOME` /
`CLAUDE_CONFIG_DIR` (the operator's real account files were never written).
The scratch repository paths inside the captures are the lab's, not a real project's.

- `codex-0.154.0-trust-dialog.txt` - codex-cli 0.154.0 directory-trust dialog on a
  standalone `git init` repository with no `projects` entry. Option 1 is highlighted by
  default.
- `codex-0.154.0-ready.txt` - the same pane a few seconds after `1`, `enter`.
- `claude-2.1.270-trust-dialog.txt` - Claude Code 2.1.270 directory-trust dialog. The
  cursor is on **No, exit** by default: a blind Enter exits the agent.
- `claude-2.1.270-ready.txt` - the same pane a few seconds after `down`, `enter`.

Synthesized from the captures above (the highlight marker moved by hand, nothing else
changed) so the selection check has a negative and a positive case for each harness:

- `claude-2.1.270-trust-dialog-accept-selected.txt` - what `down` produced live
  (the marker on **Yes, I trust this folder**), re-derived from the capture.
- `codex-0.154.0-trust-dialog-quit-selected.txt` - the marker on **2. No, quit**.

Captured 2026-09-18 the same way, against codex-cli 0.154.0 with 0.155.0 published,
in a throwaway `/private/tmp` directory codex had never seen. These are one launch
read four times, in order:

- `codex_update_dialog.txt` - the release-update prompt as drawn. It comes up before
  the directory-trust dialog and blocks the composer; the highlight opens on
  **1. Update now**, which would run `npm install -g @openai/codex` under the agent.
- `codex_update_dialog_skip_selected.txt` - the same pane after two `down` presses,
  with the highlight on **3. Skip until next version**.
- `codex_update_dialog_after_enter.txt` - what Enter on that option produced: the
  directory-trust dialog, for the same directory.
- `codex_update_banner_ready.txt` - the composer a few seconds after `1`, `enter`.
  The update notice is still in the scrollback as a box banner, so this screen
  carries "Update available!" without the dialog's shape and must classify as ready.

Answering the prompt with **3. Skip until next version** writes
`dismissed_version` into `$CODEX_HOME/version.json`, which is why the prompt does
not come back until the next release. mate does not rely on that: the launch passes
`-c check_for_update_on_startup=false` so the prompt is not drawn at all, and the
recogniser stays as defence for the launches that flag does not cover.

Captured 2026-09-24 (task 35) the same way, against codex-cli 0.154.0 with 0.156.1
published, in Herdr 0.8.2 lab panes under the scratchpad:

- `codex-0.154.0-resume-update-dialog.txt` - `codex resume <id>` launched **without**
  `-c check_for_update_on_startup=false`, in a directory codex already trusted, with
  the session's history above it. It is the same release-update prompt a fresh
  launch draws; with the flag, which every mate resume carries, it is not drawn.
- `codex-0.154.0-resume-ready.txt` - the same pane after `down`, `enter` (**2. Skip**,
  so the lab wrote nothing to the operator's `version.json`) and one more question.
  The replayed conversation puts `›` prompt lines above the composer, so this is the
  screen that proves the composer rule reads the last one.
- `codex-0.154.0-hooks-review.txt` - "Hooks need review", drawn after the
  directory-trust dialog when the effective hooks (a lab `CODEX_HOME/hooks.json` and
  the cwd's `.codex/hooks.json`) have no persisted trust. Task 37 gave the Codex Mate a
  hook of its own, and this layout is recognised with the 0.156.1 one below.

The installed codex moved from 0.154.0 to 0.156.1 during the same session (not by
mate: every lab update prompt was answered **2. Skip**), and the first live Codex
Mate start after it died on a trust dialog of a new shape. Captured the same way, in
a scratchpad directory codex had never seen:

- `codex-0.156.1-trust-dialog.txt` - "Folder access", the path, a paragraph opening
  "Trust this folder?", options **1. Trust and continue** / **2. Quit**, footer
  `enter continue · esc quit`. The highlight opens on option 1, and pressing `1`
  leaves the screen byte for byte the same (it selects, it does not confirm), so the
  0.154.0 answer (`1`, re-read, `enter`) is still right.
- `codex-0.156.1-ready.txt` - the composer a few seconds after that `enter`.
- `codex-0.156.1-trust-dialog-quit-selected.txt` - synthesized: the marker moved to
  **2. Quit**, nothing else changed.

Captured 2026-09-24 (task 37) the same way, against codex-cli 0.156.1 in Herdr 0.8.2 lab
panes, with a lab `CODEX_HOME` (the operator's auth copied, `features.hooks = true`)
and a lab Mate directory whose `.codex/hooks.json` holds one SessionStart hook,
`'/usr/local/bin/mate' hook mate-session` (a stand-in path; the tests put the real
path and command in its place). Each file is one read, 1.5 s after the key named:

- `codex-0.156.1-hooks-review.txt` - the dialog after the directory-trust dialog:
  "Hooks need review", "1 hook is new or changed.", options **1. Review hooks** (the
  highlight opens here), **2. Trust all and continue**, **3. Continue without
  trusting (hooks won't run)**, footer `enter confirm · esc skip`. The count names no
  hook, which is why the settle never answers **2**.
- `codex-0.156.1-hooks-table-review.txt` - `enter` on option 1: the hook-event table,
  "⚠ 1 hook needs review before it can run.", SessionStart `1 0 1` selected, footer
  `t trust all · enter review · esc close`.
- `codex-0.156.1-hooks-sessionstart-own.txt` - `enter` on SessionStart: the event's
  list (`› [!] Hook 1 · new`) and the selected hook's Event, Source (`Project config -
  <path>`, wrapped at the pane width), Command, Mode, Timeout and Trust, footer
  `t trust · esc back`.
- `codex-0.156.1-hooks-closed-ready.txt` - the composer after `esc` out of the review.

A second launch added an untrusted hook in the lab `CODEX_HOME/hooks.json`, so the
review lists two:

- `codex-0.156.1-hooks-review-two.txt` - the dialog: "2 hooks are new or changed."
- `codex-0.156.1-hooks-table-review-two.txt` - the table: SessionStart `2 0 2`.
- `codex-0.156.1-hooks-sessionstart-two-foreign-selected.txt` - the list with the
  operator-side hook (`User config - <CODEX_HOME>/hooks.json`) first and selected.
- `codex-0.156.1-hooks-sessionstart-two-own-selected.txt` - after `down`: mate's.
- `codex-0.156.1-hooks-sessionstart-two-own-trusted.txt` - after `t`: `[x] Hook 2`,
  `Trust Trusted`, footer `space/enter toggle · esc back`; `t` trusts the selected
  hook only.
- `codex-0.156.1-hooks-sessionstart-two-all-trusted.txt` - after `up`, `t`: nothing
  left to review, header "Turn hooks on or off. Your changes are saved automatically."
- `codex-0.156.1-hooks-table-trusted.txt` - `esc`: the table without a Review column,
  footer `enter details · esc close`.

- `codex-0.156.1-hooks-sessionstart-own-wrapped.txt` - assembled from the screen tail
  a live `TestLiveCodexMateRecallHook` refusal quoted (the Event to Trust block and
  the footer are verbatim; the banner above is the list capture's): the Source path
  wrapped at `/` with the `/` not drawn (`…/.mate` then `projects/…`), the Command
  wrapped after `mate-`, and a `Context   limit: 32000 approximate tokens` row the
  lab captures without `additionalContextLimit` do not have.

Trusting writes `[hooks.state."<hooks.json>:session_start:0:0"] trusted_hash` into
`$CODEX_HOME/config.toml`; the next launch with the same hook bytes draws no review.
