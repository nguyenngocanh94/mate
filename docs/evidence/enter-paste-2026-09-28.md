# Enter swallowed while sending review corrections

Investigated 2026-09-28. No application code or live crew input changed.

## Finding

Mate sends literal bytes with Herdr `pane send-text`, waits 300 ms, then
uses `agent send-keys enter` up to three times with 400 ms verification waits.
Herdr 0.8.2's `pane send-text` does **not** mark the text as a bracketed paste.
Codex CLI 0.157.1 therefore applies its paste-burst heuristic to the stream.
When the input consumer falls behind, text and the subsequent Enter events can
be consumed together as a paste. Those Enter events insert newlines instead
of submitting. Retrying within the same input backlog does not fix this.

This failure mode was reproduced with the exact 650-character correction from
the incident. A control experiment using a local `/status` command established
that bracketed paste avoids it under the same imposed delay. This demonstrates
the transport/heuristic race; the original process was not instrumented for
individual key events, so the specific cause of its processing delay remains
unidentified (CPU load, rendering cost, scheduling, etc.).

## Original incident

- Project `hellovietnam`, crew `ui1-theme`, Codex CLI 0.157.1, Herdr 0.8.2.
- Mate's transcript at 13:47:07 local time records the 650-character `mate send`.
- Herdr logged successful `agent.send_keys` requests at 06:47:10.363,
  06:47:10.905 and 06:47:11.429 UTC.
- At 13:47:12 Mate reported `typed ... but 3 enter(s) did not submit it`.
- The subsequent peek contained the whole correction and blank lines in the
  composer. The crew transcript contained no new user turn after 06:25:19 UTC.
- Read-only inspection later still showed that pending correction. The
  terminal was in raw mode (`-icrnl`, `-icanon`), ruling out a currently enabled
  terminal CR-to-LF translation. No custom submit keymap was configured in the
  global or worktree config examined.
- The original Codex process logged 14 paste-image-path probes from
  06:47:10.166 through 06:47:12.510 UTC, spanning and outlasting all three
  Enter API calls. Their `No such file or directory` trace messages originate
  in `handle_paste_image_path`: they are unsuccessful image-path guesses, not
  missing project directories. This is consistent with incremental paste
  processing continuing during the retries; it is not a per-key event trace.

## Isolated reproduction

Used an owned Herdr session `fm-lab-enter-20260928`, Codex CLI 0.157.1, and
a separate temporary Codex home. Its model provider pointed to the non-serving
local address `http://127.0.0.1:1/v1`; no coding task was executed by a model.
The operator's running crew was only read with `mate peek` and filesystem/DB
inspection. No key was sent to it.

1. Send the exact 650-character payload into a fresh responsive Codex TUI;
   wait 300 ms and send Enter. The composer clears and a request starts.
   See [baseline](enter-paste-2026-09-28/raw-enter-1.txt).
2. In the same lab, suspend **only the test TUI process** with SIGSTOP, send
   the same payload and three Enters at the same 300/400 ms intervals, then
   resume it with SIGCONT. This models a delayed consumer without changing
   the producer's timing. The payload remains in the composer with blank
   lines, matching the original failure.
   See [delayed consumer](enter-paste-2026-09-28/stalled-consumer.txt).
3. Repeat the delayed-consumer comparison with `/status` padded to 650
   characters. This command is local and needs no model response:
   - Raw text: `/status` remains unsubmitted in the composer.
     [Capture](enter-paste-2026-09-28/raw-status-stall.txt).
   - Same text wrapped in `ESC[200~` and `ESC[201~`: `/status` executes and
     the empty composer returns.
     [Capture](enter-paste-2026-09-28/bracketed-status-stall.txt).

The lab Herdr server and its separate Codex daemon were stopped after testing.

## Source correspondence

- [Mate send](../../internal/send/send.go): `DefaultSettle`, retry loop.
- [Mate runtime](../../internal/runtime/herdr.go): `SendText` uses
  `pane send-text`, whereas `SendKeys` uses `agent send-keys`.
- [Herdr v0.8.2 pane handler](https://github.com/herdrdev/herdr/blob/v0.8.2/src/app/api/panes.rs#L1501):
  `handle_pane_send_text` writes `params.text` directly.
- [Herdr input helpers](https://github.com/herdrdev/herdr/blob/v0.8.2/src/app/api_helpers.rs#L24):
  `encode_api_text` wraps text when bracketed-paste mode is enabled, but
  `pane send-text` does not call it. The socket `pane.send_input` path does.
- [Codex paste detector](https://github.com/openai/codex/blob/rust-v0.157.1/codex-rs/tui/src/bottom_pane/paste_burst.rs):
  distinguishes rapid character streams from explicit paste; Enter can be
  appended as newline while a burst is active. The timestamps used are
  consumption-time instants, not the API sender's timestamps.

## Repair direction

Use an explicit paste operation respecting the pane's negotiated paste mode,
then submit and verify separately. Keep the existing identity/pane checks;
do not replace delivery with an unverified `agent prompt`.

Also provide recovery for an app-owned pending send without retyping it,
and update Mate's instructions so it does not require the captain to repair
this transport failure. Verify the full pending payload and session before
retrying: the current classifier exposes only its first rendered line.

Separately, the Mate ran `mate send ... | tail -3` and then unconditionally
updated the backlog to `correction sent`. The shell pipeline hid the send's
exit status, and the write was not conditional on delivery. This records a
false success but did not cause the original input failure.
