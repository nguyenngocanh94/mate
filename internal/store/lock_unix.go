//go:build darwin || linux

package store

import (
	"os"
	"syscall"
)

// lockFile takes an advisory whole-file lock, exclusive for a writer and
// shared for a reader, and holds it for the duration of one append or one
// read. Appends are O_APPEND so the kernel already keeps a single write
// atomic; the lock is what keeps a reader from seeing a torn line and what
// serialises the append that a crew's `echo` makes from a shell.
func lockFile(f *os.File, exclusive bool) error {
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	for {
		err := syscall.Flock(int(f.Fd()), how)
		if err == syscall.EINTR {
			continue
		}
		return err
	}
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
