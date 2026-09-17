package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

func TestCmdProjectAddListRemove(t *testing.T) {
	ws := t.TempDir()
	repoA := filepath.Join(ws, "shop")
	repoB := filepath.Join(ws, "blog")
	for _, r := range []string{repoA, repoB} {
		if err := os.MkdirAll(r, 0o755); err != nil {
			t.Fatal(err)
		}
		initGitRepo(t, r)
	}

	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}

	out.Reset()
	if err := cmdProjectAdd([]string{"--workspace", ws, "shop", repoA}, &out, &errw); err != nil {
		t.Fatalf("add shop: %v", err)
	}
	out.Reset()
	if err := cmdProjectAdd([]string{"--workspace", ws, "blog", repoB}, &out, &errw); err != nil {
		t.Fatalf("add blog: %v", err)
	}

	for _, name := range []string{"shop", "blog"} {
		doc := filepath.Join(ws, ".matev2", "projects", name, "PROJECT.md")
		data, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("PROJECT.md missing for %s: %v", name, err)
		}
		if !strings.Contains(string(data), "What this project is") || !strings.Contains(string(data), "How to work here") {
			t.Fatalf("PROJECT.md for %s missing template sections: %q", name, data)
		}
	}

	// Text table: header plus one row per project, aligned columns.
	out.Reset()
	if err := cmdProjectList([]string{"--workspace", ws}, &out, &errw); err != nil {
		t.Fatalf("list: %v", err)
	}
	text := out.String()
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want header + 2 rows, got %d lines: %q", len(lines), text)
	}
	if !strings.HasPrefix(lines[0], "NAME") || !strings.Contains(lines[0], "DEFAULT BRANCH") || !strings.Contains(lines[0], "MODE") || !strings.Contains(lines[0], "YOLO") {
		t.Fatalf("header row = %q", lines[0])
	}
	if !strings.Contains(text, "shop") || !strings.Contains(text, "blog") {
		t.Fatalf("table missing project rows: %q", text)
	}

	// JSON output.
	out.Reset()
	if err := cmdProjectList([]string{"--workspace", ws, "--json"}, &out, &errw); err != nil {
		t.Fatalf("list --json: %v", err)
	}
	var rows []projectRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Mode != store.ModeLocalOnly {
			t.Fatalf("mode = %q, want %q", r.Mode, store.ModeLocalOnly)
		}
		if r.DefaultBranch != "main" {
			t.Fatalf("default branch = %q, want main", r.DefaultBranch)
		}
		if r.Repo == "" || filepath.IsAbs(r.Repo) {
			t.Fatalf("repo = %q, want a workspace-relative path", r.Repo)
		}
	}

	// Remove one, list again.
	out.Reset()
	if err := cmdProjectRemove([]string{"--workspace", ws, "blog"}, &out, &errw); err != nil {
		t.Fatalf("remove: %v", err)
	}

	out.Reset()
	if err := cmdProjectList([]string{"--workspace", ws, "--json"}, &out, &errw); err != nil {
		t.Fatalf("list after remove: %v", err)
	}
	rows = nil
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("json.Unmarshal after remove: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "shop" {
		t.Fatalf("after remove, rows = %+v", rows)
	}

	// Files under projects/blog/ stay on disk after remove.
	if _, err := os.Stat(filepath.Join(ws, ".matev2", "projects", "blog")); err != nil {
		t.Fatalf("projects/blog removed from disk, want it kept: %v", err)
	}
}

func TestCmdProjectAddFlagsAfterPositionalArgs(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	repo := filepath.Join(ws, "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)

	// The Go flag package normally stops parsing at the first positional
	// argument; --workspace here comes after <name> <repo-path>, which must
	// still be honoured.
	if err := cmdProjectAdd([]string{"shop", repo, "--workspace", ws, "--yolo"}, &out, &errw); err != nil {
		t.Fatalf("add with trailing flags: %v", err)
	}

	w, err := store.Open(ws)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if !cfg.Yolo {
		t.Fatal("--yolo after positional args was not applied")
	}
}

func TestCmdProjectListEmptyWorkspace(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}

	out.Reset()
	if err := cmdProjectList([]string{"--workspace", ws, "--json"}, &out, &errw); err != nil {
		t.Fatalf("list --json: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("empty list json = %q, want []", out.String())
	}
}

func TestCmdProjectAddRejectsNonGitDir(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	plain := filepath.Join(ws, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	err := cmdProjectAdd([]string{"--workspace", ws, "plain", plain}, &out, &errw)
	if err == nil {
		t.Fatal("want error for a directory that is not a git repository")
	}
}

func TestCmdProjectAddRejectsPathOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	outside := t.TempDir()
	initGitRepo(t, outside)

	err := cmdProjectAdd([]string{"--workspace", ws, "ext", outside}, &out, &errw)
	if err == nil {
		t.Fatal("want error for a repo outside the workspace")
	}
	var be *store.BoundaryError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want it to wrap *store.BoundaryError", err)
	}
}

func TestCmdProjectAddRejectsDuplicateName(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	repo := filepath.Join(ws, "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)

	if err := cmdProjectAdd([]string{"--workspace", ws, "shop", repo}, &out, &errw); err != nil {
		t.Fatalf("first add: %v", err)
	}
	err := cmdProjectAdd([]string{"--workspace", ws, "shop", repo}, &out, &errw)
	if err == nil {
		t.Fatal("want error for duplicate project name")
	}
	if !errors.Is(err, store.ErrProjectExists) {
		t.Fatalf("err = %v, want it to wrap store.ErrProjectExists", err)
	}
}

func TestCmdProjectAddRejectsBadName(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	repo := filepath.Join(ws, "Shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)

	err := cmdProjectAdd([]string{"--workspace", ws, "Shop!", repo}, &out, &errw)
	if err == nil {
		t.Fatal("want error for an invalid project name")
	}
}

func TestCmdProjectAddDefaultBranchOverride(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	repo := filepath.Join(ws, "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)

	if err := cmdProjectAdd([]string{"--workspace", ws, "--default-branch", "trunk", "shop", repo}, &out, &errw); err != nil {
		t.Fatalf("add: %v", err)
	}
	w, err := store.Open(ws)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if cfg.DefaultBranch != "trunk" {
		t.Fatalf("default branch = %q, want trunk", cfg.DefaultBranch)
	}
}

func TestCmdProjectRemoveUnknownProject(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := cmdProjectRemove([]string{"--workspace", ws, "nope"}, &out, &errw); err == nil {
		t.Fatal("want error for removing an unknown project")
	}
}
