| Fact | Value |
| --- | --- |
| Busy signature | a braille spinner, a space and an ellipsis (`⠹ Waiting for response…`, `⠋ Thinking…`, `⠼ Responding…`), or the hint `Ctrl+c:cancel`, while the composer box stays empty |
| Idle composer | the bottom box, its first row `❯` and nothing else; the footer names the model and `always-approve` |
| Directory trust | the launch passes `--trust`, so a worktree is trusted without a folder dialog; the privacy banner above the box does not take keys |
| Instructions | the brief is inlined with `--append-system-prompt`; the project's `AGENTS.md` still loads, and so does the operator's home `Claude.md`, because the Crew uses the operator's grok home |
| Model and effort | `--model` when one is named; `--effort` low, medium, high and xhigh are passed, and max is left out |

Grok draws `❯` at the start of the composer box.
A turn in flight leaves that box empty, so `mate state` reads the spinner and `Ctrl+c:cancel` before the composer.
Lines in the scrollback also start with `❯`; only the box at the bottom is the composer.

Grok asks no permission before a tool when the launch passes `--always-approve`.
`--no-plan` keeps plan mode off, and `--fullscreen` pins the layout these screens were measured in.

A Crew inherits the operator's global hooks and the rules in the grok home, because authentication lives in that home and no flag was measured that turns them off.
A hook that fails prints a notice above the composer and does not take the composer.

A Mate cannot run on Grok yet.
Grok has hooks, but mate has not measured a hook whose stdin is what `mate hook` writes, and a Mate's memory and inbox rest on that.
