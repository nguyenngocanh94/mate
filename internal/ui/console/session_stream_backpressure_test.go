package console

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// orderedChannel emits a strictly increasing sequence in small chunks as fast
// as the consumer will take them, then EOF.
type orderedChannel struct {
	mu     sync.Mutex
	i      int
	n      int
	closed bool
}

func (c *orderedChannel) Read(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.i >= c.n {
		return nil, io.EOF
	}
	// Deliberately split across chunk boundaries at odd sizes.
	s := fmt.Sprintf("%d,", c.i)
	c.i++
	return []byte(s), nil
}
func (c *orderedChannel) Write(context.Context, []byte) error        { return nil }
func (c *orderedChannel) Resize(context.Context, TerminalSize) error { return nil }
func (c *orderedChannel) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// TestStreamNeverDropsOrReordersBytesUnderASlowRenderer is the read path's
// backpressure contract: the controller may coalesce frames, but every byte
// the channel produced must reach the buffer exactly once and in order. It
// runs the real streamSession + Model loop and renders slowly between updates
// so the channel is always pushing more than one chunk ahead. The terminal is
// sized to hold the whole sequence on the visible screen, which is what makes
// the assertion exact: a dropped byte merges two numbers, and a reordered one
// breaks the run.
func TestStreamNeverDropsOrReordersBytesUnderASlowRenderer(t *testing.T) {
	const n = 4000
	ch := &orderedChannel{n: n}
	factory := func(context.Context, SessionTarget, TerminalSize) (SessionChannel, error) { return ch, nil }
	reader := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := streamControllerFixture(t, factory, reader.Read)

	// A window tall enough for the whole increasing sequence, so nothing is
	// lost to the viewport and the check can require all n values.
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 400, Height: 400})
	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd())
	if m.sess.stream == nil {
		t.Fatal("stream did not open")
	}
	stream := m.sess.stream

	frames := 0
	deadline := time.Now().Add(30 * time.Second)
	for cmd != nil && time.Now().Before(deadline) {
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			cmd = b[0]
			continue
		}
		var next tea.Cmd
		m, next = send(t, m, msg)
		// Simulate an expensive render on every message.
		time.Sleep(300 * time.Microsecond)
		frames++
		if m.sess.terminal == nil || m.sess.stream != stream {
			break
		}
		cmd = next
	}
	t.Logf("frames rendered = %d for %d produced chunks", frames, n)

	screen := dumpTerminal(stream.buffer)
	flat := strings.ReplaceAll(screen, "\n", "")
	flat = strings.ReplaceAll(flat, " ", "")
	var values []int
	for _, f := range strings.Split(flat, ",") {
		if f == "" {
			continue
		}
		v, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("byte stream corrupt: %q in the rendered buffer", f)
		}
		values = append(values, v)
	}
	if len(values) != n {
		t.Fatalf("rendered buffer holds %d values, want %d (bytes were dropped or merged)", len(values), n)
	}
	for i, v := range values {
		if v != i {
			t.Fatalf("byte stream out of order at %d: got %d (bytes were reordered or dropped)", i, v)
		}
	}
}

func dumpTerminal(b *TerminalBuffer) string {
	f := b.Snapshot()
	var sb strings.Builder
	for y := 0; y < f.Height; y++ {
		for x := 0; x < f.Width && x < len(f.Cells[y]); {
			c := f.Cells[y][x]
			if c.Width <= 0 {
				x++
				continue
			}
			if c.Content == "" {
				sb.WriteString(" ")
			} else {
				sb.WriteString(c.Content)
			}
			x += c.Width
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
