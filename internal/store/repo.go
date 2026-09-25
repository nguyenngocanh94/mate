package store

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
)

// ErrNoRepo is returned when a project has no repo, or none by the asked name.
var ErrNoRepo = errors.New("store: no such repo")

// ErrRepoExists is returned for a repo name or path already registered.
var ErrRepoExists = errors.New("store: repo already registered")

// ErrAmbiguousRepo is returned by SoleRepo for a project with several repos:
// the caller has to say which one.
var ErrAmbiguousRepo = errors.New("store: project has several repos")

// ErrRepoInUse is returned by RemoveRepo while an open crew works in the repo.
var ErrRepoInUse = errors.New("store: repo has open crews")

// MetaRepo is the crew `.meta` key naming the repo the crew works in
// (docs/mvp.md M9). A meta written before M9 has none; CrewRepo resolves it.
const MetaRepo = "repo"

// RepoConfig is one entry of project.yaml's `repos:` list. A project owns
// zero or more repos; a crew works in exactly one (docs/mvp.md M9).
type RepoConfig struct {
	// Name identifies the repo inside its project: `crew spawn --repo`,
	// `repo=` in a crew's meta, the `<repo>:` of a PROJECT.md anchor.
	Name string `yaml:"name"`
	// Path is the repository path relative to the workspace root.
	Path string `yaml:"path"`
	// DefaultBranch is the branch crew worktrees branch from and merge into.
	DefaultBranch string `yaml:"default_branch"`
}

// Repo returns the repo registered under name.
func (cfg ProjectConfig) Repo(name string) (RepoConfig, bool) {
	for _, r := range cfg.Repos {
		if r.Name == name {
			return r, true
		}
	}
	return RepoConfig{}, false
}

// RepoNames are the names of the project's repos, in registration order.
func (cfg ProjectConfig) RepoNames() []string {
	names := make([]string, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		names = append(names, r.Name)
	}
	return names
}

// SoleRepo is the project's one repo. It is what every caller that has no
// crew to ask uses: ErrNoRepo for a project without a repo, ErrAmbiguousRepo
// for one with several, each naming the fix.
func (cfg ProjectConfig) SoleRepo() (RepoConfig, error) {
	switch len(cfg.Repos) {
	case 0:
		return RepoConfig{}, fmt.Errorf("%w: the project has no repo yet; add one with `mate project repo add <project> <repo-path>`", ErrNoRepo)
	case 1:
		return cfg.Repos[0], nil
	default:
		return RepoConfig{}, fmt.Errorf("%w (%s): name one with --repo", ErrAmbiguousRepo, strings.Join(cfg.RepoNames(), ", "))
	}
}

// CrewRepo is the repo a crew works in: the one its meta names, or - for a
// meta written before M9, which names none - the project's sole repo.
func (cfg ProjectConfig) CrewRepo(meta map[string]string) (RepoConfig, error) {
	name := strings.TrimSpace(meta[MetaRepo])
	if name == "" {
		r, err := cfg.SoleRepo()
		if err != nil {
			return RepoConfig{}, fmt.Errorf("crew meta names no repo (written before M9): %w", err)
		}
		return r, nil
	}
	r, ok := cfg.Repo(name)
	if !ok {
		return RepoConfig{}, fmt.Errorf("%w: crew meta names repo %q, which the project no longer has", ErrNoRepo, name)
	}
	return r, nil
}

// ValidateRepoName accepts the names a repo may have inside its project, the
// same shape as a project name: `[a-z][a-z0-9-]{0,31}`.
func ValidateRepoName(name string) error {
	if !projectNamePattern.MatchString(name) {
		return fmt.Errorf("store: invalid repo name %q: want [a-z][a-z0-9-]{0,31}", name)
	}
	return nil
}

var repoNameInvalid = regexp.MustCompile(`[^a-z0-9-]+`)

// DefaultRepoName derives a valid repo name from a repo path: its last
// element, lowercased, with every run of other characters turned into one
// '-'. It is deterministic, so the name a legacy project.yaml is read with
// is the same on every read.
func DefaultRepoName(repoPath string) string {
	name := strings.ToLower(path.Base(strings.TrimRight(strings.ReplaceAll(repoPath, "\\", "/"), "/")))
	name = strings.Trim(repoNameInvalid.ReplaceAllString(name, "-"), "-")
	name = strings.TrimLeft(name, "0123456789-")
	if len(name) > 32 {
		name = strings.TrimRight(name[:32], "-")
	}
	if name == "" {
		return "repo"
	}
	return name
}

// normaliseRepos fills each repo's defaults and checks the list: every path
// inside the workspace and outside `.mate/`, names valid and unique, and no
// path registered twice.
func (w *Workspace) normaliseRepos(repos []RepoConfig) ([]RepoConfig, error) {
	out := make([]RepoConfig, 0, len(repos))
	names := map[string]bool{}
	paths := map[string]string{}
	for _, r := range repos {
		rel, err := w.RelRepo(r.Path)
		if err != nil {
			return nil, err
		}
		r.Path = rel
		if r.Name == "" {
			r.Name = DefaultRepoName(rel)
		}
		if err := ValidateRepoName(r.Name); err != nil {
			return nil, err
		}
		if r.DefaultBranch == "" {
			r.DefaultBranch = DefaultBranch
		}
		if names[r.Name] {
			return nil, fmt.Errorf("%w: name %s is used twice", ErrRepoExists, r.Name)
		}
		if other, ok := paths[rel]; ok {
			return nil, fmt.Errorf("%w: %s is registered as both %s and %s", ErrRepoExists, rel, other, r.Name)
		}
		names[r.Name], paths[rel] = true, r.Name
		out = append(out, r)
	}
	return out, nil
}

// checkReposUnclaimed refuses a repo path another project already owns: a
// crew branch is `mate/<crew>` and a crew id is unique only inside its
// project, so two projects in one repo would collide on branch names.
func (w *Workspace) checkReposUnclaimed(project string, repos []RepoConfig) error {
	for _, ref := range w.cfg.Projects {
		if ref.Name == project {
			continue
		}
		other, err := w.LoadProject(ref.Name)
		if err != nil {
			return fmt.Errorf("store: checking %s's repos: %w", ref.Name, err)
		}
		for _, r := range repos {
			for _, o := range other.Repos {
				if o.Path == r.Path {
					return fmt.Errorf("%w: %s already belongs to project %s (as %s)", ErrRepoExists, r.Path, ref.Name, o.Name)
				}
			}
		}
	}
	return nil
}

// AddRepo registers one more repo in an existing project and returns it as
// stored. An empty Name is derived from the path (DefaultRepoName).
func (w *Workspace) AddRepo(project string, repo RepoConfig) (RepoConfig, error) {
	cfg, err := w.LoadProject(project)
	if err != nil {
		return RepoConfig{}, err
	}
	repos, err := w.normaliseRepos(append(append([]RepoConfig(nil), cfg.Repos...), repo))
	if err != nil {
		return RepoConfig{}, err
	}
	added := repos[len(repos)-1]
	if err := w.checkReposUnclaimed(project, []RepoConfig{added}); err != nil {
		return RepoConfig{}, err
	}
	cfg.Repos = repos
	if err := w.SaveProject(project, cfg); err != nil {
		return RepoConfig{}, err
	}
	return added, nil
}

// RemoveRepo drops a repo from a project. The repository directory is never
// touched. It refuses while any crew that is not closed (finished or failed)
// works in the repo, since that crew's worktree and branch live there.
func (w *Workspace) RemoveRepo(project, name string) error {
	cfg, err := w.LoadProject(project)
	if err != nil {
		return err
	}
	if _, ok := cfg.Repo(name); !ok {
		return fmt.Errorf("%w: project %s has no repo %q", ErrNoRepo, project, name)
	}
	ids, err := w.CrewIDs(project)
	if err != nil {
		return err
	}
	var open []string
	for _, id := range ids {
		meta, err := w.ReadCrewMeta(project, id)
		if err != nil {
			return err
		}
		if crewstate.State(strings.TrimSpace(meta[crewstate.MetaState])).Closed() {
			continue
		}
		r, err := cfg.CrewRepo(meta)
		if err != nil || r.Name == name {
			// A crew whose repo cannot be resolved might be in this one;
			// refusing is the side that loses nothing.
			open = append(open, id)
		}
	}
	if len(open) > 0 {
		return fmt.Errorf("%w: %s (stop them first with `mate crew stop`)", ErrRepoInUse, strings.Join(open, ", "))
	}
	kept := make([]RepoConfig, 0, len(cfg.Repos)-1)
	for _, r := range cfg.Repos {
		if r.Name != name {
			kept = append(kept, r)
		}
	}
	cfg.Repos = kept
	return w.SaveProject(project, cfg)
}
