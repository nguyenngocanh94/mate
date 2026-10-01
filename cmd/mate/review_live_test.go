package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Exercise the actual CLI, independent reviewer and a deliberately broken
// implementation whose hand-back claims success. A summary must not launder it.
func TestLiveIndependentReviewRejectsFalseHandback(t *testing.T) {
	requireConsoleLive(t)
	session, home := consoleLiveLab(t)
	f := newDiffFixture(t)
	consoleUseLabSession(t, f.w, session)
	w, err := store.Open(f.w.Root())
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })
	commitInWorktree(t, f.worktree, "go.mod", "module example.com/reviewfixture\n\ngo 1.22\n", "module")
	commitInWorktree(t, f.worktree, "calc.go", "package calc\n\nfunc Add(a,b int) int { return a-b }\n", "add function")
	commitInWorktree(t, f.worktree, "calc_test.go", "package calc\nimport \"testing\"\nfunc TestAdd(t *testing.T){if got:=Add(2,3);got!=5{t.Fatalf(\"got %d want 5\",got)}}\n", "test")
	dir := filepath.Dir(w.CrewBrief("shop", "k3"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	brief := "## Captain's words\nImplement Add(a,b) to return the sum of two integers.\n\n## What we already know\n- This is a Go package.\n\n## Build\n- Add must add, not subtract.\n- Out of scope: other arithmetic.\n\n## Acceptance\n- Add(2,3) returns 5. verify: go test ./...\n\n## Open decisions\nnone\n"
	if err := os.WriteFile(w.CrewBrief("shop", "k3"), []byte(brief), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "handback.md"), []byte("## Acceptance\npass: Add(2,3) returns 5; go test ./... passed.\n## Deviations from Build\nnone\n## Still open\nnone\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	deps := spawn.LiveDeps(harnesses)
	deps.Binary = consoleBinaryPath(t)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = spawn.StopCrew(c, w, deps, "shop", "review-add", true)
	})
	result, err := (process.ExecRunner{}).Run(ctx, process.Spec{Name: deps.Binary, Args: []string{"review", "shop", "k3", "--id", "review-add", "--harness", "claude", "--model", "sonnet", "--effort", "high", "--workspace", w.Root()}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("review CLI: %v %d %s", err, result.ExitCode, result.Stderr)
	}
	t.Logf("%s", result.Stdout)
	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		crews, err := spawn.ListCrews(w, "shop")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range crews {
			if c.Crew == "review-add" && c.State == "wait-mate" {
				data, err := os.ReadFile(w.CrewReport("shop", "review-add"))
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("report:\n%s", data)
				if reviewVerdict(string(data)) != "changes-requested" {
					t.Fatalf("bad implementation not rejected: %s", data)
				}
				summary, err := reportSummary(string(data))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(strings.ToLower(summary), "subtract") && !strings.Contains(summary, "a-b") && !strings.Contains(summary, "a - b") {
					t.Fatal("summary lost the actual defect")
				}
				result, err := (process.ExecRunner{}).Run(ctx, process.Spec{Name: deps.Binary, Args: []string{"review", "shop", "k3", "--check", "review-add", "--workspace", w.Root()}})
				if err == nil && result.ExitCode == 0 {
					t.Fatal("failed review accepted by --check")
				}
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("reviewer did not hand back")
}
