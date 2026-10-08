package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBeadsLockSerializesProcessesAndHonorsCancellation(t *testing.T) {
	w, err := Init(t.TempDir(), Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.BeadsDir("missing"); err == nil {
		t.Fatal("unregistered tracker")
	}
	if _, err := w.BeadsDir("../escape"); err == nil {
		t.Fatal("escaping tracker")
	}
	unlock, err := w.LockBeads(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if release, err := other.LockBeads(ctx, "shop"); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("second writer acquired held lock: %v", err)
	}
	unlock()
	release, err := other.LockBeads(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	release()
}
