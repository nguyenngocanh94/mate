package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ErrNotWorkspace is returned by Open for a directory with no `.mate/`.
var ErrNotWorkspace = errors.New("store: not a mate workspace")

// Defaults are the harnesses a project uses when it does not say otherwise.
type Defaults struct {
	MateHarness string `yaml:"mate_harness"`
	CrewHarness string `yaml:"crew_harness"`
}

// The harnesses of decision 2 in docs/mvp.md: Mate is Claude Code, Crew is Codex.
const (
	DefaultMateHarness = "claude"
	DefaultCrewHarness = "codex"

	// DefaultBranch is what a project falls back to when none is given.
	DefaultBranch = "main"

	// ModeLocalOnly is the only mode the MVP supports.
	ModeLocalOnly = "local-only"

	// workspaceVersion is the schema version written into workspace.yaml.
	workspaceVersion = 1
)

// ProjectRef is one row of the project list in workspace.yaml: the project
// name and the repository path, relative to the workspace root.
type ProjectRef struct {
	Name string `yaml:"name"`
	Repo string `yaml:"repo"`
}

// WorkspaceConfig is `.mate/workspace.yaml`.
type WorkspaceConfig struct {
	Version int `yaml:"version"`
	// Session is the Herdr session name of this workspace. It is derived from
	// the absolute path once, at Init, and then stored, so moving or
	// re-resolving the workspace never renames a live session.
	Session  string       `yaml:"session"`
	Defaults Defaults     `yaml:"defaults"`
	Projects []ProjectRef `yaml:"projects"`
}

// Workspace is an opened workspace: a resolved root plus the parsed
// workspace.yaml. It is the handle every other package holds.
type Workspace struct {
	root string
	cfg  WorkspaceConfig
}

// Init creates `.mate/` under workspaceDir and writes a default
// workspace.yaml. It is idempotent for a directory that already has one: the
// existing workspace is opened instead, so the stored session name survives.
func Init(workspaceDir string) (*Workspace, error) {
	root, err := resolveRoot(workspaceDir)
	if err != nil {
		return nil, err
	}
	w := &Workspace{root: root}
	if _, err := os.Stat(w.WorkspaceFile()); err == nil {
		return Open(root)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	w.cfg = WorkspaceConfig{
		Version: workspaceVersion,
		Session: SessionName(root),
		Defaults: Defaults{
			MateHarness: DefaultMateHarness,
			CrewHarness: DefaultCrewHarness,
		},
	}
	if err := w.mkdirAll(w.ProjectsDir()); err != nil {
		return nil, err
	}
	if err := w.SaveConfig(); err != nil {
		return nil, err
	}
	// The captain's rules for every Mate. Every Mate's bootstrap reads this
	// file first (mateassets, section 3), so an init that did not create it
	// left each Mate opening with a "no such file" error on a file its own
	// manual names (measured 2026-09-19, task 24). Seeded once, never
	// rewritten: it is the captain's file from here on.
	if err := os.WriteFile(w.WorkspaceDoc(), []byte(workspaceDocSeed), 0o644); err != nil {
		return nil, err
	}
	// The token price table (mvp.md M5 task 27). Seeded once, at zero, with
	// a comment telling the captain it is theirs to fill in: mate records
	// tokens whether or not anyone has priced them, and an init that left
	// this file missing would make every ledger's cost column silently
	// unreadable rather than honestly "?".
	if err := os.WriteFile(w.PricingFile(), []byte(pricingFileSeed), 0o644); err != nil {
		return nil, err
	}
	// The captain's standing rules for every Crew (docs/mvp.md M7), seeded
	// with only a comment so it is discoverable and still adds nothing to a
	// brief until the captain writes a rule.
	if err := w.seedOnce(w.WorkspaceCrewDoc(), workspaceCrewDocSeed); err != nil {
		return nil, err
	}
	return w, nil
}

// workspaceDocSeed is the WORKSPACE.md a new workspace starts with: a place
// for the captain's rules, empty of rules, so a Mate that reads it learns
// only that nothing workspace-wide has been said yet.
const workspaceDocSeed = `# Workspace rules

Rules here apply to every Mate in this workspace, on top of each project's PROJECT.md.
Nothing is written here yet.
`

// Open resolves workspaceDir, which must contain `.mate/workspace.yaml`, and
// loads the configuration.
func Open(workspaceDir string) (*Workspace, error) {
	root, err := resolveRoot(workspaceDir)
	if err != nil {
		return nil, err
	}
	w := &Workspace{root: root}
	if fi, err := os.Stat(w.StateDir()); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%w: %s has no %s directory", ErrNotWorkspace, root, StateDirName)
	}
	if err := w.LoadConfig(); err != nil {
		return nil, err
	}
	return w, nil
}

// resolveRoot makes the workspace root absolute and canonical. A workspace
// reached through a symlink is ordinary (on macOS /tmp is /private/tmp), so
// the link is followed and the target becomes the root; every boundary check
// afterwards is against that resolved path.
func resolveRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("store: workspace %s is not a directory", resolved)
	}
	return resolved, nil
}

// SessionName is the Herdr session name for a workspace path: `mate-` plus a
// short stable hash of the absolute path. Init stores the result so it never
// changes for a workspace that already exists.
func SessionName(absPath string) string {
	sum := sha256.Sum256([]byte(absPath))
	return "mate-" + hex.EncodeToString(sum[:4])
}

// Config is the loaded workspace.yaml. The copy is the caller's; changes reach
// disk only through the methods on Workspace.
func (w *Workspace) Config() WorkspaceConfig {
	cfg := w.cfg
	cfg.Projects = append([]ProjectRef(nil), w.cfg.Projects...)
	return cfg
}

// Session is the Herdr session name of this workspace.
func (w *Workspace) Session() string { return w.cfg.Session }

// Defaults are the workspace-wide harness defaults.
func (w *Workspace) Defaults() Defaults { return w.cfg.Defaults }

// Projects are the registered projects, in the order workspace.yaml lists them.
func (w *Workspace) Projects() []ProjectRef {
	return append([]ProjectRef(nil), w.cfg.Projects...)
}

// Project returns the registration of one project.
func (w *Workspace) Project(name string) (ProjectRef, bool) {
	for _, p := range w.cfg.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return ProjectRef{}, false
}

// LoadConfig re-reads workspace.yaml from disk.
func (w *Workspace) LoadConfig() error {
	data, err := os.ReadFile(w.WorkspaceFile())
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s is missing", ErrNotWorkspace, w.WorkspaceFile())
		}
		return err
	}
	var cfg WorkspaceConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("store: %s: %w", w.WorkspaceFile(), err)
	}
	if cfg.Session == "" {
		cfg.Session = SessionName(w.root)
	}
	if cfg.Defaults.MateHarness == "" {
		cfg.Defaults.MateHarness = DefaultMateHarness
	}
	if cfg.Defaults.CrewHarness == "" {
		cfg.Defaults.CrewHarness = DefaultCrewHarness
	}
	w.cfg = cfg
	return nil
}

// SaveConfig writes workspace.yaml atomically.
func (w *Workspace) SaveConfig() error {
	if w.cfg.Version == 0 {
		w.cfg.Version = workspaceVersion
	}
	data, err := yaml.Marshal(w.cfg)
	if err != nil {
		return err
	}
	return w.writeFile(w.WorkspaceFile(), data, 0o644)
}

// mkdirAll creates dir and its parents after checking the boundary.
func (w *Workspace) mkdirAll(dir string) error {
	if _, err := w.resolve(dir); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

// writeFile writes path atomically: a temp file in the same directory, then a
// rename. The temp file is removed if anything fails, so a failed write leaves
// no debris next to the real file.
func (w *Workspace) writeFile(path string, data []byte, perm os.FileMode) error {
	if _, err := w.resolve(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := w.mkdirAll(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(name)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
