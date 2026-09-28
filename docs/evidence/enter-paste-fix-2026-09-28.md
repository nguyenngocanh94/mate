# Verified paste and saved-send recovery

Follow-up to [the incident investigation](enter-paste-2026-09-28.md).

## Change

- `runtime.Herdr.SendText` validates user text, resolves the named agent's
  live pane, checks it against the recorded pane, then encloses the text in
  bracketed-paste boundaries. Claude and Codex are the supported composers;
  Enter remains a separate agent-addressed operation.
- A Crew send writes `crews/<id>.send.json` before typing, under a per-Crew
  process lock. The record binds the payload and source to the runtime
  session, agent, pane, harness session reference, and Crew launch metadata.
- Repeating that exact send can retry Enter without retyping. It must match
  the whole rendered pending draft and recheck after settling. Changed text,
  a new incarnation, missing boundaries, collapsed/truncated content, empty
  input or a dialog refuse recovery. The receipt is cleared after confirmed
  submission and the `sent.log` append.
- `unknown` after Enter and `busy -> busy` are unconfirmed, not success.
  A retry within the same call also checks the whole pending draft.
- The Mate's harness-adapters instructions explain this recovery and forbid
  recording "sent" after an unsuccessful/unconfirmed command or hiding its
  exit status behind an unchecked pipeline.

This is a receipt for terminal input, not firstmate's durable instruction
inbox and not a worker acknowledgement that the task was completed.

## Validation

- Final `make check` exits 0 (gofmt check, vet, full suite; expected live
  skips are explicitly reported). `make build` succeeds and updates
  `bin/mate`. The live Codex proof below was run separately, not inferred
  from those skipped tests.
- Regression tests cover explicit paste boundaries, control-byte rejection
  before recording an attempt, unknown/busy confirmation, wrapped drafts,
  suffix edits, interior newlines, missing/truncated content, edits during
  settle, absent receipts, changed Crew incarnations, no retyping, no false
  `sent.log` entry, persistence after failure and concurrent send refusal.
- Targeted race tests pass for the send/recovery and store-lock cases.
- Live `TestLiveCodexDelayedPasteAndRecovery` passes against Codex CLI
  0.157.1 and Herdr 0.8.2 in `fm-lab-enter-fix-20260928`. It submits a known
  unsubmitted draft using recovery, then suspends only its owned Codex TUI,
  queues a 650-character bracketed paste and three Enters, resumes the TUI,
  and proves a second local `/status` output plus an empty composer.
  [Live output](enter-paste-2026-09-28/fix-live-codex.txt).
- The local probe provider points to `127.0.0.1:1`; `/status` uses zero model
  tokens. The test uses a private temporary Codex home, closes its workspace,
  and stops the private daemon/updater and their leftover children. The
  provisioned Herdr lab server and manual probe were stopped afterwards.

## Limits and observations

- The old raw-byte failure is timing-sensitive. One control run displayed a
  transient pending composer, but a queued Enter submitted it during the
  recovery settle. The new recovery correctly refused to press another key.
  The deterministic regression separates a deliberately unsubmitted draft
  from the delayed-consumer paste test; the original raw/bracketed A/B
  captures remain in the investigation report.
- Live test setup exposed Herdr's early agent detection during Codex daemon
  installation. The test now bootstraps the isolated daemon before starting
  the TUI and waits for a rendered composer/footer. Its short temp path avoids
  macOS's Unix socket path limit.
- `TestLiveSendToClaudeThreeCases` did not reach delivery: the existing
  classifier treated `Try "refactor <filepath>"` as pending input. This is
  not counted as a passing Claude live proof.
  [Blocked Claude run](enter-paste-2026-09-28/fix-live-claude.txt).
- Full-draft matching uses measured terminal structure and wrapping. Unknown
  layouts fail closed. Snapshot and keystroke remain separate operations;
  simultaneous human input or process replacement is not atomic with them.
- An empty snapshot is still the existing submission signal; this change
  does not introduce harness-level acknowledgement or eliminate stale-screen
  ambiguity. Receipts from before this fix do not exist and cannot be safely
  reconstructed just from matching visible text.
- No running production Crew received test input. Existing installed Mate
  skill files were not refreshed or its session restarted by this change.
