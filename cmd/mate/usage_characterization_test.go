package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestUsageCharacterizationOnTheFixtureCorpus pins what `mate usage` prints
// for the fixture corpus - internal/timeline's captured run, a Claude Code
// Mate and a codex-cli crew - after `mate reindex` reads it, byte for byte.
// It was recorded before the transcript capability moved behind the harness
// registry (docs/plans/harness-registry-2026-09-30.md, PR 5): that change
// must leave these numbers, and the dashboard's on the same corpus
// (internal/dashboard/numbers_characterization_test.go), as it found them.
func TestUsageCharacterizationOnTheFixtureCorpus(t *testing.T) {
	// The SPAWN→CLOSE column is local time.
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })

	w := liveCrewWorkspace(t, "shop")
	corpus := func(name string) string {
		path, err := filepath.Abs(filepath.Join("..", "..", "internal", "timeline", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	if err := w.WriteMateMeta("shop", map[string]string{
		"harness":    "claude",
		"agent":      "mate-shop",
		"session_id": "8414030c-5d90-4925-94cc-c94e12aae4a9",
		"transcript": corpus("claude-2.1.278-transcript.jsonl"),
		"started_at": "2026-09-19T10:43:30Z",
	}); err != nil {
		t.Fatalf("WriteMateMeta: %v", err)
	}
	if err := w.WriteCrewMeta("shop", "buybtn", map[string]string{
		"harness":    "codex",
		"agent":      "crew-buybtn",
		"task":       "Add a Buy button to README.md linking to the checkout page",
		"branch":     "mate/buybtn",
		"worktree":   ".worktrees/shop-buybtn",
		"session_id": "01a0b944-33bb-7503-9db7-cd51ff61855a",
		"transcript": corpus("codex-0.154-rollout.jsonl"),
		"started_at": "2026-09-19T10:44:09Z",
		"state":      "spawned",
	}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}

	runCLI(t, "reindex", w.Root())
	got := "== mate usage shop\n" + runCLI(t, "usage", "shop", "--workspace", w.Root()) +
		"== mate usage shop buybtn\n" + runCLI(t, "usage", "shop", "buybtn", "--workspace", w.Root())

	golden := filepath.Join("testdata", "usage-fixture-corpus.golden")
	if os.Getenv("MATE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (re-run with MATE_UPDATE_GOLDEN=1 to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("`mate usage` on the fixture corpus changed (%s):\n%s", golden, got)
	}
}
