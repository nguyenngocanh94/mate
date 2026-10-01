package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/harnesstest"
)

func TestClaudeDefaultUsesFileFlag(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path := harnesstest.WriteAbs(t, cwd, "context.md", "you are mate")
	spec, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Kind:        KindClaude,
		Cwd:         cwd,
		ContextPath: path,
		TaskPrompt:  "FIRST-CREW-TASK",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Delivery() != harness.DeliveryAppendSystemPromptFile {
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
		if env.Key == ClaudeConfigDirEnv {
			t.Fatalf("default launch must not set %s: %#v", ClaudeConfigDirEnv, spec.Env())
		}
	}
	if got := spec.UnsetEnv(); !slices.Equal(got, append([]string{ClaudeConfigDirEnv}, harness.NestedSessionEnv...)) {
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
	contextPath := harnesstest.WriteAbs(t, cwd, "context.md", "you are mate")
	settingsPath := harnesstest.WriteAbs(t, cwd, "settings.json", "{}\n")
	sessionID := "11111111-1111-4111-8111-111111111111"
	spec, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Kind:        KindClaude,
		Cwd:         cwd,
		ContextPath: contextPath,
		Launch:      claudeLaunch{sessionID: sessionID, settingsPath: settingsPath},
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
	contextPath := harnesstest.WriteAbs(t, cwd, "context.md", "you are mate")
	settingsPath := harnesstest.WriteAbs(t, cwd, "settings.json", "{}\n")
	resumeID := "22222222-2222-4222-8222-222222222222"
	spec, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Kind:            KindClaude,
		Cwd:             cwd,
		ContextPath:     contextPath,
		ResumeSessionID: resumeID,
		Launch:          claudeLaunch{settingsPath: settingsPath},
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
	contextPath := harnesstest.WriteAbs(t, cwd, "context.md", "you are mate")
	settingsPath := harnesstest.WriteAbs(t, cwd, "settings.json", "{}\n")
	_, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Kind:            KindClaude,
		Cwd:             cwd,
		ContextPath:     contextPath,
		ResumeSessionID: "22222222-2222-4222-8222-222222222222",
		Launch:          claudeLaunch{sessionID: "11111111-1111-4111-8111-111111111111", settingsPath: settingsPath},
	})
	if !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired", err)
	}
}

func TestClaudeResumeRequiresSettingsPath(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	contextPath := harnesstest.WriteAbs(t, cwd, "context.md", "you are mate")
	_, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Kind:            KindClaude,
		Cwd:             cwd,
		ContextPath:     contextPath,
		ResumeSessionID: "22222222-2222-4222-8222-222222222222",
	})
	if !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired", err)
	}
}

func TestClaudeLaunchArgsRequireCompleteTranscriptLocator(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	contextPath := harnesstest.WriteAbs(t, cwd, "context.md", "you are mate")
	_, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Kind:        KindClaude,
		Cwd:         cwd,
		ContextPath: contextPath,
		Launch:      claudeLaunch{sessionID: "11111111-1111-4111-8111-111111111111"},
	})
	if !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("incomplete locator err = %v, want ErrContextRequired", err)
	}
}

func TestClaudeConstructorFailsOnMissingFile(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	_, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Kind:        KindClaude,
		Cwd:         cwd,
		ContextPath: filepath.Join(cwd, "nope.md"),
	})
	if !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired from the constructor", err)
	}
}

func TestClaudeInlineCarriesContentsNotPathToken(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	body := "you are mate\nnever drop this line\n"
	path := harnesstest.WriteAbs(t, cwd, "context.md", body)
	spec, err := Claude{InlineFallback: true}.Build(context.Background(), harness.AgentSpec{
		Kind:        KindClaude,
		Cwd:         cwd,
		ContextPath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Delivery() != harness.DeliveryAppendSystemPrompt {
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
	path := harnesstest.WriteAbs(t, cwd, "context.md", strings.Repeat("x", harness.DefaultMaxInlineBytes+1))
	_, err := Claude{InlineFallback: true}.Build(context.Background(), harness.AgentSpec{
		Kind:        KindClaude,
		Cwd:         cwd,
		ContextPath: path,
	})
	if !errors.Is(err, harness.ErrContextTooLarge) {
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
	_, err = Claude{}.Build(context.Background(), harness.AgentSpec{
		Cwd:         mateCwd,
		ContextPath: "ctx.md",
	})
	if !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("relative context: err = %v", err)
	}
	abs := filepath.Join(mateCwd, "ctx.md")
	_, err = Claude{}.Build(context.Background(), harness.AgentSpec{
		Cwd:         ".",
		ContextPath: abs,
	})
	if !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("relative cwd: err = %v", err)
	}
}

func TestClaudeRejectsEmptyFile(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path := harnesstest.WriteAbs(t, cwd, "empty.md", "")
	_, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Cwd: cwd, ContextPath: path,
	})
	if !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestClaudeLaunchEnvIsAllowlisted(t *testing.T) {
	cwd := t.TempDir()
	path := harnesstest.WriteAbs(t, cwd, "context.md", "you are mate")
	configDir := filepath.Join(t.TempDir(), "claude-config")
	spec, err := Claude{ConfigDir: configDir}.Build(context.Background(), harness.AgentSpec{
		Cwd: cwd, ContextPath: path,
		Env: []harness.EnvVar{
			{Key: "MATE_AGENT_ID", Value: "mate_001"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Env()) != 2 || spec.Env()[0].Key != "MATE_AGENT_ID" || spec.Env()[1].Key != "CLAUDE_CONFIG_DIR" || spec.Env()[1].Value != configDir || spec.ClaudeConfigDir() != configDir {
		t.Fatalf("env = %#v", spec.Env())
	}
	if got := spec.UnsetEnv(); slices.Contains(got, ClaudeConfigDirEnv) || !slices.Equal(got, harness.NestedSessionEnv) {
		t.Fatalf("custom config launch must unset only the nested-session variables: %#v", got)
	}
}

func TestClaudeLaunchSpecRefusesUnknownEnv(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path := harnesstest.WriteAbs(t, cwd, "context.md", "you are mate")
	_, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Cwd: cwd, ContextPath: path,
		Env: []harness.EnvVar{
			{Key: "MATE_AGENT_ID", Value: "mate_001"},
			{Key: "SECRET", Value: "nope"},
			{Key: "HERDR_PANE_ID", Value: "w1:p1"},
		},
	})
	if err == nil {
		t.Fatal("unknown env must be refused rather than dropped onto LaunchSpec.Env")
	}
}
