package console

import (
	"context"
	"errors"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	// sessionStreamOutputCapacity is deliberately finite. The reader blocks
	// when the Console cannot keep up, which applies backpressure to the PTY
	// without ever dropping or reordering raw bytes.
	sessionStreamOutputCapacity = 32
	sessionMetadataInterval     = time.Second
	sessionStreamCloseTimeout   = 2 * time.Second
	// sessionStreamInputCapacity bounds how many encoded keystrokes/mouse
	// events can be queued ahead of writeLoop actually draining them to the
	// PTY. Interactive typing never approaches this; it exists only so a
	// stuck transport (one whose Write is blocking) cannot make enqueueWrite
	// block the Bubble Tea event loop - Update must never block on I/O. A
	// full queue drops the newest event rather than the oldest, matching
	// how a real terminal's own kernel tty buffer behaves under the same
	// pressure, and a transport stuck long enough to fill it will already be
	// failing its own Read soon after (the same fd), which is what actually
	// triggers the stream→snapshot fallback.
	sessionStreamInputCapacity = 256
)

type streamChunk struct {
	bytes []byte
	err   error
}

// streamSession owns one opened channel, its VT buffer, exactly one reader
// goroutine and exactly one writer goroutine. The bounded chunks channel is
// the read-side hand-off between the reader and Bubble Tea; the bounded
// writes channel is its input-side counterpart. Redraw coalescing happens
// by draining already-ready chunks in onSessionStreamChunk; the byte stream
// itself is never coalesced or reordered in either direction.
//
// Input ordering is why writeLoop exists at all, rather than each key/mouse
// event writing to the channel directly from its own tea.Cmd: Bubble Tea
// 1.2.4 runs every Cmd in its own unsynchronised goroutine (tea.go's
// handleCommands, "we'll have to leak the goroutine until Cmd returns" - no
// ordering is promised between two such goroutines even when the Cmds were
// produced in order). Two keystrokes typed in order could otherwise reach
// the PTY out of order under any scheduling delay, which is silent
// corruption for an interactive terminal (a reordered Enter submits a
// partial line; a reordered Esc lands mid-escape-sequence). enqueueWrite is
// called synchronously from Model.Update - which Bubble Tea itself only
// ever calls from its single event-loop goroutine - so two calls to it are
// always ordered; writeLoop then drains that queue with the one goroutine
// that ever calls channel.Write, so program order is preserved end to end.
type streamSession struct {
	channel SessionChannel
	buffer  *TerminalBuffer

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	chunks chan streamChunk
	writes chan []byte
	// writeErr carries a fatal write failure to next()'s own select
	// (below), the same way ctx.Done() and s.chunks already do, rather than
	// through the chunks channel itself: chunks is closed exactly once, by
	// readLoop, when the read side naturally ends, and writeLoop closing or
	// sending on it too would race that close. writeErr is never closed -
	// it is written to at most once (buffer of 1) and simply becomes
	// eligible for GC with the rest of the streamSession.
	writeErr chan error
	wg       sync.WaitGroup

	mu     sync.Mutex
	closed bool
	// pendingErr is held behind the final byte batch so a read that returns
	// data followed by EOF/error gets one render opportunity. The bytes are
	// visible before the controller falls back, while the error still remains
	// ordered after them.
	pendingErr error
}

func newStreamSession(ctx context.Context, channel SessionChannel, size TerminalSize) *streamSession {
	streamCtx, cancel := context.WithCancel(ctx)
	s := &streamSession{
		channel:  channel,
		buffer:   NewTerminalBuffer(size.Cols, size.Rows),
		ctx:      streamCtx,
		cancel:   cancel,
		done:     make(chan struct{}),
		chunks:   make(chan streamChunk, sessionStreamOutputCapacity),
		writes:   make(chan []byte, sessionStreamInputCapacity),
		writeErr: make(chan error, 1),
	}
	s.wg.Add(2)
	go s.readLoop()
	go s.writeLoop()
	return s
}

// enqueueWrite hands one already-encoded key/mouse event to writeLoop,
// preserving the order Update called it in. It never blocks: a full queue
// (sessionStreamInputCapacity, an interactive session never approaches it)
// drops the newest event rather than stalling the Bubble Tea event loop on
// I/O, and a session already closing refuses rather than racing close's own
// shutdown of the writes channel.
func (s *streamSession) enqueueWrite(data []byte) {
	// The whole check-then-send stays under s.mu, matching close()'s own
	// critical section: close() also closes s.writes under s.mu, and a send
	// on a channel that closes between this method's closed-check and its
	// channel send would panic - checking and sending outside one lock is
	// exactly that race.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.writes <- data:
	default:
	}
}

// writeLoop is the sole caller of channel.Write, so every enqueueWrite call
// (however many goroutines called it - in practice always the single
// Bubble Tea event-loop goroutine) is applied to the PTY in the order it
// was enqueued. A write failure means the transport is gone; next() picks
// it up the same poll cycle it would have picked up a read failure, so a
// broken write and a broken read share the one fallback path
// (onSessionStreamChunk -> beginStreamFallback) instead of two.
func (s *streamSession) writeLoop() {
	defer s.wg.Done()
	for data := range s.writes {
		if err := s.channel.Write(s.ctx, data); err != nil {
			select {
			case s.writeErr <- err:
			default:
			}
			return
		}
	}
}

func (s *streamSession) readLoop() {
	defer s.wg.Done()
	defer close(s.chunks)
	for {
		data, err := s.channel.Read(s.ctx)
		if len(data) > 0 {
			if !s.sendChunk(streamChunk{bytes: append([]byte(nil), data...)}) {
				return
			}
		}
		if err != nil {
			_ = s.sendChunk(streamChunk{err: err})
			return
		}
	}
}

func (s *streamSession) sendChunk(chunk streamChunk) bool {
	select {
	case s.chunks <- chunk:
		return true
	case <-s.done:
		return false
	}
}

func (s *streamSession) next(ctx context.Context) (streamChunk, bool) {
	s.mu.Lock()
	if s.pendingErr != nil {
		err := s.pendingErr
		s.pendingErr = nil
		s.mu.Unlock()
		return streamChunk{err: err}, true
	}
	s.mu.Unlock()
	select {
	case chunk, ok := <-s.chunks:
		return chunk, ok
	case err := <-s.writeErr:
		return streamChunk{err: err}, true
	case <-ctx.Done():
		return streamChunk{err: ctx.Err()}, true
	}
}

func (s *streamSession) deferError(err error) {
	s.mu.Lock()
	s.pendingErr = err
	s.mu.Unlock()
}

// close is safe to call repeatedly. The first caller closes the transport,
// then waits for the reader to observe that close. A fresh timeout is used so
// a cancelled Console context cannot turn a real child/PTY reap into an
// abandoned goroutine.
func (s *streamSession) close(_ context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return nil
	}
	s.closed = true
	close(s.done)
	close(s.writes)
	s.cancel()
	s.mu.Unlock()

	closeCtx, cancel := context.WithTimeout(context.Background(), sessionStreamCloseTimeout)
	defer cancel()
	closeErr := s.channel.Close(closeCtx)

	waitDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-closeCtx.Done():
		if closeErr == nil {
			closeErr = closeCtx.Err()
		}
	}
	return closeErr
}

func closeUnownedSessionChannelCmd(channel SessionChannel, gen int) tea.Cmd {
	return func() tea.Msg {
		return sessionStreamClosedMsg{gen: gen, err: closeSessionChannel(channel)}
	}
}

func closeSessionChannel(channel SessionChannel) error {
	ctx, cancel := context.WithTimeout(context.Background(), sessionStreamCloseTimeout)
	defer cancel()
	return channel.Close(ctx)
}

func (s *streamSession) resize(ctx context.Context, size TerminalSize) error {
	if err := s.channel.Resize(ctx, size); err != nil {
		return err
	}
	return nil
}

type sessionStreamOpenedMsg struct {
	gen     int
	channel SessionChannel
	err     error
}

type sessionStreamChunkMsg struct {
	gen    int
	stream *streamSession
	chunk  streamChunk
}

type sessionStreamMetadataMsg struct {
	gen      int
	snapshot SessionSnapshot
	err      error
}

type sessionStreamMetadataTickMsg struct{ gen int }

type sessionStreamResizedMsg struct {
	gen int
	err error
}

type sessionStreamClosedMsg struct {
	gen int
	err error
}

func sessionStreamOpenCmd(ctx context.Context, factory SessionStreamFactory, target SessionTarget, size TerminalSize, gen int) tea.Cmd {
	return func() tea.Msg {
		if factory == nil {
			return sessionStreamOpenedMsg{gen: gen, err: errors.New("session stream is unavailable")}
		}
		channel, err := factory(ctx, target, size)
		if err == nil && channel != nil && ctx.Err() != nil {
			_ = closeSessionChannel(channel)
			return sessionStreamOpenedMsg{gen: gen, err: ctx.Err()}
		}
		return sessionStreamOpenedMsg{gen: gen, channel: channel, err: err}
	}
}

func sessionStreamReadCmd(ctx context.Context, stream *streamSession, gen int) tea.Cmd {
	return func() tea.Msg {
		chunk, _ := stream.next(ctx)
		return sessionStreamChunkMsg{gen: gen, stream: stream, chunk: chunk}
	}
}

func sessionStreamMetadataCmd(ctx context.Context, reader SessionMetadataReader, target SessionTarget, gen int) tea.Cmd {
	return func() tea.Msg {
		if reader == nil {
			return sessionStreamMetadataMsg{gen: gen}
		}
		snapshot, err := reader(ctx, target)
		return sessionStreamMetadataMsg{gen: gen, snapshot: snapshot, err: err}
	}
}

func sessionStreamMetadataTickCmd(interval time.Duration, gen int) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return sessionStreamMetadataTickMsg{gen: gen}
	})
}

func sessionStreamResizeCmd(ctx context.Context, stream *streamSession, size TerminalSize, gen int) tea.Cmd {
	return func() tea.Msg {
		return sessionStreamResizedMsg{gen: gen, err: stream.resize(ctx, size)}
	}
}

func sessionStreamCloseCmd(stream *streamSession, gen int) tea.Cmd {
	return func() tea.Msg {
		return sessionStreamClosedMsg{gen: gen, err: stream.close(context.Background())}
	}
}
