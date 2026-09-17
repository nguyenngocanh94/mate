package harness_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/harness"
)

// manualInCwdSpec is a Mate-shaped Claude launch: a cwd that already holds
// CLAUDE.md, no context path, and the session-id/settings pair a real start
// carries.
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
		Kind:        harness.KindClaude,
		Cwd:         cwd,
		ManualInCwd: true,
	}
}

// TestClaudeManualInCwdPassesNoContextFlag is the task 17 debt fix: a Mate's
// manual reaches Claude once, through the CLAUDE.md in its cwd, so the argv
// must carry no --append-system-prompt-file at all.
func TestClaudeManualInCwdPassesNoContextFlag(t *testing.T) {
	spec, err := harness.Claude{}.BuildLaunchSpec(context.Background(), manualInCwdSpec(t))
	if err != nil {
		t.Fatalf("BuildLaunchSpec: %v", err)
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
	if _, err := (harness.Claude{}).BuildLaunchSpec(context.Background(), spec); !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired", err)
	}
}

func TestClaudeManualInCwdRefusesInlineFallback(t *testing.T) {
	spec := manualInCwdSpec(t)
	spec.InlineFallback = true
	if _, err := (harness.Claude{}).BuildLaunchSpec(context.Background(), spec); !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired", err)
	}
}

// TestClaudeWithoutManualInCwdStillRequiresAContextPath proves the relaxation
// is opt-in: a crew launch, which has no CLAUDE.md in its worktree, is still
// refused without one.
func TestClaudeWithoutManualInCwdStillRequiresAContextPath(t *testing.T) {
	spec := manualInCwdSpec(t)
	spec.ManualInCwd = false
	if _, err := (harness.Claude{}).BuildLaunchSpec(context.Background(), spec); !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("err = %v, want ErrContextRequired", err)
	}
}
