package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

func TestRunUnknownCrewSubcommandIsUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"crew", "bogus"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestCrewSpawnRequiresProjectIDAndBrief(t *testing.T) {
	cases := [][]string{
		{"crew", "spawn"},
		{"crew", "spawn", "shop"},
		{"crew", "spawn", "shop", "k3"}, // no --brief
		{"crew", "spawn", "shop", "k3", "--brief", "b", "--harness", "pi"}, // unknown harness
	}
	for _, args := range cases {
		var out, errw bytes.Buffer
		err := run(args, &out, &errw)
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Fatalf("%v: err = %v, want *usageError", args, err)
		}
	}
}

func TestCrewListPrintsTheRecordedCrews(t *testing.T) {
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repo: repo}); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if err := run([]string{"crew", "list", "shop", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew list: %v", err)
	}
	if !strings.Contains(out.String(), "no crews recorded") {
		t.Fatalf("out = %q, want the empty-list line", out.String())
	}

	if err := w.WriteCrewMeta("shop", "k3", map[string]string{
		"harness": "codex",
		"branch":  "matev2/k3",
		"pane":    "w1:p2",
		"task":    "Add a healthcheck endpoint.",
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "done: ready in branch matev2/k3"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"crew", "list", "shop", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew list: %v", err)
	}
	for _, want := range []string{"k3", "codex", "matev2/k3", "done: ready in branch matev2/k3", "w1:p2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want it to carry %q", out.String(), want)
		}
	}
}
