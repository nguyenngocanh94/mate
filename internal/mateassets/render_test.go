package mateassets

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "update golden files in testdata/")

func fixedParams() Params {
	return Params{
		ProjectName:   "shop",
		WorkspaceRoot: "/ws",
		ProjectRepo:   "/ws/shop",
		DefaultBranch: "main",
		Mode:          "local-only",
		Yolo:          false,
		Harness:       "claude-code",
		WorkspaceDoc:  "/ws/.matev2/WORKSPACE.md",
		ProjectDoc:    "/ws/.matev2/projects/shop/PROJECT.md",
		MemoryFile:    "/ws/.matev2/projects/shop/mate/memory.md",
		BacklogFile:   "/ws/.matev2/projects/shop/mate/backlog.md",
		MatevBin:      "/usr/local/bin/matev2",
	}
}

func fixedBriefParams() BriefParams {
	return BriefParams{
		RepoPath:      "/ws/shop",
		WorktreePath:  "/ws/.worktrees/shop-k3",
		Branch:        "matev2/k3",
		DefaultBranch: "main",
	}
}

func compareGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run go test -run %s -update to create it)", path, err, t.Name())
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s mismatch: rerun with -update if this change is intended\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func TestRenderAgentsGolden(t *testing.T) {
	got, err := Render(fixedParams())
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, filepath.Join("testdata", "AGENTS.md.golden"), got)
}

func TestRenderBriefGolden(t *testing.T) {
	got, err := RenderBrief(fixedBriefParams())
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, filepath.Join("testdata", "brief.md.golden"), got)
}

// unexpandedPattern catches a template action that survived execution: a
// literal `{{` (an unexecuted action) or Go's zero-value stand-in for a
// missing field.
var unexpandedPattern = regexp.MustCompile(`\{\{|<no value>`)

func TestRenderAgentsNoUnexpandedVars(t *testing.T) {
	got, err := Render(fixedParams())
	if err != nil {
		t.Fatal(err)
	}
	if loc := unexpandedPattern.FindIndex(got); loc != nil {
		t.Fatalf("rendered AGENTS.md has an unexpanded template marker at byte %d: %q", loc[0], got[loc[0]:min(loc[0]+40, len(got))])
	}
}

func TestRenderBriefNoUnexpandedVars(t *testing.T) {
	got, err := RenderBrief(fixedBriefParams())
	if err != nil {
		t.Fatal(err)
	}
	if loc := unexpandedPattern.FindIndex(got); loc != nil {
		t.Fatalf("rendered brief.md has an unexpanded template marker at byte %d: %q", loc[0], got[loc[0]:min(loc[0]+40, len(got))])
	}
}

// forbiddenWords are v1/firstmate concepts and spellings that must never
// survive into matev2's Mate manual or crew brief: tmux and treehouse are
// firstmate's terminal/worktree-pool tooling matev2 does not use, no-mistakes
// and secondmate are firstmate concepts matev2 has no equivalent of, fm- is
// firstmate's script-name prefix, gh-axi is a firstmate tool, and MATE_ is
// the old v1 env prefix (matev2 uses MATEV2_ only).
var forbiddenWords = []string{
	"tmux",
	"treehouse",
	"no-mistakes",
	"secondmate",
	"fm-",
	"gh-axi",
	"MATE_",
}

func TestRenderAgentsNoForbiddenWords(t *testing.T) {
	got, err := Render(fixedParams())
	if err != nil {
		t.Fatal(err)
	}
	checkForbidden(t, "AGENTS.md", got)
}

func TestRenderBriefNoForbiddenWords(t *testing.T) {
	got, err := RenderBrief(fixedBriefParams())
	if err != nil {
		t.Fatal(err)
	}
	checkForbidden(t, "brief.md", got)
}

func checkForbidden(t *testing.T, name string, got []byte) {
	t.Helper()
	for _, word := range forbiddenWords {
		if strings.Contains(string(got), word) {
			t.Errorf("rendered %s contains forbidden word %q", name, word)
		}
	}
}

func TestWriteCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, fixedParams()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "memory.md", "backlog.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}
	claude, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(claude) != "@AGENTS.md\n" {
		t.Errorf("CLAUDE.md = %q, want \"@AGENTS.md\\n\"", claude)
	}
}

func TestWritePreservesExistingMemory(t *testing.T) {
	dir := t.TempDir()
	const existing = "# Memory\n\n- learned something\n"
	if err := os.WriteFile(filepath.Join(dir, "memory.md"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	const existingBacklog = "# Backlog\n\n## In flight\n- task-1\n"
	if err := os.WriteFile(filepath.Join(dir, "backlog.md"), []byte(existingBacklog), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, fixedParams()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Errorf("memory.md was modified: got %q, want %q", got, existing)
	}
	gotBacklog, err := os.ReadFile(filepath.Join(dir, "backlog.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBacklog) != existingBacklog {
		t.Errorf("backlog.md was modified: got %q, want %q", gotBacklog, existingBacklog)
	}
}

func TestWriteOverwritesAgentsAndClaude(t *testing.T) {
	dir := t.TempDir()
	const marker = "PRE-EXISTING-STUB-CONTENT"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, fixedParams()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte(marker)) {
		t.Errorf("AGENTS.md was not regenerated: %q", got)
	}
}
