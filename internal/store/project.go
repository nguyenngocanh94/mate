package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrProjectExists is returned by AddProject for a name already registered.
var ErrProjectExists = errors.New("store: project already registered")

// ErrNoProject is returned for a project that is not registered.
var ErrNoProject = errors.New("store: no such project")

// ProjectConfig is `projects/<name>/project.yaml`.
// MateConfig pins the coordinator profile independently of the captain's CLI.
type MateConfig struct {
	Model  string `yaml:"model,omitempty"`
	Effort string `yaml:"effort,omitempty"`
	// RefreshContext defaults to 150000; -1 disables automatic refresh.
	RefreshContext int64 `yaml:"refresh_context,omitempty"`
}

type ProjectConfig struct {
	Mate MateConfig `yaml:"mate,omitempty"`
	// Repos are the git repositories the project owns, zero or more
	// (docs/mvp.md M9). A crew works in exactly one of them.
	Repos []RepoConfig `yaml:"repos"`
	// Budget is the optional token/cost ceiling mvp.md M5 task 27 checks at
	// the end of every observer poll. A nil Budget (the field absent from
	// project.yaml) means no limit is configured, not a limit of zero:
	// BudgetConfig's own fields carry that same "unset means no limit"
	// convention one level down.
	Budget *BudgetConfig `yaml:"budget,omitempty"`
}

// BudgetConfig is `project.yaml`'s `budget:` block. Every field is optional
// and a zero value means "no limit on this dimension" - hand-editing the
// file to add one line is the whole interface for the MVP; `project add
// --budget-usd` is the one flag that also writes it, for the caller who
// wants a project-wide ceiling at creation time.
//
// Crossing a limit opens an incident (`budget`, mvp.md section 4b) on the
// crew (for the two crew-scoped fields) or on the project's Mate (for
// ProjectUSD); per the 2026-09-20 decision it never makes a crew `blocked`
// and it is never resolved, because going over budget is a fact about the
// task's past spend, not a condition that clears.
type BudgetConfig struct {
	// CrewTokens is the total token ceiling (all four buckets summed) for
	// one crew's whole task.
	CrewTokens int64 `yaml:"crew_tokens,omitempty"`
	// CrewUSD is the cost ceiling for one crew's whole task, using
	// `pricing.yaml`. A crew whose model has no price never crosses this,
	// because its cost is unknown rather than zero.
	CrewUSD float64 `yaml:"crew_usd,omitempty"`
	// ProjectUSD is the cost ceiling across every crew this project has
	// ever spawned, checked against the Mate rather than any one crew.
	ProjectUSD float64 `yaml:"project_usd,omitempty"`
}

// AddProject registers a project: it normalises its repos (possibly none),
// refuses a repo another project owns, creates `projects/<name>/` with the
// `mate/` and `crews/` directories, writes project.yaml and appends the
// project to workspace.yaml.
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
	if err := w.checkReposUnclaimed(name, normalised.Repos); err != nil {
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
	if err := w.seedOnce(w.ProjectCrewDoc(name), projectCrewDocSeed(name)); err != nil {
		return err
	}
	w.cfg.Projects = append(w.cfg.Projects, ProjectRef{Name: name})
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
	var file projectFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return ProjectConfig{}, fmt.Errorf("store: %s: %w", path, err)
	}
	cfg := file.ProjectConfig
	if file.LegacyRepo != "" && len(cfg.Repos) == 0 {
		cfg.Repos = []RepoConfig{{
			Name: DefaultRepoName(file.LegacyRepo), Path: file.LegacyRepo, DefaultBranch: file.LegacyDefaultBranch,
		}}
	}
	for i := range cfg.Repos {
		if cfg.Repos[i].DefaultBranch == "" {
			cfg.Repos[i].DefaultBranch = DefaultBranch
		}
	}
	return cfg, nil
}

// projectFile is project.yaml as read: the current shape plus the fields
// older files carry and SaveProject never writes again - the one-repo fields
// every project.yaml had before M9, which LoadProject turns into a one-entry
// Repos, and the `mode` and `yolo` that M19 removed (the Mate picks each
// crew's delivery and merges reviewed work itself), read and ignored.
type projectFile struct {
	ProjectConfig       `yaml:",inline"`
	LegacyRepo          string `yaml:"repo"`
	LegacyDefaultBranch string `yaml:"default_branch"`
	LegacyMode          string `yaml:"mode"`
	LegacyYolo          bool   `yaml:"yolo"`
}

// CrewIDs are the ids with a `.meta` under the project's `crews/`, sorted. A
// file whose name is not a valid crew id is ignored rather than reported:
// `crews/` is a directory the user can also put things in.
func (w *Workspace) CrewIDs(project string) ([]string, error) {
	if err := ValidateProjectName(project); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(w.CrewsDir(project))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".meta")
		if !ok || ValidateCrewID(id) != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
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

// normaliseProject fills the defaults and checks the repos per
// normaliseRepos.
func (w *Workspace) normaliseProject(cfg ProjectConfig) (ProjectConfig, error) {
	repos, err := w.normaliseRepos(cfg.Repos)
	if err != nil {
		return ProjectConfig{}, err
	}
	cfg.Repos = repos
	return cfg, nil
}

// RelRepo turns a repository path - absolute, or relative to the workspace
// root - into the path stored in the configuration: relative to the root,
// slash separated, and proven to be inside the workspace. The repository need
// not be a direct child of the root, but it must not be the root itself and
// must not live under `.mate/`.
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

// Held reports whether the captain holds the project in manual mode, which
// is the presence of `mate/.manual`.
func (w *Workspace) Held(project string) bool {
	if err := ValidateProjectName(project); err != nil {
		return false
	}
	_, err := os.Stat(w.ManualHold(project))
	return err == nil
}

// SetMode is the captain's own choice of mode, the console's `m` key: auto
// turns `.auto` on and releases the hold; manual turns it off and holds it
// off, so the captain's choice is recorded. The hold
// is released before auto goes on and set before it goes off, so no reader
// between the two writes sees a state the captain did not choose.
func (w *Workspace) SetMode(project string, auto bool) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	if auto {
		if err := w.removeFlag(w.ManualHold(project)); err != nil {
			return err
		}
		return w.SetAuto(project, true)
	}
	if err := w.writeFile(w.ManualHold(project), nil, 0o644); err != nil {
		return err
	}
	return w.SetAuto(project, false)
}

func (w *Workspace) removeFlag(path string) error {
	if _, err := w.resolve(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SetAuto creates or removes `mate/.auto`.
func (w *Workspace) SetAuto(project string, on bool) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	path := w.AutoFlag(project)
	if !on {
		return w.removeFlag(path)
	}
	return w.writeFile(path, nil, 0o644)
}
