package store_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// gitInit makes a real, empty git repository at the workspace-relative rel.
func gitInit(t *testing.T, w *store.Workspace, rel string) string {
	t.Helper()
	dir := filepath.Join(w.Root(), rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git := exec.Command("git", "init", "-q", dir)
	git.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v\n%s", rel, err, out)
	}
	return dir
}

// TestInitWritesLayoutTwo: a new workspace is on the layout where a project
// is a directory under the workspace (docs/mvp.md section 3, 2026-10-08).
func TestInitWritesLayoutTwo(t *testing.T) {
	w := newWorkspace(t)
	if w.Layout() != 2 || w.LayoutOld() {
		t.Fatalf("Init: Layout() = %d, LayoutOld() = %v; want 2, false", w.Layout(), w.LayoutOld())
	}
	data, err := os.ReadFile(w.WorkspaceFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\nlayout: 2\n") {
		t.Fatalf("workspace.yaml has no `layout: 2`:\n%s", data)
	}
	opened, err := store.Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	if opened.Layout() != 2 {
		t.Fatalf("reopened Layout() = %d, want 2", opened.Layout())
	}
	if got, want := w.ProjectHome("shop"), filepath.Join(w.Root(), "shop"); got != want {
		t.Fatalf("ProjectHome(shop) = %s, want %s", got, want)
	}
}

// TestRepoOutsideProjectIsRefused is plan test 6: a repo of a project lives
// under the project's directory, and the refusal says how to fix it. The
// project directory itself is not a repo of it: repos are under it.
func TestRepoOutsideProjectIsRefused(t *testing.T) {
	w := newWorkspace(t)
	gitInit(t, w, "blog/web")
	gitInit(t, w, "loose")
	gitInit(t, w, "shopping/web")

	wantMsg := func(rel string) string {
		return "repo " + rel + " must live under " + w.ProjectHome("shop") + "; move it there, or run mate migrate on an old workspace"
	}
	for _, rel := range []string{"loose", "blog/web", "shopping/web"} {
		err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: rel}}})
		if !errors.Is(err, store.ErrRepoOutsideProject) || !strings.Contains(err.Error(), wantMsg(rel)) {
			t.Fatalf("AddProject(shop, %s) = %v, want ErrRepoOutsideProject %q", rel, err, wantMsg(rel))
		}
	}
	if _, err := os.Stat(w.ProjectFile("shop")); !os.IsNotExist(err) {
		t.Fatalf("a refused AddProject wrote project.yaml: %v", err)
	}

	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"loose", "blog/web", "shop"} {
		_, err := w.AddRepo("shop", store.RepoConfig{Path: filepath.Join(w.Root(), rel)})
		if !errors.Is(err, store.ErrRepoOutsideProject) || !strings.Contains(err.Error(), wantMsg(rel)) {
			t.Fatalf("AddRepo(shop, %s) = %v, want ErrRepoOutsideProject %q", rel, err, wantMsg(rel))
		}
	}
}

// TestNestedRepoUnderProjectIsAccepted: a repo need not be a direct child of
// the project directory (`shop/services/api`).
func TestNestedRepoUnderProjectIsAccepted(t *testing.T) {
	w := newWorkspace(t)
	gitInit(t, w, "shop/web")
	gitInit(t, w, "shop/services/api")
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop/web"}}}); err != nil {
		t.Fatalf("AddProject with a direct child repo: %v", err)
	}
	added, err := w.AddRepo("shop", store.RepoConfig{Path: filepath.Join(w.Root(), "shop", "services", "api")})
	if err != nil {
		t.Fatalf("AddRepo nested: %v", err)
	}
	if added.Path != "shop/services/api" || added.Name != "api" {
		t.Fatalf("added = %+v, want api at shop/services/api", added)
	}
}

// writeOldLayout turns w into a workspace written before layout 2: no
// `layout:` in workspace.yaml, and project shop's repo beside `.mate/`.
func writeOldLayout(t *testing.T, w *store.Workspace) {
	t.Helper()
	gitInit(t, w, "shop")
	gitInit(t, w, "api")
	if err := os.MkdirAll(w.CrewsDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	project := "repos:\n    - name: shop\n      path: shop\n      default_branch: main\n"
	if err := os.WriteFile(w.ProjectFile("shop"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := "version: 1\nsession: mate-old\nroot: " + w.Root() + "\ndefaults:\n    mate_harness: claude\n    crew_harness: codex\nprojects:\n    - name: shop\n"
	if err := os.WriteFile(w.WorkspaceFile(), []byte(ws), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestOpenRefusesTheOldLayout: a workspace with no `layout:` is refused by
// Open with the sentence every command prints, and opened only by
// OpenForMigrate, which reads it as layout 1.
func TestOpenRefusesTheOldLayout(t *testing.T) {
	w := newWorkspace(t)
	writeOldLayout(t, w)

	const msg = "this workspace has the old layout (repos beside .mate); run mate migrate first"
	if _, err := store.Open(w.Root()); !errors.Is(err, store.ErrLayoutOld) || err.Error() != msg {
		t.Fatalf("Open on the old layout = %v, want %q", err, msg)
	}
	old, err := store.OpenForMigrate(w.Root())
	if err != nil {
		t.Fatalf("OpenForMigrate on the old layout: %v", err)
	}
	if old.Layout() != 1 || !old.LayoutOld() {
		t.Fatalf("Layout() = %d, LayoutOld() = %v; want 1, true", old.Layout(), old.LayoutOld())
	}
	cfg, err := old.LoadProject("shop")
	if err != nil || len(cfg.Repos) != 1 || cfg.Repos[0].Path != "shop" {
		t.Fatalf("LoadProject on the old layout = %+v, %v", cfg, err)
	}
	// migrate's own writes keep the old repo paths and do not relabel the
	// workspace: only SetLayoutProjectDirs writes layout 2.
	cfg.Repos[0].DefaultBranch = "trunk"
	if err := old.SaveProject("shop", cfg); err != nil {
		t.Fatalf("SaveProject on the old layout: %v", err)
	}
	if err := old.SetRoot(old.Root()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(old.WorkspaceFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "layout:") {
		t.Fatalf("a save on the old layout wrote a layout:\n%s", data)
	}
}

// TestAddProjectMakesTheProjectDirectory: a project with no repo still has
// its directory under the workspace, and one that already exists is kept.
func TestAddProjectMakesTheProjectDirectory(t *testing.T) {
	w := newWorkspace(t)
	if err := w.AddProject("notes", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(w.ProjectHome("notes")); err != nil || !fi.IsDir() {
		t.Fatalf("AddProject did not make %s: %v", w.ProjectHome("notes"), err)
	}
	keep := filepath.Join(w.ProjectHome("shop"), "keep.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop/shop"}}}); err != nil {
		t.Fatalf("AddProject over an existing project directory: %v", err)
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "mine" {
		t.Fatalf("AddProject touched what was in the project directory: %q, %v", data, err)
	}
}
