package store

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadEnv reads `.mate/.env`: one `KEY=VALUE` per line, blank lines and
// `#` comments ignored, an optional `export ` prefix, a value in single or
// double quotes kept verbatim, an unquoted value cut at a ` #` comment.
// Keys are shell identifiers. A missing file is an empty map. A line that is
// not a setting is an error naming the file and line, so a typo turns into a
// message rather than a feature silently staying off.
func (w *Workspace) LoadEnv() (map[string]string, error) {
	f, err := os.Open(w.EnvFile())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	defer f.Close()
	name := filepath.Join(StateDirName, envFileName)
	env := map[string]string{}
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		key, value, ok := parseEnvLine(scanner.Text())
		if !ok {
			return nil, fmt.Errorf("store: %s:%d: expected KEY=VALUE", name, n)
		}
		if key != "" {
			env[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	return env, nil
}

// parseEnvLine is one line of `.env`. A blank or comment line is ok with an
// empty key; a line that is not a setting is not ok.
func parseEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", true
	}
	if rest, found := strings.CutPrefix(line, "export"); found && rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
		line = strings.TrimSpace(rest)
	}
	key, value, found := strings.Cut(line, "=")
	key = strings.TrimSpace(key)
	if !found || !envKey(key) {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return key, value[1 : len(value)-1], true
	}
	for i := 1; i < len(value); i++ {
		if value[i] == '#' && (value[i-1] == ' ' || value[i-1] == '\t') {
			return key, strings.TrimSpace(value[:i]), true
		}
	}
	return key, value, true
}

// envKey is a shell identifier: letters, digits and underscores, not
// starting with a digit.
func envKey(key string) bool {
	for i, r := range key {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return key != ""
}
