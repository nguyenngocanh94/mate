package harness

import (
	"context"
	"os"
	"slices"
	"testing"
)

func TestParseEffortKnowsTheFiveLevels(t *testing.T) {
	for _, s := range []string{"low", "medium", "high", "xhigh", "max", " High "} {
		if _, err := ParseEffort(s); err != nil {
			t.Errorf("ParseEffort(%q): %v", s, err)
		}
	}
	for _, s := range []string{"hgih", "ultra", "-c", "low medium"} {
		if _, err := ParseEffort(s); err == nil {
			t.Errorf("ParseEffort(%q) accepted a level no harness has", s)
		}
	}
	if e, err := ParseEffort(""); err != nil || e != "" {
		t.Fatalf("ParseEffort(\"\") = %q, %v; want the harness default", e, err)
	}
}

// Claude accepts all five (claude 2.1.282 --help: "low, medium, high,
// xhigh, max"); Codex's model_reasoning_effort advertises four for its
// catalogue models, max only for some (firstmate's codex record, verified
// on codex-cli 0.142.1 and 0.153.4). A level a harness does not take is
// recorded but not passed.
func TestEffortSupportPerHarness(t *testing.T) {
	for _, e := range []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax} {
		if !(Claude{}).Info().SupportsEffort(e) {
			t.Errorf("claude does not take %s", e)
		}
	}
	for _, e := range []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh} {
		if !(Codex{}).Info().SupportsEffort(e) {
			t.Errorf("codex does not take %s", e)
		}
	}
	if (Codex{}).Info().SupportsEffort(EffortMax) {
		t.Error("codex takes max; its catalogue does not advertise it for every model")
	}
}

func TestParseModelRefusesWhatCouldBeAFlag(t *testing.T) {
	for _, s := range []string{"opus", "claude-sonnet-5", "gpt-5.5", "claude-fable-5-1"} {
		if got, err := ParseModel(s); err != nil || got != s {
			t.Errorf("ParseModel(%q) = %q, %v", s, got, err)
		}
	}
	for _, s := range []string{"-m", "--dangerous", "gpt 5", "a\"b", "a=b"} {
		if _, err := ParseModel(s); err == nil {
			t.Errorf("ParseModel(%q) accepted a value that is not one model name", s)
		}
	}
}

func TestClaudeLaunchCarriesModelAndEffort(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	contextPath := writeAbs(t, cwd, "context.md", "you are a crew")
	settingsPath := writeAbs(t, cwd, "settings.json", "{}\n")
	spec, err := Claude{}.Build(context.Background(), AgentSpec{
		Kind: KindClaude, Cwd: cwd, ContextPath: contextPath,
		Launch: claudeLaunch{sessionID: "11111111-1111-4111-8111-111111111111", settingsPath: settingsPath},
		Model:  "haiku", Effort: EffortLow,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--dangerously-skip-permissions", "--session-id", "11111111-1111-4111-8111-111111111111",
		"--settings", settingsPath, "--model", "haiku", "--effort", "low", "--append-system-prompt-file", contextPath}
	if !slices.Equal(spec.Args(), want) {
		t.Fatalf("args = %#v, want %#v", spec.Args(), want)
	}
	if spec.Model() != "haiku" || spec.Effort() != EffortLow {
		t.Fatalf("spec model/effort = %q/%q", spec.Model(), spec.Effort())
	}
}

func TestCodexLaunchCarriesModelAndEffortBeforeTheResumeID(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("you are a crew"), 0o644); err != nil {
		t.Fatal(err)
	}
	const id = "01a0d260-cd47-77d2-bee7-46d98aa0461a"
	spec, err := Codex{}.Build(context.Background(), AgentSpec{
		Kind: KindCodex, Cwd: cwd, ResumeSessionID: id, Model: "gpt-5.5", Effort: EffortHigh,
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
	spec, err := Codex{}.Build(context.Background(), AgentSpec{Kind: KindCodex, Cwd: cwd, Effort: EffortMax})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range spec.Args() {
		if a == `model_reasoning_effort="max"` {
			t.Fatalf("codex was passed max: %#v", spec.Args())
		}
	}
	if spec.Effort() != EffortMax || !spec.EffortOmitted() {
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
	spec, err := Codex{}.Build(context.Background(), AgentSpec{Kind: KindCodex, Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range spec.Args() {
		if a == "-m" || a == "--model" || a == "--effort" {
			t.Fatalf("an empty profile added %q: %#v", a, spec.Args())
		}
	}
}
