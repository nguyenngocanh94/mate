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

// awaitingBox builds a box of n crew status entries, every one of them an
// attention verb, so the digest has n rows' worth of content to decide about.
func awaitingBox(n int) query.Field[query.BoxView] {
	entries := make([]query.BoxEntry, 0, n)
	for i := 0; i < n; i++ {
		entries = append(entries, query.BoxEntry{
			Seq: i, At: sessionTestClock(10, i), Kind: query.BoxStatus,
			Crew: fmt.Sprintf("k%d", i+1), Source: "crew", Verb: "needs-decision",
			Text: "does this fit?", Attention: true, Signal: query.BoxStatusSignal(fmt.Sprintf("k%d", i+1)),
		})
	}
	return query.KnownField(query.BoxView{Entries: entries, Crews: n, Awaiting: n})
}

// TestNarrowMateDigestHeightIsReservedBeforeSizingThePTY is the s5 follow-up's
// second B2 path. A narrow Mate frame draws the box-dependent digest between
// the two rules above the pane, and that digest grows row by row as a crew
// appends. StreamTranscriptCapacity deliberately ignores it (it is an upper
// bound); streamTerminalSize must therefore subtract it, or the PTY keeps the
// height it was opened at while the frame has fewer rows - and the renderer
// then crops the agent's own top row off an ACTIVE stream (no frozen head
// crop to save it).
//
// The expected height is computed from the same two functions the production
// path uses rather than written out: the digest's own shape is pinned
// separately by TestSessionDigestHeightMatchesWhatTheDigestDraws, and what
// this test is for is that the PTY is sized against it at all.
func TestNarrowMateDigestHeightIsReservedBeforeSizingThePTY(t *testing.T) {
	for _, entries := range []int{0, 1, 2, 3, 4, 6} {
		t.Run(fmt.Sprintf("entries_%d", entries), func(t *testing.T) {
			m, _, openedRows := narrowMateStreamFixture(t, nil)
			if openedRows < 1 {
				t.Fatalf("opened rows = %d, want a usable PTY", openedRows)
			}

			snap := SessionSnapshot{
				Target:         SessionTarget{Kind: SessionTargetMate, ID: "mate_1", AgentName: "agent-1"},
				RecordedStatus: query.KnownField("running"),
				Runtime:        SessionRuntime{Status: query.Known},
				Box:            awaitingBox(entries),
			}
			m, _ = send(t, m, sessionStreamMetadataMsg{gen: m.sess.gen, snapshot: snap})

			wantRows := StreamTranscriptCapacity(SessionTargetMate, 80, 24) -
				sessionStreamReservedLines(snap, SessionTargetMate, 80)
			bufRows := m.sess.terminal.Height()
			if bufRows != wantRows {
				t.Fatalf("PTY rows with %d box entries = %d, want %d", entries, bufRows, wantRows)
			}
			m = injectBytes(t, m, paintRows(bufRows))

			view := stripANSI(m.View())
			first, count := rowsDrawn(view, bufRows)
			if first != 0 || count != bufRows {
				t.Fatalf("entries=%d: CROP - %d of %d buffer rows drawn, frame starts at ROW%02d\n%s",
					entries, count, bufRows, first, view)
			}
			want := "none needing attention"
			if entries > 0 {
				want = fmt.Sprintf("%d attention", entries)
			}
			if !containsLine(view, want) {
				t.Errorf("the digest does not report %q:\n%s", want, view)
			}
		})
	}
}

// TestSessionDigestHeightMatchesWhatTheDigestDraws pins the reservation
// arithmetic to the renderer it reserves for: if sessionDigestLines ever gains
// or loses a line (a longer list, a different "N older" rule), this fails
// rather than the PTY quietly getting the wrong height from then on.
func TestSessionDigestHeightMatchesWhatTheDigestDraws(t *testing.T) {
	g, p := unicodeGlyphs, plainPalette()
	for n := 0; n <= 8; n++ {
		box := awaitingBox(n)
		got := len(sessionDigestLines(box, boxRail{sel: -1}, g, p, 78))
		if want := sessionDigestHeight(box); got != want {
			t.Errorf("entries=%d: sessionDigestLines drew %d lines, sessionDigestHeight reserved %d", n, got, want)
		}
	}
	// A box nobody could read reserves the same two lines it draws: the
	// header, and the line that says the read failed.
	unread := query.UnknownField[query.BoxView]("sent.log is unreadable")
	if got, want := len(sessionDigestLines(unread, boxRail{sel: -1}, g, p, 78)), sessionDigestHeight(unread); got != want {
		t.Errorf("unreadable box: drew %d lines, reserved %d", got, want)
	}
}
