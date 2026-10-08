package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// BeadsDir is the project's tracker, shared by all its repos.
func (w *Workspace) BeadsDir(project string) (string, error) {
	if err := ValidateProjectName(project); err != nil {
		return "", err
	}
	if _, ok := w.Project(project); !ok {
		return "", fmt.Errorf("project %q is not registered", project)
	}
	return w.resolve(filepath.Join(w.ProjectDir(project), ".beads"))
}

// LockBeads serializes bd's embedded single-writer engine and export across
// Mate commands. The separate lock survives replacement of tracker files.
func (w *Workspace) LockBeads(ctx context.Context, project string) (func(), error) {
	if _, err := w.BeadsDir(project); err != nil {
		return nil, err
	}
	if err := w.mkdirAll(w.ProjectDir(project)); err != nil {
		return nil, err
	}
	path, err := w.resolve(filepath.Join(w.ProjectDir(project), ".beads.lock"))
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		ok, err := tryLockFile(f, true)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			return func() { _ = unlockFile(f); _ = f.Close() }, nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
