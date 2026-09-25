package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// mkdirs creates each workspace-relative directory.
func mkdirs(t *testing.T, w *store.Workspace, rel ...string) {
	t.Helper()
	for _, r := range rel {
		if err := os.MkdirAll(filepath.Join(w.Root(), r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestProjectWithoutRepoIsRegistered: a project is a named unit of work that
// may not have a repo yet (docs/mvp.md M9), and asking it for its sole repo
// says how to add one.
func TestProjectWithoutRepoIsRegistered(t *testing.T) {
	w := newWorkspace(t)
	if err := w.AddProject("plan", store.ProjectConfig{}); err != nil {
		t.Fatalf("AddProject without a repo: %v", err)
	}
	cfg, err := w.LoadProject("plan")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repos) != 0 {
		t.Fatalf("repos = %+v, want none", cfg.Repos)
	}
	if _, err := cfg.SoleRepo(); !errors.Is(err, store.ErrNoRepo) || !strings.Contains(err.Error(), "mate project repo add") {
		t.Fatalf("SoleRepo = %v, want ErrNoRepo naming `mate project repo add`", err)
	}
}

// TestLegacyProjectFilesAreReadAsOneRepo is the one-way conversion: a
// pre-M9 project.yaml (repo and default_branch at the top) reads as one repo
// named after its directory, and the next save writes only the new shape. A
// pre-M9 workspace.yaml's per-project `repo:` is ignored and then dropped.
func TestLegacyProjectFilesAreReadAsOneRepo(t *testing.T) {
	w := newWorkspace(t)
	mkdirs(t, w, "group/Shop.v2")
	if err := os.MkdirAll(w.ProjectDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyProject := "repo: group/Shop.v2\ndefault_branch: trunk\nmode: local-only\nyolo: true\n"
	if err := os.WriteFile(w.ProjectFile("shop"), []byte(legacyProject), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyWorkspace := "version: 1\nsession: mate-test\nprojects:\n    - name: shop\n      repo: group/Shop.v2\n"
	if err := os.WriteFile(w.WorkspaceFile(), []byte(legacyWorkspace), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig on a legacy workspace.yaml: %v", err)
	}
	if refs := w.Projects(); len(refs) != 1 || refs[0].Name != "shop" {
		t.Fatalf("projects = %+v, want shop", refs)
	}

	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatalf("LoadProject on a legacy project.yaml: %v", err)
	}
	want := []store.RepoConfig{{Name: "shop-v2", Path: "group/Shop.v2", DefaultBranch: "trunk"}}
	if !reflect.DeepEqual(cfg.Repos, want) || !cfg.Yolo {
		t.Fatalf("legacy project = %+v, want repos %+v and yolo kept", cfg, want)
	}

	if err := w.SaveProject("shop", cfg); err != nil {
		t.Fatal(err)
	}
	if err := w.SaveConfig(); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(w.ProjectFile("shop"))
	if strings.Contains(string(saved), "\nrepo:") || strings.HasPrefix(string(saved), "repo:") || !strings.Contains(string(saved), "repos:") {
		t.Fatalf("saved project.yaml kept the legacy shape:\n%s", saved)
	}
	ws, _ := os.ReadFile(w.WorkspaceFile())
	if strings.Contains(string(ws), "repo:") {
		t.Fatalf("saved workspace.yaml kept the per-project repo:\n%s", ws)
	}
	again, err := w.LoadProject("shop")
	if err != nil || !reflect.DeepEqual(again.Repos, want) {
		t.Fatalf("reload after save = %+v, %v; want %+v", again.Repos, err, want)
	}
}

func TestDefaultRepoName(t *testing.T) {
	for in, want := range map[string]string{
		"shop":                  "shop",
		"group/Shop.v2":         "shop-v2",
		"services/api/":         "api",
		"2024_reports":          "reports",
		"___":                   "repo",
		strings.Repeat("a", 40): strings.Repeat("a", 32),
	} {
		got := store.DefaultRepoName(in)
		if got != want {
			t.Errorf("DefaultRepoName(%q) = %q, want %q", in, got, want)
		}
		if err := store.ValidateRepoName(got); err != nil {
			t.Errorf("DefaultRepoName(%q) = %q is not a valid name: %v", in, got, err)
		}
	}
}

// TestAddRepoAndSoleRepo covers a project growing from one repo to two: the
// added repo is stored normalised, SoleRepo then refuses and lists the names,
// and a second repo by the same name or path is refused.
func TestAddRepoAndSoleRepo(t *testing.T) {
	w := newProjectWorkspace(t)
	mkdirs(t, w, "services/api")
	added, err := w.AddRepo("shop", store.RepoConfig{Path: filepath.Join(w.Root(), "services/api"), DefaultBranch: "develop"})
	if err != nil {
		t.Fatalf("AddRepo: %v", err)
	}
	if added != (store.RepoConfig{Name: "api", Path: "services/api", DefaultBranch: "develop"}) {
		t.Fatalf("added = %+v", added)
	}
	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RepoNames(); !reflect.DeepEqual(got, []string{"shop", "api"}) {
		t.Fatalf("repo names = %v", got)
	}
	if _, err := cfg.SoleRepo(); !errors.Is(err, store.ErrAmbiguousRepo) || !strings.Contains(err.Error(), "shop, api") {
		t.Fatalf("SoleRepo on two repos = %v, want ErrAmbiguousRepo listing both", err)
	}
	if _, err := w.AddRepo("shop", store.RepoConfig{Name: "api", Path: "shop"}); !errors.Is(err, store.ErrRepoExists) {
		t.Fatalf("same name again: %v, want ErrRepoExists", err)
	}
	if _, err := w.AddRepo("shop", store.RepoConfig{Name: "api2", Path: "services/api"}); !errors.Is(err, store.ErrRepoExists) {
		t.Fatalf("same path again: %v, want ErrRepoExists", err)
	}
}

// TestRepoBelongsToOneProject: crew branches are `mate/<crew>` and crew ids
// are unique per project, so two projects sharing a repo would collide.
func TestRepoBelongsToOneProject(t *testing.T) {
	w := newProjectWorkspace(t)
	if err := w.AddProject("blog", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop"}}}); !errors.Is(err, store.ErrRepoExists) || !strings.Contains(err.Error(), "project shop") {
		t.Fatalf("second project on shop's repo: %v, want ErrRepoExists naming shop", err)
	}
	if err := w.AddProject("blog", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddRepo("blog", store.RepoConfig{Path: "shop"}); !errors.Is(err, store.ErrRepoExists) {
		t.Fatalf("AddRepo of shop's repo into blog: %v, want ErrRepoExists", err)
	}
}

// TestCrewRepoResolvesLegacyMeta: a crew meta names its repo; one written
// before M9 names none and belongs to the sole repo, which stops being an
// answer once the project has two.
func TestCrewRepoResolvesLegacyMeta(t *testing.T) {
	one := store.ProjectConfig{Repos: []store.RepoConfig{{Name: "shop", Path: "shop"}}}
	two := store.ProjectConfig{Repos: []store.RepoConfig{{Name: "shop", Path: "shop"}, {Name: "api", Path: "api"}}}
	if r, err := one.CrewRepo(map[string]string{}); err != nil || r.Name != "shop" {
		t.Fatalf("legacy meta, one repo = %+v, %v", r, err)
	}
	if _, err := two.CrewRepo(map[string]string{}); !errors.Is(err, store.ErrAmbiguousRepo) {
		t.Fatalf("legacy meta, two repos = %v, want ErrAmbiguousRepo", err)
	}
	if r, err := two.CrewRepo(map[string]string{store.MetaRepo: "api"}); err != nil || r.Name != "api" {
		t.Fatalf("meta naming api = %+v, %v", r, err)
	}
	if _, err := two.CrewRepo(map[string]string{store.MetaRepo: "gone"}); !errors.Is(err, store.ErrNoRepo) {
		t.Fatalf("meta naming a removed repo = %v, want ErrNoRepo", err)
	}
}

// TestRemoveRepoRefusesOpenCrews: a repo with a crew that is not closed
// keeps it (its worktree and branch live there); once the crew is finished
// the repo goes, and its directory is left alone.
func TestRemoveRepoRefusesOpenCrews(t *testing.T) {
	w := newProjectWorkspace(t)
	mkdirs(t, w, "api")
	if _, err := w.AddRepo("shop", store.RepoConfig{Path: "api"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "k1", map[string]string{"state": "working", store.MetaRepo: "api"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "k2", map[string]string{"state": "working", store.MetaRepo: "shop"}); err != nil {
		t.Fatal(err)
	}
	if err := w.RemoveRepo("shop", "api"); !errors.Is(err, store.ErrRepoInUse) || !strings.Contains(err.Error(), "k1") || strings.Contains(err.Error(), "k2") {
		t.Fatalf("RemoveRepo with k1 open in api: %v, want ErrRepoInUse naming k1 only", err)
	}
	if err := w.WriteCrewMeta("shop", "k1", map[string]string{"state": "finished", store.MetaRepo: "api"}); err != nil {
		t.Fatal(err)
	}
	if err := w.RemoveRepo("shop", "api"); err != nil {
		t.Fatalf("RemoveRepo after k1 finished: %v", err)
	}
	cfg, _ := w.LoadProject("shop")
	if got := cfg.RepoNames(); !reflect.DeepEqual(got, []string{"shop"}) {
		t.Fatalf("repos after remove = %v", got)
	}
	if _, err := os.Stat(filepath.Join(w.Root(), "api")); err != nil {
		t.Fatalf("RemoveRepo touched the repo directory: %v", err)
	}
	if err := w.RemoveRepo("shop", "api"); !errors.Is(err, store.ErrNoRepo) {
		t.Fatalf("RemoveRepo twice: %v, want ErrNoRepo", err)
	}
}
