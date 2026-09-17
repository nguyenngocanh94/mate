package console

import (
	"context"
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

func narrowMateStreamFixture(t *testing.T, metadata SessionMetadataReader) (Model, *blockingChannel, int) {
	t.Helper()
	ch := &blockingChannel{}
	factory := func(_ context.Context, _ SessionTarget, s TerminalSize) (SessionChannel, error) {
		ch.recordOpen(s)
		return ch, nil
	}
	reader := &recordingSessionController{snap: SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known},
	}}
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := New(spy.load, spy.attach).WithSession(reader.Read, nil, nil).WithSessionStream(factory, metadata)
	m.g = unicodeGlyphs
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter"))
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd())
	if m.sess.phase != sessionActive || m.sess.stream == nil {
		t.Fatalf("narrow Mate stream did not open: phase=%v stream=%v", m.sess.phase, m.sess.stream)
	}
	return m, ch, m.sess.terminal.Height()
}

func awaitingInbox(n int) []SessionInboxEntry {
	inbox := make([]SessionInboxEntry, 0, n)
	for i := 0; i < n; i++ {
		inbox = append(inbox, SessionInboxEntry{
			Attempt: fmt.Sprintf("attempt %d", i+1), Question: "does this fit?", Awaiting: true,
		})
	}
	return inbox
}

// TestNarrowMateDigestHeightIsReservedBeforeSizingThePTY is the s5 follow-up's
// second B2 path. A narrow Mate frame draws the Inbox-dependent digest between
// the two rules above the pane, and that digest grows row by row as replies
// arrive. StreamTranscriptCapacity deliberately ignores it (it is an upper
// bound); streamTerminalSize must therefore subtract it, or the PTY keeps the
// height it was opened at while the frame has fewer rows - and the renderer
// then crops the agent's own top row off an ACTIVE stream (no frozen head
// crop to save it). Before the fix the buffer stayed at 19 rows for every
// Inbox size below, and an Inbox with four awaiting replies lost three rows.
func TestNarrowMateDigestHeightIsReservedBeforeSizingThePTY(t *testing.T) {
	cases := []struct {
		awaiting int
		wantRows int
	}{
		{0, 19}, // digest: header only
		{1, 18},
		{2, 17},
		{3, 16}, // three awaited replies listed, no "+N more"
		{4, 15}, // 3 listed + "+1 more awaiting reply"
		{6, 15}, // more waiting never grows past one "+N more" line
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("awaiting_%d", tc.awaiting), func(t *testing.T) {
			m, _, openedRows := narrowMateStreamFixture(t, nil)
			if openedRows != 19 {
				t.Fatalf("opened rows = %d, want 19 (24 - 4 frame chrome - 1 digest header)", openedRows)
			}

			snap := SessionSnapshot{
				Target:         SessionTarget{Kind: SessionTargetMate, ID: "mate_1", AgentName: "agent-1"},
				RecordedStatus: query.KnownField("running"),
				Runtime:        SessionRuntime{Status: query.Known},
				Inbox:          awaitingInbox(tc.awaiting),
			}
			m, _ = send(t, m, sessionStreamMetadataMsg{gen: m.sess.gen, snapshot: snap})

			bufRows := m.sess.terminal.Height()
			if bufRows != tc.wantRows {
				t.Fatalf("PTY rows after %d awaiting replies = %d, want %d", tc.awaiting, bufRows, tc.wantRows)
			}
			m = injectBytes(t, m, paintRows(bufRows))

			view := stripANSI(m.View())
			first, count := rowsDrawn(view, bufRows)
			if first != 0 || count != bufRows {
				t.Fatalf("awaiting=%d: CROP - %d of %d buffer rows drawn, frame starts at ROW%02d\n%s",
					tc.awaiting, count, bufRows, first, view)
			}
			if !containsLine(view, fmt.Sprintf("%d awaiting reply", tc.awaiting)) {
				t.Errorf("the digest does not report %d awaiting replies:\n%s", tc.awaiting, view)
			}
		})
	}
}

// TestSessionDigestHeightMatchesWhatTheDigestDraws pins the reservation
// arithmetic to the renderer it reserves for: if sessionDigestLines ever gains
// or loses a line (a wider list, a different "+N more" rule), this fails
// rather than the PTY quietly getting the wrong height from then on.
func TestSessionDigestHeightMatchesWhatTheDigestDraws(t *testing.T) {
	g, p := unicodeGlyphs, plainPalette()
	for n := 0; n <= 8; n++ {
		inbox := awaitingInbox(n)
		got := len(sessionDigestLines(inbox, g, p, 78))
		if want := sessionDigestHeight(inbox); got != want {
			t.Errorf("awaiting=%d: sessionDigestLines drew %d lines, sessionDigestHeight reserved %d", n, got, want)
		}
	}
	// A recorded (replied) entry must not change the height: only awaited ones
	// get a line of their own.
	entries := []SessionInboxEntry{{Attempt: "a", Question: "q", Awaiting: false}, {Attempt: "b", Question: "r", Awaiting: true}}
	if got, want := len(sessionDigestLines(entries, g, p, 78)), sessionDigestHeight(entries); got != want {
		t.Errorf("mixed inbox: drew %d lines, reserved %d", got, want)
	}
}
