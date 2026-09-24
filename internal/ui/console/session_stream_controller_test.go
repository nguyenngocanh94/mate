package console

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nguyenngocanh94/mate/internal/query"
)

type controllerTestChannel struct {
	mu       sync.Mutex
	output   [][]byte
	closed   bool
	closeN   int
	readDone chan struct{}
	writes   [][]byte
	writeErr error
	resizes  []TerminalSize
}

func (c *controllerTestChannel) Read(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, io.EOF
	}
	if len(c.output) == 0 {
		return nil, io.EOF
	}
	out := append([]byte(nil), c.output[0]...)
	c.output = c.output[1:]
	if c.readDone != nil {
		close(c.readDone)
		c.readDone = nil
	}
	return out, nil
}

func (c *controllerTestChannel) Write(_ context.Context, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, append([]byte(nil), data...))
	return c.writeErr
}

func (c *controllerTestChannel) writtenBytes() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.writes))
	copy(out, c.writes)
	return out
}

func (c *controllerTestChannel) Resize(_ context.Context, size TerminalSize) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resizes = append(c.resizes, size)
	return nil
}

func (c *controllerTestChannel) resizedTo() []TerminalSize {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]TerminalSize(nil), c.resizes...)
}

// isClosed reports whether Close has run, under the channel's own lock:
// the close can come from the controller's goroutine, so a test that read
// the field directly would race it.
func (c *controllerTestChannel) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *controllerTestChannel) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.closeN++
	}
	return nil
}

type controllerTestFactory struct {
	mu       sync.Mutex
	opens    int
	channels []*controllerTestChannel
	err      error
}

func (f *controllerTestFactory) Open(context.Context, SessionTarget, TerminalSize) (SessionChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens++
	if f.err != nil {
		return nil, f.err
	}
	word := "old"
	if f.opens > 1 {
		word = "fresh"
	}
	channel := &controllerTestChannel{output: [][]byte{[]byte(word + "\n")}}
	f.channels = append(f.channels, channel)
	return channel, nil
}

func streamControllerFixture(t *testing.T, factory SessionStreamFactory, reader SessionReader) Model {
	t.Helper()
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := New(spy.load, spy.attach).WithSession(reader, nil, nil).WithSessionStream(factory)
	m.g = unicodeGlyphs
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 48})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter"))
	return m
}

func TestStreamControllerMakesPTYOutputThePrimaryFrameSource(t *testing.T) {
	factory := &controllerTestFactory{}
	reader := func(context.Context, SessionTarget) (SessionSnapshot, error) {
		return SessionSnapshot{}, errors.New("snapshot reader must be fallback only")
	}
	m := streamControllerFixture(t, factory.Open, reader)

	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter did not start stream open")
	}
	m, cmd = send(t, m, cmd())
	if cmd == nil {
		t.Fatal("successful stream open did not start its reader")
	}
	m, _ = send(t, m, cmd())
	if !strings.Contains(stripANSI(m.View()), "old") {
		t.Fatalf("Agent View did not render PTY output:\n%s", m.View())
	}
	if factory.opens != 1 {
		t.Fatalf("stream opens = %d, want 1", factory.opens)
	}
}

func TestStreamFailureFallsBackToSnapshotWithoutRemovingAttachFallback(t *testing.T) {
	factory := &controllerTestFactory{err: errors.New("herdr unavailable")}
	reader := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
		Transcript:     SessionTranscript{Raw: "snapshot fallback", Status: SessionTranscriptUnknown},
	}}
	m := streamControllerFixture(t, factory.Open, reader.Read)

	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd())
	if cmd == nil {
		t.Fatal("stream failure did not request snapshot fallback")
	}
	m, cmd = send(t, m, cmd())
	if cmd == nil {
		t.Fatal("snapshot fallback did not return its result")
	}
	m, _ = send(t, m, cmd())
	if !strings.Contains(stripANSI(m.View()), "snapshot fallback") {
		t.Fatalf("snapshot fallback was not rendered:\n%s", m.View())
	}
}

func TestStreamMetadataNeverReplacesTerminalBuffer(t *testing.T) {
	factory := &controllerTestFactory{}
	metadata := func(context.Context, SessionTarget) (SessionSnapshot, error) {
		return SessionSnapshot{
			RecordedStatus: query.KnownField("running"),
			Runtime:        SessionRuntime{Status: query.Known},
			Transcript:     SessionTranscript{Raw: "must not replace PTY"},
		}, nil
	}
	m := streamControllerFixture(t, factory.Open, nil).WithSessionStream(factory.Open, metadata)
	// Re-enter through the fixture's selected Mate row with the metadata seam
	// installed on the model copy.
	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd())
	if cmd == nil {
		t.Fatal("successful stream open did not start its reader")
	}
	m, _ = send(t, m, cmd())
	metadataCmd := sessionStreamMetadataTickCmd(0, m.sess.gen)
	m, metadataCmd = send(t, m, metadataCmd())
	if metadataCmd == nil {
		t.Fatal("stream metadata tick did not start a side-channel read")
	}
	m, _ = send(t, m, metadataCmd())
	if strings.Contains(stripANSI(m.View()), "must not replace PTY") {
		t.Fatalf("metadata transcript replaced the stream frame:\n%s", m.View())
	}
}

func TestStreamRendererTrustsWideCellWidthWhenContentIsConcealed(t *testing.T) {
	frame := TerminalSnapshot{
		Width:  3,
		Height: 1,
		Cells: [][]TerminalCell{{
			{Content: " ", Width: 2}, // SGR 8 wide glyph after conceal redaction.
			{Width: 0},
			{Content: "x", Width: 1},
		}},
	}
	if got := terminalSnapshotLines(frame)[0].render(3); got != "  x" {
		t.Fatalf("concealed wide cell shifted the following cell: got %q, want %q", got, "  x")
	}
}

func TestStreamRendererCoalescesIdenticalStyleRuns(t *testing.T) {
	b := NewTerminalBuffer(12, 1)
	_, _ = b.Write([]byte("\x1b[?25lhello world"))

	lines := terminalSnapshotLines(b.Snapshot())
	if got := len(lines[0].spans); got != 1 {
		t.Fatalf("identical terminal cells produced %d spans, want one coalesced run", got)
	}
	if got := lines[0].render(12); got != "hello world " {
		t.Fatalf("coalesced stream line = %q, want %q", got, "hello world ")
	}
}

func TestStaleStreamReadCannotRenderAfterReentry(t *testing.T) {
	factory := &controllerTestFactory{}
	reader := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := streamControllerFixture(t, factory.Open, reader.Read)

	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd())
	staleRead := cmd
	oldGeneration := m.sess.gen
	// Esc is forwarded to the agent while the terminal zone has focus
	// (session-view-contract.md, "Esc semantics invert"), so this fixture
	// leaves the way a real reader does: F2 to the box, then Esc.
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	m, closeCmd := send(t, m, key("esc"))
	if closeCmd != nil {
		m, _ = send(t, m, closeCmd())
	}
	m, cmd = send(t, m, key("enter"))
	m, cmd = send(t, m, cmd())
	if m.sess.gen == oldGeneration {
		t.Fatalf("re-entry reused stream generation %d", oldGeneration)
	}
	// Complete the old read after the new stream has opened. Its bytes must be
	// discarded by the generation fence, even though the command completed.
	oldMsg := staleRead()
	m, _ = send(t, m, oldMsg)
	if strings.Contains(stripANSI(m.View()), "old") {
		t.Fatalf("stale stream output crossed the generation fence:\n%s", m.View())
	}
}

func stripANSI(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\x1b", ""), "[", "")
}
