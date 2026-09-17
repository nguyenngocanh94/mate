package console

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/query"
)

// blockingChannel is a stream channel that never produces output on its own.
// Tests inject sessionStreamChunkMsg values directly instead of racing a real
// read loop, so exactly when bytes and failures arrive is under test control.
// It records the sizes the opener and every Resize reported, which is how the
// banner test checks the agent actually learns about a shrink.
type blockingChannel struct {
	mu      sync.Mutex
	opens   []TerminalSize
	resizes []TerminalSize
}

func (c *blockingChannel) Read(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (c *blockingChannel) Write(context.Context, []byte) error { return nil }
func (c *blockingChannel) Resize(_ context.Context, s TerminalSize) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resizes = append(c.resizes, s)
	return nil
}
func (c *blockingChannel) Close(context.Context) error { return nil }

func (c *blockingChannel) recordOpen(s TerminalSize) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opens = append(c.opens, s)
}

func (c *blockingChannel) lastResize() (TerminalSize, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.resizes) == 0 {
		return TerminalSize{}, false
	}
	return c.resizes[len(c.resizes)-1], true
}

// waitUntil polls cond for up to two seconds. It exists for assertions about
// work a tea.Batch schedules on its own goroutine (sessionStreamResizeCmd
// reaching the channel), never for the model's own synchronous state.
func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

// runBatchCmds starts every non-nil Cmd a handler returned on its own
// goroutine. tea.Batch's second Cmd here is always
// sessionStreamMetadataTickCmd, which is a tea.Tick that blocks for the real
// metadata interval; running it inline would stall the test.
func runBatchCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return
	}
	for _, c := range batch {
		if c != nil {
			go func(c tea.Cmd) { _ = c() }(c)
		}
	}
}

// rowsDrawn counts how many of the buffer's ROWnn markers appear in the frame
// and reports the first one, so a test can tell a head crop (ROW00 kept) from
// a tail crop (ROW00 gone).
func rowsDrawn(view string, rows int) (first, count int) {
	first = -1
	for y := 0; y < rows; y++ {
		if strings.Contains(view, fmt.Sprintf("ROW%02d", y)) {
			if first < 0 {
				first = y
			}
			count++
		}
	}
	return first, count
}

func paintRows(rows int) []byte {
	var sb strings.Builder
	sb.WriteString("\x1b[2J\x1b[H")
	for y := 0; y < rows; y++ {
		sb.WriteString(fmt.Sprintf("\x1b[%d;1HROW%02d", y+1, y))
	}
	return []byte(sb.String())
}

// streamMateFixture opens a 160x48 Mate stream over a blocking channel and
// returns the model, the channel and the pane height the opener asked for.
func streamMateFixture(t *testing.T, metadata SessionMetadataReader) (Model, *blockingChannel, int) {
	t.Helper()
	ch := &blockingChannel{}
	factory := func(_ context.Context, _ SessionTarget, s TerminalSize) (SessionChannel, error) {
		ch.recordOpen(s)
		return ch, nil
	}
	reader := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
		Transcript:     SessionTranscript{Raw: "snapshot fallback", Status: SessionTranscriptUnknown},
	}}
	m := streamControllerFixture(t, factory, reader.Read)
	m = m.WithSessionStream(factory, metadata)
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd()) // opened
	if m.sess.phase != sessionActive || m.sess.stream == nil {
		t.Fatalf("stream did not open: phase=%v stream=%v", m.sess.phase, m.sess.stream)
	}
	return m, ch, m.sess.terminal.Height()
}

func injectBytes(t *testing.T, m Model, data []byte) Model {
	t.Helper()
	m, _ = send(t, m, sessionStreamChunkMsg{gen: m.sess.gen, stream: m.sess.stream,
		chunk: streamChunk{bytes: data}})
	return m
}

// The banner that appears mid-session is the transition that produced B2:
// the agent was already painting a full screen when the runtime banner
// arrived. The emulator must shrink to match the space left, the agent must
// be told, and no row may be silently dropped off the top of the frame.
func TestBannerAppearingMidStreamNeverCropsTheAgentsTopRow(t *testing.T) {
	gone := SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Absent, Reason: "agent_not_found"},
	}
	m, ch, openedRows := streamMateFixture(t, func(context.Context, SessionTarget) (SessionSnapshot, error) {
		return gone, nil
	})
	if openedRows != 45 {
		t.Fatalf("buffer opened at %d rows, want 45 (160x48 Mate frame, no banner)", openedRows)
	}

	m = injectBytes(t, m, paintRows(openedRows))
	view := stripANSI(m.View())
	first, count := rowsDrawn(view, openedRows)
	if first != 0 || count != openedRows {
		t.Fatalf("before the banner: first drawn row = ROW%02d, %d of %d rows drawn\n%s", first, count, openedRows, view)
	}

	// The agent disappears; metadata reports it and the banner appears.
	m, cmd := send(t, m, sessionStreamMetadataMsg{gen: m.sess.gen, snapshot: gone})
	runBatchCmds(cmd)
	waitUntil(t, func() bool { _, ok := ch.lastResize(); return ok })

	view = stripANSI(m.View())
	if !strings.Contains(view, "runtime_missing") {
		t.Fatalf("the runtime banner did not appear:\n%s", view)
	}
	bufRows := m.sess.terminal.Height()
	first, count = rowsDrawn(view, openedRows)
	if count != bufRows {
		t.Errorf("CROP: the buffer holds %d rows but only %d are drawn - %d row(s) fell off the frame",
			bufRows, count, bufRows-count)
	}
	if first != 0 {
		t.Errorf("CROP: the agent's top row (ROW00) is gone; the frame starts at ROW%02d", first)
	}
	if bufRows != openedRows-1 {
		t.Errorf("buffer height after one banner = %d, want %d (one row reserved for the banner)", bufRows, openedRows-1)
	}
	if got, ok := ch.lastResize(); !ok || got.Rows != openedRows-1 {
		t.Errorf("the agent was not told about its new height: last resize = %+v (seen=%v)", got, ok)
	}
}

// TestFrozenFallbackFrameCropsTheTailNotTheHead is the user-visible half of
// the fallback defect: when the stream dies and the notice banner takes a
// frame row, the last live screen is one row taller than the frame can show.
// The buffer must NOT be resized down (Resize drops the bottom row before the
// reader sees it) and the renderer must keep the HEAD of the frozen screen
// (the agent's first line), not its tail. Re-adding the resize makes ROW44
// disappear from the buffer; cropping the tail instead makes ROW00 disappear
// from the frame - this test fails on either.
func TestFrozenFallbackFrameCropsTheTailNotTheHead(t *testing.T) {
	m, _, openedRows := streamMateFixture(t, nil)
	m = injectBytes(t, m, paintRows(openedRows))
	if got := m.sess.terminal.Height(); got != openedRows {
		t.Fatalf("buffer height before death = %d, want %d", got, openedRows)
	}

	m, _ = send(t, m, sessionStreamChunkMsg{gen: m.sess.gen, stream: m.sess.stream,
		chunk: streamChunk{err: observability.NewError(observability.CodeRuntimeUnavailable, "herdr: server shut down")}})
	if m.sess.phase != sessionFallback {
		t.Fatalf("phase = %v, want sessionFallback", m.sess.phase)
	}
	if got := m.sess.terminal.Height(); got != openedRows {
		t.Fatalf("the fallback resized the frozen buffer to %d rows, want it untouched at %d (a Resize drops the bottom row)", got, openedRows)
	}

	view := stripANSI(m.View())
	if !strings.Contains(view, "runtime_unavailable") {
		t.Fatalf("the operator is not told the taxonomy during the fallback:\n%s", view)
	}
	first, count := rowsDrawn(view, openedRows)
	if first != 0 {
		t.Errorf("CROP: the notice banner pushed the frozen frame's TOP ROW off the screen - first drawn row = ROW%02d\n%s", first, view)
	}
	if count != openedRows-1 {
		t.Errorf("count = %d drawn rows, want %d (the frozen buffer minus the one banner row)", count, openedRows-1)
	}
	if strings.Contains(view, fmt.Sprintf("ROW%02d", openedRows-1)) {
		t.Errorf("the last buffer row (ROW%02d) is still drawn; the head crop should have dropped it", openedRows-1)
	}
}

// TestHerdrDeathKeepsAUsableViewAndNamesTheTaxonomy is the original first-
// review scenario kept whole: the operator must keep a usable view of what the
// agent last printed, be told the taxonomy, and then have the snapshot take
// over while still naming the reason.
func TestHerdrDeathKeepsAUsableViewAndNamesTheTaxonomy(t *testing.T) {
	m, _, _ := streamMateFixture(t, nil)
	m = injectBytes(t, m, []byte("TOP ROW MARKER\r\nSECOND ROW MARKER"))
	if !strings.Contains(stripANSI(m.View()), "SECOND ROW MARKER") {
		t.Fatalf("stream content not on screen:\n%s", stripANSI(m.View()))
	}

	m, cmd := send(t, m, sessionStreamChunkMsg{gen: m.sess.gen, stream: m.sess.stream,
		chunk: streamChunk{err: observability.NewError(observability.CodeRuntimeUnavailable,
			"herdr: server shut down: server is shutting down")}})
	if m.sess.phase != sessionFallback {
		t.Fatalf("phase = %v, want sessionFallback", m.sess.phase)
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "runtime_unavailable") {
		t.Errorf("the operator is not told the taxonomy during the fallback:\n%s", view)
	}
	if !strings.Contains(view, "SECOND ROW MARKER") {
		t.Errorf("the last live frame was cleared instead of kept during the fallback")
	}
	if !strings.Contains(view, "TOP ROW MARKER") {
		t.Errorf("CROP: the notice banner pushed the terminal's TOP ROW off the frame")
	}

	// The snapshot read the fallback scheduled now answers.
	if cmd == nil {
		t.Fatal("the fallback scheduled no snapshot read")
	}
	m, _ = send(t, m, cmd())
	view = stripANSI(m.View())
	if !strings.Contains(view, "snapshot fallback") {
		t.Errorf("snapshot fallback did not take over:\n%s", view)
	}
	if !strings.Contains(view, "runtime_unavailable") {
		t.Errorf("the reason was dropped once the snapshot took over:\n%s", view)
	}
}
