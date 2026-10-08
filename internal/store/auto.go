package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// QuietAfter is how long the captain has to leave a Mate that has answered
// them alone before a context refresh may run (cmd/mate/context_refresh.go).
// Auto mode no longer waits on it: a captain prompt does not turn auto off
// (docs/mvp.md M18).
const QuietAfter = 5 * time.Minute

// The auto daemon's digest cursor (docs/mvp.md section 5, task 19): how far
// into each of a project's append-only files the daemon has already
// digested, so a console that restarts never re-sends an item the Mate has
// already been handed.
//
// # Why its own file and not the `.auto` flag
//
// The flag and the cursor have different writers and different lifetimes.
// `.auto` is created and deleted by two parties - the console's `m` key,
// and a user who runs `touch`/`rm` on it - and its
// whole meaning is its presence. A cursor stored inside it would be
// destroyed every time auto went off, so the next time auto came
// on the daemon would re-send every item it had already digested, which is
// precisely the property the cursor exists to guarantee. A hand-`touch`ed
// `.auto` would also read as an empty or corrupt cursor. `.auto-cursor`
// survives the flag flipping, and a stale one costs nothing: the daemon only
// ever compares offsets against the files that are actually there.
//
// # Format
//
// `<path>=<offset>` per line, one line per file the digest has read, where
// <path> is slash-separated and relative to the project's state directory
// (`crews/k3.status`, `incidents.log`) and <offset> is the byte offset of the
// last line digested out of it. Keys are sorted and the file is written whole
// and atomically, like every other key=value file in this package, so the
// same cursor is always the same bytes.
//
// The API is in absolute paths, because that is what a reader of a merged
// view holds (box.Entry.Ref.File). The relative spelling on disk is this
// package's business, and it is what makes the boundary checkable: a key that
// does not land inside the project's own directory is refused rather than
// followed.

// ReadAutoCursor reads `mate/.auto-cursor`, keyed by absolute path. A missing
// file is not an error - a project the daemon has never digested has no
// cursor - and neither is a line this package cannot parse: the cursor is a
// resume hint, and one bad line must not make the daemon forget the rest and
// re-send everything.
func (w *Workspace) ReadAutoCursor(project string) (map[string]int64, error) {
	if err := ValidateProjectName(project); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(w.AutoCursorFile(project))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]int64{}, nil
		}
		return nil, err
	}
	base := w.ProjectDir(project)
	out := map[string]int64{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		offset, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || offset < 0 {
			continue
		}
		path, err := autoCursorPath(base, key)
		if err != nil {
			continue
		}
		out[path] = offset
	}
	return out, nil
}

// WriteAutoCursor writes `mate/.auto-cursor` atomically. Keys are absolute
// paths inside the project's own state directory; anything else is refused
// and nothing is written, so a cursor can never name a file the daemon has no
// business reading.
func (w *Workspace) WriteAutoCursor(project string, cursor map[string]int64) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	base := w.ProjectDir(project)
	lines := make([]string, 0, len(cursor))
	for path, offset := range cursor {
		if offset < 0 {
			return fmt.Errorf("store: auto cursor offset for %s is negative", path)
		}
		rel, err := autoCursorKey(base, path)
		if err != nil {
			return err
		}
		lines = append(lines, rel+"="+strconv.FormatInt(offset, 10))
	}
	sort.Strings(lines)
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return w.writeFile(w.AutoCursorFile(project), []byte(b.String()), 0o644)
}

// autoCursorKey is the on-disk spelling of one absolute path: relative to the
// project directory, slash-separated, and refused if it escapes.
func autoCursorKey(base, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("store: auto cursor path %q is not absolute", path)
	}
	rel, err := filepath.Rel(base, filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("store: auto cursor path %q: %w", path, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("store: auto cursor path %q is outside %s", path, base)
	}
	if strings.ContainsAny(rel, "=\n\r") {
		return "", fmt.Errorf("store: auto cursor path %q cannot be recorded", path)
	}
	return filepath.ToSlash(rel), nil
}

// autoCursorPath reverses autoCursorKey, refusing a key that would land
// outside the project directory.
func autoCursorPath(base, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "/") {
		return "", fmt.Errorf("store: invalid auto cursor key %q", key)
	}
	path := filepath.Join(base, filepath.FromSlash(key))
	if !within(base, path) {
		return "", fmt.Errorf("store: auto cursor key %q is outside %s", key, base)
	}
	return path, nil
}
