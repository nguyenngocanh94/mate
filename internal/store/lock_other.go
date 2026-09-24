//go:build !darwin && !linux

package store

import "os"

// mate targets darwin and linux, where flock is available. Elsewhere the
// package still builds and the appends rely on O_APPEND alone.
func lockFile(f *os.File, exclusive bool) error { return nil }

func unlockFile(f *os.File) error { return nil }
