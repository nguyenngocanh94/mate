package store

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A `.meta` file is `key=value`, one key per line, keys `[a-z_]+` and values a
// single line. It is written whole and atomically, and it is small enough that
// the store never edits one in place.

// ReadMeta parses a `.meta` file. A missing file is not an error: it returns an
// empty map, because a crew that has not been recorded yet has no meta.
//
// ReadMeta and WriteMeta are the file format only; they do not check the
// workspace boundary. Use the Workspace methods for anything under `.matev2/`.
func ReadMeta(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimRight(scanner.Text(), "\r")
		if text == "" {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("store: %s line %d: no '=' in %q", path, line, text)
		}
		if !metaKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("store: %s line %d: invalid key %q: want [a-z_]+", path, line, key)
		}
		out[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// WriteMeta writes a `.meta` file atomically, keys in sorted order so the file
// is byte-identical for the same map and a diff shows only real changes. A key
// that is not `[a-z_]+`, or a value containing a newline, is refused and
// nothing is written.
func WriteMeta(path string, meta map[string]string) error {
	data, err := encodeMeta(meta)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o644)
}

func encodeMeta(meta map[string]string) ([]byte, error) {
	keys := make([]string, 0, len(meta))
	for k := range meta {
		if !metaKeyPattern.MatchString(k) {
			return nil, fmt.Errorf("store: invalid meta key %q: want [a-z_]+", k)
		}
		if strings.ContainsAny(meta[k], "\n\r") {
			return nil, fmt.Errorf("store: meta value for %q contains a newline", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(meta[k])
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// writeFileAtomic is the boundary-free form of Workspace.writeFile, used by the
// exported file-format helpers.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// ReadCrewMeta reads `crews/<crew>.meta`.
func (w *Workspace) ReadCrewMeta(project, crew string) (map[string]string, error) {
	if err := validatePair(project, crew); err != nil {
		return nil, err
	}
	return ReadMeta(w.CrewMeta(project, crew))
}

// WriteCrewMeta writes `crews/<crew>.meta` atomically, inside the workspace.
func (w *Workspace) WriteCrewMeta(project, crew string, meta map[string]string) error {
	if err := validatePair(project, crew); err != nil {
		return err
	}
	data, err := encodeMeta(meta)
	if err != nil {
		return err
	}
	return w.writeFile(w.CrewMeta(project, crew), data, 0o644)
}

// ReadMateMeta reads `mate/mate.meta`.
func (w *Workspace) ReadMateMeta(project string) (map[string]string, error) {
	if err := ValidateProjectName(project); err != nil {
		return nil, err
	}
	return ReadMeta(w.MateMeta(project))
}

// WriteMateMeta writes `mate/mate.meta` atomically, inside the workspace.
func (w *Workspace) WriteMateMeta(project string, meta map[string]string) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	data, err := encodeMeta(meta)
	if err != nil {
		return err
	}
	return w.writeFile(w.MateMeta(project), data, 0o644)
}

func validatePair(project, crew string) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	return ValidateCrewID(crew)
}
