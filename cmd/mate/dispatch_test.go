package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdVersionPrintsVersion(t *testing.T) {
	var out, errw bytes.Buffer
	if err := cmdVersion(nil, &out, &errw); err != nil {
		t.Fatalf("cmdVersion: %v", err)
	}
	if !strings.HasPrefix(out.String(), "mate ") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestRunTopLevelVersionFlag(t *testing.T) {
	var out, errw bytes.Buffer
	if err := run([]string{"--version"}, &out, &errw); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(out.String(), "mate ") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestRunUnknownCommandIsUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"bogus"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestRunUnknownProjectSubcommandIsUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"project", "bogus"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestMainRunExitCodes(t *testing.T) {
	var out, errw bytes.Buffer
	if code := mainRun([]string{"--version"}, &out, &errw); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}

	out.Reset()
	errw.Reset()
	if code := mainRun([]string{"bogus"}, &out, &errw); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.HasPrefix(errw.String(), "mate: ") {
		t.Fatalf("stderr = %q, want it to start with %q", errw.String(), "mate: ")
	}

	out.Reset()
	errw.Reset()
	ws := t.TempDir()
	missing := filepath.Join(ws, "nope")
	if code := mainRun([]string{"project", "list", "--workspace", missing}, &out, &errw); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

func TestFindWorkspaceDirWalksUp(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	nested := filepath.Join(ws, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}

	dir, err := findWorkspaceDir("")
	if err != nil {
		t.Fatalf("findWorkspaceDir: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("dir = %s, want %s", dir, ws)
	}
}

func TestFindWorkspaceDirNoneFound(t *testing.T) {
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if _, err := findWorkspaceDir(""); err == nil {
		t.Fatal("want error when no workspace is found")
	}
}

func TestFindWorkspaceDirEnvVar(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Setenv("MATE_WORKSPACE", ws)

	dir, err := findWorkspaceDir("")
	if err != nil {
		t.Fatalf("findWorkspaceDir: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("dir = %s, want %s", dir, ws)
	}
}

func TestFindWorkspaceDirFlagWinsOverEnv(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Setenv("MATE_WORKSPACE", t.TempDir())

	dir, err := findWorkspaceDir(ws)
	if err != nil {
		t.Fatalf("findWorkspaceDir: %v", err)
	}
	if dir != ws {
		t.Fatalf("dir = %s, want %s", dir, ws)
	}
}
