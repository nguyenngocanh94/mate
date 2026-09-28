package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// TryMateMaintenance holds an OS lock until release. Exclusive is used by
// refresh; delivery takes a shared lock. A process crash releases either.
// The lock file is never removed: replacing its inode would split the lock.
func (w *Workspace) TryMateMaintenance(project string, exclusive bool) (release func(), acquired bool, err error) {
	if err = ValidateProjectName(project); err != nil {
		return
	}
	if _, ok := w.Project(project); !ok {
		return nil, false, fmt.Errorf("%w: %s", ErrNoProject, project)
	}
	path := filepath.Join(w.MateDir(project), ".maintenance.lock")
	path, err = w.resolve(path)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	acquired, err = tryLockFile(f, exclusive)
	if err != nil || !acquired {
		f.Close()
		return nil, acquired, err
	}
	return func() { _ = unlockFile(f); _ = f.Close() }, true, nil
}
