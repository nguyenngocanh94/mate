package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestRunUnknownMateSubcommandIsUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"mate", "bogus"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestMateSubcommandsRequireExactlyOneProject(t *testing.T) {
	for _, sub := range []string{"start", "stop", "status"} {
		var out, errw bytes.Buffer
		err := run([]string{"mate", sub}, &out, &errw)
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Fatalf("mate %s with no project: err = %v, want *usageError", sub, err)
		}
	}
}

func TestMateStartRejectsAnUnknownHarness(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"mate", "start", "shop", "--harness", "gemini"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

// A harness that is registered but cannot run a Mate is a usage error that
// names the capability it lacks, before any workspace is looked for.
func TestMateStartRefusesACrewOnlyHarness(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"mate", "start", "shop", "--harness", "pi"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "Hooks") {
		t.Fatalf("err = %v, want a *usageError naming Hooks", err)
	}
}

// mate status is the one subcommand that answers without a Herdr server: a
// project whose meta names no agent is stopped, and it says so in one line.
func TestMateStatusPrintsOneLineForAStoppedProject(t *testing.T) {
	root := t.TempDir()
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo}}}); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if err := run([]string{"mate", "status", "shop", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("mate status: %v", err)
	}
	line := strings.TrimSpace(out.String())
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("out = %q, want a single line", out.String())
	}
	if !strings.HasPrefix(line, "shop: "+string(spawn.StateStopped)) {
		t.Fatalf("out = %q, want the project and its state", line)
	}
	if !strings.Contains(line, "session_id=none") {
		t.Fatalf("out = %q, want session_id=none for a project that never started", line)
	}
	if !strings.Contains(line, "last start: fresh") {
		t.Fatalf("out = %q, want the last-start resume state", line)
	}
}

// mate start accepts --fresh alongside the project argument; this only
// checks flag parsing (the rest needs a Herdr server), consistent with
// TestMateStartRejectsAnUnknownHarness below.
func TestMateStartAcceptsFreshFlag(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"mate", "start", "shop", "--fresh", "--workspace", t.TempDir()}, &out, &errw)
	var ue *usageError
	if errors.As(err, &ue) {
		t.Fatalf("--fresh must be a recognised flag, got usage error: %v", err)
	}
}
