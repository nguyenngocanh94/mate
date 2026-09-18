package console

import (
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The auto daemon's two surfaces in the Console (mvp.md task 19): the MODE
// cell on the Project's Mate row, and the message line.
//
// Both are drawn from query.ProjectNode.Daemon and nothing else. The Console
// cannot reach the daemon - internal/autopilot is on the far side of the same
// boundary as internal/store and Herdr - so the DTO is the whole of what it
// knows, and these tests are written against that DTO.

func autoTree(daemon query.AutoDaemon) query.Snapshot {
	tree := sampleTree()
	tree.Projects[0].Mode = query.ModeAuto
	tree.Projects[0].Daemon = daemon
	return tree
}

// mateRow is the rendered Mate row of the first Project, which is where the
// MODE cell lives.
func mateRow(t *testing.T, tree query.Snapshot, w, h int) string {
	t.Helper()
	m := newFixture(t, tree, w, h, unicodeGlyphs)
	m, _ = send(t, m, key("enter"))
	l := layout(m.w, m.h)
	// items: [0] mate header, [1] mate row.
	return m.listLines(l.List, l.Body)[1].render(l.List)
}

// Auto with nothing sent yet is just "auto": the flag is set and this console
// has delivered nothing, which is the true state right after the `m` key.
func TestModeCellSaysAutoBeforeTheDaemonHasSent(t *testing.T) {
	row := mateRow(t, autoTree(query.AutoDaemon{}), 120, 36)
	if !strings.Contains(row, "auto") {
		t.Fatalf("mate row does not show the mode:\n%s", row)
	}
	if strings.Contains(row, "sent") {
		t.Fatalf("mate row claims a digest was sent before any was:\n%s", row)
	}
}

// Once it has sent, the cell carries when - so a reader can see that lines
// really are going into the Mate's composer rather than only that they may.
func TestModeCellCarriesTheLastDigestTime(t *testing.T) {
	sentAt := time.Date(2026, 9, 18, 14, 32, 10, 0, time.UTC)
	row := mateRow(t, autoTree(query.AutoDaemon{Sends: 14, LastSentAt: sentAt}), 120, 36)
	if !strings.Contains(row, "auto · sent 14:32:10") {
		t.Fatalf("mate row does not carry the daemon indicator:\n%s", row)
	}
}

// The indicator must fit the cell at the narrowest frame the Console draws,
// or it is a signal the reader never sees.
func TestModeCellIndicatorFitsAtEveryBreakpoint(t *testing.T) {
	sentAt := time.Date(2026, 9, 18, 14, 32, 10, 0, time.UTC)
	tree := autoTree(query.AutoDaemon{Sends: 1, LastSentAt: sentAt})
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 36}, {140, 40}, {160, 48}} {
		row := mateRow(t, tree, size.w, size.h)
		if !strings.Contains(row, "auto · sent 14:32:10") {
			t.Fatalf("the daemon indicator is cut at %dx%d:\n%s", size.w, size.h, row)
		}
	}
}

// A supervised Project never grows the indicator, whatever the daemon's
// leftover state says: the mode word is the flag, and the flag is off.
func TestSupervisedModeCellNeverShowsTheDaemon(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Daemon = query.AutoDaemon{Sends: 3, LastSentAt: time.Date(2026, 9, 18, 14, 32, 10, 0, time.UTC)}
	row := mateRow(t, tree, 120, 36)
	if !strings.Contains(row, "supervised") || strings.Contains(row, "sent 14:32") {
		t.Fatalf("supervised mate row shows a daemon indicator:\n%s", row)
	}
}

// The refusal reason reaches the message line verbatim: internal/send names
// which composer state it observed, and a reader deciding whether to take the
// composer back needs that observation, not a reworded summary of it.
func TestDaemonNoticeIsTheMessageLine(t *testing.T) {
	notice := `auto digest for payments-api not delivered: agent mate-payments-api has unsubmitted text in its composer ("half a thou")`
	m := newFixture(t, autoTree(query.AutoDaemon{
		Notice: notice, NoticeAt: time.Date(2026, 9, 18, 14, 33, 0, 0, time.UTC),
	}), 120, 36, unicodeGlyphs)
	got := m.footerMessage()
	if got.text != notice {
		t.Fatalf("message line = %q, want the daemon's notice verbatim", got.text)
	}
	if got.tone != toneWarn {
		t.Fatalf("message tone = %v, want toneWarn", got.tone)
	}
}

// An explicit message - what the reader's own last keystroke did - still
// wins: the daemon's notice is a standing line, not an event, and it will
// still be there after the action message clears.
func TestAnActionMessageOutranksTheDaemonNotice(t *testing.T) {
	m := newFixture(t, autoTree(query.AutoDaemon{Notice: "auto digest not delivered"}), 120, 36, unicodeGlyphs)
	m.msg = okMsg("Mate mate-payments-api stop confirmed")
	if got := m.footerMessage(); got.text != "Mate mate-payments-api stop confirmed" {
		t.Fatalf("message line = %q, want the action's own outcome", got.text)
	}
}

// The daemon's notice outranks the standing unknown-field warning: a delivery
// the Console is failing at right now is more urgent than a field it could
// not read, and the warning is still there when the notice clears.
func TestTheDaemonNoticeOutranksTheUnknownFieldWarning(t *testing.T) {
	tree := autoTree(query.AutoDaemon{Notice: "auto digest for payments-api not delivered: agent is mid-turn"})
	tree.Warnings = []query.FieldWarning{{
		Field: "binding", Row: query.RowRef{Label: "payments-api"}, Reason: "read failed",
	}}
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	if got := m.footerMessage(); !strings.Contains(got.text, "not delivered") {
		t.Fatalf("message line = %q, want the daemon's notice", got.text)
	}

	tree.Projects[0].Daemon.Notice = ""
	m2 := newFixture(t, tree, 120, 36, unicodeGlyphs)
	if got := m2.footerMessage(); !strings.Contains(got.text, "1 field unknown") {
		t.Fatalf("message line = %q, want the warning back once the notice clears", got.text)
	}
}

// The whole frame in the state auto mode spends most of its life in: the
// daemon has sent, and its last tick was refused. Both signals are on screen
// at once, which is the arrangement a reader actually meets and the one a
// per-field assertion cannot check.
func TestGoldenProjectInAutoModeWithTheDaemonSending(t *testing.T) {
	m := newFixture(t, autoTree(query.AutoDaemon{
		Sends:      14,
		LastSentAt: time.Date(2026, 9, 18, 14, 32, 10, 0, time.UTC),
		Notice:     "auto digest for payments-api not delivered: agent mate-payments-api is mid-turn",
		NoticeAt:   time.Date(2026, 9, 18, 14, 33, 40, 0, time.UTC),
	}), 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter"))
	assertGolden(t, "project-auto-daemon-120x36-unicode", renderFrame(t, m))
}

// With more than one Project complaining, the newest notice is the one shown,
// and it names its Project - the reader may be looking at a different one.
func TestTheNewestDaemonNoticeWins(t *testing.T) {
	tree := autoTree(query.AutoDaemon{
		Notice:   "auto digest for payments-api not delivered: agent is mid-turn",
		NoticeAt: time.Date(2026, 9, 18, 14, 30, 0, 0, time.UTC),
	})
	tree.Projects[1].Mode = query.ModeAuto
	tree.Projects[1].Daemon = query.AutoDaemon{
		Notice:   "auto digest for ledger-worker not delivered: composer not recognised on screen",
		NoticeAt: time.Date(2026, 9, 18, 14, 33, 0, 0, time.UTC),
	}
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	if got := m.footerMessage(); !strings.Contains(got.text, "ledger-worker") {
		t.Fatalf("message line = %q, want the newest notice", got.text)
	}
}
