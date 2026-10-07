package prwatch_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/prwatch"
)

// fakeStarter records what it was asked to start and starts nothing.
type fakeStarter struct {
	specs []prwatch.Spec
	pid   int
}

func (f *fakeStarter) Start(spec prwatch.Spec) (int, error) {
	f.specs = append(f.specs, spec)
	return f.pid, nil
}

func setAlive(t *testing.T, alive func(int) bool) {
	t.Helper()
	old := prwatch.Alive
	prwatch.Alive = alive
	t.Cleanup(func() { prwatch.Alive = old })
}

func TestEnsureStartsOneDetachedWatcherAndRecordsItsPid(t *testing.T) {
	e := newEnv(t, prJSON("OPEN", ""))
	setAlive(t, func(pid int) bool { return pid == 4242 })
	st := &fakeStarter{pid: 4242}

	got, err := prwatch.Ensure(e.ws, st, project, crew, prURL)
	if err != nil || got.Already || got.PID != 4242 {
		t.Fatalf("Ensure = %+v, %v", got, err)
	}
	if len(st.specs) != 1 {
		t.Fatalf("started %d watchers, want 1", len(st.specs))
	}
	args := strings.Join(st.specs[0].Args, " ")
	if want := "pr watch --run --workspace " + e.ws.Root() + " shop k3 " + prURL; args != want {
		t.Fatalf("args = %q, want %q", args, want)
	}
	if want := filepath.Join(e.ws.CrewsDir(project), crew, "prwatch.log"); st.specs[0].LogPath != want {
		t.Fatalf("log = %q, want %q", st.specs[0].LogPath, want)
	}
	data, err := os.ReadFile(filepath.Join(e.ws.CrewsDir(project), crew+".prwatch"))
	if err != nil || strings.TrimSpace(string(data)) != "4242" {
		t.Fatalf(".prwatch = %q, %v", data, err)
	}

	// Asking again while it lives starts nothing.
	got, err = prwatch.Ensure(e.ws, st, project, crew, prURL)
	if err != nil || !got.Already || got.PID != 4242 || len(st.specs) != 1 {
		t.Fatalf("second Ensure = %+v, %v, %d starts", got, err, len(st.specs))
	}

	// A dead one is replaced.
	setAlive(t, func(pid int) bool { return pid == 7 })
	st.pid = 7
	got, err = prwatch.Ensure(e.ws, st, project, crew, prURL)
	if err != nil || got.Already || got.PID != 7 || len(st.specs) != 2 || prwatch.ReadPID(e.ws, project, crew) != 7 {
		t.Fatalf("third Ensure = %+v, %v, %d starts, pid file %d", got, err, len(st.specs), prwatch.ReadPID(e.ws, project, crew))
	}
}

func TestRemovePIDOnlyRemovesItsOwn(t *testing.T) {
	e := newEnv(t, prJSON("OPEN", ""))
	if err := prwatch.WritePID(e.ws, project, crew, 9); err != nil {
		t.Fatal(err)
	}
	prwatch.RemovePID(e.ws, project, crew, 8)
	if prwatch.ReadPID(e.ws, project, crew) != 9 {
		t.Fatal("RemovePID removed another watcher's pid")
	}
	prwatch.RemovePID(e.ws, project, crew, 9)
	if prwatch.ReadPID(e.ws, project, crew) != 0 {
		t.Fatal("RemovePID left its own pid")
	}
}

// ExecStarter starts a real process, detached, with its output in the log.
// It runs `true`, so nothing of mate's runs.
func TestExecStarterStartsADetachedProcessWithALog(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Fatalf("no true on PATH: %v", err)
	}
	log := filepath.Join(t.TempDir(), "crew", "prwatch.log")
	pid, err := prwatch.ExecStarter{Binary: bin}.Start(prwatch.Spec{Args: []string{"pr", "watch"}, LogPath: log})
	if err != nil || pid <= 0 {
		t.Fatalf("Start = %d, %v", pid, err)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatalf("log not created: %v", err)
	}
}
