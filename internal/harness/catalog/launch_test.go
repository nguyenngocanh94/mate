package catalog

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/harness/harnesstest"
)

func TestPrepareRefusesRelativePathsAndOtherRoles(t *testing.T) {
	for _, l := range []harness.Launcher{claude.Claude{}, codex.Codex{}} {
		for name, mutate := range map[string]func(*harness.PrepareRequest){
			"relative cwd":          func(r *harness.PrepareRequest) { r.Cwd = "." },
			"relative state":        func(r *harness.PrepareRequest) { r.StateDir = "state" },
			"relative instructions": func(r *harness.PrepareRequest) { r.ContextPath = "brief.md" },
			"user role":             func(r *harness.PrepareRequest) { r.Role = harness.RoleUser },
		} {
			req := harnesstest.PrepareFixture(t, harness.RoleCrew)
			mutate(&req)
			if _, err := l.Prepare(context.Background(), req); err == nil {
				t.Errorf("%T %s: Prepare succeeded, want a refusal", l, name)
			}
		}
	}
}

// One harness's launch data is never another's.
func TestBuildRefusesAnotherHarnessesLaunchData(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	req := harnesstest.PrepareFixture(t, harness.RoleCrew)
	claudePrep, err := claude.Claude{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	codexPrep, err := codex.Codex{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	harnesstest.WritePrepared(t, codexPrep)
	// The cwd Codex is given would launch, so only the launch-data guard
	// can refuse it.
	if _, err := (codex.Codex{}).Build(context.Background(), harness.AgentSpec{Cwd: req.Cwd}); err != nil {
		t.Fatalf("Codex refused its own prepared cwd: %v", err)
	}
	foreign := fmt.Sprintf("launch data %T", claudePrep.Launch)
	if _, err := (codex.Codex{}).Build(context.Background(), harness.AgentSpec{Cwd: req.Cwd, Launch: claudePrep.Launch}); err == nil || !strings.Contains(err.Error(), foreign) {
		t.Fatalf("Codex given Claude's launch data: err = %v, want a refusal naming %s", err, foreign)
	}
	if _, err := (claude.Claude{}).Build(context.Background(), harness.AgentSpec{Cwd: req.Cwd, ContextPath: req.ContextPath, Launch: "x"}); err == nil || !strings.Contains(err.Error(), "launch data string") {
		t.Fatalf("Claude given foreign launch data: err = %v, want a refusal naming string", err)
	}
}
