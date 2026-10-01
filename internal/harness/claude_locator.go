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
		configDir = strings.TrimSpace(os.Getenv(ClaudeConfigDirEnv))
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
	if envValue := strings.TrimSpace(os.Getenv(ClaudeConfigDirEnv)); envValue != "" {
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
