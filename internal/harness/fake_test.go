package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/observability"
)

func TestFakeRoutesToClaudeAndCodexConstructors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &Fake{}
	cwd := t.TempDir()
	path := filepath.Join(cwd, "context.md")
	if err := os.WriteFile(path, []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := f.BuildLaunchSpec(ctx, AgentSpec{Kind: KindClaude, Cwd: cwd, ContextPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Delivery() != DeliveryAppendSystemPromptFile {
		t.Fatalf("delivery = %q", spec.Delivery())
	}
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("you are crew"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err = f.BuildLaunchSpec(ctx, AgentSpec{Kind: KindCodex, Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Delivery() != DeliveryInstructionFile {
		t.Fatalf("codex delivery = %q", spec.Delivery())
	}
}

func TestFakeBuildErrInjection(t *testing.T) {
	t.Parallel()
	f := &Fake{BuildErr: observability.NewError(observability.CodeTimeout, "injected")}
	_, err := f.BuildLaunchSpec(context.Background(), AgentSpec{Kind: KindClaude, Cwd: t.TempDir(), ContextPath: filepath.Join(t.TempDir(), "x.md")})
	if !errors.Is(err, f.BuildErr) {
		t.Fatalf("err = %v", err)
	}
}

func TestFakeDoesNotFork(t *testing.T) {
	t.Parallel()
	f := &Fake{}
	if _, err := f.Validate(context.Background(), Config{Kind: KindClaude}); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) == 0 {
		t.Fatal("expected Validate call record")
	}
}
