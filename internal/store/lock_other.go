//go:build !darwin && !linux

package store

import (
	"fmt"
	"os"
)

// mate targets darwin and linux, where flock is available. Elsewhere the
// package still builds and the appends rely on O_APPEND alone.
func lockFile(f *os.File, exclusive bool) error { return nil }

func unlockFile(f *os.File) error { return nil }

func tryLockFile(f *os.File, exclusive bool) (bool, error) {
	return false, fmt.Errorf("maintenance locks require darwin or linux")
}
