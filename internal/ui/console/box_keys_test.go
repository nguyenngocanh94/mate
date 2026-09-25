package console

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// fixtureCrewID is the running crew of sampleTree: the one a box row can
// actually open, because opening a crew goes through the same snapshot the
// tree row would (box_keys.go's boxEntryCrewRow).
const fixtureCrewID = "crew_01J9P6Q6W0E5V8XK2M4B8DT"

// boxAsking is a one-item inbox: crew asked something and is waiting.
func boxAsking(crew string) query.Field[query.BoxView] {
	e := query.BoxEntry{
		Seq: 0, At: time.Date(2026, 9, 10, 14, 1, 0, 0, time.UTC),
		Kind: query.BoxStatus, Source: "crew", Target: "crew:" + crew, Crew: crew,
		Verb: "needs-decision", Text: "pick A or B", Attention: true,
		Resolve: testResolveLine(crew, "pick A or B"),
	}
	return query.KnownField(query.BoxView{
		Entries: []query.BoxEntry{e}, Inbox: []query.BoxEntry{e},
		Crews: 1, Awaiting: 1, LastAt: e.At,
	})
}

// boxMateWedged is the daemon's own incident: an inbox item whose crew is
// the literal "mate" (internal/autopilot/doc.go).
func boxMateWedged() query.Field[query.BoxView] {
	e := query.BoxEntry{
		Seq: 0, At: time.Date(2026, 9, 10, 14, 4, 0, 0, time.UTC),
		Kind: query.BoxIncident, Source: "observer", Crew: "mate",
		Verb: "wedged", Text: "the composer has held unsent text for 6m",
		Attention: true, Resolve: "resolve: incident wedged mate - the composer has held unsent text for 6m",
	}
	return query.KnownField(query.BoxView{
		Entries: []query.BoxEntry{e}, Inbox: []query.BoxEntry{e},
		Crews: 1, Awaiting: 1, LastAt: e.At,
	})
}

// Where the box keys live, per focus zone (mvp.md task 15,
// session_focus.go). The routing is the part a reader gets wrong at no cost
// to the tests and at real cost to them: an `a` typed with the terminal
// focused is a letter in the harness's composer, not an assign.
//
// The vocabulary itself is the 2026-09-19 decision: the box is a place to
// act, not to read. Enter opens the pane of the crew the row names, `a`
// hands the row to the Mate, `l` swaps the inbox for the whole log.

// recordingAction captures what the Console asked its ActionFunc for.
type recordingAction struct {
	reqs []ActionRequest
	out  string
	err  error
}

func (r *recordingAction) run(_ context.Context, req ActionRequest) (string, error) {
	r.reqs = append(r.reqs, req)
	return r.out, r.err
}

// TestProjectFrameBoxKeysAreBare is the other half of the rule: on the
// project frame the Console owns the keyboard, so the keys need no prefix -
// but only once Tab has focused the panel, because `a` and Enter already
// mean something on the list.
func TestProjectFrameBoxKeysAreBare(t *testing.T) {
	action := &recordingAction{out: "delivered"}
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m.action = action.run
	m, _ = send(t, m, key("enter")) // into the project frame

	// On the list, `a` is still the action menu and `r` still refreshes.
	m, _ = send(t, m, key("a"))
	if len(action.reqs) != 0 {
		t.Fatalf("`a` on the list ran %+v; it opens the action menu there", action.reqs)
	}
	m, _ = send(t, m, key("esc"))

	m, _ = send(t, m, key("tab")) // list -> inspector
	m, _ = send(t, m, key("tab")) // inspector -> box
	if m.focus != paneBox {
		t.Fatalf("focus = %v, want paneBox", m.focus)
	}
	// The panel draws the inbox, so its one row is already the
	// needs-decision line - no stepping back past a message to reach it,
	// which is the whole point of the filter.
	if got := m.projectBoxSelection(); got != 0 {
		t.Fatalf("panel selection = %d, want the single inbox item (0)", got)
	}
	m, cmd := send(t, m, key("a"))
	if cmd == nil {
		t.Fatal("bare `a` on the focused panel issued no command")
	}
	if _, ok := cmd().(actionDoneMsg); !ok {
		t.Fatalf("bare `a` produced %T, want actionDoneMsg", cmd())
	}
	if len(action.reqs) != 1 || action.reqs[0].Action != ActionResolve {
		t.Fatalf("action requests = %+v, want one resolve", action.reqs)
	}
	if action.reqs[0].Target != "proj_01J9M1F8K2Q7C4H6N0R3V5T8YZ" || action.reqs[0].Crew != "k3" {
		t.Errorf("assign request = %+v, want this project and crew k3", action.reqs[0])
	}
}

// TestProjectFrameBoxEnterOpensTheCrew: from the project frame there is no
// stream to close, so Enter on a row goes straight to the same place Enter
// on that crew's tree row goes.
func TestProjectFrameBoxEnterOpensTheCrew(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Box = boxAsking(fixtureCrewID)
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	spy := &stageSpy{}
	m = m.WithStage(spy.fn)
	m, _ = send(t, m, key("enter")) // into the project frame
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	if m.focus != paneBox {
		t.Fatalf("focus = %v, want paneBox", m.focus)
	}
	e, ok := boxSelectedEntry(m.projectBoxList(), m.projectBoxSelection())
	if !ok || e.Crew != fixtureCrewID {
		t.Fatalf("setup: the panel's selected entry is %+v, want the fixture crew", e)
	}

	_, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter on the panel showed nothing")
	}
	cmd()
	if len(spy.calls) != 1 || spy.calls[0].Kind != StageCrew || spy.calls[0].ID != e.Crew {
		t.Fatalf("stage calls after Enter = %+v, want crew %s", spy.calls, e.Crew)
	}
}

// TestProjectFrameBoxEnterRefusesACrewThatLeftTheSnapshot: a crew closed
// since the last read has no pane to open, and the refusal says so on the
// frame's own message line rather than opening nothing in silence.
func TestProjectFrameBoxEnterRefusesACrewThatLeftTheSnapshot(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Box = boxAsking(fixtureCrewID)
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	// The box still carries the crew's question; the crew itself is gone.
	m.tree.Projects[0].Crews = nil
	m, _ = send(t, m, key("enter"))

	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "not in this snapshot") {
		t.Fatalf("message = %+v, want a refusal saying the crew is gone", m.msg)
	}
}
