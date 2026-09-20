//go:build darwin || linux

package db

import (
	"os"
	"syscall"
)

// takeLock opens (creating) the lock file and takes a non-blocking exclusive
// flock on it. The lock is advisory and held for as long as the handle is
// open: a second writer is refused immediately rather than queued, because a
// writer that waits is a console that hangs on startup with no explanation.
//
// The file is never deleted. Unlinking it would let a second process create a
// fresh inode and lock that instead, which is a lock that locks nothing.
func takeLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == syscall.EINTR {
			continue
		}
		if err == nil {
			return f, nil
		}
		_ = f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, ErrLocked
		}
		return nil, err
	}
}

func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
