package harness

import (
	"context"
	"slices"
	"testing"
)

func TestClaudeMateHasAnIsolatedProfile(t *testing.T) {
	spec := manualInCwdSpec(t)
	spec.Role = RoleMate
	launch, err := (Claude{}).Build(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	args := launch.Args()
	for flag, value := range map[string]string{
		"--setting-sources": "project",
		"--tools":           "Bash,Read,Write,Edit,Glob,Grep,Skill",
		"--model":           "opus",
		"--effort":          "medium",
		"--autocompact":     "300000",
	} {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) || args[i+1] != value {
			t.Errorf("argv %q: want %s %s", args, flag, value)
		}
	}
	if !slices.Contains(args, "--strict-mcp-config") {
		t.Errorf("Mate inherited global MCP servers: %q", args)
	}
}

func TestClaudeMateKeepsAnExplicitModelAndEffort(t *testing.T) {
	spec := manualInCwdSpec(t)
	spec.Role, spec.Model, spec.Effort = RoleMate, "sonnet", EffortHigh
	launch, err := (Claude{}).Build(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	args := launch.Args()
	for flag, value := range map[string]string{"--model": "sonnet", "--effort": "high"} {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) || args[i+1] != value {
			t.Errorf("explicit profile was lost: %q", args)
		}
	}
}
