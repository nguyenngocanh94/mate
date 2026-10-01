package claude

import (
	"context"
	"slices"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/harnesstest"
)

func TestClaudeLaunchCarriesModelAndEffort(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	contextPath := harnesstest.WriteAbs(t, cwd, "context.md", "you are a crew")
	settingsPath := harnesstest.WriteAbs(t, cwd, "settings.json", "{}\n")
	spec, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Kind: KindClaude, Cwd: cwd, ContextPath: contextPath,
		Launch: claudeLaunch{sessionID: "11111111-1111-4111-8111-111111111111", settingsPath: settingsPath},
		Model:  "haiku", Effort: harness.EffortLow,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--dangerously-skip-permissions", "--session-id", "11111111-1111-4111-8111-111111111111",
		"--settings", settingsPath, "--model", "haiku", "--effort", "low", "--append-system-prompt-file", contextPath}
	if !slices.Equal(spec.Args(), want) {
		t.Fatalf("args = %#v, want %#v", spec.Args(), want)
	}
	if spec.Model() != "haiku" || spec.Effort() != harness.EffortLow {
		t.Fatalf("spec model/effort = %q/%q", spec.Model(), spec.Effort())
	}
}
