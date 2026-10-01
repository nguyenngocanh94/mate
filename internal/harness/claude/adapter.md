| Fact | Value |
| --- | --- |
| Busy signature | `esc to interrupt` |
| Idle composer | a `>` prompt box |
| Directory trust | a trust confirmation whose default highlight is the refusing option |
| Instructions | the brief is delivered as a launch-time system prompt, so nothing lands in the worktree |
| Model and effort | `--model` takes an alias (`haiku`, `sonnet`, `opus`) or a full name; `--effort` takes low to max |

The trust dialog's default highlight is the choice that exits the agent.
This is exactly why you have no key-sending command: a blind confirmation here kills the Crew rather than starting it.
Claude also shows a one-time bypass-permissions acceptance screen the first time a given configuration directory sees it; it looks like a dialog and reads as `unknown` the same way.

Claude renders a greyed-out suggestion inside an otherwise-empty composer after a turn ends.
It is not typed text and the classifier ignores it, but it is why a peek of an idle Claude pane can look like it has something in the box when `state` says the composer is empty; believe `state`.
