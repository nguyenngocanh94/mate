package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"golang.org/x/sys/unix"
)

const (
	// Keep enough of Herdr's stderr tail to classify the two server-side close
	// cases without retaining an unbounded diagnostic transcript.
	sessionStreamErrorTailBytes    = 4 * 1024
	sessionStreamErrorExcerptBytes = 512
	sessionStreamKillGrace         = 2 * time.Second
)

// Open implements SessionStream for the live Herdr adapter. It first asks
// Herdr to resolve the exact (named session, agent name) identity. Only after
// that observation succeeds does it allocate a PTY or start `herdr agent
// attach`. Before returning, the PTY is put into raw mode, so the returned
// channel is ready for raw terminal bytes and does not expose the startup
// window in which the kernel would otherwise line-buffer, echo, or signal
// input. The attach client is placed in its own session, so closing or killing
// that client cannot stop the server-owned agent.
// Herdr attach diagnostics are captured from stderr separately from the PTY;
// PTY bytes remain exclusively the agent's raw terminal stream.
func (h *Herdr) Open(ctx context.Context, ref AgentSessionRef, size TerminalSize) (SessionChannel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if err := size.Validate(); err != nil {
		return nil, err
	}
	if size.Cols > int(^uint16(0)) || size.Rows > int(^uint16(0)) {
		return nil, observability.NewError(
			observability.CodeUsage,
			fmt.Sprintf("terminal size exceeds PTY limit, got %dx%d", size.Cols, size.Rows),
		)
	}

	// `agent get` is the durable-identity resolution boundary. In particular,
	// do not let a successful PTY start become the first evidence that the
	// requested name exists.
	res, err := h.run(ctx, ref.HerdrSession, []string{"agent", "get", ref.AgentName})
	if err != nil {
		return nil, err
	}
	info, err := parseAgentInfo(res.Stdout)
	if err != nil {
		return nil, observability.WrapError(observability.CodeUnknown, "herdr agent get", err)
	}
	if info.Name != ref.AgentName {
		return nil, observability.NewError(
			observability.CodeStateConflict,
			fmt.Sprintf("herdr resolved agent %q, not requested %q", info.Name, ref.AgentName),
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cmd := exec.Command(h.binary(), WithSession(ref.HerdrSession, []string{"agent", "attach", ref.AgentName})...)
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		return nil, observability.WrapError(observability.CodeRuntimeUnavailable, "herdr agent attach stderr pipe", err)
	}
	cmd.Stderr = stderrWriter
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(size.Cols), Rows: uint16(size.Rows)})
	if err != nil {
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
		return nil, observability.WrapError(observability.CodeRuntimeUnavailable, "herdr agent attach failed to start", err)
	}
	_ = stderrWriter.Close()

	channel := &herdrSessionChannel{
		master:       master,
		fd:           int(master.Fd()),
		cmd:          cmd,
		stderrReader: stderrReader,
		stderrDone:   make(chan struct{}),
		waitDone:     make(chan struct{}),
		closeDone:    make(chan struct{}),
		closeWake:    make(chan struct{}),
	}
	go channel.wait()
	go channel.captureStderr()
	if _, err := term.MakeRaw(master.Fd()); err != nil {
		_ = channel.reap(master, cmd.Process)
		return nil, observability.WrapError(observability.CodeRuntimeUnavailable, "herdr agent attach PTY raw mode", err)
	}
	if err := unix.SetNonblock(channel.fd, true); err != nil {
		_ = channel.reap(master, cmd.Process)
		return nil, observability.WrapError(observability.CodeRuntimeUnavailable, "herdr agent attach PTY nonblocking mode", err)
	}
	return channel, nil
}

var _ SessionStream = (*Herdr)(nil)

// herdrSessionChannel is intentionally a transport-only object. It owns the
// local attach client and PTY master, never an agent lifecycle operation. A
// Unix signal sent to this local client or its PTY process group targets only
// that local attach process; the Herdr server owns the agent separately. The
// adapter forwards terminal control bytes through Write, but does not
// translate process signals into Herdr stop operations.
// Herdr's attach diagnostics are captured separately on stderr, so full-screen
// PTY output can never be mistaken for a lifecycle error after the stream has
// been consumed.
type herdrSessionChannel struct {
	master *os.File
	fd     int
	cmd    *exec.Cmd

	readMu  sync.Mutex
	writeMu sync.Mutex
	mu      sync.Mutex

	waitDone     chan struct{}
	waitErr      error
	stderrReader *os.File
	stderrDone   chan struct{}
	stderr       bytes.Buffer

	closeDone    chan struct{}
	closeWake    chan struct{}
	closeStarted bool
	closeErr     error
	closed       bool

	readEnded  bool
	readEndErr error
}

func (c *herdrSessionChannel) wait() {
	err := c.cmd.Wait()
	c.mu.Lock()
	c.waitErr = err
	c.mu.Unlock()
	close(c.waitDone)
}

func (c *herdrSessionChannel) captureStderr() {
	defer close(c.stderrDone)
	buf := make([]byte, 1024)
	for {
		n, err := c.stderrReader.Read(buf)
		if n > 0 {
			c.mu.Lock()
			_, _ = c.stderr.Write(buf[:n])
			if c.stderr.Len() > sessionStreamErrorTailBytes {
				data := c.stderr.Bytes()
				c.stderr.Reset()
				_, _ = c.stderr.Write(data[len(data)-sessionStreamErrorTailBytes:])
			}
			c.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (c *herdrSessionChannel) Read(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, sessionStreamReadContextError(err)
	}

	c.readMu.Lock()
	defer c.readMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, sessionChannelClosedError("read")
	}
	if c.readEnded {
		err := c.readEndErr
		c.mu.Unlock()
		return nil, err
	}
	master := c.master
	c.mu.Unlock()

	buf := make([]byte, 32*1024)
	n, readErr := c.readFile(ctx, master, buf)
	out := struct {
		data []byte
		err  error
	}{data: append([]byte(nil), buf[:n]...), err: readErr}

	if out.err == nil {
		return out.data, nil
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, sessionChannelClosedError("read")
	}
	if errors.Is(out.err, context.Canceled) || errors.Is(out.err, os.ErrDeadlineExceeded) {
		return nil, MapSessionStreamError("read", out.err)
	}
	endErr := c.endError(ctx, out.err)
	c.mu.Lock()
	c.readEnded = true
	c.readEndErr = endErr
	c.mu.Unlock()
	if len(out.data) > 0 {
		// Raw bytes are delivered before the close diagnosis on the following
		// Read, even when the kernel returns data and a PTY close together.
		return out.data, nil
	}
	return nil, endErr
}

func (c *herdrSessionChannel) Write(ctx context.Context, input []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return sessionChannelClosedError("write")
	}
	master := c.master
	c.mu.Unlock()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return MapSessionStreamError("write", c.writeFile(ctx, master, input))
}

// readFile attempts the PTY's native deadline first. Darwin PTY masters do
// not expose deadlines through os.File, so the nonblocking poll path is the
// portable fallback. In either case a context timeout is a read result, not a
// request to close the session.
func (c *herdrSessionChannel) readFile(ctx context.Context, master *os.File, buf []byte) (int, error) {
	clearDeadline := false
	if deadline, ok := ctx.Deadline(); ok {
		if err := master.SetReadDeadline(deadline); err != nil && !errors.Is(err, os.ErrNoDeadline) {
			return 0, err
		}
		clearDeadline = true
	}
	defer func() {
		if clearDeadline {
			_ = master.SetReadDeadline(time.Time{})
		}
	}()
	return c.pollRead(ctx, master, buf)
}

func (c *herdrSessionChannel) pollRead(ctx context.Context, master *os.File, buf []byte) (int, error) {
	for {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return 0, os.ErrDeadlineExceeded
			}
			return 0, err
		}
		ready, err := c.waitForFD(ctx, unix.POLLIN)
		if err != nil {
			return 0, err
		}
		if !ready {
			continue
		}
		n, err := master.Read(buf)
		if err == nil || !errors.Is(err, unix.EAGAIN) {
			return n, err
		}
	}
}

func (c *herdrSessionChannel) writeFile(ctx context.Context, master *os.File, input []byte) error {
	clearDeadline := false
	if deadline, ok := ctx.Deadline(); ok {
		if err := master.SetWriteDeadline(deadline); err != nil && !errors.Is(err, os.ErrNoDeadline) {
			return err
		}
		clearDeadline = true
	}
	defer func() {
		if clearDeadline {
			_ = master.SetWriteDeadline(time.Time{})
		}
	}()
	for len(input) > 0 {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return os.ErrDeadlineExceeded
			}
			return err
		}
		ready, err := c.waitForFD(ctx, unix.POLLOUT)
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		n, err := master.Write(input)
		if n > 0 {
			input = input[n:]
		}
		if err != nil && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		if err == nil && n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (c *herdrSessionChannel) waitForFD(ctx context.Context, events int16) (bool, error) {
	select {
	case <-c.closeWake:
		return false, os.ErrClosed
	default:
	}
	fd := []unix.PollFd{{Fd: int32(c.fd), Events: events}}
	timeout := 100
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		timeout = int((remaining + time.Millisecond - 1) / time.Millisecond)
		if timeout < 1 {
			timeout = 1
		}
		if timeout > 100 {
			timeout = 100
		}
	}
	if _, err := unix.Poll(fd, timeout); err != nil {
		if errors.Is(err, unix.EINTR) {
			return false, nil
		}
		return false, err
	}
	select {
	case <-c.closeWake:
		return false, os.ErrClosed
	default:
	}
	return fd[0].Revents&(events|unix.POLLERR|unix.POLLHUP) != 0, nil
}

func (c *herdrSessionChannel) Resize(ctx context.Context, size TerminalSize) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := size.Validate(); err != nil {
		return err
	}
	if size.Cols > int(^uint16(0)) || size.Rows > int(^uint16(0)) {
		return observability.NewError(observability.CodeUsage, "terminal size exceeds PTY limit")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return sessionChannelClosedError("resize")
	}
	return MapSessionStreamError("resize", pty.Setsize(c.master, &pty.Winsize{
		Cols: uint16(size.Cols),
		Rows: uint16(size.Rows),
	}))
}

func (c *herdrSessionChannel) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closeStarted {
		done := c.closeDone
		c.mu.Unlock()
		select {
		case <-done:
			c.mu.Lock()
			err := c.closeErr
			c.mu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.closeStarted = true
	c.closed = true
	close(c.closeWake)
	done := c.closeDone
	master := c.master
	process := c.cmd.Process
	c.mu.Unlock()

	err := c.reap(master, process)
	c.mu.Lock()
	c.closeErr = err
	close(done)
	c.mu.Unlock()
	return err
}

func (c *herdrSessionChannel) reap(master *os.File, process *os.Process) error {
	// Closing the master makes blocked reader/writer calls return. The child
	// is in the PTY-created process group; signal only that local attach group,
	// never the server-owned Herdr agent.
	masterErr := master.Close()
	if process != nil {
		_ = syscall.Kill(-process.Pid, syscall.SIGTERM)
		select {
		case <-c.waitDone:
		case <-time.After(sessionStreamKillGrace):
			_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
			_ = process.Kill()
			<-c.waitDone
		}
	} else {
		<-c.waitDone
	}
	_ = c.stderrReader.Close()
	<-c.stderrDone
	if masterErr != nil && !errors.Is(masterErr, os.ErrClosed) {
		return MapSessionStreamError("close", masterErr)
	}
	return nil
}

func (c *herdrSessionChannel) endError(ctx context.Context, readErr error) error {
	select {
	case <-c.waitDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-c.stderrDone
	c.mu.Lock()
	waitErr := c.waitErr
	stderr := append([]byte(nil), c.stderr.Bytes()...)
	c.mu.Unlock()
	if waitErr == nil {
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, syscall.EIO) {
			return io.EOF
		}
		return MapSessionStreamError("read", readErr)
	}
	return mapSessionAttachExit(waitErr, stderr)
}

func mapSessionAttachExit(waitErr error, stderr []byte) error {
	line := lastSessionDiagnosticLine(stderr)
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "herdr:") {
		message := boundedSessionErrorExcerpt(line)
		if strings.Contains(lower, "server is shutting down") ||
			strings.Contains(lower, "lost connection to server: server closed connection") {
			return NewHerdrError(HerdrServerNotRunning, message)
		}
		if strings.Contains(lower, "terminal") && strings.Contains(lower, "not found") {
			return NewHerdrError(HerdrAgentNotFound, message)
		}
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return AttachExitError(exitErr.ExitCode())
	}
	return MapSessionStreamError("read", waitErr)
}

func lastSessionDiagnosticLine(tail []byte) string {
	message := strings.TrimSpace(string(tail))
	index := strings.LastIndexByte(message, '\n')
	if index >= 0 {
		return strings.TrimSpace(message[index+1:])
	}
	return message
}

func boundedSessionErrorExcerpt(line string) string {
	if len(line) <= sessionStreamErrorExcerptBytes {
		return line
	}
	return line[:sessionStreamErrorExcerptBytes] + "…"
}
