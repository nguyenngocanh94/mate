package config

import (
	"log/slog"
	"strings"
)

// Environment keys. Workspace identity keys are injected at agent launch
// (G4); G0 only defines the names and log-level handling.
const (
	EnvLogLevel         = "MATE_LOG_LEVEL"
	EnvWorkspaceID      = "MATE_WORKSPACE_ID"
	EnvProjectID        = "MATE_PROJECT_ID"
	EnvAgentID          = "MATE_AGENT_ID"
	EnvAgentRole        = "MATE_AGENT_ROLE"
	EnvTaskID           = "MATE_TASK_ID"
	EnvCrewID           = "MATE_CREW_ID"
	EnvRuntimeSessionID = "MATE_RUNTIME_SESSION_ID"
	// EnvStatusFile is the absolute path of `crews/<id>.status`, the one
	// channel a Crew has back to the Mate (docs/mvp.md section 4:
	// `echo "state: one line" >> $MATE_STATUS`). It is injected into the
	// Crew pane at tab create, because the brief tells the Crew to echo into
	// it by name and a pane without it would make every status append write
	// to a file called the empty string.
	EnvStatusFile = "MATE_STATUS"
	// EnvCaller says who is at the keyboard of the pane a `mate` command
	// was typed in: `mate` in a Mate's pane, `crew` in a Crew's pane, and
	// absent everywhere else, which is the captain's own shell (docs/mvp.md
	// M4 decisions: "một lời gọi có MATE_CALLER=mate ... bị từ chối").
	// It is injected at pane create beside the identity keys, for the same
	// reason MATE_STATUS is: a command can only know which of the three
	// ran it from the environment its pane was given.
	EnvCaller          = "MATE_CALLER"
	EnvClaudeConfigDir = "CLAUDE_CONFIG_DIR"
	EnvCodexHome       = "CODEX_HOME"
	// EnvLive is the single opt-in that lets `TestLive*` proofs claim a real
	// Herdr lab and real harnesses (AGENTS.md). The product reads it in one
	// place only: a process that carries it may not launch Codex, or hand a
	// pane a CODEX_HOME, outside the temp directory (harness.LaunchCodexHome),
	// so a live run never writes trust or rollouts into the operator's own
	// ~/.codex.
	EnvLive = "MATE_LIVE"
)

// ParseLogLevel maps debug/info/warn/error to slog levels. Empty or unknown
// values are INFO so help/version stay quiet by default.
func ParseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// IdentityEnvKeys are the MATE_* identity keys Mate injects into an agent
// pane (workspace/tab create --env, not agent start). Both the runtime
// allowlist and the harness launch-env filter derive from this one list
// so they cannot drift. Unknown keys are refused (ADR 0009).
func IdentityEnvKeys() []string {
	return []string{
		EnvWorkspaceID,
		EnvProjectID,
		EnvAgentID,
		EnvAgentRole,
		EnvTaskID,
		EnvCrewID,
		EnvRuntimeSessionID,
		EnvStatusFile,
		EnvCaller,
	}
}

// LaunchEnvKeys are identity keys plus provider roots that Mate may explicitly
// pass through to the Herdr-owned pane environment. Claude's default root is
// intentionally not an assignment; its unset operation is carried by the
// harness LaunchSpec and applied immediately before agent start.
func LaunchEnvKeys() []string {
	keys := IdentityEnvKeys()
	return append(keys, EnvClaudeConfigDir, EnvCodexHome)
}
