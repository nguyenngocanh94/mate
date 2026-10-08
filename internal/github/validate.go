package github

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Remotes reads a repo's remote URLs. gitx.Git is the live one.
type Remotes interface {
	RemoteURL(ctx context.Context, repo, name string) (string, error)
}

// Repo is one repo of a project to check: Name for the message, Path for git.
type Repo struct {
	Name string
	Path string
}

// scpLike is git's `user@host:owner/repo` shorthand.
var scpLike = regexp.MustCompile(`^[^@/\s]+@([^:/\s]+):.+`)

// IsGitHubURL reports whether a remote URL points at github.com, in any of
// the spellings git accepts: https, ssh, git, and the scp-like shorthand.
func IsGitHubURL(remote string) bool {
	remote = strings.TrimSpace(remote)
	if m := scpLike.FindStringSubmatch(remote); m != nil && !strings.Contains(remote, "://") {
		return strings.EqualFold(m[1], "github.com")
	}
	u, err := url.Parse(remote)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "github.com")
}

// CheckProject is what setting a project's mode to `github` requires
// (docs/mvp.md M18): `gh` installed and logged in, and `origin` of every
// repo pointing at GitHub. Every problem is reported at once, each with the
// command that fixes it, so the captain fixes them in one pass.
func CheckProject(ctx context.Context, gh Client, git Remotes, repos []Repo) error {
	var problems []string
	if err := gh.AuthStatus(ctx); err != nil {
		problems = append(problems, err.Error())
	}
	for _, r := range repos {
		origin, err := git.RemoteURL(ctx, r.Path, "origin")
		if err != nil {
			return err
		}
		switch {
		case origin == "":
			problems = append(problems, fmt.Sprintf("repo %s has no origin remote; add it with `git -C %s remote add origin git@github.com:<owner>/<repo>.git`", r.Name, r.Path))
		case !IsGitHubURL(origin):
			problems = append(problems, fmt.Sprintf("repo %s: origin %s does not point to GitHub; fix it with `git -C %s remote set-url origin git@github.com:<owner>/<repo>.git`", r.Name, origin, r.Path))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the github mode is not ready:\n  - %s", strings.Join(problems, "\n  - "))
}
