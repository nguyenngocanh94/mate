package panerun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// longTempDir is a TMPDIR deep enough that no socket fits under it, as a
// sandbox or a worktree-local scratch dir can be.
func longTempDir(t *testing.T) string {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "pr-long-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	long := filepath.Join(base, strings.Repeat("d", 60), strings.Repeat("e", 60))
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	return long
}

// The Console's sockets used to live under os.TempDir() whatever its length:
// a long TMPDIR made every column's runner fail to bind with a bare "invalid
// argument" (found 2026-10-01). SocketDir moves to /tmp when they would not
// fit.
func TestSocketDirLeavesATempDirTooLongForASocket(t *testing.T) {
	long := longTempDir(t)
	t.Setenv("TMPDIR", long)

	dir, err := SocketDir("pr", "review.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if strings.HasPrefix(dir, long) {
		t.Fatalf("SocketDir = %s, under the TMPDIR no socket fits in", dir)
	}
	socket := filepath.Join(dir, "review.sock")
	if err := checkSocketPath(socket); err != nil {
		t.Fatal(err)
	}
	// The directory is good for a real runner, not just for the length check.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	s := &Server{Term: Terminal{Out: devnull, Err: devnull}}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, socket) }()
	waitFor(t, func() bool { _, err := os.Stat(socket); return err == nil })
	cancel()
	if err := <-served; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

// A short TMPDIR is used as it is.
func TestSocketDirKeepsAShortTempDir(t *testing.T) {
	short, err := os.MkdirTemp("/tmp", "pr-short-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(short)
	t.Setenv("TMPDIR", short)
	dir, err := SocketDir("pr", "stage.sock")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != short {
		t.Fatalf("SocketDir = %s, want it under %s", dir, short)
	}
}

// A path the kernel cannot bind is refused by name and length, not with the
// kernel's bare EINVAL, on both ends.
func TestAnOverlongSocketPathIsRefusedSayingWhy(t *testing.T) {
	socket := filepath.Join(longTempDir(t), "s.sock")
	err := (&Server{}).Serve(context.Background(), socket)
	if err == nil || !strings.Contains(err.Error(), "a unix socket path holds at most") {
		t.Fatalf("Serve err = %v, want the length refusal", err)
	}
	err = Send(context.Background(), socket, Command{Argv: []string{"true"}})
	if err == nil || !strings.Contains(err.Error(), "a unix socket path holds at most") {
		t.Fatalf("Send err = %v, want the length refusal", err)
	}
}
