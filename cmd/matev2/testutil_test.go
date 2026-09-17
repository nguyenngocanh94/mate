package main

import (
	"os/exec"
	"testing"
)

// initGitRepo turns dir into a git repository with one commit on branch
// "main". The task requires real `git init` repos in tests, so this is not
// skipped when git is missing: it fails loudly instead.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git not found on PATH (required for this test): %v", err)
	}
	runGitOrFatal(t, dir, "init", "-b", "main")
	runGitOrFatal(t, dir, "config", "user.email", "matev2-test@example.com")
	runGitOrFatal(t, dir, "config", "user.name", "matev2 test")
	runGitOrFatal(t, dir, "commit", "--allow-empty", "-m", "init")
}

func runGitOrFatal(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
