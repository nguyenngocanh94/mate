//go:build !darwin && !linux

package db

import "os"

// mate targets darwin and linux, where flock is available. Elsewhere the
// package still builds and the single-writer rule rests on SQLite's own
// locking alone.
func takeLock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
}

func releaseLock(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}
