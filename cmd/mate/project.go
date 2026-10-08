package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/facts"
	"github.com/nguyenngocanh94/mate/internal/github"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdProjectAdd implements `mate project add <name> [<repo-path>]`. A
// project may start without a repo (docs/mvp.md M9); repos are added and
// removed later with `mate project repo`.
func cmdProjectAdd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate project add <name> [<repo-path>] [--repo-name <name>] [--default-branch <branch>] [--workspace <dir>] [--budget-usd <amount>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	repoNameFlag := fs.String("repo-name", "", "name of the repo inside the project (default: derived from its directory)")
	defaultBranchFlag := fs.String("default-branch", "", "override the repo's detected default branch")
	budgetUSDFlag := fs.Float64("budget-usd", 0, "open a budget incident once this project's total cost crosses this amount; hand-edit project.yaml for crew_tokens/crew_usd")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fs.Usage()
		return newUsageError("mate project add: want 1 or 2 arguments: <name> [<repo-path>]")
	}
	name := fs.Arg(0)
	if fs.NArg() == 1 && (*repoNameFlag != "" || *defaultBranchFlag != "") {
		fs.Usage()
		return newUsageError("mate project add: --repo-name and --default-branch describe a repo; give its <repo-path>")
	}

	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	opts := projectAddOptions{BudgetUSD: *budgetUSDFlag}
	if fs.NArg() == 2 {
		repoPath := fs.Arg(1)
		absRepo, err := filepath.Abs(repoPath)
		if err != nil {
			return err
		}
		repo, err := repoConfigFor(absRepo, repoPath, *repoNameFlag, *defaultBranchFlag)
		if err != nil {
			return err
		}
		opts.Repos = []store.RepoConfig{repo}
	}
	saved, err := addProject(w, name, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "added project %s: %s\n", name, describeRepos(name, saved.Repos))
	return nil
}

// describeRepos is the repos part of a one-line project summary.
func describeRepos(project string, repos []store.RepoConfig) string {
	if len(repos) == 0 {
		return fmt.Sprintf("no repo yet (add one with `mate project repo add %s <repo-path>`)", project)
	}
	parts := make([]string, 0, len(repos))
	for _, r := range repos {
		parts = append(parts, describeRepo(r))
	}
	return "repos=" + strings.Join(parts, ",")
}

// describeRepo is `<name>(<path>@<default-branch>)`.
func describeRepo(r store.RepoConfig) string {
	return fmt.Sprintf("%s(%s@%s)", r.Name, r.Path, r.DefaultBranch)
}

// projectAddOptions are the settings `mate project add` takes beyond the
// name. The Console's new-project form passes only Repos.
type projectAddOptions struct {
	Repos     []store.RepoConfig // zero or more, each from repoConfigFor
	BudgetUSD float64
}

// repoConfigFor checks a repo the one way both the CLI and the Console do
// it - absRepo must be an existing directory that is the root of its git
// work tree - and fills its default branch from the repo when none is
// given. The store later proves the path is inside the workspace and owned
// by no other project. shownRepo is the path as the caller typed it, for
// error messages.
func repoConfigFor(absRepo, shownRepo, name, defaultBranch string) (store.RepoConfig, error) {
	fi, err := os.Stat(absRepo)
	if err != nil {
		return store.RepoConfig{}, fmt.Errorf("repo path %s: %w", shownRepo, err)
	}
	if !fi.IsDir() {
		return store.RepoConfig{}, fmt.Errorf("repo path %s is not a directory", shownRepo)
	}
	top, err := gitTopLevel(absRepo)
	if err != nil {
		return store.RepoConfig{}, fmt.Errorf("repo path %s is not a git repository: %w", shownRepo, err)
	}
	resolvedRepo, err := filepath.EvalSymlinks(absRepo)
	if err != nil {
		return store.RepoConfig{}, err
	}
	resolvedTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		return store.RepoConfig{}, err
	}
	if resolvedRepo != resolvedTop {
		return store.RepoConfig{}, fmt.Errorf("repo path %s is not the root of its git work tree (root is %s)", shownRepo, top)
	}
	if defaultBranch == "" {
		defaultBranch = detectDefaultBranch(absRepo, store.DefaultBranch)
	}
	return store.RepoConfig{Name: name, Path: absRepo, DefaultBranch: defaultBranch}, nil
}

// addProject registers a Project the one way both the CLI and the Console
// do it, and seeds its PROJECT.md.
func addProject(w *store.Workspace, name string, opts projectAddOptions) (store.ProjectConfig, error) {
	cfg := store.ProjectConfig{Repos: opts.Repos}
	if opts.BudgetUSD > 0 {
		cfg.Budget = &store.BudgetConfig{ProjectUSD: opts.BudgetUSD}
	}
	if err := w.AddProject(name, cfg); err != nil {
		return store.ProjectConfig{}, fmt.Errorf("project add %s: %w", name, err)
	}
	if err := ensureProjectDoc(w, name); err != nil {
		return store.ProjectConfig{}, fmt.Errorf("project add %s: %w", name, err)
	}
	return w.LoadProject(name)
}

// ghClient is the `gh` that `mate pr watch` reads pull requests with; tests
// swap it, so nothing reaches the network or a real account.
var ghClient = github.New()

// cmdProjectList implements `mate project list`.
func cmdProjectList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate project list [--workspace <dir>] [--json]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	jsonFlag := fs.Bool("json", false, "print a JSON array instead of a table")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return newUsageError("mate project list: takes no positional arguments")
	}

	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}

	refs := w.Projects()
	rows := make([]projectRow, 0, len(refs))
	for _, ref := range refs {
		cfg, err := w.LoadProject(ref.Name)
		if err != nil {
			return fmt.Errorf("project list: %s: %w", ref.Name, err)
		}
		repos := make([]repoRow, 0, len(cfg.Repos))
		for _, r := range cfg.Repos {
			repos = append(repos, repoRow(r))
		}
		rows = append(rows, projectRow{Name: ref.Name, Repos: repos})
	}

	if *jsonFlag {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	printProjectTable(stdout, rows)
	return nil
}

// cmdProjectRemove implements `mate project remove <name>`: it stops the
// project's crews and Mate, then drops it from workspace.yaml (docs/mvp.md
// task 75). Everything on disk stays, so `mate project add <name>` brings
// the history back.
func cmdProjectRemove(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate project remove <name> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate project remove: want exactly 1 argument: <name>")
	}
	name := fs.Arg(0)

	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	return projectRemove(context.Background(), w, spawn.LiveDeps(harnesses), name, spawn.CallerFromEnv(), stdout, stderr)
}

// projectRemove is cmdProjectRemove's core, over any deps, for tests.
func projectRemove(ctx context.Context, w *store.Workspace, deps spawn.Deps, name, caller string, stdout, stderr io.Writer) error {
	res, err := spawn.RemoveProject(ctx, w, deps, name, removeProjectOptions(w, deps, caller, stderr, nil))
	for _, s := range res.Stopped {
		fmt.Fprintln(stdout, stoppedAgentLine(name, s))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed project %s\n", name)
	return nil
}

// removeProjectOptions is how the CLI and the Console stop a project's Mate
// for a removal: with the stow `mate mate stop` runs.
func removeProjectOptions(w *store.Workspace, deps spawn.Deps, caller string, stderr io.Writer, progress func(string)) spawn.RemoveProjectOptions {
	return spawn.RemoveProjectOptions{
		Progress: progress,
		Stow: func(ctx context.Context, project string) error {
			_, err := stowBeforeStop(ctx, w, deps, project, caller, false, stderr)
			return err
		},
	}
}

// stoppedAgentLine is the one line a removal prints per agent it stopped.
func stoppedAgentLine(project string, s spawn.StoppedAgent) string {
	switch {
	case s.Cleared && s.Crew != "":
		return fmt.Sprintf("%s/%s: not listed by Herdr; run meta cleared", project, s.Crew)
	case s.Crew != "":
		return crewStopReport(project, s.Crew, s.Stop)
	case s.Stop.AlreadyGone:
		return fmt.Sprintf("%s: Mate already gone (agent %s)", project, s.Stop.Agent)
	}
	return fmt.Sprintf("%s: Mate stopped (agent %s, session_id kept for resume)", project, s.Stop.Agent)
}

// projectRow is one row of `mate project list`, in both the table and the
// --json output.
type projectRow struct {
	Name  string    `json:"name"`
	Repos []repoRow `json:"repos"`
}

// repoRow is one repo of a project in `project list --json` and `project
// repo list --json`.
type repoRow struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	DefaultBranch string `json:"default_branch"`
}

// printProjectTable writes an aligned plain-text table: one header row plus
// one row per project, columns padded to the widest cell.
func printProjectTable(w io.Writer, rows []projectRow) {
	headers := []string{"NAME", "REPOS"}
	table := make([][]string, 0, len(rows)+1)
	table = append(table, headers)
	for _, r := range rows {
		names := make([]string, 0, len(r.Repos))
		for _, repo := range r.Repos {
			names = append(names, repo.Name)
		}
		repos := strings.Join(names, ",")
		if repos == "" {
			repos = "-"
		}
		table = append(table, []string{r.Name, repos})
	}
	printTable(w, table)
}

// printTable writes an aligned plain-text table, columns padded to the
// widest cell and the last column unpadded.
func printTable(w io.Writer, table [][]string) {
	if len(table) == 0 {
		return
	}
	headers := table[0]

	widths := make([]int, len(headers))
	for _, row := range table {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	for _, row := range table {
		for i, cell := range row {
			if i > 0 {
				fmt.Fprint(w, "  ")
			}
			if i == len(row)-1 {
				fmt.Fprint(w, cell)
				continue
			}
			fmt.Fprintf(w, "%-*s", widths[i], cell)
		}
		fmt.Fprintln(w)
	}
}

// ensureProjectDoc creates projects/<name>/PROJECT.md from a short template
// if it does not already exist. It never overwrites a file the user or a
// crew scout has since edited.
func ensureProjectDoc(w *store.Workspace, name string) error {
	path := w.ProjectDoc(name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	_, err = f.WriteString(projectDocTemplate(name))
	return err
}

func projectDocTemplate(name string) string {
	return memory.ProjectTemplate(name)
}

// cmdProjectFacts implements `mate project facts <project>`: what the
// project's repository holds on its default branch, from git's metadata
// only - commit count, file count, top-level names, build/test files by
// name - so the Mate can tell an empty repository from a full one without
// reading it (docs/mvp.md decision 1, M7). No file is opened.
func cmdProjectFacts(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project facts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate project facts <project> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate project facts: want exactly 1 argument: <project>")
	}
	name := fs.Arg(0)
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if _, ok := w.Project(name); !ok {
		return fmt.Errorf("%w: %s", store.ErrNoProject, name)
	}
	cfg, err := w.LoadProject(name)
	if err != nil {
		return err
	}
	lines, err := projectFactsLines(context.Background(), w, gitx.New(), name, cfg, false)
	if err != nil {
		return fmt.Errorf("project facts %s: %w", name, err)
	}
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	return nil
}

// projectFactsLines is `project facts` over every repo of the project: one
// block per repo in the project's order, a blank line between blocks, or
// facts.NoRepoLines for a project with none (docs/mvp.md M9). With
// keepGoing, a repo git cannot read prints its failure in its block's place
// and the other blocks still print; without it the first failure is
// returned and nothing is.
func projectFactsLines(ctx context.Context, w *store.Workspace, git gitx.Git, project string, cfg store.ProjectConfig, keepGoing bool) ([]string, error) {
	if len(cfg.Repos) == 0 {
		return facts.NoRepoLines(project), nil
	}
	var lines []string
	for i, r := range cfg.Repos {
		if i > 0 {
			lines = append(lines, "")
		}
		f, err := facts.Gather(ctx, git, project, r.Name, w.RepoDir(r.Path), r.DefaultBranch)
		if err != nil {
			if !keepGoing {
				return nil, fmt.Errorf("repo %s: %w", r.Name, err)
			}
			lines = append(lines, fmt.Sprintf("%s: repo %s at %s: project facts failed: %v", project, r.Name, w.RepoDir(r.Path), err))
			continue
		}
		lines = append(lines, f.Lines()...)
	}
	return lines, nil
}
