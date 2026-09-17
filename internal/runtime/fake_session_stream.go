package runtime

import (
	"context"
	"io"
	"sync"
)

// FakeSessionScript is the deterministic behavior for one durably identified
// agent. Output is returned one byte slice at a time, preserving every byte.
// ReadErrors are consumed in order before scripted output is returned.
type FakeSessionScript struct {
	Output     [][]byte
	ReadErrors []error
	WriteErr   error
	ResizeErr  error
	CloseErr   error
}

// FakeSessionStream is a test double for SessionStream. A ref must first be
// seeded in Scripts; Open uses an exact session/name match and returns no
// channel for an unresolved target. The exported observations are snapshots
// of the fake's activity and are useful for asserting controller behavior.
type FakeSessionStream struct {
	mu sync.Mutex

	Scripts map[AgentSessionRef]FakeSessionScript

	// OpenErr is returned for a resolved target without creating a channel.
	OpenErr error
	// These errors apply to every channel unless that channel's script provides
	// its own operation error. Context cancellation remains authoritative.
	ReadErr   error
	WriteErr  error
	ResizeErr error
	CloseErr  error

	OpenCalls  []AgentSessionRef
	Channels   []*FakeSessionChannel
	Writes     [][]byte
	Resizes    []TerminalSize
	CloseCalls int
}

// FakeSessionChannel is the channel returned by FakeSessionStream.Open.
// Its observations are also available on the parent FakeSessionStream.
type FakeSessionChannel struct {
	mu sync.Mutex

	Ref AgentSessionRef

	parent      *FakeSessionStream
	output      [][]byte
	readErrors  []error
	writeErr    error
	resizeErr   error
	closeErr    error
	closed      bool
	closeResult error

	Writes     [][]byte
	Resizes    []TerminalSize
	CloseCalls int
}

// NewFakeSessionStream returns an empty stream with no resolvable targets.
func NewFakeSessionStream() *FakeSessionStream {
	return &FakeSessionStream{Scripts: make(map[AgentSessionRef]FakeSessionScript)}
}

// Seed installs a target and its scripted output. It returns a validation
// error rather than allowing a malformed identity into the fake's resolver.
func (f *FakeSessionStream) Seed(ref AgentSessionRef, output ...[]byte) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Scripts == nil {
		f.Scripts = make(map[AgentSessionRef]FakeSessionScript)
	}
	f.Scripts[ref] = FakeSessionScript{Output: cloneByteSlices(output)}
	return nil
}

// SetScript installs a complete script for a target. It is the convenient
// form for tests that need to inject operation-specific taxonomy errors.
func (f *FakeSessionStream) SetScript(ref AgentSessionRef, script FakeSessionScript) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Scripts == nil {
		f.Scripts = make(map[AgentSessionRef]FakeSessionScript)
	}
	f.Scripts[ref] = cloneSessionScript(script)
	return nil
}

// Open implements SessionStream. Resolution is an exact lookup performed
// before OpenErr is considered and before a FakeSessionChannel is allocated.
func (f *FakeSessionStream) Open(ctx context.Context, ref AgentSessionRef, size TerminalSize) (SessionChannel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if err := size.Validate(); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.OpenCalls = append(f.OpenCalls, ref)
	script, ok := f.Scripts[ref]
	if !ok {
		return nil, NewHerdrError(HerdrAgentNotFound, "agent target not found")
	}
	if err := MapSessionStreamError("open", f.OpenErr); err != nil {
		return nil, err
	}
	ch := &FakeSessionChannel{
		Ref:        ref,
		parent:     f,
		output:     cloneByteSlices(script.Output),
		readErrors: append([]error(nil), script.ReadErrors...),
		writeErr:   script.WriteErr,
		resizeErr:  script.ResizeErr,
		closeErr:   script.CloseErr,
	}
	f.Channels = append(f.Channels, ch)
	return ch, nil
}

// Read implements SessionChannel and returns the next scripted raw byte slice.
// Exhausted output is reported as io.EOF, as a real stream would report after
// its peer has closed it.
func (f *FakeSessionChannel) Read(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, sessionStreamReadContextError(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, sessionChannelClosedError("read")
	}
	if len(f.readErrors) > 0 {
		err := f.readErrors[0]
		f.readErrors = f.readErrors[1:]
		return nil, MapSessionStreamError("read", err)
	}
	if err := f.parent.readErr(); err != nil {
		return nil, err
	}
	if len(f.output) == 0 {
		return nil, io.EOF
	}
	out := append([]byte(nil), f.output[0]...)
	f.output = f.output[1:]
	return out, nil
}

// Write implements SessionChannel. It records and accepts arbitrary terminal
// bytes, including NULs, control bytes and ANSI/VT escape sequences.
func (f *FakeSessionChannel) Write(ctx context.Context, input []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return sessionChannelClosedError("write")
	}
	if err := MapSessionStreamError("write", f.writeErr); err != nil {
		return err
	}
	if err := f.parent.writeErr(); err != nil {
		return err
	}
	input = append([]byte(nil), input...)
	f.Writes = append(f.Writes, input)
	f.parent.recordWrite(input)
	return nil
}

// Resize implements SessionChannel. Repeating a resize is deliberately safe
// and remains observable; callers may use it to assert that resize handling is
// idempotent without the fake deduplicating controller requests.
func (f *FakeSessionChannel) Resize(ctx context.Context, size TerminalSize) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := size.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return sessionChannelClosedError("resize")
	}
	if err := MapSessionStreamError("resize", f.resizeErr); err != nil {
		return err
	}
	if err := f.parent.resizeErr(); err != nil {
		return err
	}
	f.Resizes = append(f.Resizes, size)
	f.parent.recordResize(size)
	return nil
}

// Close implements SessionChannel. The first call tears down the fake channel
// even when CloseErr is injected; later calls are no-ops returning the same
// result. No agent lifecycle operation is performed.
func (f *FakeSessionChannel) Close(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return f.closeResult
	}
	f.closed = true
	f.CloseCalls++
	f.parent.recordClose()
	f.closeResult = MapSessionStreamError("close", firstNonNil(f.closeErr, f.parent.closeErr()))
	return f.closeResult
}

func (f *FakeSessionStream) readErr() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return MapSessionStreamError("read", f.ReadErr)
}

func (f *FakeSessionStream) writeErr() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return MapSessionStreamError("write", f.WriteErr)
}

func (f *FakeSessionStream) resizeErr() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return MapSessionStreamError("resize", f.ResizeErr)
}

func (f *FakeSessionStream) closeErr() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return MapSessionStreamError("close", f.CloseErr)
}

func (f *FakeSessionStream) recordWrite(input []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Writes = append(f.Writes, append([]byte(nil), input...))
}

func (f *FakeSessionStream) recordResize(size TerminalSize) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Resizes = append(f.Resizes, size)
}

func (f *FakeSessionStream) recordClose() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CloseCalls++
}

func cloneSessionScript(script FakeSessionScript) FakeSessionScript {
	return FakeSessionScript{
		Output:     cloneByteSlices(script.Output),
		ReadErrors: append([]error(nil), script.ReadErrors...),
		WriteErr:   script.WriteErr,
		ResizeErr:  script.ResizeErr,
		CloseErr:   script.CloseErr,
	}
}

func cloneByteSlices(in [][]byte) [][]byte {
	if in == nil {
		return nil
	}
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = append([]byte(nil), in[i]...)
	}
	return out
}

func firstNonNil(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}
