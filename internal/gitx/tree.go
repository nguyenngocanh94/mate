package gitx

import (
	"context"
	"strconv"
	"strings"
)

// The calls in this file read git's metadata about a committed tree - entry
// names and types, and commit counts - and never a blob. `ls-tree` lists a
// tree object's entries without opening the files they name, which is what
// lets `matev2 project facts` tell the Mate what a repository holds without
// the Mate, or the app on its behalf, reading any of it (docs/mvp.md
// decision 1 and M7).

// TreeEntry is one entry of a tree: its name and whether it is a directory
// (`tree`), a file (`blob`) or a submodule (`commit`).
type TreeEntry struct {
	Name string
	Type string
}

// IsDir reports whether the entry is a directory.
func (e TreeEntry) IsDir() bool { return e.Type == "tree" }

// CommitCount is `git rev-list --count <rev>`: how many commits rev reaches.
func (g Git) CommitCount(ctx context.Context, dir, rev string) (int, error) {
	out, err := g.run(ctx, dir, "rev-list", "--count", rev, "--")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// TopLevelEntries is `git ls-tree -z --full-tree <rev>`: the entries of the
// root tree of rev, names and types only.
func (g Git) TopLevelEntries(ctx context.Context, dir, rev string) ([]TreeEntry, error) {
	out, err := g.run(ctx, dir, "ls-tree", "-z", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	var entries []TreeEntry
	for _, rec := range strings.Split(out, "\x00") {
		// `<mode> SP <type> SP <object> TAB <name>`
		meta, name, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) < 2 {
			continue
		}
		entries = append(entries, TreeEntry{Name: name, Type: fields[1]})
	}
	return entries, nil
}

// TreePaths is `git ls-tree -r -z --name-only --full-tree <rev>`: the path
// of every file rev's tree holds, recursively.
func (g Git) TreePaths(ctx context.Context, dir, rev string) ([]string, error) {
	out, err := g.run(ctx, dir, "ls-tree", "-r", "-z", "--name-only", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
