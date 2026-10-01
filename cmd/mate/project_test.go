package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
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
		doc := filepath.Join(ws, ".mate", "projects", name, "PROJECT.md")
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
	if !strings.HasPrefix(lines[0], "NAME") || !strings.Contains(lines[0], "REPOS") || !strings.Contains(lines[0], "MODE") || !strings.Contains(lines[0], "YOLO") {
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
		if len(r.Repos) != 1 || r.Repos[0].Name != r.Name || r.Repos[0].DefaultBranch != "main" {
			t.Fatalf("repos = %+v, want one repo named %s on main", r.Repos, r.Name)
		}
		if p := r.Repos[0].Path; p == "" || filepath.IsAbs(p) {
			t.Fatalf("repo path = %q, want a workspace-relative path", p)
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
	if _, err := os.Stat(filepath.Join(ws, ".mate", "projects", "blog")); err != nil {
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
	outsideAnyCheckout(t, ws)

	err := cmdProjectAdd([]string{"--workspace", ws, "plain", plain}, &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("err = %v, want the directory refused as not a git repository", err)
	}
}

// outsideAnyCheckout stops git's repository discovery at dir, so a test that
// means "no repository here" gets that whatever TMPDIR the suite runs under:
// a TMPDIR inside a checkout would otherwise make every temp directory part
// of that checkout's work tree.
func outsideAnyCheckout(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
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
	if len(cfg.Repos) != 1 || cfg.Repos[0].DefaultBranch != "trunk" {
		t.Fatalf("repos = %+v, want one repo on trunk", cfg.Repos)
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

// initProjectWorkspace is an initialised workspace with a git repo at each
// of the given workspace-relative paths.
func initProjectWorkspace(t *testing.T, repos ...string) string {
	t.Helper()
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, r := range repos {
		dir := filepath.Join(ws, r)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		initGitRepo(t, dir)
	}
	return ws
}

// TestCmdProjectAddWithoutRepo: a project may start with no repo (docs/mvp.md
// M9); the summary line says how to add one, and repo-only flags without a
// repo are a usage error rather than silently dropped.
func TestCmdProjectAddWithoutRepo(t *testing.T) {
	ws := initProjectWorkspace(t)
	var out, errw bytes.Buffer
	if err := cmdProjectAdd([]string{"--workspace", ws, "plan"}, &out, &errw); err != nil {
		t.Fatalf("add without a repo: %v", err)
	}
	if !strings.Contains(out.String(), "no repo yet") || !strings.Contains(out.String(), "mate project repo add plan") {
		t.Fatalf("summary = %q, want it to say how to add a repo", out.String())
	}
	if _, err := os.Stat(filepath.Join(ws, ".mate", "projects", "plan", "PROJECT.md")); err != nil {
		t.Fatalf("PROJECT.md not seeded for a repo-less project: %v", err)
	}
	var ue *usageError
	if err := cmdProjectAdd([]string{"--workspace", ws, "other", "--default-branch", "trunk"}, &out, &errw); !errors.As(err, &ue) {
		t.Fatalf("--default-branch without a repo: err = %v, want a usage error", err)
	}
	out.Reset()
	if err := cmdProjectList([]string{"--workspace", ws}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "plan  -") {
		t.Fatalf("list = %q, want plan with a - for its repos", out.String())
	}
}

// TestCmdProjectRepoAddListRemove walks a project from no repo to two and
// back to one through the CLI.
func TestCmdProjectRepoAddListRemove(t *testing.T) {
	ws := initProjectWorkspace(t, "services/api", "web")
	var out, errw bytes.Buffer
	if err := cmdProjectAdd([]string{"--workspace", ws, "shop"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cmdProjectRepo([]string{"add", "--workspace", ws, "shop", filepath.Join(ws, "services/api")}, &out, &errw); err != nil {
		t.Fatalf("repo add api: %v", err)
	}
	if !strings.Contains(out.String(), "added repo api to project shop: path=services/api default-branch=main") {
		t.Fatalf("repo add output = %q", out.String())
	}
	if err := cmdProjectRepo([]string{"add", "shop", filepath.Join(ws, "web"), "--name", "frontend", "--default-branch", "trunk", "--workspace", ws}, &out, &errw); err != nil {
		t.Fatalf("repo add web: %v", err)
	}

	out.Reset()
	if err := cmdProjectRepo([]string{"list", "--workspace", ws, "shop", "--json"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	var rows []repoRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	want := []repoRow{{Name: "api", Path: "services/api", DefaultBranch: "main"}, {Name: "frontend", Path: "web", DefaultBranch: "trunk"}}
	if len(rows) != 2 || rows[0] != want[0] || rows[1] != want[1] {
		t.Fatalf("repo list = %+v, want %+v", rows, want)
	}

	if err := cmdProjectRepo([]string{"add", "--workspace", ws, "shop", filepath.Join(ws, "web")}, &out, &errw); !errors.Is(err, store.ErrRepoExists) {
		t.Fatalf("adding web twice: err = %v, want ErrRepoExists", err)
	}

	out.Reset()
	if err := cmdProjectRepo([]string{"remove", "--workspace", ws, "shop", "api"}, &out, &errw); err != nil {
		t.Fatalf("repo remove: %v", err)
	}
	if !strings.Contains(out.String(), "not touched") {
		t.Fatalf("remove output = %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(ws, "services/api", ".git")); err != nil {
		t.Fatalf("repo remove touched the repository: %v", err)
	}
	out.Reset()
	if err := cmdProjectRepo([]string{"list", "--workspace", ws, "shop"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "services/api") || !strings.Contains(out.String(), "frontend") {
		t.Fatalf("repo list after remove = %q", out.String())
	}
}
