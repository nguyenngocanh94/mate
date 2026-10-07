// Package github is mate's whole contact with GitHub: the `gh` command line
// behind one seam (docs/mvp.md M18). Nothing here speaks HTTP and nothing
// else in the module runs `gh`, so a test that hands Runner a fake never
// touches the network or a real account.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Command is one `gh` invocation.
type Command struct {
	// Dir is the working directory, so `gh` resolves the repository the way
	// the caller's own checkout would. Empty means the process's own.
	Dir  string
	Args []string
}

// Result is the observed outcome. A non-zero exit is a result, not an
// error: the caller decides what it means.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Runner executes `gh`. ExecRunner is the live one; tests pass a fake.
type Runner interface {
	Run(ctx context.Context, cmd Command) (Result, error)
}

// ExecRunner runs `gh` through os/exec with no shell. Failing to start it
// (not installed) is an error, wrapped as ErrNotInstalled; a non-zero exit
// is a Result.
type ExecRunner struct {
	// Binary overrides the program name. Empty means "gh" on PATH.
	Binary string
}

// ErrNotInstalled is returned when the `gh` binary cannot be started.
var ErrNotInstalled = errors.New("github: gh is not installed")

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, cmd Command) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	bin := r.Binary
	if bin == "" {
		bin = "gh"
	}
	c := exec.CommandContext(ctx, bin, cmd.Args...)
	c.Dir = cmd.Dir
	c.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
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
		if errors.Is(err, exec.ErrNotFound) {
			return Result{}, ErrNotInstalled
		}
		return Result{}, fmt.Errorf("github: gh %s could not be run: %w", strings.Join(cmd.Args, " "), err)
	}
	return res, nil
}

// Client is the command surface mate needs from `gh`.
type Client struct {
	Runner Runner
}

// New returns a Client over the real `gh` binary.
func New() Client { return Client{Runner: ExecRunner{}} }

func (c Client) runner() Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return ExecRunner{}
}

// run executes one command and turns a non-zero exit into an error carrying
// gh's own first line.
func (c Client) run(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := c.runner().Run(ctx, Command{Dir: dir, Args: args})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("gh %s failed (exit %d): %s", strings.Join(args, " "), res.ExitCode, firstLine(res.Stderr, res.Stdout))
	}
	return res.Stdout, nil
}

func firstLine(parts ...string) string {
	for _, p := range parts {
		for _, l := range strings.Split(p, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				return l
			}
		}
	}
	return "no output"
}

// AuthStatus is `gh auth status`: nil when gh is installed and logged in.
func (c Client) AuthStatus(ctx context.Context) error {
	res, err := c.runner().Run(ctx, Command{Args: []string{"auth", "status"}})
	if err != nil {
		if errors.Is(err, ErrNotInstalled) {
			return fmt.Errorf("gh is not installed; install the GitHub CLI (https://cli.github.com) and run `gh auth login`")
		}
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("gh is not logged in (%s); run `gh auth login`", firstLine(res.Stderr, res.Stdout))
	}
	return nil
}

// The states of a pull request, as `gh pr view --json state` spells them.
const (
	StateOpen   = "OPEN"
	StateClosed = "CLOSED"
	StateMerged = "MERGED"
)

// PR is what `gh pr view` reports about one pull request.
type PR struct {
	// State is StateOpen, StateClosed or StateMerged.
	State string
	// MergeCommit is the commit the merge produced on the base branch: for a
	// squash or rebase merge it is not any commit of the head branch. Empty
	// until the pull request is merged.
	MergeCommit string
	// Base is the branch the pull request targets.
	Base string
	// HeadSHA is the commit the pull request's branch points at on GitHub.
	HeadSHA string
}

// PRView is `gh pr view <url> --json state,mergeCommit,baseRefName,headRefOid`.
func (c Client) PRView(ctx context.Context, dir, url string) (PR, error) {
	out, err := c.run(ctx, dir, "pr", "view", url, "--json", "state,mergeCommit,baseRefName,headRefOid")
	if err != nil {
		return PR{}, err
	}
	var raw struct {
		State       string `json:"state"`
		BaseRefName string `json:"baseRefName"`
		HeadRefOid  string `json:"headRefOid"`
		MergeCommit *struct {
			Oid string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return PR{}, fmt.Errorf("github: gh pr view %s: unreadable output: %w", url, err)
	}
	pr := PR{State: strings.ToUpper(strings.TrimSpace(raw.State)), Base: raw.BaseRefName, HeadSHA: raw.HeadRefOid}
	if raw.MergeCommit != nil {
		pr.MergeCommit = raw.MergeCommit.Oid
	}
	return pr, nil
}

// PRMerge is `gh pr merge <url> --merge`: it lands the pull request on
// GitHub with a merge commit. Branch deletion is left alone on purpose; the
// crew's branch is removed by `crew stop`. A non-empty matchHead adds
// `--match-head-commit`, which makes GitHub refuse the merge if the branch
// moved past the commit that was reviewed.
func (c Client) PRMerge(ctx context.Context, dir, url, matchHead string) error {
	args := []string{"pr", "merge", url, "--merge"}
	if matchHead != "" {
		args = append(args, "--match-head-commit", matchHead)
	}
	_, err := c.run(ctx, dir, args...)
	return err
}
