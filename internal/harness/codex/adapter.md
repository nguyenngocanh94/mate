| Fact | Value |
| --- | --- |
| Busy signature | `esc to interrupt`, usually inside a `• Working (Xs • esc to interrupt)` footer |
| Idle composer | a bordered input box with the cursor in it |
| Directory trust | `Do you trust the contents of this directory?`, asked per absolute path |
| Instructions | the brief is delivered as a file in the worktree that Codex reads at startup |
| Model and effort | `--model` becomes `-m <model>`; `--effort` low to xhigh becomes `model_reasoning_effort`; `max` is recorded, not passed |

Codex confirms trust for each absolute path separately, and a Crew worktree is always a path it has never seen, so the dialog appears on essentially every spawn.
`mate crew spawn` answers it during launch and prints a `note: answered the codex directory-trust dialog for <worktree>` line on stderr when it did.
If you peek and the dialog is still on screen, the spawn did not complete: report that to the captain rather than sending a line at it, because a line typed into that dialog is not an answer to it.

A first prompt that Codex never picked up leaves the pane idle with the brief line visible in the composer.
That is a `pending` reading, and the fix is not to type: peek, then report it, because the Crew has not started and its worktree is empty.
