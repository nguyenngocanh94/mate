package prwatch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// Spec is how to start the detached watcher: the arguments after the mate
// binary, and the file its output goes to.
type Spec struct {
	Args    []string
	LogPath string
}

// Starter starts the watcher as a process of its own and returns its pid.
// ExecStarter is the live one; tests pass a fake that records the Spec and
// starts nothing.
type Starter interface {
	Start(spec Spec) (pid int, err error)
}

// ExecStarter re-executes the mate binary in its own session, with stdout and
// stderr appended to the log, so the watcher outlives the crew's shell and
// the harness that ran `mate pr watch`.
type ExecStarter struct {
	// Binary is the mate binary; empty means the running executable.
	Binary string
}

// Start implements Starter.
func (s ExecStarter) Start(spec Spec) (int, error) {
	bin := s.Binary
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return 0, err
		}
		bin = exe
	}
	if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0o755); err != nil {
		return 0, err
	}
	log, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer log.Close()
	cmd := exec.Command(bin, spec.Args...)
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// The child is not waited for: it is meant to outlive this process.
	if err := cmd.Process.Release(); err != nil {
		return pid, err
	}
	return pid, nil
}

// RunArgs is the argument list that runs the watcher in the foreground, as
// the detached child does. The hidden `--run` flag is what tells `mate pr
// watch` it already is that child.
func RunArgs(root, project, crew, url string) []string {
	return []string{"pr", "watch", "--run", "--workspace", root, project, crew, url}
}

// Alive reports whether a process with this pid exists. It is a seam: tests
// replace it, and a pid that was reused by an unrelated process reads as
// alive, which is the safe error (a watcher is not started twice).
var Alive = func(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ReadPID is the pid in the crew's `.prwatch` file; 0 when there is none.
func ReadPID(w *store.Workspace, project, crew string) int {
	data, err := os.ReadFile(w.CrewPRWatch(project, crew))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// WritePID records the watcher's pid in the crew's `.prwatch` file.
func WritePID(w *store.Workspace, project, crew string, pid int) error {
	if err := store.ValidateProjectName(project); err != nil {
		return err
	}
	if err := store.ValidateCrewID(crew); err != nil {
		return err
	}
	if err := os.MkdirAll(w.CrewsDir(project), 0o755); err != nil {
		return err
	}
	return os.WriteFile(w.CrewPRWatch(project, crew), []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// RemovePID deletes the `.prwatch` file when it still names pid, so a
// watcher that is exiting never removes the file of the one that replaced it.
func RemovePID(w *store.Workspace, project, crew string, pid int) {
	if ReadPID(w, project, crew) == pid {
		_ = os.Remove(w.CrewPRWatch(project, crew))
	}
}

// Running reports whether the crew's watcher is alive according to its pid
// file.
func Running(w *store.Workspace, project, crew string) bool {
	pid := ReadPID(w, project, crew)
	return pid != 0 && Alive(pid)
}

// Started is what Ensure did.
type Started struct {
	PID int
	// Already is true when a live watcher was found and nothing was started.
	Already bool
}

// Ensure makes sure a detached watcher is running for the crew's pull
// request: a live pid in `.prwatch` is left alone, otherwise one is started
// through st and its pid recorded. It is what `mate pr watch` does, and what
// recovery does for a crew whose watcher died (docs/mvp.md M18).
func Ensure(w *store.Workspace, st Starter, project, crew, url string) (Started, error) {
	if pid := ReadPID(w, project, crew); pid != 0 && Alive(pid) {
		return Started{PID: pid, Already: true}, nil
	}
	pid, err := st.Start(Spec{
		Args:    RunArgs(w.Root(), project, crew, url),
		LogPath: w.CrewPRWatchLog(project, crew),
	})
	if err != nil {
		return Started{}, fmt.Errorf("prwatch: start the watcher: %w", err)
	}
	if err := WritePID(w, project, crew, pid); err != nil {
		return Started{PID: pid}, err
	}
	return Started{PID: pid}, nil
}
