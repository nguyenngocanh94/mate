package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
	"golang.org/x/sys/unix"
)

const sessionStreamHelperEnv = "MATE_SESSION_STREAM_HELPER"

// TestLiveHerdrSessionStreamSignalIsolation is the in-process proof that a
// signal delivered to the local attach client does not stop the server-owned
// agent. The lab driver provisions MATE_HERDR_LIVE_SESSION and names the
// already-running agent with MATE_HERDR_LIVE_AGENT; no lifecycle mutation is
// performed by this test.
func TestLiveHerdrSessionStreamSignalIsolation(t *testing.T) {
	requireLive(t)
	session := strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_SESSION"))
	agent := strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_AGENT"))
	if session == "" || agent == "" {
		t.Skip("set MATE_HERDR_LIVE_SESSION and MATE_HERDR_LIVE_AGENT to a provisioned lab agent")
	}
	if session == "default" || session == "firstmate" {
		t.Fatal("refusing to run the live stream test against a shared Herdr session")
	}
	h := NewHerdr(process.ExecRunner{})
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: session, AgentName: agent}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = channel.Close(context.Background()) })
	ptyChannel := channel.(*herdrSessionChannel)
	if err := ptyChannel.cmd.Process.Signal(unix.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_, _ = channel.Read(context.Background())
	if _, err := h.run(context.Background(), session, []string{"agent", "get", agent}); err != nil {
		t.Fatalf("agent did not survive attach-client SIGTERM: %v", err)
	}
	if err := channel.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.run(context.Background(), session, []string{"agent", "get", agent}); err != nil {
		t.Fatalf("agent did not survive stream Close: %v", err)
	}
}

func TestLiveHerdrSessionStreamRawOutputAndResize(t *testing.T) {
	requireLive(t)
	session := strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_SESSION"))
	agent := strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_AGENT"))
	if session == "" || agent == "" {
		t.Skip("set MATE_HERDR_LIVE_SESSION and MATE_HERDR_LIVE_AGENT to a provisioned lab agent")
	}
	if session == "default" || session == "firstmate" {
		t.Fatal("refusing to run the live stream test against a shared Herdr session")
	}
	h := NewHerdr(process.ExecRunner{})
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: session, AgentName: agent}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = channel.Close(context.Background()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var raw bool
	for i := 0; i < 4 && !raw; i++ {
		data, err := channel.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.Contains(data, []byte{0x1b, '['})
	}
	if !raw {
		t.Fatal("live agent attach did not return ANSI bytes")
	}
	resized := TerminalSize{Cols: 120, Rows: 36}
	if err := channel.Resize(ctx, resized); err != nil {
		t.Fatal(err)
	}
	rows, cols, err := pty.Getsize(channel.(*herdrSessionChannel).master)
	if err != nil {
		t.Fatal(err)
	}
	if rows != resized.Rows || cols != resized.Cols {
		t.Fatalf("live PTY size = %dx%d, want %dx%d", cols, rows, resized.Cols, resized.Rows)
	}
}

func TestLiveHerdrSessionStreamAgentDisappearance(t *testing.T) {
	requireLive(t)
	session, agent, helper := liveSessionStreamConfig(t)
	pane := strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_PANE"))
	if pane == "" {
		t.Fatal("set MATE_HERDR_LIVE_PANE to the live agent pane")
	}
	h := NewHerdr(process.ExecRunner{})
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: session, AgentName: agent}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = channel.Close(context.Background()) }()
	drained := drainLiveSessionStreamToQuiet(t, channel)
	t.Logf("drained %d PTY bytes before pane close", drained)
	if drained < liveDrainFloorBytes {
		t.Fatalf("drained live disappearance stream = %d bytes, want thousands of PTY bytes", drained)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelRead()
	if err := runSessionStreamLabCommand(helper, "run", session, "pane", "close", pane); err != nil {
		t.Fatal(err)
	}
	var streamErr error
	for {
		_, streamErr = channel.Read(readCtx)
		if streamErr != nil {
			break
		}
	}
	assertSessionStreamCode(t, streamErr, observability.CodeNotFound)
	var coded *observability.Error
	if !errors.As(streamErr, &coded) || coded.Details["herdr_code"] != HerdrAgentNotFound ||
		!strings.Contains(coded.Message, "herdr: server shut down: terminal attach") ||
		!strings.Contains(coded.Message, "not found") {
		t.Fatalf("disappearance diagnosis = %v, want the live terminal-not-found string", streamErr)
	}
}

func TestLiveHerdrSessionStreamHerdrRestart(t *testing.T) {
	requireLive(t)
	session, agent, helper := liveSessionStreamConfig(t)
	h := NewHerdr(process.ExecRunner{})
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: session, AgentName: agent}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = channel.Close(context.Background()) }()
	drained := drainLiveSessionStreamToQuiet(t, channel)
	t.Logf("drained %d PTY bytes before guarded stop", drained)
	if drained < liveDrainFloorBytes {
		t.Fatalf("drained live restart stream = %d bytes, want thousands of PTY bytes", drained)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelRead()
	if err := runSessionStreamLabCommand(helper, "stop", session); err != nil {
		t.Fatal(err)
	}
	var streamErr error
	for {
		_, streamErr = channel.Read(readCtx)
		if streamErr != nil {
			break
		}
	}
	assertSessionStreamCode(t, streamErr, observability.CodeRuntimeUnavailable)
	var coded *observability.Error
	// Herdr 0.8.2 reports a server stop with either of two stderr strings,
	// depending on how far the shutdown has progressed when attach notices:
	// "server is shutting down" (observed on a guarded `session stop`) or
	// "lost connection to server: server closed connection" (observed when the
	// connection drops mid-shutdown). Both are the same shutdown class, so pin
	// the class and the herdr_code, not one string.
	if !errors.As(streamErr, &coded) || coded.Details["herdr_code"] != HerdrServerNotRunning ||
		(!strings.Contains(coded.Message, "server is shutting down") &&
			!strings.Contains(coded.Message, "lost connection to server: server closed connection")) {
		t.Fatalf("restart diagnosis = %v, want a live Herdr server-shutdown string", streamErr)
	}
}

func drainLiveSessionStreamToQuiet(t *testing.T, channel SessionChannel) int {
	t.Helper()
	var drained int
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
		data, err := channel.Read(ctx)
		cancel()
		drained += len(data)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return drained
		}
		if err != nil {
			t.Fatalf("draining live stream after %d bytes: %v", drained, err)
		}
	}
}

// liveDrainFloorBytes is the depth a real Claude agent paints before its
// terminal goes quiet: live drills measure 3.6-6.5 KiB. The old
// screen-content classifier accepted a 56-byte mode-reset-only tail, so a
// drain that stops after the first chunk passes on the broken classifier;
// requiring thousands of bytes is what makes these tests fail there
// (verified: both tests fail on 5059ba9 with `unknown: herdr attach exited 1`).
const liveDrainFloorBytes = 2048

func liveSessionStreamConfig(t *testing.T) (session, agent, helper string) {
	t.Helper()
	session = strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_SESSION"))
	agent = strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_AGENT"))
	helper = strings.TrimSpace(os.Getenv("HERDR_LAB_HELPER"))
	if session == "" || agent == "" || helper == "" {
		t.Skip("set MATE_HERDR_LIVE_SESSION, MATE_HERDR_LIVE_AGENT and HERDR_LAB_HELPER")
	}
	if session == "default" || session == "firstmate" || !strings.HasPrefix(session, "fm-lab-") {
		t.Fatal("refusing to run the live stream test outside an fm-lab-* session")
	}
	return session, agent, helper
}

func runSessionStreamLabCommand(helper string, args ...string) error {
	cmd := exec.Command(helper, args...)
	cmd.Env = withoutEnv(os.Environ(), "XDG_CONFIG_HOME")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("guarded Herdr command %q: %w: %s", args, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func withoutEnv(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, value := range env {
		if !strings.HasPrefix(value, prefix) {
			out = append(out, value)
		}
	}
	return out
}

func TestHerdrSessionStreamResolvesBeforeStartingPTY(t *testing.T) {
	t.Parallel()
	runner := &process.FakeRunner{Default: process.Result{
		ExitCode: 1,
		Stdout:   []byte(`{"id":"agent:get","error":{"code":"agent_not_found","message":"gone"}}`),
	}}
	h := NewHerdr(runner)
	h.Binary = filepath.Join(t.TempDir(), "must-not-start")

	channel, err := h.Open(context.Background(), AgentSessionRef{
		HerdrSession: "stream-test",
		AgentName:    "stream-agent",
	}, TerminalSize{Cols: 80, Rows: 24})
	if channel != nil {
		t.Fatal("unresolved agent returned a channel")
	}
	assertSessionStreamCode(t, err, observability.CodeNotFound)
	if len(runner.Calls) != 1 {
		t.Fatalf("resolver calls = %d, want one; calls=%#v", len(runner.Calls), runner.Calls)
	}
}

func TestHerdrSessionStreamForwardsRawBytesResizesAndReopens(t *testing.T) {
	h := newTestSessionHerdr(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ref := AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}
	size := TerminalSize{Cols: 80, Rows: 24}
	channel, err := h.Open(ctx, ref, size)
	if err != nil {
		t.Fatal(err)
	}

	if got := readSessionStream(t, channel); string(got) != "\x1b[?1049hraw-\xff" {
		t.Fatalf("initial bytes = % x, want raw fixture", got)
	}
	for _, test := range []struct {
		input []byte
		want  string
	}{
		{input: []byte{0x1b, '[', 'A', '\n'}, want: "ARROW_UP"},
		{input: []byte{'\t', '\n'}, want: "TAB"},
	} {
		if err := channel.Write(ctx, test.input); err != nil {
			t.Fatal(err)
		}
		if got := readSessionSuffix(t, channel, test.want); !strings.HasSuffix(got, test.want) {
			t.Fatalf("input % x semantics = %q, want %q", test.input, got, test.want)
		}
	}

	resized := TerminalSize{Cols: 120, Rows: 36}
	if err := channel.Resize(ctx, resized); err != nil {
		t.Fatal(err)
	}
	ptyChannel := channel.(*herdrSessionChannel)
	rows, cols, err := pty.Getsize(ptyChannel.master)
	if err != nil {
		t.Fatal(err)
	}
	if rows != resized.Rows || cols != resized.Cols {
		t.Fatalf("PTY size = %dx%d, want %dx%d", cols, rows, resized.Cols, resized.Rows)
	}
	if err := channel.Resize(ctx, resized); err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(ctx); err != nil {
		t.Fatal(err)
	}

	second, err := h.Open(ctx, ref, size)
	if err != nil {
		t.Fatal(err)
	}
	if got := readSessionStream(t, second); string(got) != "\x1b[?1049hraw-\xff" {
		t.Fatalf("reopen bytes = % x, want fresh raw fixture", got)
	}
	if err := second.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestHerdrSessionStreamRawKeyFixture(t *testing.T) {
	h := newTestSessionHerdr(t)
	t.Setenv(sessionStreamHelperEnv+"_MODE", "keys")
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = channel.Close(context.Background()) }()
	if got := readSessionStream(t, channel); string(got) != "READY\n" {
		t.Fatalf("raw key fixture readiness = %q", got)
	}
	for _, test := range []struct {
		name  string
		input []byte
		hex   string
	}{
		{name: "arrow up", input: []byte{0x1b, '[', 'A'}, hex: "1b5b41"},
		{name: "tab", input: []byte{'\t'}, hex: "09"},
		{name: "ctrl-c", input: []byte{0x03}, hex: "03"},
		{name: "escape", input: []byte{0x1b}, hex: "1b"},
		{name: "utf8", input: []byte("héllo"), hex: "68c3a96c6c6f"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, value := range test.input {
				if err := channel.Write(context.Background(), []byte{value}); err != nil {
					t.Fatal(err)
				}
				got := readSessionContains(t, channel, fmt.Sprintf("BYTE=%02x", value))
				if !strings.Contains(got, fmt.Sprintf("BYTE=%02x", value)) {
					t.Fatalf("far-end report = %q, want byte %02x", got, value)
				}
			}
		})
	}
}

func TestHerdrSessionStreamReadCancellationDoesNotLatchDisappearance(t *testing.T) {
	h := newTestSessionHerdr(t)
	t.Setenv(sessionStreamHelperEnv+"_MODE", "delayed-disappearance")
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = channel.Close(context.Background()) }()
	if got := readSessionStream(t, channel); string(got) != "READY\n" {
		t.Fatalf("delayed disappearance fixture readiness = %q", got)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	readResult := make(chan error, 1)
	go func() {
		_, readErr := channel.Read(cancelled)
		readResult <- readErr
	}()
	time.Sleep(25 * time.Millisecond)
	cancel()
	if err := <-readResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read error = %v, want context.Canceled", err)
	}

	ptyChannel := channel.(*herdrSessionChannel)
	if err := ptyChannel.cmd.Process.Signal(unix.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	readContext, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRead()
	var streamErr error
	for {
		_, streamErr = channel.Read(readContext)
		if streamErr != nil {
			break
		}
	}
	assertSessionStreamCode(t, streamErr, observability.CodeNotFound)
}

func TestHerdrSessionStreamIsRawWhenOpenReturns(t *testing.T) {
	h := newTestSessionHerdr(t)
	t.Setenv(sessionStreamHelperEnv+"_MODE", "raw-at-open")
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = channel.Close(context.Background()) }()
	if got := readSessionContains(t, channel, "READY"); !strings.Contains(got, "READY") {
		t.Fatalf("raw-at-open fixture readiness = %q", got)
	}
	if err := channel.Write(context.Background(), []byte{0x1b}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var output []byte
	for !bytes.Contains(output, []byte("BYTE=1b\n")) {
		data, err := channel.Read(ctx)
		if err != nil {
			t.Fatalf("raw-at-open read = %v, output so far %q", err, output)
		}
		output = append(output, data...)
	}
	if bytes.Contains(output, []byte("^[")) {
		t.Fatalf("raw-at-open input was echoed: %q", output)
	}
}

func TestHerdrSessionStreamReadDeadlineDoesNotCloseChannel(t *testing.T) {
	h := newTestSessionHerdr(t)
	t.Setenv(sessionStreamHelperEnv+"_MODE", "keys")
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = channel.Close(context.Background()) }()
	if got := readSessionStream(t, channel); string(got) != "READY\n" {
		t.Fatalf("raw key fixture readiness = %q", got)
	}
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if _, err := channel.Read(expired); !errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("already-expired read = %v, want only os.ErrDeadlineExceeded", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := channel.Read(ctx); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("deadline read error = %v, want os.ErrDeadlineExceeded", err)
	}
	if err := channel.Write(context.Background(), []byte{'x'}); err != nil {
		t.Fatalf("write after deadline = %v", err)
	}
	if got := readSessionContains(t, channel, "BYTE=78"); !strings.Contains(got, "BYTE=78") {
		t.Fatalf("channel did not remain usable after deadline: %q", got)
	}
}

func TestHerdrSessionStreamCloseReapsChildAndDoesNotLeakResources(t *testing.T) {
	h := newTestSessionHerdr(t)
	t.Setenv(sessionStreamHelperEnv+"_MODE", "stubborn")
	ref := AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}
	beforeFDs := sessionStreamFDCount(t)
	beforeGoroutines := runtime.NumGoroutine()
	channel, err := h.Open(context.Background(), ref, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	ptyChannel := channel.(*herdrSessionChannel)
	pid := ptyChannel.cmd.Process.Pid
	if got := readSessionStream(t, channel); string(got) != "READY\n" {
		t.Fatalf("stubborn fixture readiness = %q", got)
	}
	if err := channel.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		err := unix.Kill(pid, 0)
		if errors.Is(err, unix.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("attach child pid %d survived Close (kill 0 = %v)", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	deadline = time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > beforeGoroutines || sessionStreamFDCount(t) > beforeFDs {
		if time.Now().After(deadline) {
			t.Fatalf("resources did not converge after Close: goroutines=%d (before %d), fds=%d (before %d)", runtime.NumGoroutine(), beforeGoroutines, sessionStreamFDCount(t), beforeFDs)
		}
		runtime.Gosched()
	}
}

func TestHerdrSessionStreamCancelledCloseStillReapsChild(t *testing.T) {
	h := newTestSessionHerdr(t)
	t.Setenv(sessionStreamHelperEnv+"_MODE", "stubborn")
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	pid := channel.(*herdrSessionChannel).cmd.Process.Pid
	if got := readSessionStream(t, channel); string(got) != "READY\n" {
		t.Fatalf("stubborn fixture readiness = %q", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := channel.Close(ctx); err != nil {
		t.Fatalf("cancelled Close = %v, want teardown result", err)
	}
	if err := unix.Kill(pid, 0); !errors.Is(err, unix.ESRCH) {
		t.Fatalf("child survived cancelled Close: kill 0 = %v", err)
	}
}

func TestHerdrSessionStreamResizeAndCloseAreRaceFree(t *testing.T) {
	h := newTestSessionHerdr(t)
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	var resize sync.WaitGroup
	for i := 0; i < 4; i++ {
		resize.Add(1)
		go func() {
			defer resize.Done()
			for j := 0; j < 1000; j++ {
				_ = channel.Resize(context.Background(), TerminalSize{Cols: 80 + j%2, Rows: 24 + j%2})
			}
		}()
	}
	time.Sleep(time.Millisecond)
	if err := channel.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	resize.Wait()
}

func TestMapSessionAttachExitUsesBoundedLastHerdrLine(t *testing.T) {
	waitErr := exec.Command("sh", "-c", "exit 1").Run()
	if waitErr == nil {
		t.Fatal("fixture exit unexpectedly succeeded")
	}
	screen := strings.Repeat("screen output \x1b[31m", 2000)
	err := mapSessionAttachExit(waitErr, []byte(screen+"\nbash: terminal not found in command output\nherdr: server shut down: terminal attach ended: terminal term_test not found\n"))
	assertSessionStreamCode(t, err, observability.CodeNotFound)
	var coded *observability.Error
	if !errors.As(err, &coded) {
		t.Fatal("classified error is not coded")
	}
	if len(coded.Message) > sessionStreamErrorExcerptBytes {
		t.Fatalf("diagnostic message length = %d, want <= %d", len(coded.Message), sessionStreamErrorExcerptBytes)
	}
	if strings.Contains(coded.Message, "screen output") || strings.Contains(coded.Message, "\x1b") {
		t.Fatalf("diagnostic leaked screen content: %q", coded.Message)
	}

	err = mapSessionAttachExit(waitErr, []byte("herdr: server is shutting down\nagent output mentions terminal not found\n"))
	assertSessionStreamCode(t, err, observability.CodeUnknown)
	err = mapSessionAttachExit(waitErr, []byte("agent screen says herdr: server is shutting down"))
	assertSessionStreamCode(t, err, observability.CodeUnknown)
}

func TestHerdrSessionStreamMapsAttachFailureTaxonomy(t *testing.T) {
	for _, test := range []struct {
		name string
		mode string
		code observability.Code
	}{
		{name: "agent disappearance", mode: "disappearance", code: observability.CodeNotFound},
		{name: "Herdr restart", mode: "restart", code: observability.CodeRuntimeUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newTestSessionHerdr(t)
			t.Setenv(sessionStreamHelperEnv+"_MODE", test.mode)
			channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}, TerminalSize{Cols: 80, Rows: 24})
			if err != nil {
				t.Fatal(err)
			}
			for {
				_, err = channel.Read(context.Background())
				if err != nil {
					break
				}
			}
			assertSessionStreamCode(t, err, test.code)
			_ = channel.Close(context.Background())
		})
	}
}

func TestHerdrSessionStreamEndErrorWaitsForDelayedStderr(t *testing.T) {
	t.Setenv(sessionStreamHelperEnv+"_MODE", "delayed-stderr")
	h := newTestSessionHerdr(t)
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = channel.Close(context.Background()) }()

	started := time.Now()
	var streamErr error
	for {
		_, streamErr = channel.Read(context.Background())
		if streamErr != nil {
			break
		}
	}
	if elapsed := time.Since(started); elapsed < 100*time.Millisecond {
		t.Fatalf("attach exit was classified after %s; endError did not wait for delayed stderr", elapsed)
	}
	assertSessionStreamCode(t, streamErr, observability.CodeNotFound)
}

func TestHerdrSessionStreamDrainedAgentDisappearance(t *testing.T) {
	t.Setenv(sessionStreamHelperEnv+"_MODE", "disappearance")
	h := newTestSessionHerdr(t)
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = channel.Close(context.Background()) }()
	var streamErr error
	var drained int
	for {
		data, readErr := channel.Read(context.Background())
		drained += len(data)
		if readErr != nil {
			streamErr = readErr
			break
		}
	}
	if drained == 0 {
		t.Fatal("disappearance fixture did not drain terminal output before exit")
	}
	assertSessionStreamCode(t, streamErr, observability.CodeNotFound)
}

func TestHerdrSessionStreamDrainedHerdrRestart(t *testing.T) {
	t.Setenv(sessionStreamHelperEnv+"_MODE", "restart")
	h := newTestSessionHerdr(t)
	channel, err := h.Open(context.Background(), AgentSessionRef{HerdrSession: "stream-test", AgentName: "stream-agent"}, TerminalSize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = channel.Close(context.Background()) }()
	var streamErr error
	var drained int
	for {
		data, readErr := channel.Read(context.Background())
		drained += len(data)
		if readErr != nil {
			streamErr = readErr
			break
		}
	}
	if drained == 0 {
		t.Fatal("restart fixture did not drain terminal output before exit")
	}
	assertSessionStreamCode(t, streamErr, observability.CodeRuntimeUnavailable)
}

func newTestSessionHerdr(t *testing.T) *Herdr {
	t.Helper()
	t.Setenv(sessionStreamHelperEnv, "1")
	runner := &process.FakeRunner{Default: process.Result{Stdout: []byte(agentGetEnvelope())}}
	h := NewHerdr(runner)
	script := filepath.Join(t.TempDir(), "herdr-fixture")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec \"$MATE_SESSION_STREAM_TEST_BINARY\" --session-stream-helper\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MATE_SESSION_STREAM_TEST_BINARY", os.Args[0])
	h.Binary = script
	return h
}

func agentGetEnvelope() string {
	return `{"id":"agent:get","result":{"agent":{"agent":"fixture","agent_status":"idle","name":"stream-agent","pane_id":"w1:p1","tab_id":"w1:t1","terminal_id":"term_test","workspace_id":"w1","cwd":"/tmp"}}}`
}

func readSessionStream(t *testing.T, channel SessionChannel) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := channel.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readSessionSuffix(t *testing.T, channel SessionChannel, suffix string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output []byte
	for {
		data, err := channel.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, data...)
		if strings.HasSuffix(string(output), suffix) {
			return string(output)
		}
	}
}

func readSessionContains(t *testing.T, channel SessionChannel, needle string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output []byte
	for {
		data, err := channel.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, data...)
		if strings.Contains(string(output), needle) {
			return string(output)
		}
	}
}

func assertSessionStreamCode(t *testing.T, err error, want observability.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", want)
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != want {
		t.Fatalf("error = %v, want coded %s", err, want)
	}
}

func sessionStreamFDCount(t *testing.T) int {
	t.Helper()
	count := 0
	for fd := 0; fd < 1024; fd++ {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			count++
		}
	}
	return count
}

// sessionStreamHelperProcess is the re-exec child the session-stream tests use
// as a stand-in for the herdr binary. TestMain dispatches to it on the
// sessionStreamHelperFlag argument, so it is not a test and never skips.
func sessionStreamHelperProcess() {
	if mode := os.Getenv(sessionStreamHelperEnv + "_MODE"); mode != "" {
		if mode == "disappearance" {
			_, _ = os.Stdout.WriteString("\x1b[?1049hscreen paint")
			_, _ = os.Stderr.WriteString("herdr: terminal term_test not found\n")
			os.Exit(1)
		}
		if mode == "restart" {
			_, _ = os.Stdout.WriteString("\x1b[?1049hscreen paint")
			_, _ = os.Stderr.WriteString("herdr: server is shutting down\n")
			os.Exit(1)
		}
		if mode == "delayed-stderr" {
			delayed := exec.Command("sh", "-c", "sleep 0.2; printf '%s\\n' 'herdr: terminal term_test not found' >&2")
			delayed.Stderr = os.Stderr
			delayed.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := delayed.Start(); err != nil {
				os.Exit(2)
			}
			_ = delayed.Process.Release()
			os.Exit(1)
		}
		if mode == "stubborn" {
			signal.Ignore(syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
			_, _ = os.Stdout.WriteString("READY\n")
			for {
				time.Sleep(time.Hour)
			}
		}
		if mode == "delayed-disappearance" {
			if _, err := term.MakeRaw(os.Stdin.Fd()); err != nil {
				os.Exit(2)
			}
			_, _ = os.Stdout.WriteString("READY\n")
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, syscall.SIGUSR1)
			<-signals
			_, _ = os.Stderr.WriteString("herdr: server shut down: terminal attach ended: terminal term_test not found\n")
			os.Exit(1)
		}
		if mode == "raw-at-open" {
			_, _ = os.Stdout.WriteString("READY\n")
			one := []byte{0}
			n, err := os.Stdin.Read(one)
			if err != nil {
				return
			}
			for _, value := range one[:n] {
				_, _ = fmt.Fprintf(os.Stdout, "BYTE=%02x\n", value)
			}
			return
		}
		if mode == "keys" || mode == "keys-esc" {
			if _, err := term.MakeRaw(os.Stdin.Fd()); err != nil {
				os.Exit(2)
			}
			_, _ = os.Stdout.WriteString("READY\n")
			for {
				one := []byte{0}
				n, err := os.Stdin.Read(one)
				if err != nil {
					return
				}
				for _, value := range one[:n] {
					_, _ = fmt.Fprintf(os.Stdout, "BYTE=%02x\n", value)
				}
			}
		}
	}
	if _, err := term.MakeRaw(os.Stdin.Fd()); err != nil {
		os.Exit(2)
	}
	_, _ = os.Stdout.Write([]byte("\x1b[?1049hraw-\xff"))
	var pending []byte
	for {
		input := make([]byte, 64)
		n, err := os.Stdin.Read(input)
		if err != nil {
			return
		}
		pending = append(pending, input[:n]...)
		if !bytes.Contains(pending, []byte{'\n'}) {
			continue
		}
		line := pending
		pending = nil
		if bytes.Contains(line, []byte{0x1b, '[', 'A'}) {
			_, _ = os.Stdout.WriteString("ARROW_UP")
		} else if bytes.Contains(line, []byte{'\t'}) {
			_, _ = os.Stdout.WriteString("TAB")
		} else {
			_, _ = os.Stdout.Write(line)
		}
	}
}
