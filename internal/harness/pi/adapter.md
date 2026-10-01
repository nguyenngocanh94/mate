| Fact | Value |
| --- | --- |
| Busy signature | the line above the composer turns into `── ⠴ Working ───`, with a spinner cell that changes on every read |
| Idle composer | an empty row between two full-width rules, over two footer lines (the cwd, then usage and `(provider) model • thinking`) |
| Directory trust | `Trust project folder?`, only in a directory with its own `.pi/` resources; the launch passes `--no-approve`, so it should not appear |
| Instructions | the brief is appended to the system prompt by path at launch, so nothing lands in the worktree; the project's `AGENTS.md` still loads |
| Model and effort | `--model` takes `provider/id` (`deepseek/deepseek-flash`); `--effort` low to max becomes `--thinking`, which pi may clamp to what the model supports |

pi draws no prompt glyph: a draft starts at the left edge of the row under the first rule, and a turn in flight leaves that row empty under the spinner line, which is why `mate state` reads the spinner before the composer.
pi never asks permission before running a tool, so a pi Crew does not stop on an approval prompt the way it would on another harness.

pi lowers or raises the thinking level to what the model supports without saying so: a Crew asked for `--effort medium` on `deepseek/deepseek-flash` thinks at `high`.
The level it really used is the last word of the footer's second line, and the dashboard shows it for the Crew as its runtime effort once the first turn is recorded.

A Mate cannot run on pi: pi has no hook mate can install yet, and a Mate's memory and inbox rest on hooks.
