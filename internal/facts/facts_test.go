package facts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/gitx"
)

// recorder runs the real git and remembers every argv, so a test can hold
// Gather to the promise that it never opens a file.
type recorder struct {
	calls [][]string
}

func (r *recorder) Run(ctx context.Context, cmd gitx.Command) (gitx.Result, error) {
	r.calls = append(r.calls, cmd.Args)
	return gitx.ExecRunner{}.Run(ctx, cmd)
}

// metadataOnly is every git subcommand Gather may run, with the flags that
// would make each read content ruled out below.
var metadataOnly = map[string]bool{"rev-parse": true, "rev-list": true, "ls-tree": true}

func assertNoContentRead(t *testing.T, r *recorder) {
	t.Helper()
	if len(r.calls) == 0 {
		t.Fatal("no git call recorded")
	}
	for _, args := range r.calls {
		if !metadataOnly[args[0]] {
			t.Errorf("git %s is not a metadata-only command", strings.Join(args, " "))
		}
		for _, a := range args {
			if a == "-p" || a == "--patch" || strings.HasPrefix(a, "--format") || strings.HasPrefix(a, "--pretty") {
				t.Errorf("git %s could print content", strings.Join(args, " "))
			}
		}
	}
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	return dir
}

// TestGatherOnAnEmptyRepository is the shop repository of 2026-09-19: a
// `.git` and nothing else, so the default branch is unborn.
func TestGatherOnAnEmptyRepository(t *testing.T) {
	repo := newRepo(t)
	rec := &recorder{}
	f, err := Gather(context.Background(), gitx.Git{Runner: rec}, "shop", "web", repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"shop: repo web at " + repo + ", default branch main",
		"commits: 0 (main has no commit yet)",
		"head: none (anchor web:main@none)",
		"tree: empty",
		"note: names and counts from the committed tree of main only; no file was opened, and uncommitted changes in the checkout are not seen",
	}
	if got := f.Lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	assertNoContentRead(t, rec)
}

// secret is written into every file of the populated repository. If it ever
// shows up in the output, a file was read.
const secret = "SECRET-CONTENT-7f3a"

func TestGatherOnAPopulatedRepository(t *testing.T) {
	repo := newRepo(t)
	for _, p := range []string{
		"go.mod", "Makefile", "README.md", "AGENTS.md", "main.go",
		"web/package.json", "web/vite.config.ts", "web/src/app.ts",
		"web/deep/nested/package.json", // below MaxDepth: not listed
		"vendor/x/go.mod",              // in a skipped directory
		"my notes.txt",
	} {
		full := filepath.Join(repo, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(secret+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-q", "-m", "one")
	if err := os.WriteFile(filepath.Join(repo, "uncommitted.go"), []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "commit", "-q", "--allow-empty", "-m", "two")

	rec := &recorder{}
	f, err := Gather(context.Background(), gitx.Git{Runner: rec}, "shop", "web", repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.Lines(), "\n")
	head := strings.TrimSpace(gitOut(t, repo, "rev-parse", "--short", "main"))
	if f.Head != head {
		t.Fatalf("Head = %q, want git's own short name of main %q", f.Head, head)
	}
	for _, want := range []string{
		"shop: repo web at " + repo + ", default branch main\ncommits: 2 on main\nhead: " + head + " (anchor web:main@" + head + ")",
		"tree: 11 file(s)",
		`top level: AGENTS.md Makefile README.md go.mod main.go "my notes.txt" vendor/ web/`,
		"build/test files: Makefile go.mod web/package.json web/vite.config.ts",
		"docs: AGENTS.md README.md",
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, secret) || strings.Contains(got, "uncommitted.go") {
		t.Fatalf("facts printed file content or the working tree:\n%s", got)
	}
	assertNoContentRead(t, rec)
}

// TestNoRepoLines: a project without a repo has no facts, only the line
// that says so and how one is added (docs/mvp.md M9).
func TestNoRepoLines(t *testing.T) {
	want := "repos: none\nhint: project shop has no repo yet, so no crew can be spawned; add one with `mate project repo add shop <repo-path>`"
	if got := strings.Join(NoRepoLines("shop"), "\n"); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAnchorSpelling(t *testing.T) {
	if got := Anchor("api", "release/1.x", ""); got != "api:release/1.x@none" {
		t.Fatalf("Anchor with no head = %q", got)
	}
	if got := Anchor("api", "main", "3f2a91c"); got != "api:main@3f2a91c" {
		t.Fatalf("Anchor = %q", got)
	}
}
