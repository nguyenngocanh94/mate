package beads

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/tool"
)

// dataDirName is the tracker's directory inside the project directory.
const dataDirName = ".beads"

// data is where Beads keeps a project's tasks: one tracker per project,
// shared by every repo of it, and there whether or not the project has a
// repo.
type data struct{}

// Dir is `<project>/.beads`.
func (data) Dir(projectDir string) string { return filepath.Join(projectDir, dataDirName) }

// Exists reports whether the tracker's directory is there.
func (d data) Exists(projectDir string) (bool, error) {
	st, err := os.Stat(d.Dir(projectDir))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case !st.IsDir():
		return false, fmt.Errorf("%s is not a directory", d.Dir(projectDir))
	}
	return true, nil
}

// Init creates the tracker when it is absent, without touching the
// project's instructions or hooks, and refreshes the viewer's export of it
// either way.
func (data) Init(ctx context.Context, env tool.CommandEnv, stderr io.Writer) error {
	t, err := open(env)
	if err != nil {
		return err
	}
	unlock, err := t.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := t.ensure(ctx, stderr); err != nil {
		return err
	}
	return t.export(ctx)
}

// project is one project's tracker as the core handed it over.
type project struct {
	env tool.CommandEnv
	// name is the project's: its directory's name, which is the tracker's
	// issue prefix.
	name string
	// dir is the tracker.
	dir string
}

func open(env tool.CommandEnv) (project, error) {
	if !filepath.IsAbs(env.ProjectDir) {
		return project{}, fmt.Errorf("beads: project directory %q is not an absolute path", env.ProjectDir)
	}
	if env.Run == nil {
		return project{}, errors.New("beads: no runner to start bd with")
	}
	dir := env.DataDir
	if dir == "" {
		dir = data{}.Dir(env.ProjectDir)
	}
	return project{env: env, name: filepath.Base(env.ProjectDir), dir: dir}, nil
}

// lock takes the project's tracker lock: bd's embedded engine has a single
// writer, and an export must not interleave with a write.
func (t project) lock(ctx context.Context) (func(), error) {
	if t.env.Lock == nil {
		return nil, errors.New("beads: no lock to serialize the tracker with")
	}
	return t.env.Lock(ctx)
}

// trackerEnv are the variables that would point bd or bv at another
// tracker. The inherited ones are cleared, so a project command never
// lands in a tracker the captain's shell happens to name; BEADS_DIR is
// then set to the project's.
var trackerEnv = []string{
	"BEADS_DIR", "BEADS_DB", "BD_DB",
	"BEADS_DOLT_SERVER_HOST", "BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_SERVER_SOCKET",
	"BEADS_DOLT_MODE", "BEADS_DOLT_DATABASE", "BEADS_DATABASE",
}

// environment is what bd and bv run with, set over what they inherit: the
// tracker selection cleared (bd 1.3.1 reads an empty variable as unset)
// and replaced by this tracker, and Beads kept from prompting, checking
// for updates or editing .gitignore. It is the one place the Beads
// environment is spelled, for the Command, the Data and the Viewer alike.
func environment(dir string) []string {
	var env []string
	for _, key := range trackerEnv {
		if key != "BEADS_DIR" {
			env = append(env, key+"=")
		}
	}
	return append(env, "BEADS_DIR="+dir, "BD_NON_INTERACTIVE=1", "BV_NO_UPDATE_CHECK=1", "BV_NO_GITIGNORE=1")
}

// run starts name in the project directory with the project's tracker
// selected.
func (t project) run(ctx context.Context, name string, args []string, in io.Reader, out, stderr io.Writer) error {
	err := t.env.Run(ctx, tool.Invocation{Name: name, Args: args, Dir: t.env.ProjectDir, Env: environment(t.dir)}, in, out, stderr)
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s is not installed; see %s for installation: %w", name, info.Docs, err)
	}
	return err
}

// confined refuses a tracker that is, or holds, a symlink out of the
// project directory: bd and bv would write wherever it points.
func (t project) confined() error {
	root, err := filepath.EvalSymlinks(t.env.ProjectDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return filepath.WalkDir(t.dir, func(path string, _ fs.DirEntry, err error) error {
		if path == t.dir && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("beads: cannot resolve %s: %w", path, err)
		}
		if real != root && !strings.HasPrefix(real, root+string(filepath.Separator)) {
			return fmt.Errorf("beads: %s resolves to %s, outside the project directory %s", path, real, root)
		}
		return nil
	})
}

// initialized reports whether bd has made the tracker: its metadata file
// is there.
func (t project) initialized() (bool, error) {
	if err := t.confined(); err != nil {
		return false, err
	}
	st, err := os.Stat(filepath.Join(t.dir, "metadata.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, errors.New("Beads metadata is not a regular file")
	}
	return true, nil
}

// ensure makes the tracker when it is absent. The project's AGENTS.md and
// its repos' hooks are left alone: mate's manual stays the Mate's.
func (t project) ensure(ctx context.Context, stderr io.Writer) error {
	ok, err := t.initialized()
	if err != nil || ok {
		return err
	}
	if err := os.MkdirAll(t.env.ProjectDir, 0o755); err != nil {
		return err
	}
	return t.run(ctx, tracker, []string{"init", "--prefix", t.name, "--skip-agents", "--skip-hooks", "--non-interactive"}, nil, stderr, stderr)
}

// export replaces .beads/issues.jsonl, the file bv watches, with a fresh
// one in one rename: a failed export leaves the last good one.
func (t project) export(ctx context.Context) error {
	if err := t.confined(); err != nil {
		return err
	}
	f, err := os.CreateTemp(t.dir, ".issues-*.jsonl")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	var diagnostic bytes.Buffer
	err = t.run(ctx, tracker, []string{"export"}, nil, f, &diagnostic)
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(diagnostic.String()))
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(t.dir, "issues.jsonl"))
}
