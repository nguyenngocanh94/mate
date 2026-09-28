package store

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/memory"
)

// EditBacklog serializes CLI edits. Archive first: a crash can duplicate an
// old Done entry, but can never remove it from both durable files.
func (w *Workspace) EditBacklog(project, action, id, section, text string) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	path, err := w.resolve(w.BacklogFile(project))
	if err != nil {
		return err
	}
	lockPath, err := w.resolve(filepath.Join(w.MateDir(project), ".backlog.lock"))
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lockFile(lock, true); err != nil {
		return err
	}
	defer unlockFile(lock)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	next, archive, err := memory.EditBacklog(string(data), action, id, section, text)
	if err != nil {
		return err
	}
	if archive != "" {
		archivePath := filepath.Join(w.MateDir(project), memory.BacklogArchiveName)
		old, err := os.ReadFile(archivePath)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if !strings.Contains(string(old), archive) {
			if err := w.writeFile(archivePath, append(old, []byte("\n"+archive)...), 0644); err != nil {
				return err
			}
		}
	}
	return w.writeFile(path, []byte(next), 0644)
}
