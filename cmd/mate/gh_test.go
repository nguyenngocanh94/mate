package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/github"
)

// scriptedGH is the gh seam of the cmd/mate tests: it never starts gh. The
// answer is looked up by the joined arguments and every call is kept.
type scriptedGH struct {
	results map[string]github.Result
	calls   []github.Command
}

func (f *scriptedGH) Run(_ context.Context, cmd github.Command) (github.Result, error) {
	f.calls = append(f.calls, cmd)
	return f.results[strings.Join(cmd.Args, " ")], nil
}

// useGH swaps the package's gh client for a scripted one for one test.
func useGH(t *testing.T, f *scriptedGH) {
	t.Helper()
	old := ghClient
	ghClient = github.Client{Runner: f}
	t.Cleanup(func() { ghClient = old })
}

func projectWithOrigin(t *testing.T, origin string) (ws, repo string) {
	t.Helper()
	ws = t.TempDir()
	repo = filepath.Join(ws, "shop", "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if origin != "" {
		if out, err := exec.Command("git", "-C", repo, "remote", "add", "origin", origin).CombinedOutput(); err != nil {
			t.Fatalf("git remote add: %v\n%s", err, out)
		}
	}
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if err := cmdProjectAdd([]string{"--workspace", ws, "shop", repo}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	return ws, repo
}
