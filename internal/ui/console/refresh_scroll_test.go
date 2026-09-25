package console

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// This file is the fix for a counter-review finding (B1, 2026-09-10): a
// refresh that fell back to the nearest surviving Crew after the selected
// one was removed left the inspector's scroll offset (Model.inspTop)
// exactly where the reader had left it on the removed row. reconcileSelection
// (model.go) clamps and re-anchors the list selection but never touched
// inspTop, and windowContent (seams.go) only clamps it locally while
// drawing - it never repairs the model's own stored value - so the new
// selection rendered from wherever the old one had been scrolled to,
// tail first, instead of from its own title.
//
// The fix, in reconcileSelection: reset inspTop to 0 whenever the row the
// inspector shows changes identity across the refresh - the same rule
// every other selection-changing action (moveSelection, onTab, onBack,
// open) already applies. These two tests are the reproduction the review
// made executable, once for the wide-inspector column and once for the
// narrow Detail region, since Model.inspTop is the one field backing both.

// sizedFixture is newFixture without the golden-test glyph/palette
// pinning: these tests assert on model state and on the rendered text
// content, not on an exact frame, so the real unicode glyph set is fine.
func sizedFixture(t *testing.T, tree query.Snapshot, w, h int) Model {
	t.Helper()
	tree.AsOf = goldenAsOf
	m := New(func(context.Context) (query.Snapshot, error) { return tree, nil })
	m.g = unicodeGlyphs
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m, _ = send(t, m, m.Init()())
	return m
}

// crewTaskWithTwoCrews is sampleTree's Task "Fix webhook idempotency" and
// its two Crew attempts, isolated so the removed row is unambiguous: crew 1
// (failed, first attempt) is dropped by the refresh, leaving crew 2 (the
// retry) as the only surviving row.
func crewTaskWithTwoCrews() query.Snapshot {
	return sampleTree()
}

func dropFirstCrewAttempt(tree query.Snapshot) query.Snapshot {
	tree.Projects[0].Crews = tree.Projects[0].Crews[1:]
	return tree
}

// TestRefreshResetsInspectorScrollWhenSelectionMovesToADifferentRow_Detail
// is the narrow-Detail case (<100 cols, Tab opens Detail rather than a
// split column): scroll deep into the removed Crew's own Detail view, then
// refresh it away.
func TestRefreshResetsInspectorScrollWhenSelectionMovesToADifferentRow_Detail(t *testing.T) {
	tree := crewTaskWithTwoCrews()
	removedID := tree.Projects[0].Crews[0].CrewID
	survivorID := tree.Projects[0].Crews[1].CrewID

	m := sizedFixture(t, tree, 80, 24)
	m = toFailedAttempt(t, m)
	if m.cur().selID != removedID {
		t.Fatalf("precondition: selID = %q, want the first attempt %q", m.cur().selID, removedID)
	}
	m, _ = send(t, m, key("tab")) // Detail: 80 cols has no inspector column
	if !m.detail {
		t.Fatalf("precondition: want Detail open at 80 cols")
	}
	for i := 0; i < 100; i++ {
		m, _ = send(t, m, key("down"))
	}
	if m.inspTop == 0 {
		t.Fatalf("precondition: want inspTop scrolled away from the top before the refresh")
	}

	m, _ = send(t, m, treeLoadedMsg{tree: dropFirstCrewAttempt(crewTaskWithTwoCrews())})

	if m.cur().selID != survivorID {
		t.Fatalf("selID after refresh = %q, want it to fall back to the surviving attempt %q", m.cur().selID, survivorID)
	}
	if m.inspTop != 0 {
		t.Fatalf("inspTop after refresh = %d, want 0: a new selection must read from its own top, not from where the removed row had been scrolled to", m.inspTop)
	}
	body := bodyLines(t, renderFrame(t, m))
	if !strings.Contains(body, "CREW") {
		t.Fatalf("rendered Detail body does not show the surviving Crew's own title:\n%s", body)
	}
	if !strings.Contains(body, survivorID) {
		t.Fatalf("rendered Detail body does not show the surviving Crew's own ID %q:\n%s", survivorID, body)
	}
}

// TestRefreshResetsInspectorScrollWhenSelectionMovesToADifferentRow_Wide is
// the same reproduction at a width with a real inspector column
// (100-139 cols), focus moved there with Tab instead of Detail.
func TestRefreshResetsInspectorScrollWhenSelectionMovesToADifferentRow_Wide(t *testing.T) {
	tree := crewTaskWithTwoCrews()
	removedID := tree.Projects[0].Crews[0].CrewID
	survivorID := tree.Projects[0].Crews[1].CrewID

	m := sizedFixture(t, tree, 120, 36)
	m = toFailedAttempt(t, m)
	if m.cur().selID != removedID {
		t.Fatalf("precondition: selID = %q, want the first attempt %q", m.cur().selID, removedID)
	}
	m, _ = send(t, m, key("tab")) // focus the inspector column
	if m.focus != paneInspector {
		t.Fatalf("precondition: want focus on the inspector at 120 cols")
	}
	for i := 0; i < 100; i++ {
		m, _ = send(t, m, key("down"))
	}
	if m.inspTop == 0 {
		t.Fatalf("precondition: want inspTop scrolled away from the top before the refresh")
	}

	m, _ = send(t, m, treeLoadedMsg{tree: dropFirstCrewAttempt(crewTaskWithTwoCrews())})

	if m.cur().selID != survivorID {
		t.Fatalf("selID after refresh = %q, want it to fall back to the surviving attempt %q", m.cur().selID, survivorID)
	}
	if m.inspTop != 0 {
		t.Fatalf("inspTop after refresh = %d, want 0: a new selection must read from its own top, not from where the removed row had been scrolled to", m.inspTop)
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "CREW") {
		t.Fatalf("rendered frame does not show the surviving Crew's own title:\n%s", frame)
	}
	if !strings.Contains(frame, survivorID) {
		t.Fatalf("rendered frame does not show the surviving Crew's own ID %q:\n%s", survivorID, frame)
	}
}

// TestRefreshPreservesInspectorScrollWhenTheSameRowStaysSelected: a refresh
// that does not change which row the inspector shows must not punish the
// reader by resetting their place - only a selection identity change does.
func TestRefreshPreservesInspectorScrollWhenTheSameRowStaysSelected(t *testing.T) {
	tree := crewTaskWithTwoCrews()
	m := sizedFixture(t, tree, 120, 36)
	m, _ = send(t, m, key("enter")) // Project
	m, _ = send(t, m, key("down"))  // the Task row
	m, _ = send(t, m, key("enter")) // its attempts
	m, _ = send(t, m, key("tab"))   // focus the inspector column
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down"))
	if m.inspTop != 2 {
		t.Fatalf("precondition: inspTop = %d, want 2", m.inspTop)
	}

	m, _ = send(t, m, treeLoadedMsg{tree: crewTaskWithTwoCrews()})

	if m.inspTop != 2 {
		t.Fatalf("inspTop after a refresh that kept the same selection = %d, want it preserved at 2", m.inspTop)
	}
}
