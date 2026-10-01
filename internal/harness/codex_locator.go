package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/config"
)

// EffectiveCodexHome resolves the provider home once so the launch can pin
// the value into the Herdr-owned pane environment.
func EffectiveCodexHome(codexHome string) (string, error) {
	home := strings.TrimSpace(codexHome)
	if home == "" {
		home = strings.TrimSpace(os.Getenv(CodexHomeEnv))
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

// LaunchCodexHome is EffectiveCodexHome for a home a Codex agent is about to
// run in, or a pane is about to be given. Every Codex launch and every Mate
// pane goes through it.
//
// Under MATE_LIVE=1 (a live test run) it refuses any home outside the temp
// directory. Measured 2026-09-24: 115 of the 138 `[projects."…"]` trust
// entries in an operator's ~/.codex/config.toml named deleted temp
// directories of mate live tests, because every Codex crew they spawned
// trusted its worktree in the operator's home. A live test gets its own home
// from internal/harness/codexlab; a live test that forgot to is refused here,
// before anything is launched, instead of being found in the operator's
// config afterwards.
func LaunchCodexHome(codexHome string) (string, error) {
	home, err := EffectiveCodexHome(codexHome)
	if err != nil {
		return "", err
	}
	if os.Getenv(config.EnvLive) != "1" {
		return home, nil
	}
	if !underTempDir(home) {
		return "", fmt.Errorf("%s=1 is set and CODEX_HOME resolves to %s, outside the temp directory %s: "+
			"a live test must launch Codex in a lab home (internal/harness/codexlab.Home), never the operator's own",
			config.EnvLive, home, os.TempDir())
	}
	return home, nil
}

// underTempDir reports whether path is inside os.TempDir(), comparing the
// symlink-resolved forms when they resolve (macOS's /var is /private/var).
func underTempDir(path string) bool {
	// A home that does not exist yet resolves through its nearest existing
	// ancestor, so /var/…/lab/new compares as /private/var/…/lab/new.
	resolve := func(p string) string {
		p = filepath.Clean(p)
		rest := ""
		for dir := p; ; dir = filepath.Dir(dir) {
			if r, err := filepath.EvalSymlinks(dir); err == nil {
				return filepath.Join(r, rest)
			}
			if parent := filepath.Dir(dir); parent == dir {
				return p
			}
			rest = filepath.Join(filepath.Base(dir), rest)
		}
	}
	tmp := resolve(os.TempDir())
	rel, err := filepath.Rel(tmp, resolve(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
