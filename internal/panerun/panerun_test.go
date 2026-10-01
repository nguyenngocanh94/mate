package panerun

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// capture is the pane's screen: everything the runner and its children
// write to the terminal.
type capture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func startServer(t *testing.T) (string, *capture, *atomic.Int32) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	screen := &capture{}
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := r.Read(chunk)
			screen.mu.Lock()
			screen.buf.Write(chunk[:n])
			screen.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	var restores atomic.Int32
	// A short path: a unix socket path is limited to about 100 bytes.
	dir, err := SocketDir("pr", "none.sock")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "s.sock")
	s := &Server{Idle: "nothing shown yet", Term: Terminal{Out: w, Err: w, Restore: func() { restores.Add(1) }}}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, socket) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
		_ = w.Close()
		_ = os.RemoveAll(dir)
	})
	waitFor(t, func() bool { _, err := os.Stat(socket); return err == nil })
	return socket, screen, &restores
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func send(t *testing.T, socket string, argv ...string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return Send(ctx, socket, Command{Argv: argv})
}

// The pane shows its idle line until told otherwise, then the child's
// output; a second command replaces the first child in place, and the
// terminal is reset between them.
func TestAPaneSwapsItsChildInPlace(t *testing.T) {
	socket, screen, restores := startServer(t)
	waitFor(t, func() bool { return strings.Contains(screen.String(), "nothing shown yet") })
	marker := filepath.Join(t.TempDir(), "first-ended")
	if err := send(t, socket, "sh", "-c", "trap 'touch "+marker+"; exit 0' TERM; echo first-child; while :; do sleep 0.05; done"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(screen.String(), "first-child") })
	if err := send(t, socket, "sh", "-c", "echo second-child; sleep 30"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(screen.String(), "second-child") })
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the first child was not sent SIGTERM before the second started")
	}
	out := screen.String()
	if !strings.Contains(out[strings.Index(out, "first-child"):strings.Index(out, "second-child")], resetSequence) {
		t.Fatal("the terminal was not reset between the two children")
	}
	if restores.Load() < 2 {
		t.Fatalf("terminal modes restored %d times, want before each child", restores.Load())
	}
}

// Asking for what already runs changes nothing: the child is not restarted.
func TestTheSameCommandIsANoOp(t *testing.T) {
	socket, screen, _ := startServer(t)
	argv := []string{"sh", "-c", "echo started-once; sleep 30"}
	for range 3 {
		if err := send(t, socket, argv...); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { return strings.Contains(screen.String(), "started-once") })
	time.Sleep(100 * time.Millisecond)
	if n := strings.Count(screen.String(), "started-once"); n != 1 {
		t.Fatalf("the child started %d times", n)
	}
}

// A child that will not exit on SIGTERM is killed after the grace period.
func TestAStubbornChildIsKilled(t *testing.T) {
	socket, screen, _ := startServer(t)
	if err := send(t, socket, "sh", "-c", "trap '' TERM; echo stubborn; while :; do sleep 0.05; done"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(screen.String(), "stubborn") })
	start := time.Now()
	if err := send(t, socket, "sh", "-c", "echo after; sleep 30"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < StopGrace || took > StopGrace+3*time.Second {
		t.Fatalf("replacing a stubborn child took %s, want about %s", took, StopGrace)
	}
	waitFor(t, func() bool { return strings.Contains(screen.String(), "after") })
}

// A child that exits on its own leaves the idle line, saying how it ended.
func TestAChildThatExitsLeavesTheIdleLine(t *testing.T) {
	socket, screen, _ := startServer(t)
	if err := send(t, socket, "sh", "-c", "exit 3"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(screen.String(), "sh exited: exit status 3") })
	if !strings.Contains(screen.String()[strings.Index(screen.String(), "exit status 3"):], "nothing shown yet") {
		t.Fatal("the idle line did not follow the exit note")
	}
}

func TestARefusalComesBackToTheSender(t *testing.T) {
	socket, _, _ := startServer(t)
	err := send(t, socket, "/definitely/not/a/program")
	if err == nil || !strings.Contains(err.Error(), "start /definitely/not/a/program") {
		t.Fatalf("err = %v", err)
	}
	if err := send(t, socket); err == nil {
		t.Fatal("an empty command was accepted")
	}
}

func TestSendToAClosedPaneIsErrGone(t *testing.T) {
	dir, err := SocketDir("pr", "none.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	err = Send(context.Background(), filepath.Join(dir, "none.sock"), Command{Argv: []string{"true"}})
	if !errors.Is(err, ErrGone) {
		t.Fatalf("err = %v, want ErrGone", err)
	}
}

// An exit request ends the runner and its child: the Console closing its
// columns.
func TestExitEndsTheRunnerAndItsChild(t *testing.T) {
	dir, err := SocketDir("pr", "none.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "s.sock")
	marker := filepath.Join(dir, "ended")
	s := &Server{}
	served := make(chan error, 1)
	go func() { served <- s.Serve(context.Background(), socket) }()
	waitFor(t, func() bool { _, err := os.Stat(socket); return err == nil })
	ready := filepath.Join(dir, "ready")
	if err := send(t, socket, "sh", "-c", "trap 'touch "+marker+"; exit 0' TERM; touch "+ready+"; while :; do sleep 0.05; done"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(ready); return err == nil })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Send(ctx, socket, Command{Exit: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the runner did not end")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the child was not stopped")
	}
}

// A runner whose Console is gone ends by itself.
func TestTheRunnerEndsWhenItsConsoleIsGone(t *testing.T) {
	dir, err := SocketDir("pr", "none.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	owner := exec.Command("sleep", "30")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	s := &Server{Owner: owner.Process.Pid}
	served := make(chan error, 1)
	go func() { served <- s.Serve(context.Background(), filepath.Join(dir, "s.sock")) }()
	time.Sleep(50 * time.Millisecond)
	_ = owner.Process.Kill()
	_ = owner.Wait()
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the runner outlived its Console")
	}
}

// A CLI may exit once a viewer of its own is up - terminal-code's did - and the viewer keeps
// the terminal. The column shows the program until the viewer is gone
// too, the same command is still a no-op meanwhile, and a new command ends
// the viewer before it starts.
func TestWhatAChildLeftOnTheTerminalIsPartOfTheColumn(t *testing.T) {
	dir, err := SocketDir("pr", "none.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	viewer := exec.Command("sleep", "30")
	if err := viewer.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = viewer.Process.Kill() }()
	viewerDone := make(chan struct{})
	go func() { _ = viewer.Wait(); close(viewerDone) }()
	var mu sync.Mutex
	onTTY := []int{viewer.Process.Pid}
	r, w, _ := os.Pipe()
	screen := &capture{}
	go func() {
		_, _ = io.Copy(struct{ io.Writer }{writerFunc(func(p []byte) (int, error) { screen.mu.Lock(); defer screen.mu.Unlock(); return screen.buf.Write(p) })}, r)
	}()
	defer w.Close()
	s := &Server{Idle: "idle-line", Term: Terminal{Out: w, Err: w}, Occupants: func() []int {
		mu.Lock()
		defer mu.Unlock()
		select {
		case <-viewerDone:
			return nil
		default:
		}
		// The viewer takes the terminal once the CLI has run.
		if !strings.Contains(screen.String(), "cli-ran") {
			return nil
		}
		return append([]int(nil), onTTY...)
	}}
	socket := filepath.Join(dir, "s.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Serve(ctx, socket) }()
	waitFor(t, func() bool { _, err := os.Stat(socket); return err == nil })

	cli := []string{"sh", "-c", "echo cli-ran"}
	if err := send(t, socket, cli...); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(screen.String(), "cli-ran") })
	time.Sleep(2 * occupantPoll)
	if strings.Count(screen.String(), "idle-line") != 1 {
		t.Fatalf("the idle line came back while the viewer still had the terminal: %q", screen.String())
	}
	if err := send(t, socket, cli...); err != nil {
		t.Fatal(err)
	}
	if strings.Count(screen.String(), "cli-ran") != 1 {
		t.Fatal("the same command started again while its viewer was up")
	}
	if err := send(t, socket, "sh", "-c", "echo next-program; sleep 30"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-viewerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the viewer the last program left was not ended")
	}
	waitFor(t, func() bool { return strings.Contains(screen.String(), "next-program") })
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// The exit reply comes only once the child is gone: whoever closes the
// pane next closes an empty one.
func TestExitRepliesAfterTheChildIsGone(t *testing.T) {
	dir, err := SocketDir("pr", "none.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "s.sock")
	s := &Server{}
	go func() { _ = s.Serve(context.Background(), socket) }()
	waitFor(t, func() bool { _, err := os.Stat(socket); return err == nil })
	ready := filepath.Join(dir, "ready")
	if err := send(t, socket, "sh", "-c", "trap 'sleep 0.3; exit 0' TERM; touch "+ready+"; while :; do sleep 0.05; done"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(ready); return err == nil })
	s.mu.Lock()
	child := s.child
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Send(ctx, socket, Command{Exit: true}); err != nil {
		t.Fatal(err)
	}
	if child.ProcessState == nil {
		t.Fatal("the exit was acknowledged while the child still ran")
	}
}
