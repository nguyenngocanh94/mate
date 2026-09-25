package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestStoreAppendStatusRefusesSymlinkEscape is the boundary test of
// docs/mvp.md section 3: a symlink planted under `.mate/` must not let a
// write land outside the workspace.
func TestStoreAppendStatusRefusesSymlinkEscape(t *testing.T) {
	w := newProjectWorkspace(t)
	outside := t.TempDir()

	crews := w.CrewsDir("shop")
	if err := os.RemoveAll(crews); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, crews); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := w.AppendStatus("shop", "k3", "done: escaped")

	var boundary *store.BoundaryError
	if !errors.As(err, &boundary) {
		t.Fatalf("AppendStatus through a symlinked crews dir: err = %v, want *store.BoundaryError", err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("the write escaped the workspace: %v %v", entries, err)
	}
}

func TestStoreWritesRefuseSymlinkEscape(t *testing.T) {
	cases := []struct {
		name string
		// link is the path inside the workspace that becomes a symlink to a
		// directory outside it, relative to the workspace root.
		link  string
		write func(w *store.Workspace) error
	}{
		{
			name:  "sent.log through a symlinked project dir",
			link:  ".mate/projects/shop",
			write: func(w *store.Workspace) error { return w.AppendSent("shop", store.SentEntry{Text: "x"}) },
		},
		{
			name:  "crew meta through a symlinked crews dir",
			link:  ".mate/projects/shop/crews",
			write: func(w *store.Workspace) error { return w.WriteCrewMeta("shop", "k3", map[string]string{"task": "x"}) },
		},
		{
			name:  "mate meta through a symlinked mate dir",
			link:  ".mate/projects/shop/mate",
			write: func(w *store.Workspace) error { return w.WriteMateMeta("shop", map[string]string{"harness": "claude"}) },
		},
		{
			name:  "auto flag through a symlinked mate dir",
			link:  ".mate/projects/shop/mate",
			write: func(w *store.Workspace) error { return w.SetAuto("shop", true) },
		},
		{
			name: "project.yaml through a symlinked project dir",
			link: ".mate/projects/shop",
			write: func(w *store.Workspace) error {
				return w.SaveProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop"}}})
			},
		},
		{
			name:  "workspace.yaml through a symlinked state dir",
			link:  ".mate",
			write: func(w *store.Workspace) error { return w.SaveConfig() },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newProjectWorkspace(t)
			outside := t.TempDir()
			path := filepath.Join(w.Root(), filepath.FromSlash(tc.link))
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Fatalf("symlink: %v", err)
			}

			err := tc.write(w)
			var boundary *store.BoundaryError
			if !errors.As(err, &boundary) {
				t.Fatalf("write through %s: err = %v, want *store.BoundaryError", tc.link, err)
			}
			entries, readErr := os.ReadDir(outside)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("the write escaped the workspace: %v", entries)
			}
		})
	}
}

func TestStoreWriteRefusesSymlinkedLeaf(t *testing.T) {
	w := newProjectWorkspace(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "stolen.log")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, w.SentLog("shop")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := w.AppendSent("shop", store.SentEntry{Source: store.SourceApp, Target: store.TargetMate, Text: "x"})
	var boundary *store.BoundaryError
	if !errors.As(err, &boundary) {
		t.Fatalf("append to a symlinked sent.log: err = %v, want *store.BoundaryError", err)
	}
	if data, err := os.ReadFile(target); err != nil || len(data) != 0 {
		t.Fatalf("the append escaped the workspace: %q %v", data, err)
	}
}

func TestStoreRepoOutsideWorkspaceIsRefused(t *testing.T) {
	w := newWorkspace(t)
	outside := t.TempDir()

	// A symlink inside the workspace pointing at a repo outside it is still
	// outside: the boundary is resolved, not lexical.
	link := filepath.Join(w.Root(), "linked-repo")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	for _, repo := range []string{outside, "linked-repo", "../elsewhere"} {
		if _, err := w.RelRepo(repo); err == nil {
			t.Fatalf("RelRepo(%q) succeeded, want a refusal", repo)
		}
	}

	inside := filepath.Join(w.Root(), "shop")
	rel, err := w.RelRepo(inside)
	if err != nil {
		t.Fatalf("RelRepo(%q): %v", inside, err)
	}
	if rel != "shop" {
		t.Fatalf("RelRepo = %q, want %q", rel, "shop")
	}
}

func TestStoreBoundaryErrorDescribesTheEscape(t *testing.T) {
	w := newProjectWorkspace(t)
	outside := t.TempDir()
	crews := w.CrewsDir("shop")
	if err := os.RemoveAll(crews); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, crews); err != nil {
		t.Fatal(err)
	}

	err := w.AppendStatus("shop", "k3", "done: x")
	var boundary *store.BoundaryError
	if !errors.As(err, &boundary) {
		t.Fatalf("err = %v, want *store.BoundaryError", err)
	}
	if boundary.Root != w.Root() {
		t.Errorf("Root = %q, want %q", boundary.Root, w.Root())
	}
	if boundary.Path != w.CrewStatus("shop", "k3") {
		t.Errorf("Path = %q, want %q", boundary.Path, w.CrewStatus("shop", "k3"))
	}
	if boundary.Resolved == "" {
		t.Error("Resolved is empty")
	}
	if boundary.Error() == "" {
		t.Error("Error() is empty")
	}
}
