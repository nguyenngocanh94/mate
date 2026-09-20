package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

// cmdProjectAdd implements `matev2 project add <name> <repo-path>`.
func cmdProjectAdd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: matev2 project add <name> <repo-path> [--workspace <dir>] [--default-branch <branch>] [--mode local-only] [--yolo] [--budget-usd <amount>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	defaultBranchFlag := fs.String("default-branch", "", "override the detected default branch")
	modeFlag := fs.String("mode", store.ModeLocalOnly, "project mode (only local-only is supported)")
	yoloFlag := fs.Bool("yolo", false, "let Mate merge without asking the user")
	budgetUSDFlag := fs.Float64("budget-usd", 0, "open a budget incident once this project's total cost crosses this amount; hand-edit project.yaml for crew_tokens/crew_usd")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("matev2 project add: want exactly 2 arguments: <name> <repo-path>")
	}
	name := fs.Arg(0)
	repoPath := fs.Arg(1)

	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}

	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return err
	}
	fi, err := os.Stat(absRepo)
	if err != nil {
		return fmt.Errorf("repo path %s: %w", repoPath, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("repo path %s is not a directory", repoPath)
	}

	top, err := gitTopLevel(absRepo)
	if err != nil {
		return fmt.Errorf("repo path %s is not a git repository: %w", repoPath, err)
	}
	resolvedRepo, err := filepath.EvalSymlinks(absRepo)
	if err != nil {
		return err
	}
	resolvedTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		return err
	}
	if resolvedRepo != resolvedTop {
		return fmt.Errorf("repo path %s is not the root of its git work tree (root is %s)", repoPath, top)
	}

	branch := *defaultBranchFlag
	if branch == "" {
		branch = detectDefaultBranch(absRepo, store.DefaultBranch)
	}

	cfg := store.ProjectConfig{
		Repo:          absRepo,
		DefaultBranch: branch,
		Mode:          *modeFlag,
		Yolo:          *yoloFlag,
	}
	if *budgetUSDFlag > 0 {
		cfg.Budget = &store.BudgetConfig{ProjectUSD: *budgetUSDFlag}
	}
	if err := w.AddProject(name, cfg); err != nil {
		return fmt.Errorf("project add %s: %w", name, err)
	}
	if err := ensureProjectDoc(w, name); err != nil {
		return fmt.Errorf("project add %s: %w", name, err)
	}

	saved, err := w.LoadProject(name)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "added project %s: repo=%s default-branch=%s mode=%s yolo=%t\n",
		name, saved.Repo, saved.DefaultBranch, saved.Mode, saved.Yolo)
	return nil
}

// cmdProjectList implements `matev2 project list`.
func cmdProjectList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: matev2 project list [--workspace <dir>] [--json]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	jsonFlag := fs.Bool("json", false, "print a JSON array instead of a table")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return newUsageError("matev2 project list: takes no positional arguments")
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
		rows = append(rows, projectRow{
			Name:          ref.Name,
			Repo:          cfg.Repo,
			DefaultBranch: cfg.DefaultBranch,
			Mode:          cfg.Mode,
			Yolo:          cfg.Yolo,
		})
	}

	if *jsonFlag {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	printProjectTable(stdout, rows)
	return nil
}

// cmdProjectRemove implements `matev2 project remove <name>`.
func cmdProjectRemove(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: matev2 project remove <name> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("matev2 project remove: want exactly 1 argument: <name>")
	}
	name := fs.Arg(0)

	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if err := w.RemoveProject(name); err != nil {
		return fmt.Errorf("project remove %s: %w", name, err)
	}
	fmt.Fprintf(stdout, "removed project %s\n", name)
	return nil
}

// cmdProjectYolo implements `matev2 project yolo <name> on|off`: the one
// switch that decides whether a Mate may run `matev2 merge` itself, or has
// to report the branch and wait for the captain (docs/mvp.md M4 decisions).
//
// It is its own subcommand rather than a flag on some edit command because
// it is the whole of what a reader wants to change, and because the answer
// they need back is not "saved" but when the Mate will believe it: the
// manual is rendered from `project.yaml` at every `mate start`, so a Mate
// that is already running is quoting the old value until it is restarted.
func cmdProjectYolo(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project yolo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: matev2 project yolo <name> on|off [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("matev2 project yolo: want exactly 2 arguments: <name> on|off")
	}
	name := fs.Arg(0)
	var on bool
	switch fs.Arg(1) {
	case "on":
		on = true
	case "off":
		on = false
	default:
		fs.Usage()
		return newUsageErrorf("matev2 project yolo: want on or off, got %q", fs.Arg(1))
	}

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
	was := cfg.Yolo
	cfg.Yolo = on
	if err := w.SaveProject(name, cfg); err != nil {
		return fmt.Errorf("project yolo %s: %w", name, err)
	}
	if was == on {
		fmt.Fprintf(stdout, "%s: yolo is already %t; nothing changed\n", name, on)
		return nil
	}
	if on {
		fmt.Fprintf(stdout, "%s: yolo is on; the Mate may run `matev2 merge %s <crew>` itself. A running Mate still reads the old value in its manual until its next restart.\n", name, name)
		return nil
	}
	fmt.Fprintf(stdout, "%s: yolo is off; the Mate reports the branch and the captain merges. A running Mate still reads the old value in its manual until its next restart.\n", name)
	return nil
}

// projectRow is one row of `matev2 project list`, in both the table and the
// --json output.
type projectRow struct {
	Name          string `json:"name"`
	Repo          string `json:"repo"`
	DefaultBranch string `json:"default_branch"`
	Mode          string `json:"mode"`
	Yolo          bool   `json:"yolo"`
}

// printProjectTable writes an aligned plain-text table: one header row plus
// one row per project, columns padded to the widest cell.
func printProjectTable(w io.Writer, rows []projectRow) {
	headers := []string{"NAME", "REPO", "DEFAULT BRANCH", "MODE", "YOLO"}
	table := make([][]string, 0, len(rows)+1)
	table = append(table, headers)
	for _, r := range rows {
		table = append(table, []string{r.Name, r.Repo, r.DefaultBranch, r.Mode, strconv.FormatBool(r.Yolo)})
	}

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
	return fmt.Sprintf("# %s\n\n## What this project is\n\n## How to work here\n", name)
}
