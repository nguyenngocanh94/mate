// Package panerun is the program that lives in each of the Console's
// sibling host panes (docs/mvp.md M13): the agent stage and the file
// review. The host creates each pane once, running `mate pane serve`, and
// the Console then tells the pane what to show over a unix socket. The
// runner swaps its one child program in place - `herdr agent attach` for
// the stage, `tode --review <folder>` for the review - so a switch never
// closes or re-splits a pane, and the column widths the host set at the
// start stay as they are.
//
// The child owns the terminal while it runs: it inherits the runner's
// stdin, stdout and stderr, and the runner itself never reads the tty.
// Between two children the runner restores the terminal modes it started
// with and resets the screen, so a TUI that was killed mid-frame leaves
// nothing behind for the next one.
package panerun

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"
)

// Command is what a pane is asked to show: one program, run in Dir. Exit
// instead ends the runner, and with it the pane.
type Command struct {
	Argv []string `json:"argv,omitempty"`
	Dir  string   `json:"dir,omitempty"`
	// Env is KEY=VALUE pairs set over the runner's own environment: the
	// Console's PATH, since a pane Ghostty opens starts from login's.
	Env  []string `json:"env,omitempty"`
	Exit bool     `json:"exit,omitempty"`
}

func (c Command) valid() error {
	if c.Exit {
		return nil
	}
	if len(c.Argv) == 0 || c.Argv[0] == "" {
		return errors.New("panerun: a command needs a program")
	}
	return nil
}

type reply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// StopGrace is how long a child has to exit after SIGTERM before it is
// killed.
const StopGrace = 2 * time.Second

// Terminal is the tty the runner hands its children, and restores between
// them.
type Terminal struct {
	In, Out, Err *os.File
	// Restore puts the tty back into the modes the runner started with; nil
	// when the runner is not on a tty.
	Restore func()
}

// Server is one pane's runner.
type Server struct {
	// Idle is shown while no child runs, and after one exits.
	Idle string
	Term Terminal

	// Owner, when set, is the Console's pid: the runner ends when it is
	// gone, so a Console that crashed leaves no column behind.
	Owner int
	// Occupants lists the processes on the pane's terminal other than the
	// runner. A program may leave its display to a process of its own that
	// outlives it - terminal-code's CLI exits once its viewer is up - so
	// what a column shows is everything on its tty, not just the child.
	// nil when the runner has no tty.
	Occupants func() []int

	mu      sync.Mutex
	current Command
	child   *exec.Cmd
	done    chan struct{}
	// gen counts starts and stops, never reset, so a watcher of an earlier
	// program never clears a later one; showing is whether a program is
	// on the column now.
	gen     int
	showing bool
	quit    chan struct{}
	once    sync.Once
}

// Serve listens on socket until ctx ends, starting each Command it is sent
// in place of the last. A stale socket file is replaced.
func (s *Server) Serve(ctx context.Context, socket string) error {
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("panerun: listen %s: %w", socket, err)
	}
	defer os.Remove(socket)
	s.mu.Lock()
	if s.quit == nil {
		s.quit = make(chan struct{})
	}
	quit := s.quit
	s.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
		case <-quit:
		case <-ownerGone(s.Owner, quit):
		}
		_ = ln.Close()
	}()
	s.showIdle("")
	for {
		conn, err := ln.Accept()
		if err != nil {
			s.stop()
			select {
			case <-quit:
				return nil
			default:
			}
			if ctx.Err() != nil || s.Owner != 0 && !alive(s.Owner) {
				return nil
			}
			return fmt.Errorf("panerun: accept: %w", err)
		}
		s.handle(conn)
	}
}

// ownerPoll is how often the runner checks its Console is still there.
const ownerPoll = time.Second

// ownerGone closes when pid no longer exists; never, for pid 0.
func ownerGone(pid int, quit <-chan struct{}) <-chan struct{} {
	gone := make(chan struct{})
	if pid == 0 {
		return gone
	}
	go func() {
		t := time.NewTicker(ownerPoll)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
				if !alive(pid) {
					close(gone)
					return
				}
			}
		}
	}()
	return gone
}

// alive reports whether pid exists: signal 0 checks without sending.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return
	}
	var cmd Command
	r := reply{OK: true}
	if err := json.Unmarshal(line, &cmd); err != nil {
		r = reply{Error: "panerun: " + err.Error()}
	} else if cmd.Exit {
		// Stopped before the reply: a sender that closes the pane next
		// finds nothing left running in it.
		s.stop()
		s.once.Do(func() { close(s.quit) })
	} else if err := s.Run(cmd); err != nil {
		r = reply{Error: err.Error()}
	}
	out, _ := json.Marshal(r)
	_, _ = conn.Write(append(out, '\n'))
}

// Run shows cmd: a no-op when cmd is what the column already shows,
// otherwise whatever it shows is ended and cmd started in its place.
func (s *Server) Run(cmd Command) error {
	if err := cmd.valid(); err != nil {
		return err
	}
	s.mu.Lock()
	same := s.showing && sameCommand(s.current, cmd)
	s.mu.Unlock()
	if same {
		return nil
	}
	s.stop()
	s.reset()
	c := exec.Command(cmd.Argv[0], cmd.Argv[1:]...)
	c.Dir = cmd.Dir
	if len(cmd.Env) > 0 {
		c.Env = append(os.Environ(), cmd.Env...)
	}
	c.Stdin, c.Stdout, c.Stderr = s.Term.In, s.Term.Out, s.Term.Err
	if err := c.Start(); err != nil {
		s.showIdle(fmt.Sprintf("could not start %s: %v", cmd.Argv[0], err))
		return fmt.Errorf("panerun: start %s: %w", cmd.Argv[0], err)
	}
	done := make(chan struct{})
	s.mu.Lock()
	s.gen++
	gen := s.gen
	s.current, s.child, s.done, s.showing = cmd, c, done, true
	s.mu.Unlock()
	go s.wait(c, done, gen)
	return nil
}

// occupantPoll is how often a column whose child has exited checks whether
// what the child left on the terminal has gone too.
const occupantPoll = 500 * time.Millisecond

// wait reaps a child. The column still shows the program while anything
// the child left runs on the terminal; once nothing does, the idle line
// comes back with how it ended. A program that stop replaced is silent.
func (s *Server) wait(c *exec.Cmd, done chan struct{}, gen int) {
	err := c.Wait()
	s.mu.Lock()
	if s.child == c {
		s.child = nil
	}
	s.mu.Unlock()
	close(done)
	for len(s.occupants()) > 0 {
		time.Sleep(occupantPoll)
		s.mu.Lock()
		stale := s.gen != gen
		s.mu.Unlock()
		if stale {
			return
		}
	}
	s.mu.Lock()
	ours := s.gen == gen
	if ours {
		s.showing, s.current = false, Command{}
	}
	s.mu.Unlock()
	if ours {
		// What it printed last - herdr's reason for refusing an attach -
		// stays on screen; only the modes it left are undone.
		s.restoreModes()
		s.appendIdle(exitNote(c.Args[0], err))
	}
}

func (s *Server) occupants() []int {
	if s.Occupants == nil {
		return nil
	}
	return s.Occupants()
}

// stop ends what the column shows: the child, then anything still on the
// terminal. Each gets SIGTERM, then SIGKILL after StopGrace.
func (s *Server) stop() {
	s.mu.Lock()
	c, done := s.child, s.done
	s.child, s.current, s.showing = nil, Command{}, false
	s.gen++ // retire the watcher of what is being stopped
	s.mu.Unlock()
	if c != nil {
		_ = c.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(StopGrace):
			_ = c.Process.Kill()
			<-done
		}
	}
	s.sweep()
}

// sweep ends every process left on the terminal.
func (s *Server) sweep() {
	left := s.occupants()
	if len(left) == 0 {
		return
	}
	for _, pid := range left {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(StopGrace)
	for time.Now().Before(deadline) {
		if left = s.occupants(); len(left) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, pid := range left {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// modesSequence turns off every mouse and paste mode a TUI may have set.
// It does not leave the alternate screen: outside it, that restores a
// saved cursor, and the next line lands on top of what the program
// printed. resetSequence leaves the alternate screen too, deletes kitty
// graphics images and resets the terminal (RIS), which clears it.
const (
	modesSequence = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[?25h\x1b[0m"
	resetSequence = "\x1b[?1049l" + modesSequence + "\x1b_Ga=d\x1b\\\x1bc"
)

// reset readies the terminal for the next program: modes restored, screen
// cleared.
func (s *Server) reset() { s.write(resetSequence) }

// restoreModes undoes what a program that exited left set, keeping what
// it printed.
func (s *Server) restoreModes() { s.write(modesSequence) }

func (s *Server) write(seq string) {
	if s.Term.Restore != nil {
		s.Term.Restore()
	}
	if s.Term.Out != nil {
		_, _ = io.WriteString(s.Term.Out, seq)
	}
}

func (s *Server) showIdle(note string) {
	if s.Term.Out == nil {
		return
	}
	text := s.Idle
	if note != "" {
		text = note + "\r\n\r\n" + text
	}
	_, _ = io.WriteString(s.Term.Out, "\x1b[2J\x1b[H\x1b[2m"+text+"\x1b[0m\r\n")
}

// appendIdle writes the note and the idle line below what is on screen.
func (s *Server) appendIdle(note string) {
	if s.Term.Out == nil {
		return
	}
	_, _ = io.WriteString(s.Term.Out, "\r\n\x1b[2m"+note+"\r\n\r\n"+s.Idle+"\x1b[0m\r\n")
}

func exitNote(prog string, err error) string {
	if err == nil {
		return prog + " exited"
	}
	return fmt.Sprintf("%s exited: %v", prog, err)
}

func sameCommand(a, b Command) bool {
	return a.Dir == b.Dir && slices.Equal(a.Argv, b.Argv) && slices.Equal(a.Env, b.Env)
}

// Send asks the runner listening on socket to show cmd, and returns its
// refusal, if any. A socket nobody listens on - the captain closed the
// pane - is ErrGone.
func Send(ctx context.Context, socket string, cmd Command) error {
	if err := cmd.valid(); err != nil {
		return err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
			return fmt.Errorf("%w: %s", ErrGone, socket)
		}
		return fmt.Errorf("panerun: dial %s: %w", socket, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	line, _ := json.Marshal(cmd)
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("panerun: send: %w", err)
	}
	raw, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("panerun: read reply: %w", err)
	}
	var r reply
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("panerun: reply: %w", err)
	}
	if !r.OK {
		return errors.New(r.Error)
	}
	return nil
}

// ErrGone is a pane whose runner is no longer listening.
var ErrGone = errors.New("panerun: the pane is gone")
