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
	"github.com/nguyenngocanh94/mate/internal/store"
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
	repo = filepath.Join(ws, "shop")
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

func projectMode(t *testing.T, ws string) string {
	t.Helper()
	w, err := store.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Mode
}

func TestProjectModeGitHubChecksGhAndOrigin(t *testing.T) {
	t.Setenv("MATE_CALLER", "")
	ws, _ := projectWithOrigin(t, "git@github.com:acme/shop.git")
	useGH(t, &scriptedGH{})
	var out, errw bytes.Buffer
	if err := cmdProjectMode([]string{"--workspace", ws, "shop", "github"}, &out, &errw); err != nil {
		t.Fatalf("mode github: %v", err)
	}
	if got := projectMode(t, ws); got != store.ModeGitHub {
		t.Fatalf("mode = %q, want github", got)
	}
	out.Reset()
	if err := cmdProjectMode([]string{"--workspace", ws, "shop"}, &out, &errw); err != nil || !strings.Contains(out.String(), "mode is github") {
		t.Fatalf("show = %q, %v", out.String(), err)
	}
	if err := cmdProjectMode([]string{"--workspace", ws, "shop", "local-only"}, &out, &errw); err != nil {
		t.Fatalf("mode local-only: %v", err)
	}
	if got := projectMode(t, ws); got != store.ModeLocalOnly {
		t.Fatalf("mode = %q, want local-only again", got)
	}
}

func TestProjectModeGitHubRefusesAndChangesNothing(t *testing.T) {
	t.Setenv("MATE_CALLER", "")
	for name, tc := range map[string]struct {
		origin string
		gh     map[string]github.Result
		want   string
	}{
		"not logged in": {"git@github.com:acme/shop.git", map[string]github.Result{"auth status": {ExitCode: 1, Stderr: "not logged in\n"}}, "gh auth login"},
		"no origin":     {"", nil, "has no origin remote"},
		"not github":    {"git@gitlab.com:acme/shop.git", nil, "does not point to GitHub"},
	} {
		t.Run(name, func(t *testing.T) {
			ws, _ := projectWithOrigin(t, tc.origin)
			useGH(t, &scriptedGH{results: tc.gh})
			var out, errw bytes.Buffer
			err := cmdProjectMode([]string{"--workspace", ws, "shop", "github"}, &out, &errw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if got := projectMode(t, ws); got != store.ModeLocalOnly {
				t.Fatalf("mode = %q after a refusal, want local-only", got)
			}
		})
	}
}

func TestProjectModeIsTheCaptainsOwn(t *testing.T) {
	ws, _ := projectWithOrigin(t, "git@github.com:acme/shop.git")
	useGH(t, &scriptedGH{})
	for _, caller := range []string{"mate", "crew"} {
		t.Setenv("MATE_CALLER", caller)
		var out, errw bytes.Buffer
		err := cmdProjectMode([]string{"--workspace", ws, "shop", "github"}, &out, &errw)
		if err == nil || !strings.Contains(err.Error(), "the captain sets the project mode") {
			t.Fatalf("%s: err = %v, want a refusal naming the captain", caller, err)
		}
	}
	if got := projectMode(t, ws); got != store.ModeLocalOnly {
		t.Fatalf("mode = %q, want local-only", got)
	}
}

func TestProjectAddGitHubModeIsChecked(t *testing.T) {
	ws := t.TempDir()
	repo := filepath.Join(ws, "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	useGH(t, &scriptedGH{})
	err := cmdProjectAdd([]string{"--workspace", ws, "--mode", "github", "shop", repo}, &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "has no origin remote") {
		t.Fatalf("add --mode github over a repo with no origin: %v", err)
	}
	if err := cmdProjectAdd([]string{"--workspace", ws, "--mode", "bogus", "shop", repo}, &out, &errw); err == nil {
		t.Fatal("an unknown --mode was accepted")
	}
}
