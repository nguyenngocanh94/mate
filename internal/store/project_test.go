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

// newProjectWorkspace initialises a workspace with the `shop` project already
// registered.
func newProjectWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := newWorkspace(t)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop/shop"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

func TestStoreProjectAddLoadSave(t *testing.T) {
	w := newWorkspace(t)

	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop/shop"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	want := []store.RepoConfig{{Name: "shop", Path: "shop/shop", DefaultBranch: store.DefaultBranch}}
	if !reflect.DeepEqual(cfg.Repos, want) {
		t.Fatalf("defaults not applied: %+v", cfg)
	}

	for _, dir := range []string{w.ProjectDir("shop"), w.MateDir("shop"), w.CrewsDir("shop")} {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Fatalf("AddProject did not create %s: %v", dir, err)
		}
	}

	cfg.Repos[0].DefaultBranch = "trunk"
	if err := w.SaveProject("shop", cfg); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	reloaded, err := w.LoadProject("shop")
	if err != nil {
		t.Fatalf("LoadProject after save: %v", err)
	}
	if reloaded.Repos[0].DefaultBranch != "trunk" {
		t.Fatalf("round trip lost fields: %+v", reloaded)
	}

	// The registration reaches workspace.yaml and survives a reopen.
	reopened, err := store.Open(w.Root())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	refs := reopened.Projects()
	if len(refs) != 1 || refs[0].Name != "shop" {
		t.Fatalf("projects = %+v, want one shop entry", refs)
	}

	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop/shop"}}}); !errors.Is(err, store.ErrProjectExists) {
		t.Fatalf("duplicate AddProject: err = %v, want ErrProjectExists", err)
	}

	if err := w.RemoveProject("shop"); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	if len(w.Projects()) != 0 {
		t.Fatalf("project still registered: %+v", w.Projects())
	}
	if _, err := os.Stat(w.ProjectDir("shop")); err != nil {
		t.Fatalf("RemoveProject deleted the state directory: %v", err)
	}
	if err := w.RemoveProject("shop"); !errors.Is(err, store.ErrNoProject) {
		t.Fatalf("RemoveProject twice: err = %v, want ErrNoProject", err)
	}
}

func TestStoreProjectNameValidation(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"shop", true},
		{"a", true},
		{"my-blog-2", true},
		{"abcdefghijabcdefghijabcdefghijab", true}, // 32 chars
		{"", false},
		{"Shop", false},
		{"2shop", false},
		{"-shop", false},
		{"shop_1", false},
		{"shop/../evil", false},
		{"abcdefghijabcdefghijabcdefghijabc", false}, // 33 chars
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := store.ValidateProjectName(tc.name)
			if tc.ok != (err == nil) {
				t.Fatalf("ValidateProjectName(%q) = %v, want ok=%v", tc.name, err, tc.ok)
			}
		})
	}
}

func TestStoreCrewIDValidation(t *testing.T) {
	cases := []struct {
		id string
		ok bool
	}{
		{"k3", true}, // ids spawned before names were words stay valid
		{"fix-cart-total", true},
		{"k-3", true},
		{"abcdefghij-abcdefghij-ab", true}, // 24 chars
		{"k", false},                       // too short
		{"", false},
		{"K3", false},
		{"3k", false},
		{"fix--cart", false},
		{"fix-cart-", false},
		{"fix_cart", false},
		{"abcdefghij-abcdefghij-abc", false}, // 25 chars
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			err := store.ValidateCrewID(tc.id)
			if tc.ok != (err == nil) {
				t.Fatalf("ValidateCrewID(%q) = %v, want ok=%v", tc.id, err, tc.ok)
			}
		})
	}
}

func TestStoreAddProjectRejectsBadConfig(t *testing.T) {
	w := newWorkspace(t)
	outside := t.TempDir()

	cases := []struct {
		name string
		proj string
		cfg  store.ProjectConfig
	}{
		{"invalid name", "Shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop/shop"}}}},
		{"empty repo path", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: ""}}}},
		{"invalid repo name", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Name: "Api", Path: "shop/shop"}}}},
		{"repo name twice", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Name: "a", Path: "shop/shop"}, {Name: "a", Path: "shop/blog"}}}},
		{"repo path twice", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Name: "a", Path: "shop/shop"}, {Name: "b", Path: "./shop/shop"}}}},
		{"repo outside workspace", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: outside}}}},
		{"repo escaping with ..", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "../elsewhere"}}}},
		{"repo is the root", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "."}}}},
		{"repo inside state dir", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: ".mate/projects"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := w.AddProject(tc.proj, tc.cfg); err == nil {
				t.Fatalf("AddProject(%q, %+v) succeeded, want an error", tc.proj, tc.cfg)
			}
			if len(w.Projects()) != 0 {
				t.Fatalf("a rejected project was registered: %+v", w.Projects())
			}
		})
	}
}

func TestStoreRepoNeedNotBeADirectChild(t *testing.T) {
	w := newWorkspace(t)
	nested := filepath.Join(w.Root(), "blog", "group", "blog")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := w.AddProject("blog", store.ProjectConfig{Repos: []store.RepoConfig{{Path: nested}}}); err != nil {
		t.Fatalf("AddProject with a nested repo: %v", err)
	}
	cfg, err := w.LoadProject("blog")
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	repo, err := cfg.SoleRepo()
	if err != nil {
		t.Fatal(err)
	}
	if repo.Path != "blog/group/blog" || repo.Name != "blog" {
		t.Fatalf("repo = %+v, want path blog/group/blog named blog", repo)
	}
	if w.RepoDir(repo.Path) != nested {
		t.Fatalf("RepoDir = %q, want %q", w.RepoDir(repo.Path), nested)
	}
}

func TestStoreAutoFlag(t *testing.T) {
	w := newWorkspace(t)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop/shop"}}}); err != nil {
		t.Fatal(err)
	}

	if w.Auto("shop") {
		t.Fatal("auto mode is on before anything set it")
	}
	if err := w.SetAuto("shop", true); err != nil {
		t.Fatalf("SetAuto(true): %v", err)
	}
	if !w.Auto("shop") {
		t.Fatal("auto mode is off after SetAuto(true)")
	}
	if _, err := os.Stat(w.AutoFlag("shop")); err != nil {
		t.Fatalf(".auto file missing: %v", err)
	}
	if err := w.SetAuto("shop", true); err != nil {
		t.Fatalf("SetAuto(true) twice: %v", err)
	}
	if err := w.SetAuto("shop", false); err != nil {
		t.Fatalf("SetAuto(false): %v", err)
	}
	if w.Auto("shop") {
		t.Fatal("auto mode still on after SetAuto(false)")
	}
	if err := w.SetAuto("shop", false); err != nil {
		t.Fatalf("SetAuto(false) on a missing flag: %v", err)
	}
}

// M19 removed the project's `mode` and `yolo`: a project.yaml that still
// carries them loads, and the next save drops them.
func TestProjectDropsRemovedModeAndYolo(t *testing.T) {
	w := newWorkspace(t)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop/shop"}}}); err != nil {
		t.Fatal(err)
	}
	old := "repos:\n    - name: shop\n      path: shop/shop\n      default_branch: main\nmode: github\nyolo: true\n"
	if err := os.WriteFile(w.ProjectFile("shop"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := w.LoadProject("shop")
	if err != nil || len(cfg.Repos) != 1 {
		t.Fatalf("LoadProject = %+v, %v; want the one repo", cfg, err)
	}
	if err := w.SaveProject("shop", cfg); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(w.ProjectFile("shop"))
	if strings.Contains(string(saved), "mode:") || strings.Contains(string(saved), "yolo:") {
		t.Fatalf("saved project.yaml kept a removed field:\n%s", saved)
	}
}

func TestUpdateCrewMetaKeepsEveryOtherKey(t *testing.T) {
	w := newWorkspace(t)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop/shop"}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.UpdateCrewMeta("shop", "k3", map[string]string{"a": "b"}); err == nil {
		t.Fatal("UpdateCrewMeta created a meta for a crew that has none")
	}
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{"branch": "mate/k3", "state": "spawned"}); err != nil {
		t.Fatal(err)
	}
	if err := w.UpdateCrewMeta("shop", "k3", map[string]string{"pr_url": "u", "state": "working"}); err != nil {
		t.Fatal(err)
	}
	got, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if got["branch"] != "mate/k3" || got["pr_url"] != "u" || got["state"] != "working" {
		t.Fatalf("meta = %v", got)
	}
}
