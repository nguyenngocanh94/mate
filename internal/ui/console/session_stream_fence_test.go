package console

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// TestStaleMetadataMustNotOverwriteTheNewSession is what makes the generation
// fence in onSessionStreamMetadata non-deletable. A metadata message carries
// no stream pointer - only a gen - so the other guards (phase, stream != nil,
// !fallback) all pass for a message scheduled during a previous session while
// the reader detaches and re-enters inside the metadata interval. Without the
// gen check the stale box/runtime the first session reported is applied over
// the second session's frame, and resizeStreamForReserve then sizes the new
// PTY from the old target's chrome. The message is real: the chain restarts on
// every entry (beginSession), so a tick from the departed session is still in
// flight.
func TestStaleMetadataMustNotOverwriteTheNewSession(t *testing.T) {
	factory := &controllerTestFactory{}
	reader := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
		Transcript:     SessionTranscript{Raw: "snapshot fallback", Status: SessionTranscriptUnknown},
	}}
	m := streamControllerFixture(t, factory.Open, reader.Read)

	// Session 1.
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd())
	if m.sess.stream == nil {
		t.Fatal("stream 1 did not open")
	}
	staleGen := m.sess.gen
	staleSnapshot := SessionSnapshot{
		Target:         m.sess.target,
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Absent, Reason: "session-one-agent-gone"},
		Box:            awaitingBox(4),
	}

	// Leave (F2 to the box, then Esc) and re-enter: session 2 is live and
	// healthy.
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	m, closeCmd := send(t, m, key("esc"))
	if closeCmd == nil {
		t.Fatal("detach did not schedule a close")
	}
	m, _ = send(t, m, closeCmd())
	m, cmd = send(t, m, key("enter"))
	m, _ = send(t, m, cmd())
	if m.sess.phase != sessionActive || m.sess.stream == nil {
		t.Fatalf("session 2 not active: phase=%v stream=%v", m.sess.phase, m.sess.stream)
	}
	if m.sess.gen == staleGen {
		t.Fatal("re-entering did not advance the generation; the test cannot distinguish a stale message")
	}

	// Give session 2 a healthy poll for its own generation, so the assertion
	// below is about the stale message rather than about the empty placeholder
	// a freshly opened session starts from.
	healthy := SessionSnapshot{
		Target:         m.sess.target,
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
	}
	m, _ = send(t, m, sessionStreamMetadataMsg{gen: m.sess.gen, snapshot: healthy})
	if m.sess.snapshot.Runtime.Status != query.Known {
		t.Fatalf("session 2's own metadata was not applied: %+v", m.sess.snapshot.Runtime)
	}

	m, cmd = send(t, m, sessionStreamMetadataMsg{gen: staleGen, snapshot: staleSnapshot})

	if m.sess.gen == staleGen {
		t.Fatalf("a stale metadata message was accepted: generation rolled back to %d", staleGen)
	}
	if m.sess.snapshot.Runtime.Status != query.Known {
		t.Fatalf("a stale metadata message overwrote the live runtime: %+v", m.sess.snapshot.Runtime)
	}
	if m.sess.snapshot.ControllerNotice != "" {
		t.Errorf("a stale metadata message raised a banner on the live session: %q", m.sess.snapshot.ControllerNotice)
	}
	if len(m.sess.snapshot.Box.Value.Entries) != 0 {
		t.Errorf("a stale metadata message adopted the previous session's box: %+v", m.sess.snapshot.Box)
	}
	if cmd != nil {
		t.Fatalf("a stale metadata message scheduled work: %T", cmd())
	}
	if containsLine(stripANSI(m.View()), "runtime_missing") {
		t.Errorf("the stale runtime banner reached the live frame:\n%s", stripANSI(m.View()))
	}
}

// A stale chunk that carries an ERROR rather than bytes is the other half:
// apply() returns the error and beginStreamFallback tears the whole session
// down. The chunk path additionally compares the stream pointer, so this test
// survives deleting the gen check alone - it is the stream-identity guard's
// case, kept so that guard cannot be removed either.
func TestStaleStreamErrorMustNotCollapseTheNewSession(t *testing.T) {
	factory := &controllerTestFactory{}
	reader := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
		Transcript:     SessionTranscript{Raw: "snapshot fallback", Status: SessionTranscriptUnknown},
	}}
	m := streamControllerFixture(t, factory.Open, reader.Read)

	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd())
	stream1 := m.sess.stream
	gen1 := m.sess.gen
	if stream1 == nil {
		t.Fatal("stream 1 did not open")
	}

	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	m, closeCmd := send(t, m, key("esc"))
	if closeCmd == nil {
		t.Fatal("detach did not schedule a close")
	}
	m, _ = send(t, m, closeCmd())
	m, cmd = send(t, m, key("enter"))
	m, _ = send(t, m, cmd())
	if m.sess.phase != sessionActive || m.sess.stream == nil {
		t.Fatalf("session 2 not active: phase=%v stream=%v", m.sess.phase, m.sess.stream)
	}
	gen2 := m.sess.gen

	m, cmd = send(t, m, sessionStreamChunkMsg{gen: gen1, stream: stream1, chunk: streamChunk{err: errors.New("stream 1 ended")}})

	if m.sess.gen != gen2 {
		t.Fatalf("a stale stream error changed the live generation: %d -> %d", gen2, m.sess.gen)
	}
	if m.sess.phase != sessionActive {
		t.Fatalf("a stale stream error moved the live session out of sessionActive: phase=%v", m.sess.phase)
	}
	if m.sess.fallback {
		t.Fatal("a stale stream error pushed the live session into the snapshot fallback")
	}
	if m.sess.stream == nil {
		t.Fatal("a stale stream error closed the live stream")
	}
	if cmd != nil {
		t.Fatalf("a stale stream error scheduled work: %T", cmd())
	}
}
