package runtime

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

func TestAllowlistedEnvRefusesUnknownKeysRatherThanDroppingThem(t *testing.T) {
	t.Parallel()
	_, err := AllowlistedEnv([]EnvVar{
		{Key: "MATE_AGENT_ID", Value: "mate_001"},
		{Key: "SECRET", Value: "nope"},
	}, nil)
	if err == nil {
		t.Fatal("unknown keys must be refused; a silently dropped identity value is worse than a refused launch")
	}
	if observability.ExitCode(err) != observability.ExitUsage {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("refusal must name the rejected key: %v", err)
	}
}

func TestAllowlistedEnvRefusesHerdrKeys(t *testing.T) {
	t.Parallel()
	_, err := AllowlistedEnv([]EnvVar{{Key: "HERDR_PANE_ID", Value: "w1:p1"}}, nil)
	if err == nil {
		t.Fatal("HERDR_* is injected by Herdr, not Mate")
	}
}

func TestAllowlistedEnvRefusesEmptyValuesAndDuplicates(t *testing.T) {
	t.Parallel()
	if _, err := AllowlistedEnv([]EnvVar{{Key: "MATE_AGENT_ID", Value: ""}}, nil); err == nil {
		t.Fatal("empty value must be omitted by the caller, not injected")
	}
	if _, err := AllowlistedEnv([]EnvVar{
		{Key: "MATE_AGENT_ID", Value: "a"},
		{Key: "MATE_AGENT_ID", Value: "b"},
	}, nil); err == nil {
		t.Fatal("duplicate keys must be refused")
	}
}

func TestAllowlistedEnvKeepsIdentityKeys(t *testing.T) {
	t.Parallel()
	got, err := AllowlistedEnv([]EnvVar{
		{Key: "MATE_AGENT_ID", Value: "mate_001"},
		{Key: "MATE_AGENT_ROLE", Value: "mate"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %#v", got)
	}
}

// A harness's own variable is allowed only when the caller says a
// registered harness declares it: the runtime holds no harness's name.
func TestAllowlistedEnvTakesProviderKeysFromTheCaller(t *testing.T) {
	t.Parallel()
	vars := []EnvVar{{Key: "MATE_AGENT_ID", Value: "a"}, {Key: "SOME_HOME", Value: "/h"}}
	if _, err := AllowlistedEnv(vars, nil); err == nil {
		t.Fatal("an undeclared provider key was allowed")
	}
	got, err := AllowlistedEnv(vars, []string{"SOME_HOME"})
	if err != nil || len(got) != 2 {
		t.Fatalf("a declared provider key: %v, %v", got, err)
	}
}

func TestPaneEnvCopiesLaunchSpecEnv(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path := filepath.Join(cwd, "context.md")
	if err := os.WriteFile(path, []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	launch, err := harness.Claude{}.Build(context.Background(), harness.AgentSpec{
		Kind:        harness.KindClaude,
		Cwd:         cwd,
		ContextPath: path,
		Env: []harness.EnvVar{
			{Key: "MATE_AGENT_ID", Value: "mate_001"},
			{Key: "MATE_AGENT_ROLE", Value: "mate"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(launch.Env()) == 0 {
		t.Fatal("LaunchSpec.Env must carry the identity env so PaneEnv has a live source")
	}
	got, err := PaneEnv(launch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "MATE_AGENT_ID" || got[0].Value != "mate_001" || got[1].Key != "MATE_AGENT_ROLE" {
		t.Fatalf("PaneEnv = %#v", got)
	}
	if got := launch.UnsetEnv(); !slices.Equal(got, append([]string{"CLAUDE_CONFIG_DIR"}, harness.NestedSessionEnv...)) {
		t.Fatalf("PaneEnv launch unset env = %#v", got)
	}
}

func TestPaneEnvRefusesUnknownLaunchEnv(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	path := filepath.Join(cwd, "context.md")
	if err := os.WriteFile(path, []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := harness.Claude{}.Build(context.Background(), harness.AgentSpec{
		Kind:        harness.KindClaude,
		Cwd:         cwd,
		ContextPath: path,
		Env:         []harness.EnvVar{{Key: "SECRET", Value: "nope"}},
	})
	if err == nil {
		t.Fatal("Build must refuse unknown env rather than drop it onto LaunchSpec.Env")
	}
}
