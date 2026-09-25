package console

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// loaded builds a sized, loaded model - or a failed one, when err is
// non-nil. 120x36 is the middle breakpoint: an inspector column exists, so
// both panes are exercised.
//
// tree.AsOf is pinned to goldenAsOf, like newFixture: the header's "As of"
// reads Snapshot.AsOf directly, so this is where every test's read time is
// fixed.
func loaded(t *testing.T, tree query.Snapshot, err error) Model {
	t.Helper()
	tree.AsOf = goldenAsOf
	m := New(func(context.Context) (query.Snapshot, error) { return tree, err })
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m, _ = send(t, m, m.Init()())
	return m
}

func TestInitStartsLoadingAndSaysSo(t *testing.T) {
	m := New(func(context.Context) (query.Snapshot, error) { return query.Snapshot{}, nil })
	if m.phase != phaseLoading {
		t.Fatalf("phase = %v, want phaseLoading", m.phase)
	}
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	view := renderFrame(t, m)
	if !strings.Contains(view, "Loading snapshot") {
		t.Fatalf("loading view = %q, want a loading message", view)
	}
	if strings.Contains(view, "As of") {
		t.Fatalf("loading view claims an as-of time before any read completed:\n%s", view)
	}
}

func TestTreeLoadFailurePinsFailedPhaseNotBlank(t *testing.T) {
	m := loaded(t, query.Snapshot{}, errors.New("db unreachable"))
	if m.phase != phaseFailed {
		t.Fatalf("phase = %v, want phaseFailed", m.phase)
	}
	view := renderFrame(t, m)
	if !strings.Contains(view, "db unreachable") {
		t.Fatalf("error view = %q, want the failure reason", view)
	}
	if !strings.Contains(view, "r retries") || !strings.Contains(view, "Retry") {
		t.Fatalf("error view = %q, want a retry hint in the body and on the key line", view)
	}
	if strings.Contains(view, "As of") {
		t.Fatalf("a failed read must not claim an as-of time:\n%s", view)
	}
}

func TestEmptyWorkspaceRendersEmptyStateNotBlank(t *testing.T) {
	m := loaded(t, query.Snapshot{WorkspaceID: "ws_1"}, nil)
	view := renderFrame(t, m)
	if !strings.Contains(view, "No projects recorded") {
		t.Fatalf("view = %q, want an explicit empty-Projects message", view)
	}
	if !strings.Contains(view, "0 projects") {
		t.Fatalf("view = %q, want the breadcrumb count to say zero", view)
	}
}

func TestNavigationDrillsInAndBackOutRestoringSelection(t *testing.T) {
	m := loaded(t, sampleTree(), nil)

	// Move onto the second Project, drill in, come back: the frame below
	// kept its own selection, so there is nothing to restore.
	m, _ = send(t, m, key("down"))
	if got := m.cur().sel; got != 1 {
		t.Fatalf("sel = %d, want 1", got)
	}
	m, _ = send(t, m, key("enter"))
	if m.cur().kind != frameProject || m.cur().id != sampleTree().Projects[1].ProjectID {
		t.Fatalf("frame = %+v, want the second Project", m.cur())
	}
	m, _ = send(t, m, key("esc"))
	if m.cur().kind != frameWorkspace || m.cur().sel != 1 {
		t.Fatalf("frame = %+v, want the Workspace with sel 1", m.cur())
	}

	// Into the first Project, past the Mate row onto its first Crew, then
	// back out again.
	m, _ = send(t, m, key("up"))
	m, _ = send(t, m, key("enter"))
	if m.cur().kind != frameProject {
		t.Fatalf("frame = %+v, want a Project frame", m.cur())
	}
	rows := m.currentRows()
	if len(rows) != 3 || rows[0].kind != rowMate || rows[1].kind != rowCrew || rows[2].kind != rowCompletedGroup {
		t.Fatalf("project rows = %+v, want the Mate row, one Crew and the Completed group", rows)
	}
	m, _ = send(t, m, key("down")) // off the Mate row, onto the active Crew
	r, ok := m.selectedRow()
	if !ok || r.kind != rowCrew {
		t.Fatalf("selected row = %+v (ok=%v), want the first Crew row", r, ok)
	}
	m, _ = send(t, m, key("esc"))
	if m.cur().kind != frameWorkspace || len(m.stack) != 1 {
		t.Fatalf("Esc at the root changed the stack: %+v", m.stack)
	}
}

// TestRefreshKeepsSelectionByIdentityNotIndex is N9 sharpened by the
// stack: preserving an index across a refresh is not enough. A Project
// inserted above the selection would keep the index and move the reader
// onto a different row; re-finding by selID keeps them on the row they
// were looking at.
func TestRefreshKeepsSelectionByIdentityNotIndex(t *testing.T) {
	tree := sampleTree()
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("down")) // the second Project
	wantID := m.cur().selID
	if wantID != tree.Projects[1].ProjectID {
		t.Fatalf("precondition: selID = %q, want the second Project", wantID)
	}

	inserted := query.Snapshot{WorkspaceID: tree.WorkspaceID}
	inserted.Projects = append(inserted.Projects,
		query.ProjectNode{ProjectID: "proj_new", Name: "brand-new", Mate: absentMate("this project has no designated Mate")})
	inserted.Projects = append(inserted.Projects, tree.Projects...)

	m, _ = send(t, m, treeLoadedMsg{tree: inserted})
	if m.cur().selID != wantID {
		t.Fatalf("selID after a refresh that inserted a row above = %q, want %q", m.cur().selID, wantID)
	}
	if m.cur().sel != 2 {
		t.Fatalf("sel = %d, want 2 - the row moved down, so the index must follow it", m.cur().sel)
	}
}

// TestRefreshClampsWhenTheSelectedRowIsGone: falling back to the same index
// lands on the nearest surviving neighbour, which is where a reader expects
// to be, and never past the end of a shorter list.
func TestRefreshClampsWhenTheSelectedRowIsGone(t *testing.T) {
	full := sampleTree()
	m := loaded(t, full, nil)
	m, _ = send(t, m, key("down"))
	if m.cur().sel != 1 {
		t.Fatalf("precondition: sel = %d, want 1", m.cur().sel)
	}
	shrunk := query.Snapshot{WorkspaceID: full.WorkspaceID, Projects: full.Projects[:1]}
	m, _ = send(t, m, treeLoadedMsg{tree: shrunk})
	if m.cur().sel != 0 || m.cur().selID != full.Projects[0].ProjectID {
		t.Fatalf("frame = %+v, want the selection clamped onto the one surviving Project", m.cur())
	}
}

// TestRefreshDropsFramesWhoseEntityIsGone: a Project the refresh no longer
// reports must not leave the reader inside an empty frame that says nothing
// about why it is empty. The stack unwinds to the deepest level that still
// resolves.
func TestRefreshDropsFramesWhoseEntityIsGone(t *testing.T) {
	full := sampleTree()
	m := loaded(t, full, nil)
	m, _ = send(t, m, key("enter")) // Project
	m, _ = send(t, m, key("down"))  // its active Crew
	if len(m.stack) != 2 {
		t.Fatalf("precondition: stack depth %d, want 2", len(m.stack))
	}

	withoutCrew := sampleTree()
	withoutCrew.Projects[0].Crews = withoutCrew.Projects[0].Crews[1:]
	m, _ = send(t, m, treeLoadedMsg{tree: withoutCrew})
	if len(m.stack) != 2 || m.cur().kind != frameProject {
		t.Fatalf("stack = %+v, want it still on the Project frame", m.stack)
	}

	withoutProject := query.Snapshot{WorkspaceID: full.WorkspaceID, Projects: full.Projects[1:]}
	m, _ = send(t, m, treeLoadedMsg{tree: withoutProject})
	if len(m.stack) != 1 || m.cur().kind != frameWorkspace {
		t.Fatalf("stack = %+v, want it unwound to the Workspace frame", m.stack)
	}
}

// TestRefreshUpdatesAsOf: the header's honesty depends on it. There is no
// UI-owned clock - the header reads Snapshot.AsOf straight from whatever
// LoadFunc returns - so this test's own load closure returns a later
// snapshot on the second call rather than injecting a clock into the model.
func TestRefreshUpdatesAsOf(t *testing.T) {
	tree := sampleTree()
	tree.AsOf = goldenAsOf
	later := goldenAsOf.Add(90 * time.Second)
	calls := 0
	m := New(func(context.Context) (query.Snapshot, error) {
		calls++
		if calls > 1 {
			tree.AsOf = later
		}
		return tree, nil
	}, nil)
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m, _ = send(t, m, m.Init()())
	if m.tree.AsOf != goldenAsOf {
		t.Fatalf("AsOf = %v, want the first read's snapshot time", m.tree.AsOf)
	}
	m, cmd := send(t, m, key("r"))
	if cmd == nil {
		t.Fatalf("r must re-read")
	}
	m, _ = send(t, m, cmd())
	if m.tree.AsOf != later {
		t.Fatalf("AsOf after a refresh = %v, want %v", m.tree.AsOf, later)
	}
	if !strings.Contains(renderFrame(t, m), "live "+m.g.Dot+" "+later.Format("15:04:05")) {
		t.Fatalf("header does not show the new read time")
	}
}

// TestTabSwitchesFocusWithAnInspectorAndOpensDetailWithout is the design's
// one new interaction, and the reason focus and Detail are separate fields:
// at 120 columns the inspector is beside the list, so Tab moves focus; at
// 80 there is nowhere to put it, so Tab covers the main region instead.
func TestTabSwitchesFocusWithAnInspectorAndOpensDetailWithout(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, key("tab"))
	if m.focus != paneInspector || m.detail {
		t.Fatalf("focus=%v detail=%v at 120 cols, want the inspector focused and no Detail", m.focus, m.detail)
	}
	m, _ = send(t, m, key("tab"))
	if m.focus != paneList {
		t.Fatalf("focus = %v, want Tab to come back to the list", m.focus)
	}

	m, _ = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = send(t, m, key("tab"))
	if !m.detail || m.focus != paneList {
		t.Fatalf("focus=%v detail=%v at 80 cols, want Detail open over the main region", m.focus, m.detail)
	}
	if !strings.Contains(renderFrame(t, m), "Detail view") {
		t.Fatalf("breadcrumb does not say the reader is in Detail")
	}
	m, _ = send(t, m, key("esc"))
	if m.detail {
		t.Fatalf("Esc did not close Detail")
	}
}

// TestShrinkingBelowTheInspectorBreakpointResetsFocusToTheList: focus names
// the pane the arrow keys act on, and paneInspector stops being a legal
// answer once the inspector column disappears. Left set, the arrow keys
// scroll a pane that is not drawn, no pane renders as accent, and the key
// line describes a Scroll behaviour the screen no longer has.
func TestShrinkingBelowTheInspectorBreakpointResetsFocusToTheList(t *testing.T) {
	m := loaded(t, sampleTree(), nil) // 120x36: has an inspector column
	m, _ = send(t, m, key("tab"))
	if m.focus != paneInspector {
		t.Fatalf("precondition: the inspector is not focused")
	}
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.focus != paneList {
		t.Fatalf("focus = %v after shrinking below the inspector breakpoint, want the list", m.focus)
	}
	if m.detail {
		t.Fatalf("Detail opened on its own after the resize; only Tab should open it")
	}
	// The arrow keys must land back on the list's own selection, not on the
	// stranded inspector's scroll offset.
	before := m.cur().sel
	m, _ = send(t, m, key("down"))
	if m.cur().sel == before {
		t.Fatalf("down did not move the list selection after the resize; it is still routed to the inspector")
	}
	if m.inspTop != 0 {
		t.Fatalf("inspTop = %d, want the scroll offset untouched by a list move", m.inspTop)
	}
}

// TestGrowingPastTheInspectorBreakpointClosesDetail: Detail is what stands
// in for the inspector below 100 columns. Left set at a width that has a
// real inspector column, the body would be full-width Detail under a rule
// drawn with a tee - a frame the chrome contract does not describe.
func TestGrowingPastTheInspectorBreakpointClosesDetail(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = send(t, m, key("tab"))
	if !m.detail {
		t.Fatalf("precondition: Detail is not open")
	}
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 48})
	if m.detail {
		t.Fatalf("Detail survived a resize to a width that has its own inspector column")
	}
	renderFrame(t, m)
}

// TestEscLeavesTheInspectorBeforeGoingUpALevel: Esc unwinds one step at a
// time, in the order the reader built them.
func TestEscLeavesTheInspectorBeforeGoingUpALevel(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, key("enter")) // Project
	m, _ = send(t, m, key("tab"))   // focus the inspector
	m, _ = send(t, m, key("esc"))
	if m.focus != paneList {
		t.Fatalf("focus = %v, want the list", m.focus)
	}
	if len(m.stack) != 2 {
		t.Fatalf("Esc left the level as well as the inspector: stack %+v", m.stack)
	}
	m, _ = send(t, m, key("esc"))
	if len(m.stack) != 1 {
		t.Fatalf("the second Esc did not go up a level: stack %+v", m.stack)
	}
}

// TestSelectionStaysVisibleAfterPagingAndShrinking is the scroll half of
// relayout: whatever the body height becomes, the selected row is inside
// the window that gets drawn.
func TestSelectionStaysVisibleAfterPagingAndShrinking(t *testing.T) {
	tree := query.Snapshot{WorkspaceID: "ws_1"}
	for i := 0; i < 60; i++ {
		tree.Projects = append(tree.Projects, query.ProjectNode{
			ProjectID: "proj_" + strings.Repeat("x", i%3) + itoa(i),
			Name:      "project-" + itoa(i),
			Mate:      absentMate("this project has no designated Mate"),
		})
	}
	m := loaded(t, tree, nil)
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 36}, {Width: 160, Height: 48}, {Width: 80, Height: 20}, {Width: 120, Height: 17}} {
		m, _ = send(t, m, size)
		for _, k := range []string{"pgdn", "pgdn", "down", "down", "pgup", "up"} {
			m, _ = send(t, m, key(k))
			l := layout(m.w, m.h)
			f := m.cur()
			top := clampTop(f.top, f.sel, len(m.currentRows()), l.listRows())
			if f.sel < top || f.sel >= top+l.listRows() {
				t.Fatalf("at %dx%d after %q: sel %d outside the visible window [%d,%d)", m.w, m.h, k, f.sel, top, top+l.listRows())
			}
			frame := renderFrame(t, m)
			sel := m.currentRows()[f.sel]
			want := sel.id
			if proj, ok := m.projectByID(sel.id); ok {
				want = proj.Name
			}
			if !strings.Contains(frame, want) {
				t.Fatalf("at %dx%d after %q the selected row is not on screen:\n%s", m.w, m.h, k, frame)
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestAFailedRefreshKeepsTheSnapshotAndSaysSo: the error screen states
// "no earlier snapshot is loaded", which is true of a first load and false
// of a refresh. Throwing away a usable picture over a transient read would
// also be worse for the reader than an unchanged one whose age the header
// already states.
func TestAFailedRefreshKeepsTheSnapshotAndSaysSo(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	before := renderFrame(t, m)
	m, _ = send(t, m, key("down"))
	wantSel := m.cur().selID

	m, _ = send(t, m, treeLoadedMsg{err: errors.New("database is locked")})
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want phaseReady: a failed refresh is not a failed load", m.phase)
	}
	if m.cur().selID != wantSel {
		t.Fatalf("selID = %q, want %q: a failed refresh must not move the selection", m.cur().selID, wantSel)
	}
	if m.tree.AsOf != goldenAsOf {
		t.Fatalf("AsOf = %v, want it unchanged: nothing was read", m.tree.AsOf)
	}
	view := renderFrame(t, m)
	if !strings.Contains(view, "Refresh failed") || !strings.Contains(view, "database is locked") {
		t.Fatalf("view = %q, want the failed refresh on the message line", view)
	}
	if !strings.Contains(view, "still showing the snapshot from "+goldenAsOf.Format("15:04:05")) {
		t.Fatalf("view = %q, want it to say what is still on screen", view)
	}
	if strings.Contains(view, "No earlier snapshot is loaded") {
		t.Fatalf("a failed refresh rendered the first-load error page:\n%s", view)
	}
	// And the rows are still there.
	for _, p := range sampleTree().Projects {
		if !strings.Contains(view, p.Name) {
			t.Fatalf("the Project %q vanished on a failed refresh:\n%s", p.Name, view)
		}
	}
	if before == view {
		t.Fatalf("the failed refresh left no trace at all on the frame")
	}
}

func TestQuitStopsTheProgram(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m, cmd := send(t, m, key("q"))
	if !m.Quitting() {
		t.Fatalf("q did not set quitting")
	}
	if cmd == nil {
		t.Fatalf("q must return tea.Quit")
	}
	if m.View() != "" {
		t.Fatalf("quitting view = %q, want empty", m.View())
	}
}

// TestViewIsEmptyBeforeTheFirstSizeMessage: every size comes from
// tea.WindowSizeMsg, so there is nothing to draw until one arrives - and
// nothing is drawn, rather than a frame at a guessed size.
func TestViewIsEmptyBeforeTheFirstSizeMessage(t *testing.T) {
	m := New(func(context.Context) (query.Snapshot, error) { return sampleTree(), nil })
	if got := m.View(); got != "" {
		t.Fatalf("View before any size message = %q, want empty", got)
	}
	m, _ = send(t, m, m.Init()())
	if got := m.View(); got != "" {
		t.Fatalf("View after a load but before any size message = %q, want empty", got)
	}
}

// TestOneUnreadableFieldDoesNotTakeTheWholeScreenToAnErrorPage: phase and
// query.FieldState are different axes. A crew whose worktree read failed
// still renders its row, its status and everything else that did read.
func TestOneUnreadableFieldDoesNotTakeTheWholeScreenToAnErrorPage(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Crews[0].Worktree = query.UnknownField[query.WorktreeValue]("worktree lookup failed")
	tree.Projects[0].Crews[0].AgentName = query.UnknownField[string]("binding lookup failed")
	tree.Projects[0].Crews[0].Binding = query.UnknownField[query.BindingValue]("binding lookup failed")
	m := loaded(t, tree, nil)
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want phaseReady: one field is not a read failure", m.phase)
	}
	m = toFailedAttempt(t, m)
	view := renderFrame(t, m)
	if !strings.Contains(view, string(query.CrewFailed)) {
		t.Fatalf("view = %q, want the recorded status of a crew whose other fields failed to read", view)
	}
	if !strings.Contains(view, "unknown") {
		t.Fatalf("view = %q, want the unreadable fields marked unknown", view)
	}
}
