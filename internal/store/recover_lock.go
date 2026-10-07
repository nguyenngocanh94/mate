package store

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// recoverLockName is the lock two consoles opened at once take turns on, so
// only one of them restarts what a machine restart took.
const recoverLockName = "recover.lock"

// RecoverLockFile is `.mate/recover.lock`.
func (w *Workspace) RecoverLockFile() string {
	return filepath.Join(w.StateDir(), recoverLockName)
}

// LockRecover waits for the recovery lock and returns what releases it. The
// wait ends with ctx. The lock is advisory and dies with the process, so a
// console that crashed mid-recovery leaves nothing to clean up.
func (w *Workspace) LockRecover(ctx context.Context) (unlock func(), err error) {
	f, err := os.OpenFile(w.RecoverLockFile(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		ok, err := tryLockFile(f, true)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			return func() {
				_ = unlockFile(f)
				_ = f.Close()
			}, nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
