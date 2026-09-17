package harness

import (
	"path/filepath"
	"strings"
)

// samePath reports whether a and b name the same location, resolving
// symlinks when both sides exist. v1 kept this in internal/fsboundary; the
// codex trust reader is the only harness caller, so it is inlined here
// rather than pulling that package across.
func samePath(a, b string) bool {
	ca := filepath.Clean(strings.TrimSpace(a))
	cb := filepath.Clean(strings.TrimSpace(b))
	if ca == cb {
		return true
	}
	ra, err := filepath.EvalSymlinks(ca)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(cb)
	if err != nil {
		return false
	}
	return filepath.Clean(ra) == filepath.Clean(rb)
}
