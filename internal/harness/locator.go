package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EffectiveClaudeConfigDir resolves the provider config root used to find
// Claude transcripts. It deliberately does not describe the launch
// environment: Claude's transcript root and account identity are controlled
// by the same provider setting but are different concerns.
func EffectiveClaudeConfigDir(configDir string) (string, error) {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		configDir = strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	}
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("claude config directory: %w", err)
		}
		configDir = filepath.Join(home, ".claude")
	}
	if !filepath.IsAbs(configDir) {
		return "", fmt.Errorf("claude config directory must be absolute")
	}
	return filepath.Clean(configDir), nil
}

// ClaudeConfigDirForLaunch resolves the optional CLAUDE_CONFIG_DIR assignment
// for a child process. The provider default is returned with set=false so a
// caller can leave the variable unset and let Claude resolve its account from
// HOME/.claude.json. A configured value is returned with set=true and must be
// passed through exactly.
func ClaudeConfigDirForLaunch(configDir string) (value string, set bool, err error) {
	if strings.TrimSpace(configDir) != "" {
		value, err = EffectiveClaudeConfigDir(configDir)
		return value, true, err
	}
	if envValue := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); envValue != "" {
		value, err = EffectiveClaudeConfigDir(envValue)
		return value, true, err
	}
	value, err = EffectiveClaudeConfigDir("")
	return value, false, err
}

// ClaudeProjectsDir returns the effective Claude project-transcript root.
func ClaudeProjectsDir() (string, error) {
	configDir, err := EffectiveClaudeConfigDir("")
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "projects"), nil
}

// EffectiveCodexHome resolves the provider home once so the launch can pin
// the value into the Herdr-owned pane environment.
func EffectiveCodexHome(codexHome string) (string, error) {
	home := strings.TrimSpace(codexHome)
	if home == "" {
		home = strings.TrimSpace(os.Getenv("CODEX_HOME"))
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("codex home: %w", err)
		}
		home = filepath.Join(userHome, ".codex")
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("CODEX_HOME must be absolute when set")
	}
	return filepath.Clean(home), nil
}

// CodexSessionsDir returns the effective Codex rollout search root. An
// explicit config value wins, then CODEX_HOME, then the provider default
// ~/.codex. The path is recorded before launch while the rollout identity is
// still pending; a later sync must validate its session_meta before adoption.
func CodexSessionsDir(codexHome string) (string, error) {
	home, err := EffectiveCodexHome(codexHome)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "sessions"), nil
}
