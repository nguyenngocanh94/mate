package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCmdInitCreatesWorkspace(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := cmdInit([]string{dir}, &stdout, &stderr); err != nil {
		t.Fatalf("cmdInit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".matev2", "workspace.yaml")); err != nil {
		t.Fatalf("workspace.yaml missing: %v", err)
	}

	// Idempotent: a second init on the same directory does not fail.
	stdout.Reset()
	stderr.Reset()
	if err := cmdInit([]string{dir}, &stdout, &stderr); err != nil {
		t.Fatalf("second cmdInit: %v", err)
	}
}

func TestCmdInitDefaultsToCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := cmdInit(nil, &stdout, &stderr); err != nil {
		t.Fatalf("cmdInit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".matev2")); err != nil {
		t.Fatalf(".matev2 missing: %v", err)
	}
}

func TestCmdInitRejectsExtraArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := cmdInit([]string{"a", "b"}, &stdout, &stderr); err == nil {
		t.Fatal("want error for extra arguments")
	}
}
