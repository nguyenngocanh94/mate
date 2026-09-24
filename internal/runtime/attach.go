package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// TTYHandoff is the interactive attach Herdr.Attach expects: it hands the
// caller's terminal to `herdr … attach …` and returns when the user detaches.
//
// What it guarantees, exactly:
//
//   - The child inherits the caller's terminal file descriptors, so Herdr's
//     own raw-mode/alt-screen handling is the one in charge. Mate does not
//     touch termios.
//   - A caller whose stdin or stdout is not a terminal is refused before the
//     exec. `herdr terminal attach` does not validate its target first: on a
//     pipe it panics inside its TUI init (exit 101, live 0.8.2). Refusing is
//     the only way that stays a usage error instead of a crash.
//   - SIGINT and SIGQUIT are ignored in this process while the child runs, so
//     a ^C typed at the attached agent reaches the child's terminal without
//     killing the parent and leaving the terminal in raw mode. They are
//     restored afterwards.
//
// What it does not guarantee: Ghostty-native TTY restore, IME and mouse
// state after the handoff. That is unproven (AssumptionGhosttyTTYRestore) and
// this type does not claim it.
type TTYHandoff struct {
	// Binary is the herdr executable; empty means "herdr" on PATH.
	Binary string
	// Stdin, Stdout and Stderr are the caller's terminal. Nil means the
	// process's own standard streams.
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
}

func (h TTYHandoff) binary() string {
	if strings.TrimSpace(h.Binary) == "" {
		return "herdr"
	}
	return h.Binary
}

func (h TTYHandoff) stdin() *os.File {
	if h.Stdin != nil {
		return h.Stdin
	}
	return os.Stdin
}

func (h TTYHandoff) stdout() *os.File {
	if h.Stdout != nil {
		return h.Stdout
	}
	return os.Stdout
}

func (h TTYHandoff) stderr() *os.File {
	if h.Stderr != nil {
		return h.Stderr
	}
	return os.Stderr
}

// Attach runs argv (already carrying --session in the option region) with the
// caller's terminal. It returns nil on a clean detach.
func (h TTYHandoff) Attach(ctx context.Context, argv []string) error {
	if len(argv) == 0 {
		return observability.NewError(observability.CodeUsage, "attach requires a herdr argv")
	}
	if err := h.CheckTerminal(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, h.binary(), argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = h.stdin(), h.stdout(), h.stderr()
	// The child stays in this process group so the terminal keeps sending it
	// job-control signals; the parent stops reacting to them for the
	// duration instead.
	restore := ignoreInterrupts()
	defer restore()
	return AttachRunError(cmd.Run())
}

// ignoreInterrupts makes SIGINT and SIGQUIT no-ops for this process until the
// returned function is called. Without it, a ^C typed inside the attached
// agent would also kill mate, which owns the terminal the attach client put
// into raw mode.
func ignoreInterrupts() func() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// CheckTerminal reports whether this handoff has a terminal to give away. It
// is exported so a caller can refuse early, before an adapter runs probes
// whose answers a terminal-less caller could not use.
func (h TTYHandoff) CheckTerminal() error {
	if err := checkTerminal("stdin", h.stdin()); err != nil {
		return err
	}
	return checkTerminal("stdout", h.stdout())
}

// checkTerminal refuses a stream that is not a terminal. The check is a real
// tty ioctl, not a character-device mode bit: /dev/null is a character device
// and would pass that weaker test, which is exactly the shape `go test` hands
// a process.
func checkTerminal(what string, f *os.File) error {
	if f == nil {
		return observability.NewError(observability.CodeUsage,
			fmt.Sprintf("attach needs a terminal on %s", what))
	}
	if !term.IsTerminal(f.Fd()) {
		return observability.NewError(observability.CodeUsage,
			fmt.Sprintf("attach needs a terminal on %s; %s is not a terminal (attach is an interactive TTY handoff, detach with %s)", what, what, DetachKey))
	}
	return nil
}

// AttachRunError maps the result of running an attach client. A clean detach
// is exit 0. Anything else is reported without leaking os/exec wording,
// because the attach client's own stderr already went to the user's terminal.
func AttachRunError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return AttachExitError(exitErr.ExitCode())
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return observability.WrapError(observability.CodeRuntimeUnavailable, "herdr attach failed to run", err)
}

// AttachExitError maps an attach client's exit status. Exit 2 is Herdr CLI
// usage (live: human text, no JSON envelope); everything else is unknown,
// because the client streams a terminal and does not print a machine-readable
// envelope once it is running.
func AttachExitError(exit int) error {
	if exit == 0 {
		return nil
	}
	code := observability.CodeUnknown
	if exit == 2 {
		code = observability.CodeUsage
	}
	return observability.NewError(code, fmt.Sprintf("herdr attach exited %d", exit)).
		WithDetails(map[string]any{"exit": exit})
}
