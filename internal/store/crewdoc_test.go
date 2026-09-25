package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitAndAddProjectSeedCrewDocs(t *testing.T) {
	root := t.TempDir()
	w, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", ProjectConfig{Repos: []RepoConfig{{Path: repo}}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{w.WorkspaceCrewDoc(), w.ProjectCrewDoc("shop")} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s not seeded: %v", path, err)
		}
		if !strings.HasPrefix(string(body), "<!--") {
			t.Errorf("%s seed is not a comment: %q", path, body)
		}
	}
	// A seeded file contributes nothing to a brief.
	ws, proj, err := w.CrewRules("shop")
	if err != nil {
		t.Fatal(err)
	}
	if ws != "" || proj != "" {
		t.Fatalf("seeded CREW.md files yield rules %q / %q, want none", ws, proj)
	}

	// The captain's edits survive a second Init, and are read back without
	// the comment.
	if err := os.WriteFile(w.WorkspaceCrewDoc(), []byte("<!-- mine -->\n\n- Reproduce first.\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	ws, _, err = w.CrewRules("shop")
	if err != nil {
		t.Fatal(err)
	}
	if ws != "- Reproduce first." {
		t.Fatalf("workspace rules = %q", ws)
	}
}

func TestCrewRulesMissingFilesAreEmpty(t *testing.T) {
	w, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(w.WorkspaceCrewDoc()); err != nil {
		t.Fatal(err)
	}
	ws, proj, err := w.CrewRules("nosuch")
	if err != nil || ws != "" || proj != "" {
		t.Fatalf("CrewRules on missing files = %q, %q, %v", ws, proj, err)
	}
}

func TestStripComments(t *testing.T) {
	for in, want := range map[string]string{
		"a<!-- x -->b":         "ab",
		"a<!-- x\ny -->b<!--c": "ab",
		"no comment":           "no comment",
	} {
		if got := StripComments(in); got != want {
			t.Errorf("StripComments(%q) = %q, want %q", in, got, want)
		}
	}
}
