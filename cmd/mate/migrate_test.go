package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/migrate"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestMigrateIsTheCaptains: a Mate or a Crew that runs `mate migrate` is
// refused before the workspace is even opened, as `project mode` was.
func TestMigrateIsTheCaptains(t *testing.T) {
	for _, caller := range []string{"mate", "crew"} {
		t.Setenv("MATE_CALLER", caller)
		var stdout, stderr bytes.Buffer
		code := mainRun([]string{"migrate", t.TempDir(), "--dry-run"}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "the captain runs mate migrate; a "+caller+" cannot") {
			t.Fatalf("MATE_CALLER=%s: exit %d, stderr %q", caller, code, stderr.String())
		}
	}
}

func TestMigrateUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := mainRun([]string{"migrate", "a", "b"}, &stdout, &stderr); code != 2 {
		t.Fatalf("two arguments: exit %d, want 2 (usage)\n%s", code, stderr.String())
	}
}

// TestMigrateCommandMovesAnOldWorkspace drives the command's core over a
// one-repo old workspace: the dry run changes nothing, the run moves.
func TestMigrateCommandMovesAnOldWorkspace(t *testing.T) {
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	root := w.Root()
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, filepath.Join(root, "web"))
	if err := os.MkdirAll(w.CrewsDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectFile("shop"), []byte("repos:\n    - name: web\n      path: web\n      default_branch: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.WorkspaceFile(), []byte("version: 1\nsession: mate-old\nroot: "+root+"\nprojects:\n    - name: shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w, err = store.Open(root); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runMigrate(context.Background(), w, migrate.Deps{}, true, &out); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if want := "would move " + root + "/web -> " + root + "/shop/web\nwould write workspace layout 2\n"; out.String() != want {
		t.Fatalf("dry run printed %q, want %q", out.String(), want)
	}
	if _, err := os.Stat(filepath.Join(root, "web")); err != nil {
		t.Fatalf("the dry run moved web: %v", err)
	}
	out.Reset()
	if err := runMigrate(context.Background(), w, migrate.Deps{}, false, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "moved " + root + "/web -> " + root + "/shop/web\nrepaired 0 worktree(s)\nrewrote 0 brief(s)\nworkspace layout 2\n"; out.String() != want {
		t.Fatalf("run printed %q, want %q", out.String(), want)
	}
}
