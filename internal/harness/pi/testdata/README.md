# pi captures

Copied unchanged from `docs/evidence/pi-contract-2026-10-01/`, where the runs that made them are written up (`docs/evidence/pi-contract-2026-10-01.md`).
All of them are pi 0.99.1 in a 93x39 Herdr 0.8.2 pane of an isolated lab session, with `deepseek/deepseek-flash`.
The lab paths inside them are the lab's, not a real project's.

## screens

Read with `herdr agent read <name> --source <source> --format text`.
A name with `.visible` was read through `visible`, the source pi's profile uses; one without was read through `recent-unwrapped`.
The `.ansi` files are the same reads with `--format ansi`, kept for the colours (pi's cursor is a reverse-video space).

- `run1-empty-composer.visible.txt` - the empty composer after startup, under the "Update Available" banner.
- `run6-idle-after-turn.visible.txt` - the empty composer after a finished turn.
- `run6-draft.visible.txt` - `draft text not sent` typed and not sent.
- `run6-draft.recent-unwrapped.txt` - the same moment through `recent-unwrapped`: the rules and the composer row come back joined into one 664-character line, which is why pi is read through `visible`.
- `run1-busy-1.txt`, `run1-busy-3.txt`, `run8-busy-reads.visible.txt` - a turn in flight: the spinner on the composer's opening rule.
- `run1-busy-10.txt` - the same turn finished.
- `run1-after-ctrl-u.txt` - a draft emptied by `ctrl+u`.
- `run8-offline-startup.txt`, `run2-resume-startup.txt`, `run3-sessid-startup.txt` - startup with `--offline`, on a resume, and with a new `--session-id`.
- `trust-c5-startup.txt` - the trust dialog, highlight on its default "Trust", in a directory with `.pi/settings.json`.
- `trust-c5-dialog-third-option.visible.txt` - the same dialog after two `down`, highlight on "Trust (this session only)".
- `trust-c5-after-session-trust.visible.txt` - the composer after Enter on that option.
- `trust-c5-no-approve-startup.txt` - the same directory launched with `--no-approve`: no dialog.

## session

Two session files as pi wrote them.

- `*_c954e7e3-*.jsonl` - asked for `--thinking xhigh`, recorded `max`; one prompt, one answer.
- `*_3f0c5a8e-*.jsonl` - `--thinking low`; a prompt that ran `bash`, then a second prompt into which a probe extension injected context (the `custom_message` entry, `probe-run7-inject.jsonl`).
