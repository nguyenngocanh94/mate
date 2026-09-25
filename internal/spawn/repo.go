package spawn

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// ErrRepoRefused matches (errors.Is) every spawn refused because the repo
// the crew is to work in could not be settled: the project has none, has
// several and `--repo` was not given, or has none by the given name. The
// CLI reports it as a usage error.
var ErrRepoRefused = errors.New("crew spawn: no repo settled")

// repoRefusal carries the one-line reason and matches ErrRepoRefused, so the
// message a reader sees is the reason and not the sentinel's text.
type repoRefusal struct{ why string }

func (e *repoRefusal) Error() string        { return e.why }
func (e *repoRefusal) Is(target error) bool { return target == ErrRepoRefused }

// spawnRepo is the repo a new crew works in (docs/mvp.md M9): the one
// `--repo` names, else the project's only repo. Every other case is a usage
// refusal naming the fix, returned before anything exists.
func spawnRepo(cfg store.ProjectConfig, project, name string) (store.RepoConfig, error) {
	name = strings.TrimSpace(name)
	names := strings.Join(cfg.RepoNames(), ", ")
	var why string
	switch {
	case len(cfg.Repos) == 0:
		why = fmt.Sprintf("project %s has no repo yet; add one with `mate project repo add %s <repo-path>`", project, project)
	case name != "":
		if r, ok := cfg.Repo(name); ok {
			return r, nil
		}
		why = fmt.Sprintf("project %s has no repo %q; its repos are: %s", project, name, names)
	case len(cfg.Repos) == 1:
		return cfg.Repos[0], nil
	default:
		why = fmt.Sprintf("project %s has several repos (%s); name the crew's repo with --repo <name>", project, names)
	}
	return store.RepoConfig{}, observability.WrapError(observability.CodeUsage,
		"crew spawn refused, nothing was created", &repoRefusal{why: why})
}
