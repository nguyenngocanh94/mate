package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// locksDirName is `projects/<p>/locks/`, where each tool the project uses
// has its lock: the only trace a tool leaves under `.mate`, an empty file
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, section 5.2). The
// tool's data lives in the project directory, never here.
const locksDirName = "locks"

// ToolLockFile is `projects/<project>/locks/<name>.lock`.
func (w *Workspace) ToolLockFile(project, name string) string {
	return filepath.Join(w.ProjectDir(project), locksDirName, name+".lock")
}

// LockTool waits for the lock of tool name in project and returns what
// releases it: mate's commands on one tool's data in one project take
// turns. The wait ends with ctx. The lock is advisory and dies with the
// process; the file stays, empty. The store does not know which tool asks.
func (w *Workspace) LockTool(ctx context.Context, project, name string) (func(), error) {
	if err := ValidateProjectName(project); err != nil {
		return nil, err
	}
	if _, ok := w.Project(project); !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoProject, project)
	}
	if name == "" || name != strings.ToLower(strings.TrimSpace(name)) || filepath.Base(name) != name || strings.HasPrefix(name, ".") {
		return nil, fmt.Errorf("store: invalid tool name %q", name)
	}
	path, err := w.resolve(w.ToolLockFile(project, name))
	if err != nil {
		return nil, err
	}
	if err := w.mkdirAll(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
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
