package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The Crew layout ADR 0004 probed: cwd is the worktree root, its .git is a
// pointer FILE, and an enclosing repo has its own AGENTS.md that Codex never
// reaches. The planner must report the worktree's own tracked AGENTS.md as
// shadowed, because that is the document the Mate-written override displaces.
func TestPlanCodexOverrideReportsShadowedTrackedFileInCrewWorktree(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(ws, CodexBaseName), "WORKSPACE INSTRUCTIONS")
	crew := filepath.Join(ws, ".worktrees", "crew-1")
	writeFile(t, filepath.Join(crew, ".git"), "gdir: /nowhere\n")
	writeFile(t, filepath.Join(crew, CodexBaseName), "REPO INSTRUCTIONS")

	plan, err := PlanCodexOverride(DiscoverRequest{Cwd: crew}, CodexDefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Shadowed == nil {
		t.Fatalf("no shadowed doc reported; the Crew's tracked %s would be lost silently", CodexBaseName)
	}
	if got, want := string(plan.Shadowed.Bytes), "REPO INSTRUCTIONS"; got != want {
		t.Fatalf("shadowed bytes = %q, want %q", got, want)
	}
	if len(plan.Others) != 0 {
		t.Fatalf("discovery must stop at the .git pointer file, got others %v", plan.Others)
	}
	if plan.Budget != CodexDefaultMaxBytes {
		t.Fatalf("budget = %d, want the whole cap %d", plan.Budget, CodexDefaultMaxBytes)
	}
}

// An existing override at cwd is last-start debris, not part of the budget
// the next override must fit into: charging it would refuse a launch that
// actually fits.
func TestPlanCodexOverrideDoesNotChargeTheOverrideItReplaces(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, CodexInstructionPath(cwd), strings.Repeat("S", 5000))

	plan, err := PlanCodexOverride(DiscoverRequest{Cwd: cwd}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Budget != 100 {
		t.Fatalf("budget = %d, want 100 (a stale override is replaced, not added)", plan.Budget)
	}
	if plan.Shadowed != nil {
		t.Fatalf("shadowed = %v, want none", plan.Shadowed)
	}
}

// Ancestor project files between the git root and cwd are metered before the
// override, so they eat the override's budget.
func TestPlanCodexOverrideSubtractsAncestorProjectFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(root, CodexBaseName), strings.Repeat("R", 300))
	sub := filepath.Join(root, "sub")
	writeFile(t, filepath.Join(sub, CodexBaseName), strings.Repeat("S", 200))
	deep := filepath.Join(sub, "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanCodexOverride(DiscoverRequest{Cwd: deep}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Others) != 2 {
		t.Fatalf("others = %d, want the two ancestor files", len(plan.Others))
	}
	if plan.Budget != 1000-500 {
		t.Fatalf("budget = %d, want 500", plan.Budget)
	}
	if err := plan.Fits([]byte(strings.Repeat("O", 500))); err != nil {
		t.Fatalf("exactly-at-budget override refused: %v", err)
	}
	err = plan.Fits([]byte(strings.Repeat("O", 501)))
	if !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("one byte over budget: err = %v, want ErrContextTooLarge", err)
	}
	if !strings.Contains(err.Error(), "501") || !strings.Contains(err.Error(), "500") {
		t.Fatalf("refusal must name the actual and the allowed size, got %q", err)
	}
}

// The global $CODEX_HOME document, the marker and the joiners are rendered
// unmetered (ADR 0004 probe). A planner that charged them would refuse
// launches that fit.
func TestPlanCodexOverrideDoesNotChargeGlobalDoc(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, CodexBaseName), strings.Repeat("G", 900))
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, ".git", "HEAD"), "ref: refs/heads/main\n")

	plan, err := PlanCodexOverride(DiscoverRequest{Cwd: cwd, CodexHome: home}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Budget != 1000 {
		t.Fatalf("budget = %d, want 1000; the global doc is exempt (ADR 0004)", plan.Budget)
	}
	if plan.Global == nil || len(plan.Global.Bytes) != 900 {
		t.Fatalf("global doc not reported: %v", plan.Global)
	}
}

// Ancestors alone over the cap leave no room at all; the refusal must say so
// rather than report a negative budget as if it were headroom.
func TestPlanCodexOverrideRefusesWhenAncestorsAlreadyOverflow(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(root, CodexBaseName), strings.Repeat("R", 300))
	deep := filepath.Join(root, "sub")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCodexOverride(DiscoverRequest{Cwd: deep}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Budget > 0 {
		t.Fatalf("budget = %d, want <= 0", plan.Budget)
	}
	if err := plan.Fits([]byte("x")); !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("err = %v, want ErrContextTooLarge", err)
	}
	if err := plan.Fits(nil); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("empty override: err = %v, want ErrContextRequired", err)
	}
}

// The planner's arithmetic must agree with the refusal the constructor runs
// against the bytes actually on disk. Anything else is a plan that blesses a
// launch BuildLaunchSpec then rejects, or worse the other way round.
func TestPlanCodexOverrideAgreesWithBuildLaunchSpecAtEveryBoundary(t *testing.T) {
	t.Parallel()
	for _, size := range []int{498, 499, 500, 501} {
		t.Run(fmt.Sprintf("size%d", size), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
			writeFile(t, filepath.Join(root, CodexBaseName), strings.Repeat("R", 500))
			cwd := filepath.Join(root, "sub")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			plan, err := PlanCodexOverride(DiscoverRequest{Cwd: cwd}, 1000)
			if err != nil {
				t.Fatal(err)
			}
			content := []byte(strings.Repeat("O", size))
			planErr := plan.Fits(content)
			writeFile(t, CodexInstructionPath(cwd), string(content))
			_, buildErr := Codex{MaxChainBytes: 1000}.BuildLaunchSpec(t.Context(), AgentSpec{Cwd: cwd})
			if (planErr == nil) != (buildErr == nil) {
				t.Fatalf("plan.Fits = %v but BuildLaunchSpec = %v", planErr, buildErr)
			}
		})
	}
}
