package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BoundaryError is the refusal for a path that resolves outside the workspace
// root. It is returned by every write in this package, so a symlink planted
// under `.mate/` cannot make the store touch a file elsewhere on disk.
type BoundaryError struct {
	// Path is the path as the layout helpers built it, before resolution.
	Path string
	// Resolved is where it really lands. Empty when resolution failed.
	Resolved string
	// Root is the resolved workspace root it had to stay inside.
	Root string
	// Err is the underlying filesystem error, if resolution failed.
	Err error
}

func (e *BoundaryError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("store: cannot resolve %s inside workspace %s: %v", e.Path, e.Root, e.Err)
	}
	return fmt.Sprintf("store: %s resolves to %s, outside workspace %s", e.Path, e.Resolved, e.Root)
}

func (e *BoundaryError) Unwrap() error { return e.Err }

// resolve returns the canonical location of path and refuses it unless that
// location is the workspace root or something inside it. The deepest existing
// ancestor is symlink-resolved and the missing tail is appended to it, so the
// check works for a file that does not exist yet while still catching a link
// anywhere above it - including a link at the leaf itself, which exists and is
// therefore resolved to its target.
func (w *Workspace) resolve(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", &BoundaryError{Path: path, Root: w.root, Err: fmt.Errorf("path is not absolute")}
	}
	clean := filepath.Clean(path)

	var tail []string
	probe := clean
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			if !within(w.root, resolved) {
				return "", &BoundaryError{Path: clean, Resolved: resolved, Root: w.root}
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", &BoundaryError{Path: clean, Root: w.root, Err: err}
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", &BoundaryError{Path: clean, Root: w.root, Err: err}
		}
		tail = append(tail, filepath.Base(probe))
		probe = parent
	}
}

// within reports whether path is root or lives under it. Both must already be
// clean and absolute.
func within(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

// Resolve is the exported form of the boundary check: it returns the
// canonical location of an absolute path and a *BoundaryError when that
// location is outside the workspace. Callers that accept a path from a user
// or an agent - `crew spawn --brief <file>` is the first - use it to refuse
// a file outside the workspace before reading it.
func (w *Workspace) Resolve(path string) (string, error) { return w.resolve(path) }
