// Package facts answers `mate project facts`: what a project's repository
// holds, from git's metadata alone - whether the default branch has a
// commit, how many files its tree has, the names at its top level, and which
// build and test files it has, recognised by name.
//
// It exists because the Mate may not read the repository (docs/mvp.md
// decision 1), and the one thing firstmate gets from reading it that no
// brief can do without is the difference between "a repository with an app
// in it" and "an empty repository" (the buyesp32 brief told a Crew to
// "inspect the existing app" of a repository with no files at all). Every
// fact here is a name or a count; nothing opens a file, which the tests hold
// the package to by recording every git call it makes.
package facts

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/gitx"
)

// Facts is what Gather found about one branch of one repository.
type Facts struct {
	Project string
	// RepoName is the repo's name inside its project, the `<repo>:` of a
	// PROJECT.md anchor (docs/mvp.md M9).
	RepoName      string
	Repo          string
	DefaultBranch string
	// HasCommit is false for a branch with no commit, including the unborn
	// branch of a repository nobody has committed to.
	HasCommit bool
	// Head is the abbreviated commit the branch points at, empty when it
	// has none. It is the anchor PROJECT.md's repo-state lines carry
	// (`<repo>:main@<head>`), so a fact can be told apart from one
	// recorded before the branch moved.
	Head    string
	Commits int
	Files   int
	// TopLevel is the names at the root of the tree, directories with a
	// trailing slash, sorted.
	TopLevel []string
	// BuildFiles are the paths of recognised build and test files within
	// MaxDepth of the root.
	BuildFiles []string
	// Docs are the recognised instruction and readme files at the root.
	Docs []string
}

// MaxDepth is how deep a build file is still recognised: the root and one
// directory down, which covers a `web/package.json` beside a `go.mod`
// without listing every manifest of a vendored tree.
const MaxDepth = 2

// buildFileNames are recognised build, test and task-runner files, by exact
// base name. Names only: whether a Makefile has a `test` target is for a
// Crew to find out.
var buildFileNames = map[string]bool{
	"Makefile": true, "makefile": true, "GNUmakefile": true, "justfile": true, "Justfile": true, "Taskfile.yml": true,
	"go.mod":       true,
	"package.json": true, "deno.json": true, "tsconfig.json": true,
	"pyproject.toml": true, "setup.py": true, "setup.cfg": true, "requirements.txt": true, "Pipfile": true, "tox.ini": true, "pytest.ini": true, "noxfile.py": true,
	"Cargo.toml": true,
	"pom.xml":    true, "build.gradle": true, "build.gradle.kts": true, "settings.gradle": true, "settings.gradle.kts": true,
	"CMakeLists.txt": true, "meson.build": true,
	"Gemfile": true, "Rakefile": true,
	"composer.json": true, "mix.exs": true, "Package.swift": true, "pubspec.yaml": true,
	"Dockerfile": true, "docker-compose.yml": true, "docker-compose.yaml": true, "compose.yaml": true, "compose.yml": true,
	"hugo.toml": true, "hugo.yaml": true,
}

// buildFilePrefixes catch the config files whose extension varies:
// `vite.config.ts`, `jest.config.js`, `playwright.config.mjs`.
var buildFilePrefixes = []string{"vite.config.", "vitest.config.", "jest.config.", "playwright.config.", "webpack.config.", "next.config.", "astro.config.", "svelte.config.", "nuxt.config.", "eslint.config."}

// docNames are the files at the root a Crew reads for project knowledge.
var docNames = map[string]bool{"AGENTS.md": true, "CLAUDE.md": true, "README.md": true, "README": true, "README.rst": true, "CONTRIBUTING.md": true}

// skippedDirs are never searched for build files: what is in them is
// somebody else's project.
var skippedDirs = map[string]bool{"node_modules": true, "vendor": true, "third_party": true, ".git": true}

// Gather reads the facts of branch in the repository at repo, registered in
// project as repoName.
func Gather(ctx context.Context, git gitx.Git, project, repoName, repo, branch string) (Facts, error) {
	f := Facts{Project: project, RepoName: repoName, Repo: repo, DefaultBranch: branch}
	ref := "refs/heads/" + branch
	has, err := git.RevisionExists(ctx, repo, ref)
	if err != nil {
		return f, err
	}
	if !has {
		return f, nil
	}
	f.HasCommit = true
	if f.Head, err = git.ShortCommit(ctx, repo, ref); err != nil {
		return f, err
	}
	if f.Commits, err = git.CommitCount(ctx, repo, ref); err != nil {
		return f, err
	}
	top, err := git.TopLevelEntries(ctx, repo, ref)
	if err != nil {
		return f, err
	}
	for _, e := range top {
		name := e.Name
		if e.IsDir() {
			name += "/"
		} else if docNames[e.Name] {
			f.Docs = append(f.Docs, e.Name)
		}
		f.TopLevel = append(f.TopLevel, name)
	}
	paths, err := git.TreePaths(ctx, repo, ref)
	if err != nil {
		return f, err
	}
	f.Files = len(paths)
	for _, p := range paths {
		if isBuildFile(p) {
			f.BuildFiles = append(f.BuildFiles, p)
		}
	}
	sort.Strings(f.TopLevel)
	sort.Strings(f.BuildFiles)
	sort.Strings(f.Docs)
	return f, nil
}

func isBuildFile(p string) bool {
	parts := strings.Split(p, "/")
	if len(parts) > MaxDepth {
		return false
	}
	for _, dir := range parts[:len(parts)-1] {
		if skippedDirs[dir] {
			return false
		}
	}
	base := path.Base(p)
	if buildFileNames[base] {
		return true
	}
	for _, prefix := range buildFilePrefixes {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}

// NoHead is the head line of a branch with no commit, and the anchor a
// PROJECT.md fact recorded then carries (`<repo>:main@none`).
const NoHead = "none"

// Anchor is the anchor a PROJECT.md repo-state line carries for a fact
// about repo: `<repo>:<branch>@<head>`, or `@none` for an empty head
// (docs/mvp.md M9).
func Anchor(repo, branch, head string) string {
	if head == "" {
		head = NoHead
	}
	return repo + ":" + branch + "@" + head
}

// Anchor is the anchor a fact recorded now about this repo carries.
func (f Facts) Anchor() string {
	return Anchor(f.RepoName, f.DefaultBranch, f.Head)
}

// NoRepoLines is what `project facts` prints for a project without a repo:
// there is nothing to gather, and nothing a crew could work in.
func NoRepoLines(project string) []string {
	return []string{
		"repos: none",
		fmt.Sprintf("hint: project %s has no repo yet, so no crew can be spawned; add one with `mate project repo add %s <repo-path>`", project, project),
	}
}

// maxListed bounds each list line: past it the line ends in `+N more`.
const maxListed = 30

// Lines renders the facts as the fixed block `project facts` prints for
// one repo. The head line spells out the anchor a fact recorded now carries.
func (f Facts) Lines() []string {
	lines := []string{fmt.Sprintf("%s: repo %s at %s, default branch %s", f.Project, f.RepoName, f.Repo, f.DefaultBranch)}
	if !f.HasCommit {
		lines = append(lines,
			fmt.Sprintf("commits: 0 (%s has no commit yet)", f.DefaultBranch),
			fmt.Sprintf("head: %s (anchor %s)", NoHead, f.Anchor()),
			"tree: empty",
		)
	} else {
		lines = append(lines, fmt.Sprintf("commits: %d on %s", f.Commits, f.DefaultBranch), fmt.Sprintf("head: %s (anchor %s)", f.Head, f.Anchor()))
		if f.Files == 0 {
			lines = append(lines, "tree: empty")
		} else {
			lines = append(lines,
				fmt.Sprintf("tree: %d file(s)", f.Files),
				"top level: "+list(f.TopLevel),
				"build/test files: "+list(f.BuildFiles),
				"docs: "+list(f.Docs),
			)
		}
	}
	return append(lines, fmt.Sprintf("note: names and counts from the committed tree of %s only; no file was opened, and uncommitted changes in the checkout are not seen", f.DefaultBranch))
}

func list(names []string) string {
	if len(names) == 0 {
		return "none recognised"
	}
	shown := names
	more := 0
	if len(shown) > maxListed {
		shown, more = shown[:maxListed], len(shown)-maxListed
	}
	out := make([]string, 0, len(shown)+1)
	for _, n := range shown {
		if strings.ContainsAny(n, " \t\"") {
			n = strconv.Quote(n)
		}
		out = append(out, n)
	}
	if more > 0 {
		out = append(out, fmt.Sprintf("+%d more", more))
	}
	return strings.Join(out, " ")
}
