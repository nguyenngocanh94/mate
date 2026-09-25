package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// newProjectWorkspace initialises a workspace with the `shop` project already
// registered.
func newProjectWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := newWorkspace(t)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

func TestStoreProjectAddLoadSave(t *testing.T) {
	w := newWorkspace(t)

	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	want := []store.RepoConfig{{Name: "shop", Path: "shop", DefaultBranch: store.DefaultBranch}}
	if !reflect.DeepEqual(cfg.Repos, want) || cfg.Mode != store.ModeLocalOnly || cfg.Yolo {
		t.Fatalf("defaults not applied: %+v", cfg)
	}

	for _, dir := range []string{w.ProjectDir("shop"), w.MateDir("shop"), w.CrewsDir("shop")} {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Fatalf("AddProject did not create %s: %v", dir, err)
		}
	}

	cfg.Yolo = true
	cfg.Repos[0].DefaultBranch = "trunk"
	if err := w.SaveProject("shop", cfg); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	reloaded, err := w.LoadProject("shop")
	if err != nil {
		t.Fatalf("LoadProject after save: %v", err)
	}
	if !reloaded.Yolo || reloaded.Repos[0].DefaultBranch != "trunk" {
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

	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop"}}}); !errors.Is(err, store.ErrProjectExists) {
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
		{"k3", true},
		{"crew1", true},
		{"abcdefghijabcdef", true}, // 16 chars
		{"k", false},               // too short
		{"", false},
		{"K3", false},
		{"3k", false},
		{"k-3", false},
		{"abcdefghijabcdefg", false}, // 17 chars
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
		{"invalid name", "Shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop"}}}},
		{"empty repo path", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: ""}}}},
		{"invalid repo name", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Name: "Api", Path: "shop"}}}},
		{"repo name twice", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Name: "a", Path: "shop"}, {Name: "a", Path: "blog"}}}},
		{"repo path twice", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Name: "a", Path: "shop"}, {Name: "b", Path: "./shop"}}}},
		{"repo outside workspace", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: outside}}}},
		{"repo escaping with ..", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "../elsewhere"}}}},
		{"repo is the root", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "."}}}},
		{"repo inside state dir", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: ".mate/projects"}}}},
		{"unsupported mode", "shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop"}}, Mode: "remote"}},
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
	nested := filepath.Join(w.Root(), "group", "blog")
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
	if repo.Path != "group/blog" || repo.Name != "blog" {
		t.Fatalf("repo = %+v, want path group/blog named blog", repo)
	}
	if w.RepoDir(repo.Path) != nested {
		t.Fatalf("RepoDir = %q, want %q", w.RepoDir(repo.Path), nested)
	}
}

func TestStoreAutoFlag(t *testing.T) {
	w := newWorkspace(t)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop"}}}); err != nil {
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
