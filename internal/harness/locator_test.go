package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeProjectsDirUsesConfiguredRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(string(filepath.Separator), "tmp", "claude-config"))
	got, err := ClaudeProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(string(filepath.Separator), "tmp", "claude-config", "projects")
	if got != want {
		t.Fatalf("projects dir = %q, want %q", got, want)
	}
}

func TestClaudeProjectsDirRejectsRelativeConfiguredRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "relative-claude")
	if _, err := ClaudeProjectsDir(); err == nil {
		t.Fatal("relative CLAUDE_CONFIG_DIR was accepted")
	}
}

func TestClaudeConfigDirForLaunchSeparatesDefaultAndConfiguredValues(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, set, err := ClaudeConfigDirForLaunch("")
	if err != nil {
		t.Fatal(err)
	}
	if set || got != filepath.Join(home, ".claude") {
		t.Fatalf("default launch config = (%q, %t)", got, set)
	}

	custom := filepath.Join(string(filepath.Separator), "tmp", "custom-claude")
	got, set, err = ClaudeConfigDirForLaunch(custom)
	if err != nil {
		t.Fatal(err)
	}
	if !set || got != custom {
		t.Fatalf("explicit launch config = (%q, %t), want (%q, true)", got, set, custom)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	got, set, err = ClaudeConfigDirForLaunch("")
	if err != nil {
		t.Fatal(err)
	}
	if !set || got != custom {
		t.Fatalf("inherited launch config = (%q, %t), want (%q, true)", got, set, custom)
	}
}

func TestClaudeTranscriptPathUsesProviderSessionID(t *testing.T) {
	projects := filepath.Join(string(filepath.Separator), "tmp", "claude", "projects")
	cwd := filepath.Join(string(filepath.Separator), "work", "repo")
	const sessionID = "11111111-1111-4111-8111-111111111111"
	got := ClaudeTranscriptPath(projects, cwd, sessionID)
	want := filepath.Join(projects, ClaudeProjectSlug(cwd), sessionID+".jsonl")
	if got != want {
		t.Fatalf("transcript path = %q, want %q", got, want)
	}
}

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
