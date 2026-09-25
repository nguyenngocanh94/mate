package console

import (
	"strings"
	"testing"
)

// A refresh that falls back to the nearest surviving Crew after the
// selected one was removed must not leave detail's field cursor where the
// reader had left it on the removed row: the new selection reads from its
// own first field. The same row surviving the refresh keeps its cursor.

func mdlDropFailedCrew(t *testing.T) (Model, string) {
	t.Helper()
	tree := sampleTree()
	removedID := tree.Projects[0].Crews[0].CrewID
	m := toFailedAttempt(t, loaded(t, tree, nil))
	if m.cur().selID != removedID {
		t.Fatalf("precondition: selID = %q, want the failed crew %q", m.cur().selID, removedID)
	}
	return m, removedID
}

func TestRefreshResetsTheDetailCursorWhenTheSelectionMovesToADifferentRow(t *testing.T) {
	m, _ := mdlDropFailedCrew(t)
	survivorID := sampleTree().Projects[0].Crews[1].CrewID
	m, _ = send(t, m, key("tab"))
	for i := 0; i < 4; i++ {
		m, _ = send(t, m, key("down"))
	}
	if m.focus != paneDetail || m.detailSel == 0 {
		t.Fatalf("precondition: focus=%v detailSel=%d, want the cursor walked down detail", m.focus, m.detailSel)
	}

	tree := sampleTree()
	tree.Projects[0].Crews = tree.Projects[0].Crews[1:]
	m, _ = send(t, m, treeLoadedMsg{tree: tree})

	if m.cur().selID != survivorID {
		t.Fatalf("selID after refresh = %q, want the surviving crew %q", m.cur().selID, survivorID)
	}
	if m.detailSel != 0 {
		t.Fatalf("detailSel after refresh = %d, want 0: a new selection reads from its own first field", m.detailSel)
	}
	if !strings.Contains(renderFrame(t, m), shortID(survivorID, m.g)) {
		t.Fatalf("detail does not show the surviving crew's own id:\n%s", renderFrame(t, m))
	}
}

func TestRefreshPreservesTheDetailCursorWhenTheSameRowStaysSelected(t *testing.T) {
	m, removedID := mdlDropFailedCrew(t)
	m, _ = send(t, m, key("tab"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down"))
	want := m.detailSel
	if want == 0 {
		t.Fatal("precondition: the cursor must have moved")
	}
	m, _ = send(t, m, treeLoadedMsg{tree: sampleTree()})
	if m.cur().selID != removedID || m.detailSel != want {
		t.Fatalf("after a refresh of the same row: selID=%q detailSel=%d, want %q and %d", m.cur().selID, m.detailSel, removedID, want)
	}
}
