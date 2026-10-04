# grok captures

Measured on grok 1.0.46 in a 93x39 pane, read as visible text.
The paths inside the screens are the probe's, not a project's.

## screens

- `empty.txt` — the empty composer after startup, under the privacy banner and a failed global-hook notice.
- `draft-pong.txt` — `Reply with exactly: pong` typed and not sent.
- `busy-waiting.txt`, `busy-thinking.txt`, `busy-responding.txt` — one turn in flight: the composer box stays empty, and the status line is `Waiting for response…`, `Thinking…`, then `Responding…`, each with `Ctrl+c:cancel`.
- `idle-after-turn.txt` — the same turn finished: scrollback shows the prompt and `pong`, and the box is empty.
- `resume.txt` — `--resume` of that session: the same empty box, with the earlier scrollback still on screen.

The privacy banner stayed up for the whole session and did not take a key that was typed into the box.
A startup line `⠴ MCP (5/10)` is a braille spinner without an ellipsis, and it is not busy; the captures here are from after startup.

## session

`updates.jsonl` is the usage record of the one measured turn, plus the user message, the thought and the answer that preceded it.
Hook lines were left out.
The authoritative usage is the `turn_completed` update: inputTokens 21895, of which cachedReadTokens 1664 are a subset, outputTokens 137, of which reasoningTokens 136 are a part, totalTokens 22032, stop_reason `end_turn`, model `grok-4.7-build`.
No tool call was in this turn.
