package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestReviewRejectsChangedCommitOrAcceptanceContract(t *testing.T) {
	for _, change := range []string{"commit", "brief", "handback", "base", "dirty", "verdict"} {
		t.Run(change, func(t *testing.T) {
			f := newDiffFixture(t)
			commitInWorktree(t, f.worktree, "README.md", "review this\n", "change")
			dir := filepath.Dir(f.w.CrewBrief("shop", "k3"))
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"brief.md", "handback.md"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("original acceptance evidence"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			fp, err := reviewFingerprint(context.Background(), f.w, gitx.New(), "shop", "k3")
			if err != nil {
				t.Fatal(err)
			}
			if problems := brief.Check(reviewBrief(f.w, "shop", "k3", fp), brief.Scout); len(problems) > 0 {
				t.Fatal(problems)
			}
			path, err := reviewRecordPath(f.w, "shop", "review-k3")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.WriteMeta(path, fp); err != nil {
				t.Fatal(err)
			}
			if err := f.w.WriteCrewMeta("shop", "review-k3", map[string]string{spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaKind: "scout"}); err != nil {
				t.Fatal(err)
			}
			if err := f.w.AppendStatus("shop", "review-k3", "wait-mate: review done"); err != nil {
				t.Fatal(err)
			}
			report := f.w.CrewReport("shop", "review-k3")
			if err := os.WriteFile(report, []byte("## Summary\nAll criteria checked.\n## Verdict\npass\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := checkReview(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3"); err != nil {
				t.Fatalf("valid review: %v", err)
			}
			switch change {
			case "commit":
				commitInWorktree(t, f.worktree, "README.md", "changed again\n", "later")
			case "base":
				commitInWorktree(t, f.repo, "new.md", "new base\n", "base changes")
			case "brief", "handback":
				_ = os.WriteFile(filepath.Join(dir, change+".md"), []byte("new contract"), 0644)
			case "dirty":
				_ = os.WriteFile(filepath.Join(f.worktree, "dirty.md"), []byte("unreviewed"), 0644)
			case "verdict":
				_ = os.WriteFile(report, []byte("## Summary\nMissing test.\n## Verdict\nincomplete\n"), 0644)
			}
			if _, _, err := checkReview(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3"); err == nil {
				t.Fatal("changed review accepted")
			}
		})
	}
}

func TestReviewVerdictDoesNotInferApprovalFromProse(t *testing.T) {
	for _, text := range []string{"tests pass", "## Verdict\npass with reservations", "## Verdict\nchanges-requested"} {
		if reviewVerdict(text) == "pass" {
			t.Fatal(text)
		}
	}
	if !strings.Contains(reviewBrief(nilSafeReviewWorkspace(t), "shop", "k3", map[string]string{}), "FULL committed change") {
		t.Fatal("missing full review")
	}
}
func nilSafeReviewWorkspace(t *testing.T) *store.Workspace {
	w, err := store.Init(t.TempDir(), workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	return w
}
