package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// manualInCwdSpec is a Mate-shaped Claude launch: a cwd that already holds
// CLAUDE.md and no context path.
func manualInCwdSpec(t *testing.T) harness.AgentSpec {
	t.Helper()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("@AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("# Mate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return harness.AgentSpec{
		Kind:   KindClaude,
		Cwd:    cwd,
		Launch: claudeLaunch{manualInCwd: true},
	}
}

// TestClaudeManualInCwdPassesNoContextFlag is the task 17 debt fix: a Mate's
// manual reaches Claude once, through the CLAUDE.md in its cwd, so the argv
// must carry no --append-system-prompt-file at all.
func TestClaudeManualInCwdPassesNoContextFlag(t *testing.T) {
	spec, err := Claude{}.Build(context.Background(), manualInCwdSpec(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	args := spec.Args()
	if slices.Contains(args, "--append-system-prompt-file") || slices.Contains(args, "--append-system-prompt") {
		t.Fatalf("args = %v, want no context flag when the manual is loaded from cwd", args)
	}
	if want := []string{"--dangerously-skip-permissions"}; !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	if spec.Delivery() != harness.DeliveryCwdManual {
		t.Fatalf("delivery = %q, want %q", spec.Delivery(), harness.DeliveryCwdManual)
	}
	if spec.ContextPath() != "" {
		t.Fatalf("context path = %q, want empty", spec.ContextPath())
	}
	if len(spec.ContextFiles()) != 0 {
		t.Fatalf("context files = %v, want none", spec.ContextFiles())
	}
	if !spec.Startable() {
		t.Fatal("a cwd-manual spec must still be startable")
	}
	if err := spec.ValidateRequiredContext(); err != nil {
		t.Fatalf("ValidateRequiredContext: %v", err)
	}
}

// TestClaudeManualInCwdRefusesAContextPath keeps the two ways of delivering
// the manual mutually exclusive: a spec that does both is the exact double
// delivery this flag removes.
func TestClaudeManualInCwdRefusesAContextPath(t *testing.T) {
	spec := manualInCwdSpec(t)
	spec.ContextPath = filepath.Join(spec.Cwd, "AGENTS.md")
	if _, err := (Claude{}).Build(context.Background(), spec); !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired", err)
	}
}

func TestClaudeManualInCwdRefusesInlineFallback(t *testing.T) {
	spec := manualInCwdSpec(t)
	if _, err := (Claude{InlineFallback: true}).Build(context.Background(), spec); !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired", err)
	}
}

// TestClaudeWithoutManualInCwdStillRequiresAContextPath proves the relaxation
// is Prepare's to grant: a crew launch, which has no CLAUDE.md in its worktree, is still
// refused without one.
func TestClaudeWithoutManualInCwdStillRequiresAContextPath(t *testing.T) {
	spec := manualInCwdSpec(t)
	spec.Launch = nil
	if _, err := (Claude{}).Build(context.Background(), spec); !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired", err)
	}
}
