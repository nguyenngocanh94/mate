// Package gitx is the only place mate runs git. It is deliberately tiny:
// the worktree saga of `mate crew spawn` (docs/mvp.md task 11) is the
// single caller, and every command is argv over os/exec - never a shell, so
// a branch or path containing a space, a quote or a `$` cannot become
// another command.
//
// The runner is a port so the saga can be unit tested against scripted
// failures; the package's own tests run the real git against temp
// repositories, because a fake git proves nothing about git.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// Command is one git invocation: the directory git runs in and the argv
// after the program name. Dir is passed as `-C <dir>` rather than as the
// process working directory so a failure message can repeat it verbatim.
type Command struct {
	Dir  string
	Args []string
}

// Result is the observed outcome. A non-zero exit is a result, not an
// error: callers decide whether it means "no" (a probe) or "failed".
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Runner executes git. ExecRunner is the live one; tests pass a fake.
type Runner interface {
	Run(ctx context.Context, cmd Command) (Result, error)
}

// ExecRunner runs git through os/exec with no shell. Failing to start git
// (not installed, bad directory) is an error; a non-zero exit is a Result.
type ExecRunner struct {
	// Binary overrides the program name. Empty means "git" on PATH.
	Binary string
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, cmd Command) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	bin := r.Binary
	if bin == "" {
		bin = "git"
	}
	argv := make([]string, 0, len(cmd.Args)+2)
	if cmd.Dir != "" {
		argv = append(argv, "-C", cmd.Dir)
	}
	argv = append(argv, cmd.Args...)
	c := exec.CommandContext(ctx, bin, argv...)
	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{}, ctxErr
	}
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return Result{}, observability.WrapError(observability.CodeRuntimeUnavailable,
			fmt.Sprintf("git %s could not be run", strings.Join(argv, " ")), err)
	}
	return res, nil
}

// Git is the command surface the crew saga needs.
type Git struct {
	Runner Runner
}

// New returns a Git over the real git binary.
func New() Git { return Git{Runner: ExecRunner{}} }

func (g Git) runner() Runner {
	if g.Runner != nil {
		return g.Runner
	}
	return ExecRunner{}
}

// run executes one command and turns a non-zero exit into a coded error.
func (g Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := g.runner().Run(ctx, Command{Dir: dir, Args: args})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return res.Stdout, observability.NewError(observability.CodeUnknown,
			fmt.Sprintf("git %s failed in %s (exit %d): %s",
				strings.Join(args, " "), dir, res.ExitCode, firstLine(res.Stderr, res.Stdout))).
			WithDetails(map[string]any{
				"git_args":  strings.Join(args, " "),
				"git_dir":   dir,
				"exit_code": res.ExitCode,
			})
	}
	return res.Stdout, nil
}

// probe executes one command whose non-zero exit is an answer, not a
// failure. It reports whether the exit was zero.
func (g Git) probe(ctx context.Context, dir string, args ...string) (bool, error) {
	res, err := g.runner().Run(ctx, Command{Dir: dir, Args: args})
	if err != nil {
		return false, err
	}
	return res.ExitCode == 0, nil
}

// Toplevel is `git -C dir rev-parse --show-toplevel`: the root of the
// working tree dir belongs to. For a linked worktree it is that worktree,
// not the primary checkout - which is exactly what the tangle guard asks.
func (g Git) Toplevel(ctx context.Context, dir string) (string, error) {
	out, err := g.run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsRepo reports whether dir is inside a git working tree.
func (g Git) IsRepo(ctx context.Context, dir string) (bool, error) {
	return g.probe(ctx, dir, "rev-parse", "--is-inside-work-tree")
}

// BranchExists reports whether refs/heads/<branch> is present in repo.
func (g Git) BranchExists(ctx context.Context, repo, branch string) (bool, error) {
	return g.probe(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
}

// RevisionExists reports whether rev names something git can resolve to a
// commit. The crew saga uses it on the default branch before branching from
// it, so an empty repository is refused with its own message rather than
// git's.
func (g Git) RevisionExists(ctx context.Context, repo, rev string) (bool, error) {
	return g.probe(ctx, repo, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

// AddWorktree is `git -C repo worktree add -b <branch> <path> <base>`: a new
// linked worktree on a brand new branch. git itself refuses an existing
// branch or a non-empty path; the caller checks both first so the refusal
// names what mate would have done.
func (g Git) AddWorktree(ctx context.Context, repo, path, branch, base string) error {
	_, err := g.run(ctx, repo, "worktree", "add", "-b", branch, path, base)
	return err
}

// RemoveWorktree is `git -C repo worktree remove --force <path>`, followed
// by a prune so a directory a caller already deleted by hand does not leave
// an administrative entry behind. Both are best effort in compensation, so
// the first error is returned and the prune still runs.
func (g Git) RemoveWorktree(ctx context.Context, repo, path string) error {
	_, err := g.run(ctx, repo, "worktree", "remove", "--force", path)
	if _, pruneErr := g.run(ctx, repo, "worktree", "prune"); err == nil {
		err = pruneErr
	}
	return err
}

// DeleteBranch is `git -C repo branch -D <branch>`.
func (g Git) DeleteBranch(ctx context.Context, repo, branch string) error {
	_, err := g.run(ctx, repo, "branch", "-D", branch)
	return err
}

// GitPath resolves one of git's own files for a working tree, absolutely:
// `git -C dir rev-parse --path-format=absolute --git-path <name>`. The crew
// saga uses it for info/exclude, which git shares across a repo's worktrees.
func (g Git) GitPath(ctx context.Context, dir, name string) (string, error) {
	out, err := g.run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-path", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// HeadCommit is the commit `rev` resolves to.
func (g Git) HeadCommit(ctx context.Context, dir, rev string) (string, error) {
	out, err := g.run(ctx, dir, "rev-parse", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsAncestor reports whether branch's tip is already contained in base's
// history: `git merge-base --is-ancestor <branch> <base>`. True means
// branch carries no commit base does not already have, so a teardown may
// delete it without `--discard`. It is a probe: a non-zero exit (the normal
// "no" answer) is not an error.
func (g Git) IsAncestor(ctx context.Context, dir, branch, base string) (bool, error) {
	return g.probe(ctx, dir, "merge-base", "--is-ancestor", branch, base)
}

// AheadCount is the number of commits reachable from branch but not from
// base: `git rev-list --count <base>..<branch>`. It is the number a crew
// teardown refusal reports as "commits ahead".
func (g Git) AheadCount(ctx context.Context, dir, branch, base string) (int, error) {
	out, err := g.run(ctx, dir, "rev-list", "--count", base+".."+branch)
	if err != nil {
		return 0, err
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(out))
	if convErr != nil {
		return 0, observability.WrapError(observability.CodeUnknown,
			fmt.Sprintf("git rev-list --count did not print a number: %q", out), convErr)
	}
	return n, nil
}

// Diff is the review diff of one crew branch: `git -C dir diff
// <base>...<branch>`, with --stat when the caller wants the summary instead
// of the patch.
//
// Three dots, not two. `base...branch` is the branch measured against the
// merge base it actually grew from, so a default branch that moved on while
// the crew worked does not turn every unrelated commit on it into a deletion
// in the crew's diff. `base..branch` would print exactly that lie, and it is
// the one a reader reviewing a crew would believe.
//
// It is read-only: nothing here writes an index, a ref or a file.
func (g Git) Diff(ctx context.Context, dir, base, branch string, stat bool) (string, error) {
	args := []string{"diff"}
	if stat {
		args = append(args, "--stat")
	}
	return g.run(ctx, dir, append(args, base+"..."+branch)...)
}

// LogOneline is `git -C dir log --oneline <base>..<branch>`: the commits the
// branch carries that base does not, newest first, one line each.
//
// Two dots here where Diff uses three, and the difference is not an
// oversight. `log base..branch` is exactly "the commits on this branch",
// which is what a review list is; the three-dot form of log would also list
// base's own commits since the merge base, which belong to somebody else.
func (g Git) LogOneline(ctx context.Context, dir, base, branch string) (string, error) {
	return g.run(ctx, dir, "log", "--oneline", base+".."+branch)
}

// Log is `git -C dir log <revisions> <options...>`, read-only, for a caller
// that needs more of a commit than a review list carries: the timeline wants
// each commit's own time and the files it touched, and a `--oneline` summary
// has neither.
//
// The revision range is passed first and the options after it, because git
// accepts them in either order and putting the range first makes the call
// site read as the question it is asking.
func (g Git) Log(ctx context.Context, dir, revisions string, options ...string) (string, error) {
	args := append([]string{"log", revisions}, options...)
	return g.run(ctx, dir, args...)
}

// IsDirty is the number of entries `git -C worktree status --porcelain`
// lists: uncommitted changes a teardown would otherwise discard silently.
// 0 means the worktree is clean.
func (g Git) IsDirty(ctx context.Context, worktree string) (int, error) {
	out, err := g.run(ctx, worktree, "status", "--porcelain")
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimRight(out, "\n")
	if trimmed == "" {
		return 0, nil
	}
	return len(strings.Split(trimmed, "\n")), nil
}

// CurrentBranch is `git -C dir rev-parse --abbrev-ref HEAD`: the name of the
// branch dir has checked out, or the literal "HEAD" when the checkout is
// detached. The merge of docs/mvp.md task 22 asks it of the primary
// repository before touching anything, because `git merge --ff-only`
// advances whatever HEAD happens to be and a caller that did not look would
// move the wrong branch.
func (g Git) CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := g.run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// DetachedHead is what CurrentBranch reports for a detached checkout: git
// prints the word HEAD rather than a branch name.
const DetachedHead = "HEAD"

// MergeFFOnly is `git -C repo merge --ff-only <branch>` in the primary
// repository: the crew's branch is landed by moving the checked-out branch
// forward onto it, and nothing else. No commit is created, so a branch that
// is not a descendant of the checked-out one is refused by git itself rather
// than turned into a merge commit nobody asked for.
//
// It re-reads HEAD first and refuses a detached checkout, and refuses a
// repository that already has `branch` checked out - merging a branch into
// itself is a no-op git reports as success, which would let a caller print
// "merged" for a fast-forward that never happened. That the checked-out
// branch is the *project's* default branch is the caller's fact, not git's:
// spawn.MergeCrew establishes it with CurrentBranch and refuses by name, so
// the reason a merge did not happen is reported in the project's words.
func (g Git) MergeFFOnly(ctx context.Context, repo, branch string) error {
	head, err := g.CurrentBranch(ctx, repo)
	if err != nil {
		return err
	}
	if head == DetachedHead {
		return observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("%s has a detached HEAD; check out a branch before merging %s", repo, branch))
	}
	if head == branch {
		return observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("%s already has %s checked out; a branch cannot be fast-forwarded into itself", repo, branch))
	}
	_, err = g.run(ctx, repo, "merge", "--ff-only", branch)
	return err
}

// SamePath reports whether a and b name the same location, resolving
// symlinks when both sides exist. macOS /tmp is /private/tmp and git reports
// the resolved path, so the tangle guard cannot compare strings.
func SamePath(a, b string) bool {
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

func firstLine(candidates ...string) string {
	for _, c := range candidates {
		trimmed := strings.TrimSpace(c)
		if trimmed == "" {
			continue
		}
		if i := strings.IndexByte(trimmed, '\n'); i >= 0 {
			return trimmed[:i]
		}
		return trimmed
	}
	return "no output"
}
