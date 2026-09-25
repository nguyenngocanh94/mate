package console

import (
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The auto daemon's two surfaces in the Console (mvp.md task 19): the mode
// field in the Mate's detail and the Project's peek, and the status line.
// Both are drawn from query.ProjectNode.Daemon and nothing else.

func autoTree(daemon query.AutoDaemon) query.Snapshot {
	tree := sampleTree()
	tree.Projects[0].Mode = query.ModeAuto
	tree.Projects[0].Daemon = daemon
	return tree
}

// mdlModeLine is the rendered "mode" line of the first Project's Mate
// detail, and of its peek on the workspace level.
func mdlModeLine(t *testing.T, tree query.Snapshot, w, h int) (mate, peek string) {
	t.Helper()
	m := newFixture(t, tree, w, h, unicodeGlyphs)
	find := func(frame string) string {
		for _, l := range strings.Split(frame, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "mode ") {
				return l
			}
		}
		t.Fatalf("no mode field on the frame:\n%s", frame)
		return ""
	}
	peek = find(renderFrame(t, m))
	m, _ = send(t, m, key("enter"))
	return find(renderFrame(t, m)), peek
}

// Auto with nothing sent yet is just "auto".
func TestModeFieldSaysAutoBeforeTheDaemonHasSent(t *testing.T) {
	for _, l := range mdlPair(mdlModeLine(t, autoTree(query.AutoDaemon{}), 40, 36)) {
		if !strings.Contains(l, "auto") || strings.Contains(l, "sent") {
			t.Fatalf("mode line = %q, want auto and no send", l)
		}
	}
}

// Once it has sent, the field says how long ago - so a reader can see
// lines really are going into the Mate's composer.
func TestModeFieldCarriesTheLastDigestAge(t *testing.T) {
	daemon := query.AutoDaemon{Sends: 14, LastSentAt: goldenAsOf.Add(-3 * time.Minute)}
	for _, l := range mdlPair(mdlModeLine(t, autoTree(daemon), 40, 36)) {
		if !strings.Contains(l, "auto · sent 3m ago") {
			t.Fatalf("mode line = %q, want the daemon's last send", l)
		}
	}
}

// The indicator must fit at the narrowest split the Console draws.
func TestModeFieldIndicatorFitsAtEverySize(t *testing.T) {
	tree := autoTree(query.AutoDaemon{Sends: 1, LastSentAt: goldenAsOf.Add(-3 * time.Minute)})
	for _, size := range []struct{ w, h int }{{32, 36}, {40, 24}, {48, 48}} {
		mate, _ := mdlModeLine(t, tree, size.w, size.h)
		if !strings.Contains(mate, "auto · sent 3m ago") {
			t.Fatalf("the daemon indicator is cut at %dx%d: %q", size.w, size.h, mate)
		}
	}
}

// A manual Project never grows the indicator, whatever the daemon's
// leftover state says.
func TestManualModeFieldNeverShowsTheDaemon(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Daemon = query.AutoDaemon{Sends: 3, LastSentAt: goldenAsOf.Add(-time.Minute)}
	for _, l := range mdlPair(mdlModeLine(t, tree, 40, 36)) {
		if !strings.Contains(l, "manual") || strings.Contains(l, "sent") {
			t.Fatalf("manual mode line shows a daemon indicator: %q", l)
		}
	}
}

// The refusal reason reaches the status line verbatim.
func TestDaemonNoticeIsTheStatusLine(t *testing.T) {
	notice := `auto digest for payments-api not delivered: agent mate-payments-api has unsubmitted text in its composer ("half a thou")`
	m := newFixture(t, autoTree(query.AutoDaemon{Notice: notice, NoticeAt: goldenAsOf}), 40, 36, unicodeGlyphs)
	got := m.footerMessage()
	if got.text != notice || got.tone != toneWarn {
		t.Fatalf("status message = %+v, want the daemon's notice verbatim as a warning", got)
	}
	if !strings.Contains(renderFrame(t, m), "! auto digest for payments-api") {
		t.Fatalf("the notice is not on the status line:\n%s", renderFrame(t, m))
	}
}

// What the reader's own last keystroke did still wins; the notice is a
// standing line and comes back when the message clears.
func TestAnActionMessageOutranksTheDaemonNotice(t *testing.T) {
	m := newFixture(t, autoTree(query.AutoDaemon{Notice: "auto digest not delivered"}), 40, 36, unicodeGlyphs)
	m.msg = okMsg("Mate mate-payments-api stop confirmed")
	if got := m.footerMessage(); got.text != "Mate mate-payments-api stop confirmed" {
		t.Fatalf("status message = %q, want the action's own outcome", got.text)
	}
}

// A delivery failing now outranks a field that could not be read.
func TestTheDaemonNoticeOutranksTheUnknownFieldWarning(t *testing.T) {
	tree := autoTree(query.AutoDaemon{Notice: "auto digest for payments-api not delivered: agent is mid-turn"})
	tree.Warnings = []query.FieldWarning{{Field: "binding", Row: query.RowRef{Label: "payments-api"}, Reason: "read failed"}}
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	if got := m.footerMessage(); !strings.Contains(got.text, "not delivered") {
		t.Fatalf("status message = %q, want the daemon's notice", got.text)
	}
	tree.Projects[0].Daemon.Notice = ""
	m2 := newFixture(t, tree, 40, 36, unicodeGlyphs)
	if got := m2.footerMessage(); !strings.Contains(got.text, "1 field unknown") {
		t.Fatalf("status message = %q, want the warning back once the notice clears", got.text)
	}
}

// With more than one Project complaining, the newest notice wins.
func TestTheNewestDaemonNoticeWins(t *testing.T) {
	tree := autoTree(query.AutoDaemon{
		Notice: "auto digest for payments-api not delivered: agent is mid-turn", NoticeAt: goldenAsOf.Add(-3 * time.Minute),
	})
	tree.Projects[1].Mode = query.ModeAuto
	tree.Projects[1].Daemon = query.AutoDaemon{
		Notice: "auto digest for ledger-worker not delivered: composer not recognised on screen", NoticeAt: goldenAsOf,
	}
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	if got := m.footerMessage(); !strings.Contains(got.text, "ledger-worker") {
		t.Fatalf("status message = %q, want the newest notice", got.text)
	}
}

func mdlPair(a, b string) []string { return []string{a, b} }
