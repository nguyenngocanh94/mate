# Beads task management

Mate uses [Beads](https://github.com/gastownhall/beads) (`bd`) for task records and [Beads Viewer](https://github.com/Dicklesworthstone/beads_viewer) (`bv`) for the task tab. Verified together on macOS arm64: **bd 1.3.1 + bv 0.25.2**. Both must be on the PATH available to the console host. Mate does not download executables at runtime.

Install the pinned release archives for your platform from the two release pages and verify their SHA-256 against the release checksums before putting `bd` and `bv` on PATH. Homebrew alternatives are `brew install beads` and `brew install dicklesworthstone/tap/bv`; check the installed versions because formula versions can differ. Verify with `bd --version` and `bv --version`.

## Use

In the console, select a project and press `t`. Mate opens/reuses a separate Tasks tab without requiring Fresh or a running agent. `mate tasks hellovietnam --workspace ~/workspace` opens the same viewer in a terminal. In the viewer, `E` opens the epic tree, `b` Kanban, `g` the graph, `/` search, and `q` exits. Use `?` for the installed viewer's key reference.

Beads Viewer provides browsing and triage. Create/edit/claim/close with Beads commands, or ask Mate to do them:

```sh
mate beads hellovietnam --workspace ~/workspace -- create --title 'Checkout' --type epic --json
mate beads hellovietnam --workspace ~/workspace -- create --title 'Payment API' --type task --parent <epic-id> --description 'Acceptance criteria' --json
mate beads hellovietnam --workspace ~/workspace -- ready --limit 10 --json
mate beads hellovietnam --workspace ~/workspace -- update <task-id> --claim --json
mate beads hellovietnam --workspace ~/workspace -- dep add <dependent-id> <blocker-id> --type blocks
mate beads hellovietnam --workspace ~/workspace -- comments add <task-id> 'Crew: payment-api; accepted delivery: <evidence>'
mate beads hellovietnam --workspace ~/workspace -- close <task-id> --reason 'Accepted delivery'
mate tasks hellovietnam --workspace ~/workspace --list
mate tasks hellovietnam --workspace ~/workspace --json
mate task-triage hellovietnam --workspace ~/workspace
```

The `--` separator belongs to Mate; every following argument belongs to `bd`. Multiline descriptions and paths with spaces are passed as literal arguments, without a shell. Inspect current flags with `mate beads hellovietnam -- help <command>`. `mate tasks <project> --init` initializes an absent tracker or refreshes the viewer export of an existing one.

Mate receives the generated `task-management` skill. It checks existing work, records an epic/task, claims before dispatch, includes the Beads ID in the Crew brief, and appends the Crew/repo/brief link to the Beads task. Review follows the existing delivery contract. A Crew reporting `wait-mate` leaves the task in progress; close only after the ship lands or the captain accepts the scout report. A discarded Crew does not cancel its planned task automatically.

## Storage and refresh

One `.mate/projects/<project>/.beads/` tracker covers every repo in that project, including a project without repos. Beads owns its Dolt records and history. Initialization skips upstream AGENTS generation and git hooks so Mate's manual stays authoritative. Removing/re-adding a project preserves this tracker with the other project state.

`mate beads` clears ambient tracker-selection environment variables, selects the project explicitly, and locks per project around embedded Dolt access. The wrapper exports after every command, including a failed multi-record command that may have changed some records. Exports replace `.beads/issues.jsonl` atomically; the viewer watches it. If the Beads write succeeds but export fails, inspect the saved issue and run `mate tasks <project> --init` to refresh. Repeating a create may duplicate work.

`bv` 0.25.2 also supports the Dolt export bridge. If you use raw `bd` instead of the wrapper, refresh with Ctrl+r/F5 or `mate tasks <project> --init`. Raw CLI access bypasses Mate's project lock; use the wrapper for concurrent agent work. Viewer triage is advice; use `bd ready` and the atomic claim to decide actual readiness and ownership. The viewer's `O` action opens the JSONL in a GUI editor or applies terminal-editor changes through `br`; that editing path is unsuitable for this Dolt-backed `bd` tracker. Use `mate beads ... -- update` for durable edits.

Recall queries Beads directly for up to ten active/blocked and ten ready records, within three seconds. It never initializes a tracker or trusts JSONL for readiness. A missing binary, corrupt tracker or timeout is reported in recall while the remaining memory still loads.

The JSONL export is not a database backup. Preserve the whole tracker when backing up `.mate/`; use Beads' Dolt backup/remote commands for supported cross-machine sync. Deleting `.mate/` deletes task data as well as Mate state.

## Unreleased native-plan data

The former `tasks.yaml` manager was replaced before release; the real workspace had no task files when switching. Mate refuses to initialize an empty tracker over a trial `tasks.yaml`. To preserve such a trial, initialize Beads directly in that project's directory with `bd init --prefix <project> --skip-agents --skip-hooks --non-interactive`, then recreate the epics and children through `mate beads`. Map `todo` → `open`, `doing` → `in_progress`, `blocked` → `blocked`, `done` → `closed`. Preserve old IDs in notes, titles, descriptions and parent relationships. Compare every record with the YAML, then rename the YAML to a retained backup. There is no second active task store and no native CRUD TUI.
