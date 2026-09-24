// Package codexlab gives a live test its own CODEX_HOME, so no Codex agent
// the test launches - a Crew, a Mate, a relaunch of either, or a Crew the
// Mate spawns from its own pane - reads or writes the operator's ~/.codex.
//
// Measured 2026-09-24: 115 of the 138 `[projects."…"]` trust entries in an
// operator's ~/.codex/config.toml pointed at deleted temp directories of
// mate live tests, and task 37 had added a `hooks.state` entry there. Each
// one was a Codex agent trusting its cwd in the only home it could see.
//
// How the lab home reaches every launch: Home sets CODEX_HOME in the test
// process; harness.LaunchCodexHome resolves every Codex launch and every Mate
// pane's CODEX_HOME from it; the runtime exports a launch's environment into
// the pane before each agent start; and a Mate pane carries CODEX_HOME to the
// `mate crew spawn` it runs. The guard is harness.LaunchCodexHome itself:
// with MATE_LIVE=1, a launch whose home is outside the temp directory is
// refused before anything starts. Home's cleanup is the second check, after
// the fact: the operator's config.toml must not have gained a line naming a
// temp path.
package codexlab

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/harness"
)

// AuthFile is the one file copied from the operator's home. codex-cli 0.156.1
// stores its login in $CODEX_HOME/auth.json (`cli_auth_credentials_store`
// defaults to "file"; `codex login status` reads it from there); nothing
// else in the operator's home is needed to run, and nothing else is read.
const AuthFile = "auth.json"

// Config is the whole of the lab home's config.toml: hooks on, as the
// operator's own Codex runs them (the Mate's SessionStart hook, task 37).
// No trust, no hooks, no model: whatever the operator configured is theirs.
const Config = "[features]\nhooks = true\n"

// Marker is the file that says a home is a lab home, so a second Home in one
// test is refused instead of copying a lab's auth into another lab.
const Marker = "mate-codexlab"

// Home creates a lab CODEX_HOME under the temp directory for t, copies the
// operator's auth file into it (a copy, never a link, so Codex writing to it
// cannot reach the operator's), sets CODEX_HOME for the rest of the test,
// and returns the home. Its cleanup fails the test if the operator's
// config.toml gained a line naming a temp directory while it ran.
//
// Call it before anything that starts a Mate or launches Codex. It uses
// t.Setenv, so the test cannot be parallel, which live tests are not.
func Home(t testing.TB) string {
	t.Helper()
	operator, err := harness.EffectiveCodexHome("")
	if err != nil {
		t.Fatalf("codexlab: the operator's Codex home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(operator, Marker)); err == nil {
		t.Fatalf("codexlab: CODEX_HOME is already a lab home (%s); call Home once per test", operator)
	}
	auth, err := os.ReadFile(filepath.Join(operator, AuthFile))
	if err != nil {
		t.Fatalf("codexlab: no Codex login to copy into a lab CODEX_HOME: %v", err)
	}
	before := configLines(t, operator)

	// Not t.TempDir: a Codex process still shutting down can write into the
	// home after the test ends, and a failed removal would fail the test.
	home, err := os.MkdirTemp("", "codexlab-")
	if err != nil {
		t.Fatalf("codexlab: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		// Herdr and Codex report resolved paths (docs/mvp.md section 7).
		home = resolved
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	if err := os.WriteFile(filepath.Join(home, AuthFile), auth, 0o600); err != nil {
		t.Fatalf("codexlab: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(Config), 0o644); err != nil {
		t.Fatalf("codexlab: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, Marker), []byte("a mate live test's lab CODEX_HOME\n"), 0o644); err != nil {
		t.Fatalf("codexlab: %v", err)
	}
	t.Setenv(config.EnvCodexHome, home)
	if _, err := harness.LaunchCodexHome(""); err != nil {
		t.Fatalf("codexlab: the lab home would be refused: %v", err)
	}
	t.Cleanup(func() {
		after := configLines(t, operator)
		var leaked []string
		for _, l := range after.order {
			if _, old := before.set[l]; !old && mentionsTemp(l) {
				leaked = append(leaked, l)
			}
		}
		if len(leaked) > 0 {
			t.Errorf("codexlab: the operator's %s gained %d line(s) naming a temp directory while this test ran; a Codex launch escaped the lab home:\n%s",
				filepath.Join(operator, "config.toml"), len(leaked), strings.Join(leaked, "\n"))
		}
	})
	return home
}

type lines struct {
	set   map[string]struct{}
	order []string
}

// configLines reads the operator's config.toml, read-only. A missing file
// is empty.
func configLines(t testing.TB, home string) lines {
	t.Helper()
	out := lines{set: map[string]struct{}{}}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("codexlab: %v", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		if _, dup := out.set[l]; !dup {
			out.set[l] = struct{}{}
			out.order = append(out.order, l)
		}
	}
	return out
}

// tempRoots are the spellings a temp path can take in Codex's config: the
// temp directory as given and resolved, and macOS's two forms of it.
func tempRoots() []string {
	roots := []string{filepath.Clean(os.TempDir())}
	if r, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		roots = append(roots, r)
	}
	return append(roots, "/private/tmp/", "/private/var/folders/", "/var/folders/")
}

func mentionsTemp(line string) bool {
	for _, r := range tempRoots() {
		if strings.Contains(line, r) {
			return true
		}
	}
	return false
}
