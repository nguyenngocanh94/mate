package console

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// TestContextCancellationIsNotReportedAsAStreamFailure: the Console's own
// context ending (the reader quit, or the program is being torn down) makes
// the blocking PTY read return context.Canceled. That is not the stream
// failing, so it must not paint "Live stream unavailable; falling back to
// snapshot" over a session nobody is watching any more, and it must not
// schedule a snapshot read for a dead Console. The old code treated every
// error the same, so a quit raced against an in-flight read produced a false
// diagnosis in the log/frame.
func TestContextCancellationIsNotReportedAsAStreamFailure(t *testing.T) {
	m, _, _ := streamMateFixture(t, nil)

	m, cmd := send(t, m, sessionStreamChunkMsg{gen: m.sess.gen, stream: m.sess.stream,
		chunk: streamChunk{err: context.Canceled}})

	if m.sess.snapshot.ControllerNotice != "" {
		t.Errorf("a cancelled context was reported as a stream failure: %q", m.sess.snapshot.ControllerNotice)
	}
	if m.msg.text != "" {
		t.Errorf("a cancelled context wrote a message line: %q", m.msg.text)
	}
	if m.sess.fallback {
		t.Error("a cancelled context was pushed into the snapshot fallback")
	}
	if m.sess.stream != nil {
		t.Error("a cancelled context left the dead stream wired up")
	}
	if cmd != nil {
		t.Errorf("a cancelled context scheduled a snapshot read: %T", cmd())
	}
}

// TestRealStreamFailureStillFallsBack is the guard against the cancellation
// special-case swallowing the failure it sits beside: a coded runtime error
// must still take the fallback path with its taxonomy named.
func TestRealStreamFailureStillFallsBack(t *testing.T) {
	m, _, _ := streamMateFixture(t, nil)
	m, cmd := send(t, m, sessionStreamChunkMsg{gen: m.sess.gen, stream: m.sess.stream,
		chunk: streamChunk{err: observability.NewError(observability.CodeRuntimeUnavailable, "herdr: server shut down")}})
	if m.sess.phase != sessionFallback {
		t.Fatalf("phase = %v, want sessionFallback", m.sess.phase)
	}
	if !containsSubstring(m.sess.snapshot.ControllerNotice, "runtime_unavailable") {
		t.Errorf("the taxonomy is missing from the notice: %q", m.sess.snapshot.ControllerNotice)
	}
	if cmd == nil {
		t.Error("a real stream failure scheduled no snapshot read")
	}
}

// TestDeadlineErrorsAreNotCancellation keeps the special-case narrow. The
// runtime documents a read deadline as a read result, not a request to close
// the session, so it must keep the ordinary fallback treatment (whatever that
// is judged to be) rather than silently exiting the way cancellation does.
func TestDeadlineErrorsAreNotCancellation(t *testing.T) {
	err := os.ErrDeadlineExceeded
	if errors.Is(err, context.Canceled) {
		t.Fatal("test fixture bug: watchdog error is context.Canceled")
	}
	m, _, _ := streamMateFixture(t, nil)
	m, _ = send(t, m, sessionStreamChunkMsg{gen: m.sess.gen, stream: m.sess.stream, chunk: streamChunk{err: err}})
	if m.sess.snapshot.ControllerNotice == "" {
		t.Error("a deadline error was treated as cancellation; it must keep its normal fallback handling")
	}
}
