package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdRemember implements `mate remember <project> --captain|--lesson
// [--perishable "<expiry>"] --source <src> "<one line>"` (docs/mvp.md M8
// task 36, B5): one entry of the B3 shape, dated today, at the end of its
// section of `mate/memory.md`. It never merges or deletes; curating is the
// Mate's own edit (skill stow).
func cmdRemember(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("remember", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: mate remember <project> --captain|--lesson [--perishable "<expiry condition>"] --source <src> "<one line>" [--workspace <dir>]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	captainFlag := fs.Bool("captain", false, "a captain preference, working style or authority boundary (## Captain, never expires)")
	lessonFlag := fs.Bool("lesson", false, "an operational lesson about Crews, harnesses or the app here (## Lessons, aging)")
	perishableFlag := fs.String("perishable", "", "with --lesson: the checkable condition under which the lesson expires")
	sourceFlag := fs.String("source", "", "where it came from: the captain and a date, or a path relative to the project directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError(`mate remember: want exactly 2 arguments: <project> "<one line>"`)
	}
	if *captainFlag == *lessonFlag {
		fs.Usage()
		return newUsageError("mate remember: give exactly one of --captain and --lesson")
	}
	section := memory.LessonsSection
	if *captainFlag {
		section = memory.CaptainSection
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	res, err := remember(w, fs.Arg(0), section, fs.Arg(1), *sourceFlag, *perishableFlag, time.Now())
	if err != nil {
		var ue *rememberRefusal
		if errors.As(err, &ue) {
			return &usageError{err}
		}
		return err
	}
	fmt.Fprintf(stdout, "remembered in %s under ## %s: %s\n", w.MemoryFile(fs.Arg(0)), section, res.Line)
	if res.Budget.Over() {
		fmt.Fprintln(stdout, res.Budget.OverLine())
	}
	return nil
}

// rememberRefusal is a shape the entry cannot have: a usage mistake, exit 2.
type rememberRefusal struct{ err error }

func (e *rememberRefusal) Error() string { return "mate remember: " + e.err.Error() }
func (e *rememberRefusal) Unwrap() error { return e.err }

type rememberResult struct {
	Line   string
	Budget memoryBudget
}

// remember is cmdRemember's core, separate from flag parsing for tests.
func remember(w *store.Workspace, project, section, text, source, expiry string, now time.Time) (rememberResult, error) {
	if err := requireProject(w, project); err != nil {
		return rememberResult{}, err
	}
	e, err := memory.NewEntry(section, text, source, expiry, now)
	if err != nil {
		return rememberResult{}, &rememberRefusal{err}
	}
	if err := memory.CheckSourcePaths(e.Source, w.Root(), w.ProjectDir(project)); err != nil {
		return rememberResult{}, &rememberRefusal{fmt.Errorf("--source %w", err)}
	}
	current, err := readOptional(w.MemoryFile(project))
	if err != nil {
		return rememberResult{}, err
	}
	if err := w.ReplaceMemoryFile(project, []byte(memory.Append(current, section, e))); err != nil {
		return rememberResult{}, err
	}
	budget, err := measureMemory(w, project)
	return rememberResult{Line: e.Line(), Budget: budget}, err
}

func requireProject(w *store.Workspace, project string) error {
	if err := store.ValidateProjectName(project); err != nil {
		return err
	}
	if _, ok := w.Project(project); !ok {
		return fmt.Errorf("%w: %s", store.ErrNoProject, project)
	}
	return nil
}

// readOptional reads path, and an absent file as empty.
func readOptional(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

// memoryBudget is the estimated-token cost of the three files every
// session reads, against memory.BudgetTokens.
type memoryBudget struct {
	Files  []budgetFile
	Total  int
	Budget int
}

type budgetFile struct {
	Name   string
	Tokens int
	Absent bool
}

func (b memoryBudget) Over() bool { return b.Total > b.Budget }

// Line is the budget line `memory check` always prints.
func (b memoryBudget) Line() string {
	parts := make([]string, 0, len(b.Files))
	for _, f := range b.Files {
		if f.Absent {
			parts = append(parts, f.Name+" absent")
			continue
		}
		parts = append(parts, f.Name+" "+strconv.Itoa(f.Tokens))
	}
	return fmt.Sprintf("budget: %d of %d estimated tokens (%s)", b.Total, b.Budget, strings.Join(parts, ", "))
}

// OverLine is the problem an over-budget total is.
func (b memoryBudget) OverLine() string {
	return fmt.Sprintf("budget: %d of %d estimated tokens, over by %d; curate with skill stow (archive what is stale, merge duplicates, move the rest to memory-archive.md) until it fits",
		b.Total, b.Budget, b.Total-b.Budget)
}

func measureMemory(w *store.Workspace, project string) (memoryBudget, error) {
	b := memoryBudget{Budget: memory.BudgetTokens}
	for _, f := range []struct{ name, path string }{
		{memory.FileName, w.MemoryFile(project)},
		{memory.ProjectFileName, w.ProjectDoc(project)},
		{"WORKSPACE.md", w.WorkspaceDoc()},
	} {
		data, err := os.ReadFile(f.path)
		if errors.Is(err, os.ErrNotExist) {
			b.Files = append(b.Files, budgetFile{Name: f.name, Absent: true})
			continue
		}
		if err != nil {
			return b, err
		}
		n := memory.EstimateTokens(len(data))
		b.Files = append(b.Files, budgetFile{Name: f.name, Tokens: n})
		b.Total += n
	}
	return b, nil
}

// cmdMemory dispatches `mate memory <check>`.
func cmdMemory(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate memory check <project> [--workspace <dir>]")
	}
	switch args[0] {
	case "check":
		return cmdMemoryCheck(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown memory subcommand %q", args[0])
	}
}

// cmdMemoryCheck implements `mate memory check <project>`: the shape gate
// over memory.md and PROJECT.md's repo-state lines, and the budget, in the
// manner of `brief check`: one line per problem on stderr, exit 1 on any. It
// judges shape and age, never meaning.
func cmdMemoryCheck(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("memory check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate memory check <project> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate memory check: want exactly 1 argument: <project>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	rep, err := memoryCheck(context.Background(), w, gitx.New(), fs.Arg(0), time.Now())
	if err != nil {
		return err
	}
	return writeMemoryCheck(stdout, stderr, fs.Arg(0), rep)
}

// memoryReport is what memoryCheck found.
type memoryReport struct {
	Budget   memoryBudget
	Problems []string
	Warnings []string
	Captain  int
	Lessons  int
	Anchored int
}

// memoryCheck is cmdMemoryCheck's core, over a real git for the anchors.
func memoryCheck(ctx context.Context, w *store.Workspace, git gitx.Git, project string, now time.Time) (memoryReport, error) {
	var rep memoryReport
	if err := requireProject(w, project); err != nil {
		return rep, err
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return rep, err
	}
	text, err := readOptional(w.MemoryFile(project))
	if err != nil {
		return rep, err
	}
	entries, problems := memory.Check(text, now, w.Root(), w.ProjectDir(project))
	for _, e := range entries {
		if e.Section == memory.CaptainSection {
			rep.Captain++
		} else {
			rep.Lessons++
		}
	}
	doc, err := readOptional(w.ProjectDoc(project))
	if err != nil {
		return rep, err
	}
	anchors, projectProblems := memory.CheckProject(doc, anchorRepos(cfg))
	rep.Anchored = len(anchors)
	for _, p := range append(problems, projectProblems...) {
		rep.Problems = append(rep.Problems, p.String())
	}
	if rep.Budget, err = measureMemory(w, project); err != nil {
		return rep, err
	}
	if rep.Budget.Over() {
		rep.Problems = append(rep.Problems, rep.Budget.OverLine())
	}
	for _, r := range cfg.Repos {
		warnings, err := anchorWarnings(ctx, git, w.RepoDir(r.Path), r, anchorsIn(anchors, r.Name))
		if err != nil {
			return rep, fmt.Errorf("comparing %s's anchors with repo %s: %w", memory.ProjectFileName, r.Name, err)
		}
		rep.Warnings = append(rep.Warnings, warnings...)
	}
	return rep, nil
}

// anchorRepos are the repos a PROJECT.md anchor may name, in the
// project's order.
func anchorRepos(cfg store.ProjectConfig) []memory.Repo {
	out := make([]memory.Repo, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		out = append(out, memory.Repo{Name: r.Name, Branch: r.DefaultBranch})
	}
	return out
}

// anchorsIn are the anchors that name repo.
func anchorsIn(anchors []memory.Anchor, repo string) []memory.Anchor {
	var out []memory.Anchor
	for _, a := range anchors {
		if a.Repo == repo {
			out = append(out, a)
		}
	}
	return out
}

// anchorWarnings says which PROJECT.md anchors on one repo are older than
// its default branch's head; repo is that repository's directory. An old
// anchor is not an error: the fact may still hold, and only a Crew can say.
// It is a hint that the line is a hint.
func anchorWarnings(ctx context.Context, git gitx.Git, repo string, r store.RepoConfig, anchors []memory.Anchor) ([]string, error) {
	if len(anchors) == 0 {
		return nil, nil
	}
	branch := r.DefaultBranch
	label := r.Name + ":" + branch
	ref := "refs/heads/" + branch
	hasHead, err := git.RevisionExists(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	head := memory.NoCommit
	if hasHead {
		if head, err = git.HeadCommit(ctx, repo, ref); err != nil {
			return nil, err
		}
	}
	bySHA := map[string][]int{}
	var order []string
	for _, a := range anchors {
		if _, seen := bySHA[a.SHA]; !seen {
			order = append(order, a.SHA)
		}
		bySHA[a.SHA] = append(bySHA[a.SHA], a.Line)
	}
	sort.Strings(order)
	var out []string
	for _, sha := range order {
		var why string
		switch {
		case sha == memory.NoCommit && !hasHead, hasHead && sha != memory.NoCommit && strings.HasPrefix(head, sha):
			continue
		case !hasHead:
			why = fmt.Sprintf("%s has no commit now", label)
		case sha == memory.NoCommit:
			n, err := git.CommitCount(ctx, repo, ref)
			if err != nil {
				return nil, err
			}
			why = fmt.Sprintf("%s has %d commit(s) since", label, n)
		default:
			n, err := git.CommitCount(ctx, repo, sha+".."+ref)
			switch {
			case err != nil:
				why = fmt.Sprintf("%s's history does not contain %s", label, sha)
			case n == 0:
				why = fmt.Sprintf("%s is not %s's head", sha, label)
			default:
				why = fmt.Sprintf("%s has %d newer commit(s)", label, n)
			}
		}
		out = append(out, fmt.Sprintf("warning: %s %s anchored at %s@%s, and %s; treat %s as a hint until a report or hand-back confirms it",
			memory.ProjectFileName, lineList(bySHA[sha]), label, sha, why, itThem(len(bySHA[sha]))))
	}
	return out, nil
}

func lineList(lines []int) string {
	s := make([]string, len(lines))
	for i, n := range lines {
		s[i] = strconv.Itoa(n)
	}
	if len(lines) == 1 {
		return "line " + s[0] + " is"
	}
	return "lines " + strings.Join(s, ", ") + " are"
}

func itThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// writeMemoryCheck prints the report: the budget line and the verdict on
// stdout, every warning and problem on stderr.
func writeMemoryCheck(stdout, stderr io.Writer, project string, rep memoryReport) error {
	fmt.Fprintln(stdout, rep.Budget.Line())
	for _, w := range rep.Warnings {
		fmt.Fprintln(stderr, w)
	}
	for _, p := range rep.Problems {
		fmt.Fprintln(stderr, p)
	}
	if len(rep.Problems) > 0 {
		return fmt.Errorf("memory check: %d problem(s) in %s's memory", len(rep.Problems), project)
	}
	fmt.Fprintf(stdout, "memory ok: %d captain entr%s, %d lesson(s), %d anchored %s line(s)\n",
		rep.Captain, plural(rep.Captain, "y", "ies"), rep.Lessons, rep.Anchored, memory.ProjectFileName)
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
