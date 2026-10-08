package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
)

// cmdProjectRepo dispatches `mate project repo <add|list|remove>`: a
// project's repos, zero or more (docs/mvp.md M9).
func cmdProjectRepo(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate project repo <add|list|remove> <project> ...")
	}
	switch args[0] {
	case "add":
		return cmdProjectRepoAdd(args[1:], stdout, stderr)
	case "list":
		return cmdProjectRepoList(args[1:], stdout, stderr)
	case "remove":
		return cmdProjectRepoRemove(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown project repo subcommand %q", args[0])
	}
}

// cmdProjectRepoAdd implements `mate project repo add <project> <repo-path|git-url>`.
func cmdProjectRepoAdd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project repo add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate project repo add <project> <repo-path|git-url> [--name <name>] [--default-branch <branch>] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	nameFlag := fs.String("name", "", "name of the repo inside the project (default: derived from its directory)")
	defaultBranchFlag := fs.String("default-branch", "", "override the repo's detected default branch")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate project repo add: want exactly 2 arguments: <project> <repo-path>")
	}
	project, repoPath := fs.Arg(0), fs.Arg(1)
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if err := requireProject(w, project); err != nil {
		return err
	}
	shown := repoPath
	if isGitURL(repoPath) {
		cloned, err := cloneRepo(stdout, w.ProjectHome(project), repoPath, *nameFlag)
		if err != nil {
			return fmt.Errorf("project repo add %s: %w", project, err)
		}
		repoPath, shown = cloned, filepath.Base(cloned)
	}
	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return err
	}
	repo, err := repoConfigFor(absRepo, shown, *nameFlag, *defaultBranchFlag)
	if err != nil {
		return err
	}
	// Refused before the empty first commit: a repo mate will not register
	// is left exactly as it was.
	if err := w.CheckAddRepo(project, repo); err != nil {
		return fmt.Errorf("project repo add %s: %w", project, err)
	}
	made, err := ensureFirstCommit(absRepo, repo.DefaultBranch)
	if err != nil {
		return fmt.Errorf("project repo add %s: %w", project, err)
	}
	if made {
		fmt.Fprintf(stdout, "%s had no commit; made an empty first commit on %s so crews can branch from it (nothing was pushed)\n", shown, repo.DefaultBranch)
	}
	added, err := w.AddRepo(project, repo)
	if err != nil {
		return fmt.Errorf("project repo add %s: %w", project, err)
	}
	fmt.Fprintf(stdout, "added repo %s to project %s: path=%s default-branch=%s\n", added.Name, project, added.Path, added.DefaultBranch)
	return nil
}

// cmdProjectRepoList implements `mate project repo list <project>`.
func cmdProjectRepoList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project repo list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate project repo list <project> [--workspace <dir>] [--json]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	jsonFlag := fs.Bool("json", false, "print a JSON array instead of a table")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate project repo list: want exactly 1 argument: <project>")
	}
	project := fs.Arg(0)
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if err := requireProject(w, project); err != nil {
		return err
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return err
	}
	rows := make([]repoRow, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		rows = append(rows, repoRow(r))
	}
	if *jsonFlag {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	if len(rows) == 0 {
		fmt.Fprintf(stdout, "project %s has no repo yet; add one with `mate project repo add %s <repo-path>`\n", project, project)
		return nil
	}
	table := [][]string{{"NAME", "PATH", "DEFAULT BRANCH"}}
	for _, r := range rows {
		table = append(table, []string{r.Name, r.Path, r.DefaultBranch})
	}
	printTable(stdout, table)
	return nil
}

// cmdProjectRepoRemove implements `mate project repo remove <project> <name>`.
// The repository directory is never touched.
func cmdProjectRepoRemove(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project repo remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate project repo remove <project> <name> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate project repo remove: want exactly 2 arguments: <project> <name>")
	}
	project, name := fs.Arg(0), fs.Arg(1)
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if err := requireProject(w, project); err != nil {
		return err
	}
	if err := w.RemoveRepo(project, name); err != nil {
		return fmt.Errorf("project repo remove %s: %w", project, err)
	}
	fmt.Fprintf(stdout, "removed repo %s from project %s; the repository itself was not touched\n", name, project)
	return nil
}
