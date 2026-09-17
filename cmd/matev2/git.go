package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// runGit runs git with cwd dir, via os/exec directly (never through a shell),
// and returns trimmed stdout.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// gitTopLevel returns the work tree root of the git repository at dir.
func gitTopLevel(dir string) (string, error) {
	return runGit(dir, "rev-parse", "--show-toplevel")
}

// detectDefaultBranch returns the branch dir's HEAD points at, or
// store.DefaultBranch if HEAD is detached (no symbolic ref).
func detectDefaultBranch(dir, fallback string) string {
	branch, err := runGit(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil || branch == "" {
		return fallback
	}
	return branch
}
