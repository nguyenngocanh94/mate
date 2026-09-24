package runtime_test

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
)

func streamRef() runtime.AgentSessionRef {
	return runtime.AgentSessionRef{HerdrSession: "mate-session", AgentName: "mate-agent"}
}

func streamSize() runtime.TerminalSize {
	return runtime.TerminalSize{Cols: 80, Rows: 24}
}

func TestFakeSessionStreamResolvesDurableIdentityBeforeOpening(t *testing.T) {
	t.Parallel()
	stream := runtime.NewFakeSessionStream()
	if err := stream.Seed(streamRef(), []byte("ready")); err != nil {
		t.Fatal(err)
	}

	// The same agent name in another session is not the target. Resolution is
	// an exact durable (session, name) lookup, not a focus or basename lookup.
	wrongSession := streamRef()
	wrongSession.HerdrSession = "another-session"
	channel, err := stream.Open(context.Background(), wrongSession, streamSize())
	if err == nil || channel != nil {
		t.Fatalf("unresolved target opened a channel: channel=%v err=%v", channel, err)
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeNotFound {
		t.Fatalf("unresolved target error = %v, want coded not_found", err)
	}
	if len(stream.Channels) != 0 {
		t.Fatalf("unresolved target created %d channels", len(stream.Channels))
	}

	// An invalid identity also fails before the fake allocates a channel.
	invalid := streamRef()
	invalid.AgentName = "not a Herdr name"
	if channel, err := stream.Open(context.Background(), invalid, streamSize()); err == nil || channel != nil {
		t.Fatalf("invalid target opened a channel: channel=%v err=%v", channel, err)
	}
	if len(stream.Channels) != 0 {
		t.Fatalf("invalid target created a channel")
	}

	if channel, err := stream.Open(context.Background(), streamRef(), streamSize()); err != nil || channel == nil {
		t.Fatalf("resolved target did not open: channel=%v err=%v", channel, err)
	}
	if len(stream.Channels) != 1 {
		t.Fatalf("resolved target opened %d channels, want 1", len(stream.Channels))
	}
}

func TestFakeSessionChannelPreservesRawOutputAndAcceptsRawInput(t *testing.T) {
	t.Parallel()
	stream := runtime.NewFakeSessionStream()
	output := []byte("\x1b[?1049h\x1b[31mhello\x1b[0m\x1b[2J\x1b[H\x00\xff")
	input := []byte{0x1b, '[', '2', 'A', 0x03, 0x00, 0xff, '\n'}
	if err := stream.Seed(streamRef(), output); err != nil {
		t.Fatal(err)
	}
	channel, err := stream.Open(context.Background(), streamRef(), streamSize())
	if err != nil {
		t.Fatal(err)
	}
	fake := channel.(*runtime.FakeSessionChannel)

	got, err := channel.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(output) {
		t.Fatalf("Read changed raw bytes: got % x want % x", got, output)
	}
	// Mutating the returned slice must not corrupt the fake's own copy of the
	// scripted output: a second, independently opened channel over the same
	// seeded ref must still read the original bytes.
	got[0] = 'X'
	second, err := stream.Open(context.Background(), streamRef(), streamSize())
	if err != nil {
		t.Fatal(err)
	}
	got2, err := second.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got2) != string(output) {
		t.Fatalf("mutating a Read result corrupted scripted output for another channel: got % x want % x", got2, output)
	}
	if _, err := channel.Read(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("exhausted script error = %v, want io.EOF", err)
	}

	if err := channel.Write(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	// Mutating the input slice after Write returns must not corrupt what the
	// fake recorded: Write must copy, not alias, the caller's buffer.
	original := append([]byte(nil), input...)
	input[0] = 0x7f
	if len(fake.Writes) != 1 || string(fake.Writes[0]) != string(original) {
		t.Fatalf("channel write = % x, want % x (Write must copy the input buffer)", fake.Writes, original)
	}
	if len(stream.Writes) != 1 || string(stream.Writes[0]) != string(original) {
		t.Fatalf("stream write = % x, want % x (Write must copy the input buffer)", stream.Writes, original)
	}
}

func TestFakeSessionStreamSeedAndSetScriptCloneTheirInputs(t *testing.T) {
	t.Parallel()
	stream := runtime.NewFakeSessionStream()
	output := []byte("seeded-output")
	if err := stream.Seed(streamRef(), output); err != nil {
		t.Fatal(err)
	}
	// Mutating the slice passed to Seed after the call must not change what a
	// later Open/Read observes.
	output[0] = 'X'

	channel, err := stream.Open(context.Background(), streamRef(), streamSize())
	if err != nil {
		t.Fatal(err)
	}
	got, err := channel.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "seeded-output" {
		t.Fatalf("Seed did not clone its output: got %q", got)
	}
}

func TestFakeSessionChannelCloseIsIdempotentAndDoesNotStopAgent(t *testing.T) {
	t.Parallel()
	stream := runtime.NewFakeSessionStream()
	if err := stream.Seed(streamRef(), []byte("still addressable")); err != nil {
		t.Fatal(err)
	}
	channel, err := stream.Open(context.Background(), streamRef(), streamSize())
	if err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake := channel.(*runtime.FakeSessionChannel)
	if fake.CloseCalls != 1 || stream.CloseCalls != 1 {
		t.Fatalf("close calls = channel %d, stream %d; want one underlying close", fake.CloseCalls, stream.CloseCalls)
	}
	if _, err := channel.Read(context.Background()); err == nil {
		t.Fatal("closed channel remained readable")
	}

	// Closing the stream channel does not remove the durable target or invoke
	// an agent stop operation: the target can be opened again independently.
	second, err := stream.Open(context.Background(), streamRef(), streamSize())
	if err != nil {
		t.Fatalf("close made target unopenable: %v", err)
	}
	if second == nil || len(stream.Channels) != 2 {
		t.Fatalf("reopen after close = channel %v, channels %d", second, len(stream.Channels))
	}
}

func TestFakeSessionChannelCancelledCloseStillCloses(t *testing.T) {
	t.Parallel()
	stream := runtime.NewFakeSessionStream()
	if err := stream.Seed(streamRef()); err != nil {
		t.Fatal(err)
	}
	channel, err := stream.Open(context.Background(), streamRef(), streamSize())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := channel.Close(ctx); err != nil {
		t.Fatalf("cancelled Close = %v", err)
	}
	if _, err := channel.Read(context.Background()); err == nil {
		t.Fatal("cancelled Close left fake channel open")
	}
}

func TestFakeSessionChannelResizeIsIdempotent(t *testing.T) {
	t.Parallel()
	stream := runtime.NewFakeSessionStream()
	if err := stream.Seed(streamRef()); err != nil {
		t.Fatal(err)
	}
	channel, err := stream.Open(context.Background(), streamRef(), streamSize())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := channel.Resize(context.Background(), streamSize()); err != nil {
			t.Fatalf("resize %d: %v", i, err)
		}
	}
	fake := channel.(*runtime.FakeSessionChannel)
	if len(fake.Resizes) != 2 || len(stream.Resizes) != 2 {
		t.Fatalf("resize observations = channel %d, stream %d; want two safe calls", len(fake.Resizes), len(stream.Resizes))
	}
}

func TestFakeSessionStreamPreservesLifecycleErrorTaxonomy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("agent disappearance", func(t *testing.T) {
		stream := runtime.NewFakeSessionStream()
		if err := stream.SetScript(streamRef(), runtime.FakeSessionScript{
			ReadErrors: []error{runtime.NewHerdrError(runtime.HerdrAgentNotFound, "agent disappeared")},
		}); err != nil {
			t.Fatal(err)
		}
		channel, err := stream.Open(ctx, streamRef(), streamSize())
		if err != nil {
			t.Fatal(err)
		}
		_, err = channel.Read(ctx)
		assertCode(t, err, observability.CodeNotFound)
	})

	t.Run("Herdr restart or unavailable", func(t *testing.T) {
		stream := runtime.NewFakeSessionStream()
		if err := stream.Seed(streamRef()); err != nil {
			t.Fatal(err)
		}
		stream.OpenErr = runtime.NewHerdrError(runtime.HerdrServerNotRunning, "Herdr restarted")
		channel, err := stream.Open(ctx, streamRef(), streamSize())
		if channel != nil {
			t.Fatal("unavailable Herdr returned a channel")
		}
		assertCode(t, err, observability.CodeRuntimeUnavailable)
	})

	t.Run("timeout", func(t *testing.T) {
		stream := runtime.NewFakeSessionStream()
		if err := stream.SetScript(streamRef(), runtime.FakeSessionScript{
			WriteErr: observability.NewError(observability.CodeTimeout, "write timed out"),
		}); err != nil {
			t.Fatal(err)
		}
		channel, err := stream.Open(ctx, streamRef(), streamSize())
		if err != nil {
			t.Fatal(err)
		}
		err = channel.Write(ctx, []byte("\x03"))
		assertCode(t, err, observability.CodeTimeout)
	})

	t.Run("cancellation", func(t *testing.T) {
		stream := runtime.NewFakeSessionStream()
		if err := stream.Seed(streamRef(), []byte("output")); err != nil {
			t.Fatal(err)
		}
		channel, err := stream.Open(ctx, streamRef(), streamSize())
		if err != nil {
			t.Fatal(err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = channel.Read(cancelled)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v, want context.Canceled", err)
		}
		var coded *observability.Error
		if errors.As(err, &coded) {
			t.Fatalf("cancellation was collapsed into coded runtime error %v", coded)
		}
	})

	// A raw transport deadline (a PTY/socket SetReadDeadline firing, distinct
	// from a Go context deadline) must stay a distinguishable sentinel too,
	// not get folded into the generic runtime_unavailable code.
	t.Run("transport deadline", func(t *testing.T) {
		stream := runtime.NewFakeSessionStream()
		if err := stream.SetScript(streamRef(), runtime.FakeSessionScript{
			ReadErrors: []error{os.ErrDeadlineExceeded},
		}); err != nil {
			t.Fatal(err)
		}
		channel, err := stream.Open(ctx, streamRef(), streamSize())
		if err != nil {
			t.Fatal(err)
		}
		_, err = channel.Read(ctx)
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("transport deadline error = %v, want os.ErrDeadlineExceeded", err)
		}
		var coded *observability.Error
		if errors.As(err, &coded) {
			t.Fatalf("transport deadline was collapsed into coded runtime error %v", coded)
		}
	})

	t.Run("read context deadline uses transport timeout sentinel", func(t *testing.T) {
		stream := runtime.NewFakeSessionStream()
		if err := stream.Seed(streamRef(), []byte("output")); err != nil {
			t.Fatal(err)
		}
		channel, err := stream.Open(ctx, streamRef(), streamSize())
		if err != nil {
			t.Fatal(err)
		}
		deadline, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
		defer cancel()
		_, err = channel.Read(deadline)
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("read context deadline = %v, want os.ErrDeadlineExceeded", err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("read context deadline = %v, must not be context.DeadlineExceeded", err)
		}
	})
}

// TestMapSessionStreamErrorTaxonomy exercises MapSessionStreamError directly
// rather than only through the fake, so each branch has a test that fails if
// that branch is deleted (an operation-specific fake test can pass through a
// different branch and mask a regression here).
func TestMapSessionStreamErrorTaxonomy(t *testing.T) {
	t.Parallel()

	if err := runtime.MapSessionStreamError("op", nil); err != nil {
		t.Fatalf("nil input = %v, want nil", err)
	}

	if err := runtime.MapSessionStreamError("op", context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("context.Canceled mapped to %v, want unchanged", err)
	}

	if err := runtime.MapSessionStreamError("op", context.DeadlineExceeded); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("context.DeadlineExceeded mapped to %v, want unchanged", err)
	}

	if err := runtime.MapSessionStreamError("op", os.ErrDeadlineExceeded); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("os.ErrDeadlineExceeded mapped to %v, want unchanged", err)
	}

	if err := runtime.MapSessionStreamError("op", io.EOF); !errors.Is(err, io.EOF) {
		t.Fatalf("io.EOF mapped to %v, want unchanged", err)
	}

	coded := runtime.NewHerdrError(runtime.HerdrAgentNotFound, "gone")
	if err := runtime.MapSessionStreamError("op", coded); err != coded {
		t.Fatalf("already-coded error was rewrapped: got %v, want the same *observability.Error", err)
	}

	unknown := errors.New("raw transport failure")
	err := runtime.MapSessionStreamError("op", unknown)
	assertCode(t, err, observability.CodeRuntimeUnavailable)
	if !errors.Is(err, unknown) {
		t.Fatalf("mapped unknown error lost its cause: %v", err)
	}
}

// TestFakeSessionChannelScriptErrorTakesPrecedenceOverStreamGlobalError pins
// the documented precedence (FakeSessionStream doc comment on the Err
// fields): a per-target script error must win over a stream-wide default so
// a test can inject one target's specific taxonomy outcome without it being
// shadowed by an unrelated global error set for other assertions.
func TestFakeSessionChannelScriptErrorTakesPrecedenceOverStreamGlobalError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("read", func(t *testing.T) {
		stream := runtime.NewFakeSessionStream()
		if err := stream.SetScript(streamRef(), runtime.FakeSessionScript{
			ReadErrors: []error{observability.NewError(observability.CodeTimeout, "target-specific timeout")},
		}); err != nil {
			t.Fatal(err)
		}
		stream.ReadErr = runtime.NewHerdrError(runtime.HerdrServerNotRunning, "unrelated global failure")
		channel, err := stream.Open(ctx, streamRef(), streamSize())
		if err != nil {
			t.Fatal(err)
		}
		_, err = channel.Read(ctx)
		assertCode(t, err, observability.CodeTimeout)
	})

	t.Run("write", func(t *testing.T) {
		stream := runtime.NewFakeSessionStream()
		if err := stream.SetScript(streamRef(), runtime.FakeSessionScript{
			WriteErr: observability.NewError(observability.CodeTimeout, "target-specific timeout"),
		}); err != nil {
			t.Fatal(err)
		}
		stream.WriteErr = runtime.NewHerdrError(runtime.HerdrServerNotRunning, "unrelated global failure")
		channel, err := stream.Open(ctx, streamRef(), streamSize())
		if err != nil {
			t.Fatal(err)
		}
		err = channel.Write(ctx, []byte("x"))
		assertCode(t, err, observability.CodeTimeout)
	})

	t.Run("resize", func(t *testing.T) {
		stream := runtime.NewFakeSessionStream()
		if err := stream.SetScript(streamRef(), runtime.FakeSessionScript{
			ResizeErr: observability.NewError(observability.CodeTimeout, "target-specific timeout"),
		}); err != nil {
			t.Fatal(err)
		}
		stream.ResizeErr = runtime.NewHerdrError(runtime.HerdrServerNotRunning, "unrelated global failure")
		channel, err := stream.Open(ctx, streamRef(), streamSize())
		if err != nil {
			t.Fatal(err)
		}
		err = channel.Resize(ctx, streamSize())
		assertCode(t, err, observability.CodeTimeout)
	})

	t.Run("close", func(t *testing.T) {
		stream := runtime.NewFakeSessionStream()
		if err := stream.SetScript(streamRef(), runtime.FakeSessionScript{
			CloseErr: observability.NewError(observability.CodeTimeout, "target-specific timeout"),
		}); err != nil {
			t.Fatal(err)
		}
		stream.CloseErr = runtime.NewHerdrError(runtime.HerdrServerNotRunning, "unrelated global failure")
		channel, err := stream.Open(ctx, streamRef(), streamSize())
		if err != nil {
			t.Fatal(err)
		}
		err = channel.Close(ctx)
		assertCode(t, err, observability.CodeTimeout)
	})
}

func assertCode(t *testing.T, err error, want observability.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want %s", want)
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != want {
		t.Fatalf("error = %v, want coded %s", err, want)
	}
}
