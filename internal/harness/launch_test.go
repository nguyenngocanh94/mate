package harness

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// stubScreen is a ScreenProfile no test reads: NewLaunchSpec only needs a
// launch to name one.
type stubScreen struct{ ScreenProfile }

func writeAbs(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// NewLaunchSpec runs the harness's own context check after the generic
// ones, and ValidateRequiredContext runs it again, so the runtime boundary
// refuses a spec whose context stopped being deliverable after it was built.
func TestNewLaunchSpecRunsTheHarnessCheckEachTime(t *testing.T) {
	cwd := t.TempDir()
	path := writeAbs(t, cwd, "context.md", "you are a crew")
	refuse := false
	plan := LaunchPlan{
		RuntimeKind: "fake", Screen: stubScreen{}, Cwd: cwd, Delivery: DeliveryAppendSystemPromptFile,
		Args: []string{"--append-system-prompt-file", path}, ContextPath: path, ContextRequired: true,
		CheckContext: func(LaunchSpec) error {
			if refuse {
				return ErrContextRequired
			}
			return nil
		},
	}
	spec, err := NewLaunchSpec(plan)
	if err != nil {
		t.Fatal(err)
	}
	refuse = true
	if err := spec.ValidateRequiredContext(); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("revalidation err = %v, want the harness check's refusal", err)
	}
	if _, err := NewLaunchSpec(plan); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("NewLaunchSpec err = %v, want the harness check's refusal", err)
	}
	refuse = false
	plan.Env = []EnvVar{{Key: "SECRET", Value: "x"}}
	if _, err := NewLaunchSpec(plan); err == nil {
		t.Fatal("NewLaunchSpec accepted an env key off the allowlist")
	}
}

// The cwd is absolute on every launch, not only one whose context is
// required: a relative one would resolve against the Mate process cwd.
func TestNewLaunchSpecRefusesARelativeCwdWithoutRequiredContext(t *testing.T) {
	plan := LaunchPlan{RuntimeKind: "claude", Screen: stubScreen{}, Cwd: "rel/dir", Delivery: DeliveryCwdManual, Args: []string{"--x"}}
	if spec, err := NewLaunchSpec(plan); err == nil {
		t.Fatalf("NewLaunchSpec accepted cwd %q: %+v", plan.Cwd, spec)
	}
	plan.Cwd = t.TempDir()
	if _, err := NewLaunchSpec(plan); err != nil {
		t.Fatalf("NewLaunchSpec refused an absolute cwd: %v", err)
	}
}

// A launch names the screen profile its pane is read with; a plan without
// one is refused rather than started into a pane nobody can classify.
func TestNewLaunchSpecRefusesAPlanWithoutAScreen(t *testing.T) {
	plan := LaunchPlan{RuntimeKind: "claude", Cwd: t.TempDir(), Delivery: DeliveryCwdManual, Args: []string{"--x"}}
	if spec, err := NewLaunchSpec(plan); err == nil {
		t.Fatalf("NewLaunchSpec accepted a plan with no screen profile: %+v", spec)
	}
	plan.Screen = stubScreen{}
	spec, err := NewLaunchSpec(plan)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Screen() != (stubScreen{}) {
		t.Fatalf("spec screen = %#v, want the plan's", spec.Screen())
	}
}

func TestHandAssembledSpecRejectsDuplicateAndTerminator(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path := writeAbs(t, cwd, "context.md", "you are mate")
	spec, err := NewLaunchSpec(LaunchPlan{
		RuntimeKind: "fake", Screen: stubScreen{}, Cwd: cwd, Delivery: DeliveryAppendSystemPromptFile,
		Args: []string{"--append-system-prompt-file", path}, ContextPath: path, ContextRequired: true,
	})
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

func TestLaunchSpecNotStartableWhenZero(t *testing.T) {
	t.Parallel()
	var s LaunchSpec
	if s.Startable() {
		t.Fatal("zero spec must not be startable")
	}
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
