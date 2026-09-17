package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/config"
)

func TestClaudeDefaultUsesFileFlag(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path := writeAbs(t, cwd, "context.md", "you are mate")
	spec, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind:        KindClaude,
		Cwd:         cwd,
		ContextPath: path,
		TaskPrompt:  "FIRST-CREW-TASK",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Delivery() != DeliveryAppendSystemPromptFile {
		t.Fatalf("delivery = %q", spec.Delivery())
	}
	args := spec.Args()
	if len(args) != 3 || args[0] != "--dangerously-skip-permissions" || args[1] != "--append-system-prompt-file" || args[2] != path {
		t.Fatalf("args = %#v", args)
	}
	if !spec.Startable() {
		t.Fatal("spec must be startable")
	}
	if spec.TaskPrompt() != "FIRST-CREW-TASK" {
		t.Fatalf("task prompt = %q", spec.TaskPrompt())
	}
	if got := spec.ClaudeConfigDir(); got == "" {
		t.Fatal("default transcript locator must still retain the effective Claude config directory")
	}
	for _, env := range spec.Env() {
		if env.Key == config.EnvClaudeConfigDir {
			t.Fatalf("default launch must not set %s: %#v", config.EnvClaudeConfigDir, spec.Env())
		}
	}
	if got := spec.UnsetEnv(); !slices.Equal(got, append([]string{config.EnvClaudeConfigDir}, NestedSessionEnv...)) {
		t.Fatalf("default launch unset env = %#v", got)
	}
	notes := strings.Join(spec.Notes(), " ")
	if strings.Contains(notes, "prompts only") {
		t.Fatalf("notes must not claim the flag suppresses prompts only; a second Bypass Permissions wall exists, notes=%q", notes)
	}
	if !strings.Contains(notes, "directory-trust") || !strings.Contains(notes, "Bypass Permissions mode") {
		t.Fatalf("notes must name both one-time walls the flag does not clear, notes=%q", notes)
	}
}

func TestClaudeLaunchArgsCarryTranscriptLocator(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	contextPath := writeAbs(t, cwd, "context.md", "you are mate")
	settingsPath := writeAbs(t, cwd, "settings.json", "{}\n")
	sessionID := "11111111-1111-4111-8111-111111111111"
	spec, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind:               KindClaude,
		Cwd:                cwd,
		ContextPath:        contextPath,
		ClaudeSessionID:    sessionID,
		ClaudeSettingsPath: settingsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--dangerously-skip-permissions", "--session-id", sessionID, "--settings", settingsPath, "--append-system-prompt-file", contextPath}
	if got := spec.Args(); !slices.Equal(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestClaudeResumeUsesResumeFlagNotSessionID(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	contextPath := writeAbs(t, cwd, "context.md", "you are mate")
	settingsPath := writeAbs(t, cwd, "settings.json", "{}\n")
	resumeID := "22222222-2222-4222-8222-222222222222"
	spec, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind:               KindClaude,
		Cwd:                cwd,
		ContextPath:        contextPath,
		ResumeSessionID:    resumeID,
		ClaudeSettingsPath: settingsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--dangerously-skip-permissions", "--resume", resumeID, "--settings", settingsPath, "--append-system-prompt-file", contextPath}
	got := spec.Args()
	if !slices.Equal(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	if slices.Contains(got, "--session-id") {
		t.Fatalf("resume args must never carry --session-id: %#v", got)
	}
}

func TestClaudeResumeAndSessionIDAreMutuallyExclusive(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	contextPath := writeAbs(t, cwd, "context.md", "you are mate")
	settingsPath := writeAbs(t, cwd, "settings.json", "{}\n")
	_, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind:               KindClaude,
		Cwd:                cwd,
		ContextPath:        contextPath,
		ClaudeSessionID:    "11111111-1111-4111-8111-111111111111",
		ResumeSessionID:    "22222222-2222-4222-8222-222222222222",
		ClaudeSettingsPath: settingsPath,
	})
	if !errors.Is(err, ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired", err)
	}
}

func TestClaudeResumeRequiresSettingsPath(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	contextPath := writeAbs(t, cwd, "context.md", "you are mate")
	_, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind:            KindClaude,
		Cwd:             cwd,
		ContextPath:     contextPath,
		ResumeSessionID: "22222222-2222-4222-8222-222222222222",
	})
	if !errors.Is(err, ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired", err)
	}
}

func TestClaudeLaunchArgsRequireCompleteTranscriptLocator(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	contextPath := writeAbs(t, cwd, "context.md", "you are mate")
	_, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind:            KindClaude,
		Cwd:             cwd,
		ContextPath:     contextPath,
		ClaudeSessionID: "11111111-1111-4111-8111-111111111111",
	})
	if !errors.Is(err, ErrContextRequired) {
		t.Fatalf("incomplete locator err = %v, want ErrContextRequired", err)
	}
}

func TestClaudeConstructorFailsOnMissingFile(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	_, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind:        KindClaude,
		Cwd:         cwd,
		ContextPath: filepath.Join(cwd, "nope.md"),
	})
	if !errors.Is(err, ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired from the constructor", err)
	}
}

func TestClaudeInlineCarriesContentsNotPathToken(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	body := "you are mate\nnever drop this line\n"
	path := writeAbs(t, cwd, "context.md", body)
	spec, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind:           KindClaude,
		Cwd:            cwd,
		ContextPath:    path,
		InlineFallback: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Delivery() != DeliveryAppendSystemPrompt {
		t.Fatalf("delivery = %q", spec.Delivery())
	}
	args := spec.Args()
	if len(args) != 3 || args[0] != "--dangerously-skip-permissions" || args[1] != "--append-system-prompt" || args[2] != body {
		t.Fatalf("inline must carry file contents, got %#v", args)
	}
	notes := strings.Join(spec.Notes(), " ")
	if !strings.Contains(notes, "process table") {
		t.Fatalf("inline tradeoff must stay recorded, notes=%q", notes)
	}
}

func TestClaudeInlineRejectsOversizedBeforeReadingIntoArgv(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path := writeAbs(t, cwd, "context.md", strings.Repeat("x", DefaultMaxInlineBytes+1))
	_, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind:           KindClaude,
		Cwd:            cwd,
		ContextPath:    path,
		InlineFallback: true,
	})
	if !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("err = %v, want ErrContextTooLarge", err)
	}
}

func TestClaudeRejectsRelativeContextAndCwd(t *testing.T) {
	mateCwd := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(mateCwd); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()
	if err := os.WriteFile("ctx.md", []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Cwd:         mateCwd,
		ContextPath: "ctx.md",
	})
	if !errors.Is(err, ErrContextRequired) {
		t.Fatalf("relative context: err = %v", err)
	}
	abs := filepath.Join(mateCwd, "ctx.md")
	_, err = Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Cwd:         ".",
		ContextPath: abs,
	})
	if !errors.Is(err, ErrContextRequired) {
		t.Fatalf("relative cwd: err = %v", err)
	}
}

func TestClaudeRejectsEmptyFile(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path := writeAbs(t, cwd, "empty.md", "")
	_, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Cwd: cwd, ContextPath: path,
	})
	if !errors.Is(err, ErrContextRequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestHandAssembledSpecRejectsDuplicateAndTerminator(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path := writeAbs(t, cwd, "context.md", "you are mate")
	spec, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{Cwd: cwd, ContextPath: path})
	if err != nil {
		t.Fatal(err)
	}
	spec.args = []string{"--", "--append-system-prompt-file", path}
	if err := spec.ValidateRequiredContext(); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("flag after -- : err = %v", err)
	}
	spec.args = []string{"--append-system-prompt-file", path, "--append-system-prompt-file", filepath.Join(cwd, "other.md")}
	if err := spec.ValidateRequiredContext(); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("duplicate flag: err = %v", err)
	}
	spec.args = []string{"--append-system-prompt", "@" + path}
	spec.delivery = DeliveryAppendSystemPrompt
	if err := spec.ValidateRequiredContext(); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("path token: err = %v", err)
	}
}

func TestClaudeLaunchEnvIsAllowlisted(t *testing.T) {
	cwd := t.TempDir()
	path := writeAbs(t, cwd, "context.md", "you are mate")
	configDir := filepath.Join(t.TempDir(), "claude-config")
	spec, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Cwd: cwd, ContextPath: path,
		Config: Config{ClaudeConfigDir: configDir},
		Env: []EnvVar{
			{Key: "MATEV2_AGENT_ID", Value: "mate_001"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Env()) != 2 || spec.Env()[0].Key != "MATEV2_AGENT_ID" || spec.Env()[1].Key != "CLAUDE_CONFIG_DIR" || spec.Env()[1].Value != configDir || spec.ClaudeConfigDir() != configDir {
		t.Fatalf("env = %#v", spec.Env())
	}
	if got := spec.UnsetEnv(); slices.Contains(got, config.EnvClaudeConfigDir) || !slices.Equal(got, NestedSessionEnv) {
		t.Fatalf("custom config launch must unset only the nested-session variables: %#v", got)
	}
}

func TestClaudeLaunchSpecRefusesUnknownEnv(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path := writeAbs(t, cwd, "context.md", "you are mate")
	_, err := Claude{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Cwd: cwd, ContextPath: path,
		Env: []EnvVar{
			{Key: "MATEV2_AGENT_ID", Value: "mate_001"},
			{Key: "SECRET", Value: "nope"},
			{Key: "HERDR_PANE_ID", Value: "w1:p1"},
		},
	})
	if err == nil {
		t.Fatal("unknown env must be refused rather than dropped onto LaunchSpec.Env")
	}
}

func TestLaunchSpecNotStartableWhenZero(t *testing.T) {
	t.Parallel()
	var s LaunchSpec
	if s.Startable() {
		t.Fatal("zero spec must not be startable")
	}
}

func writeAbs(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Go always allows the zero LaunchSpec from any package. It must not pass
// validation: a validator that reports no problem about an unusable value is
// a trap for a caller who checks validation but not startability.
func TestZeroLaunchSpecFailsValidation(t *testing.T) {
	t.Parallel()
	var s LaunchSpec
	if s.Startable() {
		t.Fatal("zero spec must not be startable")
	}
	if err := s.ValidateRequiredContext(); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("zero spec must fail validation, err = %v", err)
	}
}
