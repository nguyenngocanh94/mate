package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A repo can be added to a Project by URL as well as by path (docs/mvp.md
// task 59), so a Mate sets up its own Project with mate commands alone: the
// clone lands in the project's directory, `<root>/<project>/` (docs/mvp.md
// section 3), and a repo with no commit at all is
// given an empty first one, which `crew spawn` needs to branch from. Neither
// step ever pushes.

// scpLikeURL is git's `user@host:path` remote form. A drive letter or a bare
// `name:path` without the `user@` is not one, so a path is never cloned.
var scpLikeURL = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^/]`)

// isGitURL reports whether a repo argument names a remote to clone rather
// than a directory on this machine.
func isGitURL(s string) bool {
	if scpLikeURL.MatchString(s) {
		return true
	}
	for _, scheme := range []string{"https://", "http://", "ssh://", "git://", "file://"} {
		if strings.HasPrefix(s, scheme) {
			return true
		}
	}
	return false
}

// repoNameFromURL is the directory git itself would clone url into: the
// last path element, without a trailing `.git`.
func repoNameFromURL(url string) string {
	path := strings.TrimRight(url, "/")
	if i := strings.LastIndexAny(path, "/:"); i >= 0 {
		path = path[i+1:]
	}
	return strings.TrimSuffix(path, ".git")
}

// cloneRepo clones url into dir/<name>, dir being the project's directory,
// and returns that directory. It refuses a destination that already exists,
// whatever is in it: a repo the captain keeps there is theirs, and cloning
// beside it under another name is the caller's choice to make with --name.
func cloneRepo(stdout io.Writer, dir, url, name string) (string, error) {
	if name == "" {
		name = repoNameFromURL(url)
	}
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) {
		return "", fmt.Errorf("cannot name a clone of %s; pass --name", url)
	}
	dest := filepath.Join(dir, name)
	if _, err := os.Lstat(dest); err == nil {
		return "", fmt.Errorf("%s already exists in %s; add it by path, or pass --name to clone under another name", name, dir)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// git makes dir if the captain removed it since `project add`; running
	// from its parent, the workspace root, works either way.
	if _, err := runGit(filepath.Dir(dir), "clone", "--quiet", "--", url, dest); err != nil {
		return "", fmt.Errorf("clone %s: %w", url, err)
	}
	fmt.Fprintf(stdout, "cloned %s into %s\n", url, name)
	return dest, nil
}

// ensureFirstCommit gives a repo with no commit on any ref an empty first
// commit on branch, and reports whether it did. A repo with any history at
// all - a local branch, a remote-tracking one - is left exactly as it is.
func ensureFirstCommit(dir, branch string) (bool, error) {
	out, err := runGit(dir, "rev-list", "-n", "1", "--all")
	if err != nil {
		return false, err
	}
	if out != "" {
		return false, nil
	}
	if _, err := runGit(dir, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		return false, err
	}
	if _, err := runGit(dir, "commit", "--quiet", "--allow-empty", "-m", "Initial commit"); err != nil {
		return false, fmt.Errorf("make the first commit: %w", err)
	}
	return true, nil
}
