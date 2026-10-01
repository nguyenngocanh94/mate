package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness/codex/codexlab"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// requireConsoleLive gates this file the same way internal/spawn/live_test.go
// gates its own proofs: a live Herdr lab session and a real Claude binary are
// machine facts, and MATE_LIVE=1 is the single opt-in that claims them.
// scripts/gotestreport allows TestLive* and nothing else to skip.
func requireConsoleLive(t *testing.T) {
	t.Helper()
	if os.Getenv("MATE_LIVE") != "1" {
		t.Skip("set MATE_LIVE=1 to run live Herdr proofs")
	}
}

func consoleLiveLab(t *testing.T) (session, configHome string) {
	t.Helper()
	session = strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_SESSION"))
	if session == "" {
		t.Skip("set MATE_HERDR_LIVE_SESSION to a provisioned fm-lab-* Herdr session")
	}
	if session == "default" || session == "firstmate" {
		t.Fatal("refusing to run a live console session against the default or firstmate session")
	}
	if !strings.HasPrefix(session, "fm-lab-") {
		t.Fatalf("live test requires an fm-lab- session, got %q", session)
	}
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		t.Fatal("HOME is required to resolve the Herdr socket")
	}
	// Every live test that reaches a lab session runs Codex in a lab
	// CODEX_HOME, never the operator's ~/.codex (internal/harness/codex/codexlab).
	codexlab.Home(t)
	return session, filepath.Join(home, ".config")
}

// liveWorkspaceRoot is the directory a live proof builds its workspace in.
// By default it is t.TempDir(), removed with the test. With
// MATE_LIVE_KEEP=<dir> it is a fresh directory under <dir> that outlives
// the test, so the run's own files - status files, sent.log, briefs,
// hand-backs, the repositories - stay where the transcripts recorded them
// and `mate reindex` can rebuild the timeline from them afterwards
// (docs/mvp.md task 34 measures prompting that way). The directory must not
// go through a symlink, for the same reason TMPDIR must not.
func liveWorkspaceRoot(t *testing.T) string {
	t.Helper()
	keep := strings.TrimSpace(os.Getenv("MATE_LIVE_KEEP"))
	if keep == "" {
		return t.TempDir()
	}
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatalf("MATE_LIVE_KEEP: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(keep)
	if err != nil {
		t.Fatalf("MATE_LIVE_KEEP: %v", err)
	}
	if abs, _ := filepath.Abs(keep); abs != resolved {
		t.Fatalf("MATE_LIVE_KEEP=%s goes through a symlink (resolves to %s); Herdr reports resolved cwds", keep, resolved)
	}
	// A subtest's name holds a "/", which MkdirTemp refuses in a pattern.
	root, err := os.MkdirTemp(resolved, strings.ReplaceAll(t.Name(), "/", "-")+"-")
	if err != nil {
		t.Fatalf("MATE_LIVE_KEEP: %v", err)
	}
	t.Logf("workspace kept at %s (MATE_LIVE_KEEP)", root)
	return root
}

// consoleUseLabSession rewrites workspace.yaml's session name to the
// provisioned lab, which the runner owns: store.Init derives the name from
// the workspace path, and a lab run must not use that.
func consoleUseLabSession(t *testing.T, w *store.Workspace, session string) {
	t.Helper()
	path := w.WorkspaceFile()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	scanner := bufio.NewScanner(f)
	replaced := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "session:") {
			line = "session: " + session
			replaced = true
		}
		out = append(out, line)
	}
	f.Close()
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !replaced {
		t.Fatalf("%s carries no session key", path)
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// consoleBinaryPath builds the mate binary the rendered manual points at,
// so the live Mate's AGENTS.md names a real executable, not the test binary.
func consoleBinaryPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "mate")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/nguyenngocanh94/mate/cmd/mate")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build mate: %v\n%s", err, out)
	}
	return bin
}
