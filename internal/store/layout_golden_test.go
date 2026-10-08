package store_test

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// The layout golden pins, byte for byte, the two configuration files a
// workspace is made of today: `.mate/workspace.yaml` and
// `.mate/projects/<p>/project.yaml`, as store.Init, AddProject and AddRepo
// write them. It is the safety net for the workspace layout plan
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, PR 0): a later PR
// that moves these files must show that what they say is unchanged. A diff
// here is a behaviour change; rerun with -update only when that change is
// intended, and read the golden diff before committing it.
var updateLayout = flag.Bool("update", false, "rewrite testdata/layout/*.golden")

func TestWorkspaceAndProjectYAMLGolden(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "shop", "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := exec.Command("git", "init", "-q", repo)
	git.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	w, err := store.Init(dir, store.Defaults{MateHarness: "claude", CrewHarness: "codex"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if _, err := w.AddRepo("shop", store.RepoConfig{Path: repo}); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	// The root is absolute and the session name is a hash of it; both are
	// the test's temp directory, not behaviour.
	normalize := strings.NewReplacer(w.Root(), "{{WORKSPACE}}", w.Session(), "{{SESSION}}").Replace
	for _, tc := range []struct{ path, golden string }{
		{w.WorkspaceFile(), "workspace.yaml.golden"},
		{w.ProjectFile("shop"), "project.yaml.golden"},
	} {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		checkLayoutGolden(t, filepath.Join("testdata", "layout", tc.golden), normalize(string(data)))
	}
}

func checkLayoutGolden(t *testing.T, path, got string) {
	t.Helper()
	if *updateLayout {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/store -run TestWorkspaceAndProjectYAMLGolden -update to record it)", err)
	}
	if got != string(want) {
		t.Errorf("%s changed byte for byte.\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}
