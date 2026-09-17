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
