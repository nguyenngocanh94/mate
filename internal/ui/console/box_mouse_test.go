package console

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The focus model's own proofs (session_focus.go, session_mouse.go). What
// they pin is one sentence: at every moment the frame says which zone owns
// the next keystroke, and nothing reaches the agent's PTY from the other
// one. That is the whole reason the Ctrl+b prefix was removed - a prefix
// says nothing on screen, and a mis-typed one put box keys in the Mate's
// composer.

func press(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

// selectMenuAction moves the menu cursor onto one action by name, the way a
// reader would with j.
func selectMenuAction(t *testing.T, m Model, want Action) Model {
	t.Helper()
	for i, c := range m.actionChoices {
		if c.action == want {
			m.actionIndex = i
			return m
		}
	}
	t.Fatalf("the menu does not offer %s: %+v", want, m.actionChoices)
	return m
}

// TestClickOnTheProjectFrameBoxPanelFocusesAndSelectsIt: the project frame's
// panel answers the mouse the same way the rail does (mvp.md task 15), so a
// reader does not learn one set of gestures per surface. The click both
// selects the row and opens the crew it names, which is one gesture with one
// outcome - the panel's own proof that the two surfaces agree.
func TestClickOnTheProjectFrameBoxPanelFocusesAndSelectsIt(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Box = boxAsking(fixtureCrewID)
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	spy := &stageSpy{}
	m = m.WithStage(spy.fn)
	m, _ = send(t, m, key("enter")) // into the project frame
	top, h, ok := m.boxPanelBodyRegion()
	if !ok {
		t.Fatal("setup: the project frame draws no box panel at 120x36")
	}
	b, has := m.projectBox()
	if !has {
		t.Fatal("setup: the fixture project has no box entries")
	}
	// The panel pads at the top when it has fewer entries than rows, so the
	// row that certainly carries one is the oldest drawn, not the first.
	row, want := -1, 0
	for i := 0; i < h; i++ {
		if got, drawn := boxEntryAt(b, m.projectBoxSelection(), h, i, m.w); drawn {
			row, want = i, got
			break
		}
	}
	if row < 0 {
		t.Fatal("setup: the panel draws no entries at all")
	}

	m, cmd := send(t, m, press(4, top+row))
	if m.focus != paneBox {
		t.Fatalf("focus after the click = %v, want paneBox", m.focus)
	}
	if got := m.projectBoxSelection(); got != want {
		t.Fatalf("selection after the click = %d, want the clicked entry %d", got, want)
	}
	if cmd == nil {
		t.Fatal("the click showed nothing")
	}
	cmd()
	if len(spy.calls) != 1 || spy.calls[0].Kind != StageCrew || spy.calls[0].ID != fixtureCrewID {
		t.Fatalf("stage calls after the click = %+v, want crew %s", spy.calls, fixtureCrewID)
	}
}

// TestF2MovesFocusOntoTheProjectFrameBoxPanelAndBack.
func TestF2MovesFocusOntoTheProjectFrameBoxPanelAndBack(t *testing.T) {
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	if m.focus != paneBox {
		t.Fatalf("focus after F2 = %v, want paneBox", m.focus)
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	if m.focus != paneList {
		t.Fatalf("focus after a second F2 = %v, want paneList", m.focus)
	}
}

// ---------- helpers ----------

func placementOf(labels []placedLabel, id labelID) (placedLabel, bool) {
	for _, l := range labels {
		if l.id == id {
			return l, true
		}
	}
	return placedLabel{}, false
}
