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
	EnvClaudeConfigDir  = "CLAUDE_CONFIG_DIR"
	EnvCodexHome        = "CODEX_HOME"
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
