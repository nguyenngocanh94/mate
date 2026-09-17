package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nguyenngocanh94/matev2/internal/observability"
)

// TerminalSize is the terminal geometry requested for an interactive agent
// session. Cols and Rows are display cells, not bytes or pixels.
type TerminalSize struct {
	Cols int
	Rows int
}

// Validate checks the geometry before a stream or PTY is opened.
func (s TerminalSize) Validate() error {
	if s.Cols <= 0 || s.Rows <= 0 {
		return observability.NewError(
			observability.CodeUsage,
			fmt.Sprintf("terminal size must have positive columns and rows, got %dx%d", s.Cols, s.Rows),
		)
	}
	return nil
}

// AgentSessionRef is the durable runtime identity of one agent. The pair is
// deliberately used as an exact address: a session name alone is not enough,
// and a live pane or current focus is never a substitute for this identity.
type AgentSessionRef struct {
	HerdrSession string
	AgentName    string
}

// Validate checks the parts of a durable runtime identity that are also
// required by Herdr's addressing contract. Open implementations must resolve
// this exact pair before allocating a stream or starting any transport.
func (r AgentSessionRef) Validate() error {
	if strings.TrimSpace(r.HerdrSession) == "" {
		return observability.NewError(observability.CodeUsage, "session stream requires a Herdr session")
	}
	if err := checkSessionName(r.HerdrSession); err != nil {
		return observability.WrapError(observability.CodeUsage, "invalid Herdr session", err)
	}
	if strings.TrimSpace(r.AgentName) == "" {
		return observability.NewError(observability.CodeUsage, "session stream requires an agent name")
	}
	if !ValidAgentName(r.AgentName) {
		return observability.NewError(
			observability.CodeUsage,
			fmt.Sprintf("invalid Herdr agent name %q", r.AgentName),
		)
	}
	return nil
}

// SessionStream opens raw terminal channels for durably identified agents.
// Implementations must resolve and validate ref before opening any PTY or
// child process. The stream observes an agent; closing it never stops one.
type SessionStream interface {
	Open(ctx context.Context, ref AgentSessionRef, size TerminalSize) (SessionChannel, error)
}

// SessionChannel is one raw terminal byte stream. Read and Write do not parse
// or line-split data: ANSI/VT sequences and control bytes pass unchanged.
// Read returns context.Canceled when its context is canceled. A read deadline
// returns os.ErrDeadlineExceeded, whether the context deadline expires while
// waiting or was already expired when Read begins; callers should use
// errors.Is with that sentinel for read timeouts. A canceled or timed-out Read
// does not close or otherwise poison the channel.
type SessionChannel interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, input []byte) error
	Resize(ctx context.Context, size TerminalSize) error
	Close(ctx context.Context) error
}

func sessionStreamReadContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return os.ErrDeadlineExceeded
	}
	return err
}

var errSessionChannelClosed = errors.New("session channel is closed")

func sessionChannelClosedError(operation string) error {
	return observability.WrapError(
		observability.CodeStateConflict,
		"session channel is closed; cannot "+operation,
		errSessionChannelClosed,
	)
}

var _ SessionStream = (*FakeSessionStream)(nil)
var _ SessionChannel = (*FakeSessionChannel)(nil)
