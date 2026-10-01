package codex

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

func TestCodexLaunchCarriesModelAndEffortBeforeTheResumeID(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("you are a crew"), 0o644); err != nil {
		t.Fatal(err)
	}
	const id = "01a0d260-cd47-77d2-bee7-46d98aa0461a"
	spec, err := Codex{}.Build(context.Background(), harness.AgentSpec{
		Kind: KindCodex, Cwd: cwd, ResumeSessionID: id, Model: "gpt-5.5", Effort: harness.EffortHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"resume", "--dangerously-bypass-approvals-and-sandbox", "-c", CodexDisableUpdateCheck,
		"-c", CodexProjectDocMaxBytesOverride, "-m", "gpt-5.5", "-c", `model_reasoning_effort="high"`, id}
	if !slices.Equal(spec.Args(), want) {
		t.Fatalf("args = %#v, want %#v", spec.Args(), want)
	}
}

// An effort the harness does not take is kept on the spec - so it is
// recorded - but never reaches the argv as a value the CLI would refuse.
func TestAnUnsupportedEffortIsRecordedNotPassed(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("you are a crew"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := Codex{}.Build(context.Background(), harness.AgentSpec{Kind: KindCodex, Cwd: cwd, Effort: harness.EffortMax})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range spec.Args() {
		if a == `model_reasoning_effort="max"` {
			t.Fatalf("codex was passed max: %#v", spec.Args())
		}
	}
	if spec.Effort() != harness.EffortMax || !spec.EffortOmitted() {
		t.Fatalf("effort = %q omitted=%v; want max recorded and omitted", spec.Effort(), spec.EffortOmitted())
	}
}

// No model and no effort is the harness's own default: no flag at all.
func TestNoProfileMeansNoFlags(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("you are a crew"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := Codex{}.Build(context.Background(), harness.AgentSpec{Kind: KindCodex, Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range spec.Args() {
		if a == "-m" || a == "--model" || a == "--effort" {
			t.Fatalf("an empty profile added %q: %#v", a, spec.Args())
		}
	}
}
