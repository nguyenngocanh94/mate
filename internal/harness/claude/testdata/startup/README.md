# Claude Code startup screens

Codex's are in `internal/harness/codex/testdata/startup`.

Captured 2026-09-14 through `herdr agent read <name> --source recent-unwrapped --lines 40`
against Herdr 0.8.2 panes in an isolated lab session, with isolated `CODEX_HOME` /
`CLAUDE_CONFIG_DIR` (the operator's real account files were never written).
The scratch repository paths inside the captures are the lab's, not a real project's.

- `claude-2.1.270-trust-dialog.txt` - Claude Code 2.1.270 directory-trust dialog. The
  cursor is on **No, exit** by default: a blind Enter exits the agent.
- `claude-2.1.270-ready.txt` - the same pane a few seconds after `down`, `enter`.

Synthesized from the captures above (the highlight marker moved by hand, nothing else
changed) so the selection check has a negative and a positive case:

- `claude-2.1.270-trust-dialog-accept-selected.txt` - what `down` produced live
  (the marker on **Yes, I trust this folder**), re-derived from the capture.

Captured 2026-09-25 the same way, against Claude Code 2.1.282 (auto-updated that
morning), in a Mate directory of a scratch workspace. Unlike the lab captures above it
ran with the operator's own Claude config, so its status line and plan name are theirs:

- `claude-2.1.282-ready.txt` - the empty composer. 2.1.282 draws a dim suggestion in
  it, `❯` NBSP `Try "edit <filepath> to..."`, where 2.1.270 drew `❯` NBSP alone; the
  suggestion's wording changes between launches (`Try "refactor <filepath>"` was also
  seen). Before the classifier knew this shape every Claude Mate start timed out with
  `startup screen not recognised`.
