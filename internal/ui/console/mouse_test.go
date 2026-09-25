package console

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nguyenngocanh94/mate/internal/query"
)

func press(x, y int) tea.MouseMsg {
	return tea.MouseMsg(tea.MouseEvent{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
}

func bxWheel(x, y int, down bool) tea.MouseMsg {
	b := tea.MouseButtonWheelUp
	if down {
		b = tea.MouseButtonWheelDown
	}
	return tea.MouseMsg(tea.MouseEvent{X: x, Y: y, Action: tea.MouseActionPress, Button: b})
}

// bxSlot is a pane's slot, or the test fails.
func bxSlot(t *testing.T, m Model, k slotKind) slot {
	t.Helper()
	sl, ok := m.plan().slot(k)
	if !ok {
		t.Fatalf("the frame has no slot %d", k)
	}
	return sl
}

// bxRowLine is the frame row a list row's first line is drawn on.
func bxRowLine(t *testing.T, m Model, row int) int {
	t.Helper()
	sl := bxSlot(t, m, slotList)
	top, h := sl.body()
	for i, r := range m.listBody(m.plan(), h).rows {
		if r == row {
			return top + i
		}
	}
	t.Fatalf("row %d is not drawn", row)
	return -1
}

// A click on a Mate or Crew row is Enter: the row is selected and shown in
// the next pane.
func TestClickOnACrewRowSelectsAndShowsIt(t *testing.T) {
	m, _, spy := bxPayments(t, 40, 36)
	y := bxRowLine(t, m, 1)             // the codex crew, k7
	m, cmd := send(t, m, press(6, y+1)) // its second line counts too
	if r, _ := m.selectedRow(); r.id != "k7" {
		t.Fatalf("selection after the click = %+v, want crew k7", r)
	}
	if cmd == nil {
		t.Fatal("the click showed nothing")
	}
	cmd()
	if len(spy.calls) != 1 || spy.calls[0].ID != "k7" {
		t.Fatalf("stage calls = %+v, want crew k7", spy.calls)
	}
}

// On the workspace a click on a Project row opens it, like Enter.
func TestClickOnAProjectRowOpensIt(t *testing.T) {
	m := newFixture(t, designTree(), 40, 36, unicodeGlyphs)
	m, _ = send(t, m, press(4, bxRowLine(t, m, 3))) // docs-site
	if m.cur().kind != frameProject || m.currentProject().Name != "docs-site" {
		t.Fatalf("frame after the click = %+v, want docs-site opened", m.cur())
	}
}

// A click on ▸ Completed toggles the group.
func TestClickOnCompletedTogglesIt(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	y := bxRowLine(t, m, 3)
	m, _ = send(t, m, press(2, y))
	if !m.completedOpen["payments-api"] {
		t.Fatal("the click did not open Completed")
	}
	m, _ = send(t, m, press(2, bxRowLine(t, m, 3)))
	if m.completedOpen["payments-api"] {
		t.Fatal("a second click did not close Completed")
	}
}

// A click on a pane's rule focuses that pane and runs nothing.
func TestClickOnARuleFocusesThatPane(t *testing.T) {
	m, act, spy := bxPayments(t, 40, 36)
	m, cmd := send(t, m, press(10, bxSlot(t, m, slotDetail).top))
	if m.focus != paneDetail || cmd != nil {
		t.Fatalf("focus = %v cmd = %v after clicking detail's rule, want detail and nothing run", m.focus, cmd != nil)
	}
	m, _ = send(t, m, press(10, bxSlot(t, m, slotBox).top))
	if m.focus != paneBox {
		t.Fatalf("focus = %v after clicking the box rule, want the box", m.focus)
	}
	m, _ = send(t, m, press(10, bxSlot(t, m, slotList).top))
	if m.focus != paneList {
		t.Fatalf("focus = %v after clicking the list rule, want the list", m.focus)
	}
	if len(act.reqs) != 0 || len(spy.calls) != 0 {
		t.Fatalf("a rule click ran something: %+v %+v", act.reqs, spy.calls)
	}
}

// A click in detail focuses it and puts the cursor on the field clicked.
func TestClickInDetailMovesTheFieldCursor(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	top, _ := bxSlot(t, m, slotDetail).body()
	m, _ = send(t, m, press(12, top+2)) // the third field: status
	if m.focus != paneDetail || m.detailSel != 2 {
		t.Fatalf("focus = %v detailSel = %d, want detail on field 2", m.focus, m.detailSel)
	}
}

// A click on [assign] hands the item to the Mate; anywhere else on the
// item shows its crew. The two never swap.
func TestClickOnAssignAssignsAndTheRestOfTheItemShowsTheCrew(t *testing.T) {
	m, act, spy := bxPayments(t, 40, 36)
	top, h := bxSlot(t, m, slotBox).body()
	b := m.boxLines(m.plan(), m.boxMode(m.plan()))
	var at, x int
	for i, hit := range b.hits[:h] {
		if hit.assign > 0 {
			at, x = i, hit.assignX0+1
		}
	}
	if x == 0 {
		t.Fatal("setup: no [assign] button is drawn")
	}
	_, cmd := send(t, m, press(x, top+at))
	if cmd == nil {
		t.Fatal("[assign] ran nothing")
	}
	cmd()
	if len(act.reqs) != 1 || act.reqs[0].Action != ActionResolve || len(spy.calls) != 0 {
		t.Fatalf("[assign]: reqs=%+v stage=%+v, want one resolve and no show", act.reqs, spy.calls)
	}

	m2, act2, spy2 := bxPayments(t, 40, 36)
	_, cmd = send(t, m2, press(6, top+at))
	if cmd == nil {
		t.Fatal("a click on the item's title showed nothing")
	}
	cmd()
	if len(spy2.calls) != 1 || spy2.calls[0].ID != "k3" || len(act2.reqs) != 0 {
		t.Fatalf("item click: stage=%+v reqs=%+v, want crew k3 shown and nothing assigned", spy2.calls, act2.reqs)
	}
}

// The wheel scrolls the pane under the pointer, and only that pane.
func TestWheelScrollsThePaneUnderThePointer(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	listTop, _ := bxSlot(t, m, slotList).body()
	m, _ = send(t, m, bxWheel(4, listTop, true))
	if r, _ := m.selectedRow(); r.id != "k7" {
		t.Fatalf("wheel over the list: selection %+v, want the next row", r)
	}
	detailTop, _ := bxSlot(t, m, slotDetail).body()
	sel := m.cur().sel
	m, _ = send(t, m, bxWheel(4, detailTop, true))
	if m.detailSel != 1 || m.cur().sel != sel {
		t.Fatalf("wheel over detail: detailSel=%d list sel %d->%d, want the field cursor to move alone", m.detailSel, sel, m.cur().sel)
	}
}

// A sheet is modal: clicks on the panes above it do nothing.
func TestClicksAboveAnOpenSheetDoNothing(t *testing.T) {
	m, _, spy := bxPayments(t, 40, 36)
	m, _ = send(t, m, key("a"))
	before := m.cur().sel
	m, cmd := send(t, m, press(6, bxRowLine(t, m, 1)))
	if cmd != nil || m.cur().sel != before || !m.actions || len(spy.calls) != 0 {
		t.Fatalf("a click above the sheet acted: sel %d->%d actions=%v", before, m.cur().sel, m.actions)
	}
}

// A click on an actions row runs it, the same as its key.
func TestClickOnAnActionRowRunsIt(t *testing.T) {
	m, _, spy := bxPayments(t, 40, 36)
	m, _ = send(t, m, key("a"))
	top, _ := bxSlot(t, m, slotSheet).body()
	_, cmd := send(t, m, press(4, top)) // "enter  Show in next pane"
	if cmd == nil {
		t.Fatal("clicking Show ran nothing")
	}
	cmd()
	if len(spy.calls) != 1 || spy.calls[0].Kind != StageMate {
		t.Fatalf("stage calls = %+v, want the Mate", spy.calls)
	}
}

// Motion and releases are not clicks.
func TestMotionIsNotAClick(t *testing.T) {
	m, _, spy := bxPayments(t, 40, 36)
	y := bxRowLine(t, m, 1)
	for _, a := range []tea.MouseAction{tea.MouseActionMotion, tea.MouseActionRelease} {
		m2, cmd := send(t, m, tea.MouseMsg(tea.MouseEvent{X: 6, Y: y, Action: a, Button: tea.MouseButtonLeft}))
		if cmd != nil || m2.cur().sel != m.cur().sel || len(spy.calls) != 0 {
			t.Fatalf("mouse action %v acted like a click", a)
		}
	}
}

// A click on a harness row is Enter on it: the picker creates the Mate
// with the harness clicked, like the actions sheet runs the row clicked.
func TestClickOnAHarnessRowPicksIt(t *testing.T) {
	var got []ActionRequest
	tree := sampleTree()
	tree.Projects[0].Mate = absentMate("this project has no designated Mate")
	tree.AsOf = goldenAsOf
	m := New(func(context.Context) (query.Snapshot, error) { return tree, nil },
		func(_ context.Context, req ActionRequest) (string, error) { got = append(got, req); return "", nil })
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 36})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter")) // the project; its Mate row
	m, _ = send(t, m, key("s"))     // create: the harness picker
	if !m.harnessPick {
		t.Fatal("setup: s did not open the harness picker")
	}
	sl, ok := m.plan().slot(slotSheet)
	if !ok {
		t.Fatal("setup: the picker draws no sheet")
	}
	top, _ := sl.body()
	m, cmd := send(t, m, tea.MouseMsg{X: 4, Y: top + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if cmd == nil {
		t.Fatal("a click on codex ran nothing")
	}
	cmd()
	if len(got) != 1 || got[0].Harness != query.HarnessCodex {
		t.Fatalf("requests = %+v, want one create with codex", got)
	}
}
