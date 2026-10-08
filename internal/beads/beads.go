// Package beads connects a project's plan to the external bd and bv tools.
// Beads owns the records; issues.jsonl is a disposable viewer projection.
package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
)

type Command struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}
type Runner func(context.Context, Command, io.Reader, io.Writer, io.Writer) error

func Exec(ctx context.Context, c Command, in io.Reader, out, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir, cmd.Env = c.Dir, c.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("%s is not installed; see docs/beads.md for installation: %w", c.Name, err)
		}
		return fmt.Errorf("%s: %w", c.Name, err)
	}
	return nil
}

type Tracker struct {
	w            *store.Workspace
	project, dir string
	run          Runner
}

func Open(w *store.Workspace, project string, run Runner) (*Tracker, error) {
	dir, err := w.BeadsDir(project)
	if err != nil {
		return nil, err
	}
	if run == nil {
		run = Exec
	}
	return &Tracker{w, project, dir, run}, nil
}

func (t *Tracker) Dir() string { return t.dir }

// Environment prevents ambient tracker settings from redirecting a project
// command. Upstream variables are scoped to these subprocesses.
func (t *Tracker) Environment() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "BEADS_DIR", "BEADS_DB", "BD_DB", "BEADS_DOLT_SERVER_HOST", "BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_SERVER_SOCKET", "BEADS_DOLT_MODE", "BEADS_DOLT_DATABASE", "BEADS_DATABASE":
			continue
		}
		env = append(env, entry)
	}
	return append(env, "BEADS_DIR="+t.dir, "BD_NON_INTERACTIVE=1", "BV_NO_UPDATE_CHECK=1", "BV_NO_GITIGNORE=1")
}

func (t *Tracker) command(name string, args ...string) Command {
	return Command{name, args, t.w.ProjectDir(t.project), t.Environment()}
}

// Existing descendants are checked too: a symlink below .beads must not
// redirect an external tool's writes outside the workspace.
func (t *Tracker) validate() error {
	if _, err := t.w.BeadsDir(t.project); err != nil {
		return err
	}
	return filepath.WalkDir(t.dir, func(path string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == t.dir {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = t.w.Resolve(path)
		return err
	})
}

func (t *Tracker) Initialized() (bool, error) {
	if err := t.validate(); err != nil {
		return false, err
	}
	st, err := os.Stat(filepath.Join(t.dir, "metadata.json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("Beads metadata is not a regular file")
	}
	return true, nil
}

func (t *Tracker) ensure(ctx context.Context, stderr io.Writer) error {
	ok, err := t.Initialized()
	if err != nil || ok {
		return err
	}
	// Preserve unreleased native-plan trial data instead of hiding it behind
	// an empty tracker. Ordinary workspaces never had this file.
	legacy, err := t.w.Resolve(filepath.Join(t.w.ProjectDir(t.project), "tasks.yaml"))
	if err != nil {
		return err
	}
	if _, err := os.Stat(legacy); err == nil {
		return fmt.Errorf("legacy plan exists at %s; import its tasks into Beads first (docs/beads.md)", legacy)
	} else if !os.IsNotExist(err) {
		return err
	}
	return t.run(ctx, t.command("bd", "init", "--prefix", t.project, "--skip-agents", "--skip-hooks", "--non-interactive"), nil, stderr, stderr)
}

// Init is lazy and preserves Mate instructions and repository hooks.
func (t *Tracker) Init(ctx context.Context, stderr io.Writer) error {
	unlock, err := t.w.LockBeads(ctx, t.project)
	if err != nil {
		return err
	}
	defer unlock()
	if err := t.ensure(ctx, stderr); err != nil {
		return err
	}
	return t.export(ctx)
}

// Run passes bd arguments through unchanged, within this project's store.
// Failed multi-issue commands may write some records, so export follows failures
// too. An export error never suggests rerunning a possibly successful write.
func (t *Tracker) Run(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		// These text arguments can themselves look like flags. Leave their
		// values literal rather than mistaking a description for scope.
		switch a {
		case "--title", "--description", "-d", "--notes", "--append-notes", "--reason", "--body-file", "--metadata", "--set-metadata", "--actor":
			i++
			continue
		}
		for _, flag := range []string{"--db", "--database", "--directory", "--global", "--repo"} {
			if a == flag || strings.HasPrefix(a, flag+"=") {
				return fmt.Errorf("%s would change project scope; select the Mate project instead", flag)
			}
		}
		if strings.HasPrefix(a, "-C") {
			return fmt.Errorf("-C would change project scope; select the Mate project instead")
		}
	}
	if len(args) == 0 || args[0] == "init" {
		return fmt.Errorf("use mate tasks %s --init to initialize; pass a bd subcommand after --", t.project)
	}
	unlock, err := t.w.LockBeads(ctx, t.project)
	if err != nil {
		return err
	}
	defer unlock()
	if err := t.ensure(ctx, stderr); err != nil {
		return err
	}
	commandErr := t.run(ctx, t.command("bd", args...), in, out, stderr)
	exportErr := t.export(ctx)
	if exportErr != nil {
		exportErr = fmt.Errorf("Beads command may already have saved changes; viewer refresh failed: %w; run mate tasks %s --init to refresh, do not repeat the write", exportErr, t.project)
	}
	return errors.Join(commandErr, exportErr)
}

func (t *Tracker) export(ctx context.Context) error {
	if err := t.validate(); err != nil {
		return err
	}
	path := filepath.Join(t.dir, "issues.jsonl")
	if _, err := t.w.Resolve(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(t.dir, ".issues-*.jsonl")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	var diagnostic bytes.Buffer
	err = t.run(ctx, t.command("bd", "export"), nil, f, &diagnostic)
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(diagnostic.String()))
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

// Viewer releases the project lock after a fresh export. Run updates that
// export after each bd command; bv watches it while the view stays open.
func (t *Tracker) Viewer(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer) error {
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := t.Init(initCtx, stderr); err != nil {
		return err
	}
	return t.run(ctx, t.command("bv", append([]string{"--db", t.dir}, args...)...), in, out, stderr)
}

type Issue struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Type     string `json:"issue_type"`
	Priority int    `json:"priority"`
}

// Work reads bounded authoritative resume context, never initializes a
// tracker or uses the viewer export as evidence of readiness.
func (t *Tracker) Work(ctx context.Context) (active, ready []Issue, err error) {
	ok, err := t.Initialized()
	if err != nil || !ok {
		return nil, nil, err
	}
	unlock, err := t.w.LockBeads(ctx, t.project)
	if err != nil {
		return nil, nil, err
	}
	defer unlock()
	read := func(args ...string) ([]Issue, error) {
		var out, diagnostic bytes.Buffer
		if err := t.run(ctx, t.command("bd", args...), nil, &out, &diagnostic); err != nil {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(diagnostic.String()))
		}
		var issues []Issue
		err := json.Unmarshal(out.Bytes(), &issues)
		return issues, err
	}
	active, err = read("list", "--status", "in_progress,blocked", "--limit", "10", "--sort", "priority", "--brief", "--json", "--readonly")
	if err != nil {
		return nil, nil, err
	}
	ready, err = read("ready", "--limit", "10", "--exclude-type", "epic", "--brief", "--json", "--readonly")
	return active, ready, err
}
