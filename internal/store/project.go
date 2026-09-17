package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ErrProjectExists is returned by AddProject for a name already registered.
var ErrProjectExists = errors.New("store: project already registered")

// ErrNoProject is returned for a project that is not registered.
var ErrNoProject = errors.New("store: no such project")

// ProjectConfig is `projects/<name>/project.yaml`.
type ProjectConfig struct {
	// Repo is the repository path relative to the workspace root.
	Repo string `yaml:"repo"`
	// DefaultBranch is the branch crew worktrees branch from.
	DefaultBranch string `yaml:"default_branch"`
	// Mode is `local-only` in the MVP; no other value is accepted.
	Mode string `yaml:"mode"`
	// Yolo lets Mate merge without asking the user.
	Yolo bool `yaml:"yolo"`
}

// AddProject registers a project: it normalises the repository path, creates
// `projects/<name>/` with the `mate/` and `crews/` directories, writes
// project.yaml and appends the project to workspace.yaml.
func (w *Workspace) AddProject(name string, cfg ProjectConfig) error {
	if err := ValidateProjectName(name); err != nil {
		return err
	}
	if _, ok := w.Project(name); ok {
		return fmt.Errorf("%w: %s", ErrProjectExists, name)
	}
	normalised, err := w.normaliseProject(cfg)
	if err != nil {
		return err
	}
	for _, dir := range []string{w.ProjectDir(name), w.MateDir(name), w.CrewsDir(name)} {
		if err := w.mkdirAll(dir); err != nil {
			return err
		}
	}
	if err := w.SaveProject(name, normalised); err != nil {
		return err
	}
	w.cfg.Projects = append(w.cfg.Projects, ProjectRef{Name: name, Repo: normalised.Repo})
	if err := w.SaveConfig(); err != nil {
		return err
	}
	return nil
}

// RemoveProject drops a project from workspace.yaml. The state directory under
// `projects/` is left on disk: like `crews/<id>/`, it is history and only the
// user deletes it.
func (w *Workspace) RemoveProject(name string) error {
	for i, p := range w.cfg.Projects {
		if p.Name == name {
			w.cfg.Projects = append(w.cfg.Projects[:i:i], w.cfg.Projects[i+1:]...)
			return w.SaveConfig()
		}
	}
	return fmt.Errorf("%w: %s", ErrNoProject, name)
}

// LoadProject reads `projects/<name>/project.yaml`.
func (w *Workspace) LoadProject(name string) (ProjectConfig, error) {
	if err := ValidateProjectName(name); err != nil {
		return ProjectConfig{}, err
	}
	path := w.ProjectFile(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectConfig{}, fmt.Errorf("%w: %s", ErrNoProject, name)
		}
		return ProjectConfig{}, err
	}
	var cfg ProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ProjectConfig{}, fmt.Errorf("store: %s: %w", path, err)
	}
	if cfg.DefaultBranch == "" {
		cfg.DefaultBranch = DefaultBranch
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeLocalOnly
	}
	return cfg, nil
}

// SaveProject writes `projects/<name>/project.yaml` atomically.
func (w *Workspace) SaveProject(name string, cfg ProjectConfig) error {
	if err := ValidateProjectName(name); err != nil {
		return err
	}
	normalised, err := w.normaliseProject(cfg)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(normalised)
	if err != nil {
		return err
	}
	return w.writeFile(w.ProjectFile(name), data, 0o644)
}

// normaliseProject fills the defaults and checks the fields: the repository
// must be inside the workspace and outside `.matev2/`, and the mode must be
// the one the MVP supports.
func (w *Workspace) normaliseProject(cfg ProjectConfig) (ProjectConfig, error) {
	if cfg.DefaultBranch == "" {
		cfg.DefaultBranch = DefaultBranch
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeLocalOnly
	}
	if cfg.Mode != ModeLocalOnly {
		return ProjectConfig{}, fmt.Errorf("store: invalid mode %q: the MVP supports only %q", cfg.Mode, ModeLocalOnly)
	}
	repo, err := w.RelRepo(cfg.Repo)
	if err != nil {
		return ProjectConfig{}, err
	}
	cfg.Repo = repo
	return cfg, nil
}

// RelRepo turns a repository path - absolute, or relative to the workspace
// root - into the path stored in the configuration: relative to the root,
// slash separated, and proven to be inside the workspace. The repository need
// not be a direct child of the root, but it must not be the root itself and
// must not live under `.matev2/`.
func (w *Workspace) RelRepo(repo string) (string, error) {
	if repo == "" {
		return "", fmt.Errorf("store: repo path is empty")
	}
	abs := repo
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(w.root, abs)
	}
	resolved, err := w.resolve(abs)
	if err != nil {
		return "", err
	}
	if resolved == w.root {
		return "", fmt.Errorf("store: repo %s is the workspace root", repo)
	}
	if within(w.StateDir(), resolved) {
		return "", fmt.Errorf("store: repo %s is inside %s", repo, StateDirName)
	}
	rel, err := filepath.Rel(w.root, resolved)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// Auto reports whether the project is in auto mode, which is the presence of
// `mate/.auto`.
func (w *Workspace) Auto(project string) bool {
	if err := ValidateProjectName(project); err != nil {
		return false
	}
	_, err := os.Stat(w.AutoFlag(project))
	return err == nil
}

// SetAuto creates or removes `mate/.auto`.
func (w *Workspace) SetAuto(project string, on bool) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	path := w.AutoFlag(project)
	if !on {
		if _, err := w.resolve(path); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return w.writeFile(path, nil, 0o644)
}
