package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestToolLockSerializesProcessesAndHonorsCancellation(t *testing.T) {
	w, err := Init(t.TempDir(), Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ project, name string }{
		{"missing", "beads"}, {"../escape", "beads"},
		{"shop", ""}, {"shop", "../escape"}, {"shop", "a/b"}, {"shop", ".hidden"}, {"shop", "Beads"},
	} {
		if release, err := w.LockTool(context.Background(), tc.project, tc.name); err == nil {
			release()
			t.Errorf("LockTool(%q, %q) was taken", tc.project, tc.name)
		}
	}
	unlock, err := w.LockTool(context.Background(), "shop", "beads")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.Root(), ".mate", "projects", "shop", "locks", "beads.lock")
	if path != w.ToolLockFile("shop", "beads") {
		t.Fatalf("ToolLockFile = %s, want %s", w.ToolLockFile("shop", "beads"), path)
	}
	if st, err := os.Stat(path); err != nil || st.Size() != 0 {
		t.Fatalf("lock file %s: %v", path, err)
	}
	other, err := Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if release, err := other.LockTool(ctx, "shop", "beads"); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("second writer acquired held lock: %v", err)
	}
	// Another tool's lock is its own.
	release, err := other.LockTool(context.Background(), "shop", "fresh")
	if err != nil {
		t.Fatal(err)
	}
	release()
	unlock()
	release, err = other.LockTool(context.Background(), "shop", "beads")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

// A lock file that is a symlink out of the workspace is refused, not
// followed.
func TestToolLockRefusesAnEscapingSymlink(t *testing.T) {
	w, err := Init(t.TempDir(), Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	path := w.ToolLockFile("shop", "beads")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.lock")
	if err := os.WriteFile(elsewhere, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, path); err != nil {
		t.Fatal(err)
	}
	var boundary *BoundaryError
	if release, err := w.LockTool(context.Background(), "shop", "beads"); !errors.As(err, &boundary) {
		if release != nil {
			release()
		}
		t.Fatalf("LockTool through an escaping symlink = %v, want a BoundaryError", err)
	}
}
