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
  launch draws; with the flag, which every matev2 resume carries, it is not drawn.
- `codex-0.154.0-resume-ready.txt` - the same pane after `down`, `enter` (**2. Skip**,
  so the lab wrote nothing to the operator's `version.json`) and one more question.
  The replayed conversation puts `›` prompt lines above the composer, so this is the
  screen that proves the composer rule reads the last one.
- `codex-0.154.0-hooks-review.txt` - "Hooks need review", drawn after the
  directory-trust dialog when the effective hooks (a lab `CODEX_HOME/hooks.json` and
  the cwd's `.codex/hooks.json`) have no persisted trust. matev2 installs no Codex
  hook today, so the settle does **not** answer it: it is pinned as unrecognised, and
  task 37 decides whether a Codex Mate gets a hook and how this screen is handled
  (`--dangerously-bypass-hook-trust` on the launch skips hook trust for that run).

The installed codex moved from 0.154.0 to 0.156.1 during the same session (not by
matev2: every lab update prompt was answered **2. Skip**), and the first live Codex
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
