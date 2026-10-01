package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexSessionsDirPrecedenceAndAbsoluteValidation(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(string(filepath.Separator), "env", "codex"))
	got, err := CodexSessionsDir(filepath.Join(string(filepath.Separator), "arg", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(string(filepath.Separator), "arg", "codex", "sessions")
	if got != want {
		t.Fatalf("explicit sessions dir = %q, want %q", got, want)
	}
	if _, err := CodexSessionsDir("relative-codex"); err == nil {
		t.Fatal("relative explicit CODEX_HOME was accepted")
	}

	got, err = CodexSessionsDir("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join("env", "codex", "sessions")) {
		t.Fatalf("environment sessions dir = %q", got)
	}
}

func TestCodexSessionsDirUsesDefaultWhenUnset(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := CodexSessionsDir("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".codex", "sessions")
	if got != want {
		t.Fatalf("default sessions dir = %q, want %q", got, want)
	}
}

// A live run (MATE_LIVE=1) may launch Codex only in a home under the temp
// directory: the operator's ~/.codex is refused before anything starts, and
// so is an explicit home elsewhere. Without MATE_LIVE nothing changes.
func TestLaunchCodexHomeRefusesTheOperatorsHomeInALiveRun(t *testing.T) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	operator := filepath.Join(userHome, ".codex")
	lab := t.TempDir()

	t.Setenv("MATE_LIVE", "")
	t.Setenv("CODEX_HOME", "")
	if got, err := LaunchCodexHome(""); err != nil || got != operator {
		t.Fatalf("outside a live run: LaunchCodexHome() = %q, %v; want the default %q", got, err, operator)
	}

	t.Setenv("MATE_LIVE", "1")
	for _, refused := range []struct{ name, explicit, env string }{
		{"default home", "", ""},
		{"CODEX_HOME in the environment", "", operator},
		{"explicit home", operator, lab},
		{"the temp directory itself", os.TempDir(), ""},
	} {
		t.Setenv("CODEX_HOME", refused.env)
		if got, err := LaunchCodexHome(refused.explicit); err == nil {
			t.Fatalf("%s: a live run was allowed to launch Codex in %q", refused.name, got)
		} else if !strings.Contains(err.Error(), "codexlab") {
			t.Fatalf("%s: refusal %q does not say where a lab home comes from", refused.name, err)
		}
	}
	t.Setenv("CODEX_HOME", lab)
	if got, err := LaunchCodexHome(""); err != nil || got != lab {
		t.Fatalf("a lab home under the temp directory: LaunchCodexHome() = %q, %v", got, err)
	}
	if got, err := LaunchCodexHome(filepath.Join(lab, "nested")); err != nil || got != filepath.Join(lab, "nested") {
		t.Fatalf("an explicit lab home: LaunchCodexHome() = %q, %v", got, err)
	}
}
