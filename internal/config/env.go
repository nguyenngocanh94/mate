package config

import (
	"log/slog"
	"strings"
)

// Environment keys. Workspace identity keys are injected at agent launch
// (G4); G0 only defines the names and log-level handling.
const (
	EnvLogLevel         = "MATEV2_LOG_LEVEL"
	EnvWorkspaceID      = "MATEV2_WORKSPACE_ID"
	EnvProjectID        = "MATEV2_PROJECT_ID"
	EnvAgentID          = "MATEV2_AGENT_ID"
	EnvAgentRole        = "MATEV2_AGENT_ROLE"
	EnvTaskID           = "MATEV2_TASK_ID"
	EnvCrewID           = "MATEV2_CREW_ID"
	EnvRuntimeSessionID = "MATEV2_RUNTIME_SESSION_ID"
	// EnvStatusFile is the absolute path of `crews/<id>.status`, the one
	// channel a Crew has back to the Mate (docs/mvp.md section 4:
	// `echo "state: one line" >> $MATEV2_STATUS`). It is injected into the
	// Crew pane at tab create, because the brief tells the Crew to echo into
	// it by name and a pane without it would make every status append write
	// to a file called the empty string.
	EnvStatusFile = "MATEV2_STATUS"
	// EnvCaller says who is at the keyboard of the pane a `matev2` command
	// was typed in: `mate` in a Mate's pane, `crew` in a Crew's pane, and
	// absent everywhere else, which is the captain's own shell (docs/mvp.md
	// M4 decisions: "một lời gọi có MATEV2_CALLER=mate ... bị từ chối").
	// It is injected at pane create beside the identity keys, for the same
	// reason MATEV2_STATUS is: a command can only know which of the three
	// ran it from the environment its pane was given.
	EnvCaller          = "MATEV2_CALLER"
	EnvClaudeConfigDir = "CLAUDE_CONFIG_DIR"
	EnvCodexHome       = "CODEX_HOME"
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

// IdentityEnvKeys are the MATEV2_* identity keys Mate injects into an agent
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
