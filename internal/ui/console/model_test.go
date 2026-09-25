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

func TestInitStartsLoadingAndSaysSo(t *testing.T) {
	m := New(func(context.Context) (query.Snapshot, error) { return query.Snapshot{}, nil })
	if m.phase != phaseLoading {
		t.Fatalf("phase = %v, want phaseLoading", m.phase)
	}
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 36})
	view := renderFrame(t, m)
	if !strings.Contains(view, "reading the workspace") {
		t.Fatalf("loading view does not say it is reading:\n%s", view)
	}
}

func TestTreeLoadFailurePinsFailedPhaseNotBlank(t *testing.T) {
	m := loaded(t, query.Snapshot{}, errors.New("db unreachable"))
	if m.phase != phaseFailed {
		t.Fatalf("phase = %v, want phaseFailed", m.phase)
	}
	view := renderFrame(t, m)
	for _, want := range []string{"can't read the workspace", "db unreachable", "Retry now"} {
		if !strings.Contains(view, want) {
			t.Fatalf("error view missing %q:\n%s", want, view)
		}
	}
	m, cmd := send(t, m, key("r"))
	if cmd == nil {
		t.Fatal("r on the error screen must retry the read")
	}
	if !m.treeLoadInFlight {
		t.Fatal("the retry must mark a load in flight")
	}
}

func TestEmptyWorkspaceRendersEmptyStateNotBlank(t *testing.T) {
	tree := query.Snapshot{WorkspaceID: "ws_1", Workspace: query.KnownField(query.WorkspaceValue{Name: "acme"})}
	m := loaded(t, tree, nil)
	view := renderFrame(t, m)
	for _, want := range []string{"No projects in acme yet.", "New project", "Refresh"} {
		if !strings.Contains(view, want) {
			t.Fatalf("empty view missing %q:\n%s", want, view)
		}
	}
	if first := strings.Split(view, "\n")[0]; !strings.Contains(first, " 0 ") {
		t.Fatalf("the list rule must count zero projects, got %q", first)
	}
	m, _ = send(t, m, key("n"))
	if !m.actionInputMode {
		t.Fatal("n on the empty workspace must open the new-project sheet")
	}
}

func TestNavigationDrillsInAndBackOutRestoringSelection(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
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

	m, _ = send(t, m, key("up"))
	m, _ = send(t, m, key("enter"))
	rows := m.currentRows()
	if len(rows) != 3 || rows[0].kind != rowMate || rows[1].kind != rowCrew || rows[2].kind != rowCompletedGroup {
		t.Fatalf("project rows = %+v, want the Mate row, one Crew and the Completed group", rows)
	}
	m, _ = send(t, m, key("down"))
	if r, ok := m.selectedRow(); !ok || r.kind != rowCrew {
		t.Fatalf("selected row = %+v (ok=%v), want the Crew row", r, ok)
	}
	m, _ = send(t, m, key("esc"))
	m, _ = send(t, m, key("esc"))
	if m.cur().kind != frameWorkspace || len(m.stack) != 1 {
		t.Fatalf("Esc at the root changed the stack: %+v", m.stack)
	}
}

// Preserving an index across a refresh is not enough: a Project inserted
// above the selection would move the reader onto a different row.
func TestRefreshKeepsSelectionByIdentityNotIndex(t *testing.T) {
	tree := sampleTree()
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("down"))
	wantID := m.cur().selID
	if wantID != tree.Projects[1].ProjectID {
		t.Fatalf("precondition: selID = %q, want the second Project", wantID)
	}
	inserted := query.Snapshot{WorkspaceID: tree.WorkspaceID, Workspace: tree.Workspace}
	inserted.Projects = append(inserted.Projects,
		query.ProjectNode{ProjectID: "proj_new", Name: "brand-new", Mate: absentMate("this project has no designated Mate")})
	inserted.Projects = append(inserted.Projects, tree.Projects...)

	m, _ = send(t, m, treeLoadedMsg{tree: inserted})
	if m.cur().selID != wantID || m.cur().sel != 2 {
		t.Fatalf("frame after an insert above = %+v, want selID %q at index 2", m.cur(), wantID)
	}
}

func TestRefreshClampsWhenTheSelectedRowIsGone(t *testing.T) {
	full := sampleTree()
	m := loaded(t, full, nil)
	m, _ = send(t, m, key("down"))
	shrunk := query.Snapshot{WorkspaceID: full.WorkspaceID, Projects: full.Projects[:1]}
	m, _ = send(t, m, treeLoadedMsg{tree: shrunk})
	if m.cur().sel != 0 || m.cur().selID != full.Projects[0].ProjectID {
		t.Fatalf("frame = %+v, want the selection clamped onto the one surviving Project", m.cur())
	}
}

// A Project the refresh no longer reports must not leave the reader inside
// an empty frame: the stack unwinds to the deepest level that resolves.
func TestRefreshDropsFramesWhoseEntityIsGone(t *testing.T) {
	full := sampleTree()
	m := loaded(t, full, nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))

	withoutCrew := sampleTree()
	withoutCrew.Projects[0].Crews = withoutCrew.Projects[0].Crews[:1]
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

// There is no UI-owned clock: ages are measured against Snapshot.AsOf, so
// a refresh that returns a later read moves them.
func TestRefreshUpdatesAsOfAndTheAgesMeasuredFromIt(t *testing.T) {
	tree := sampleTree()
	tree.AsOf = goldenAsOf
	later := goldenAsOf.Add(time.Hour)
	calls := 0
	m := New(func(context.Context) (query.Snapshot, error) {
		calls++
		if calls > 1 {
			tree.AsOf = later
		}
		return tree, nil
	})
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 36})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter")) // the Mate row: bound since 09:14:02
	if !strings.Contains(renderFrame(t, m), "running · 4h") {
		t.Fatalf("the Mate's age at the first read is not 4h:\n%s", renderFrame(t, m))
	}
	m, cmd := send(t, m, key("r"))
	if cmd == nil {
		t.Fatal("r must re-read")
	}
	m, _ = send(t, m, cmd())
	if m.tree.AsOf != later {
		t.Fatalf("AsOf after a refresh = %v, want %v", m.tree.AsOf, later)
	}
	if !strings.Contains(renderFrame(t, m), "running · 5h") {
		t.Fatalf("the Mate's age did not follow the new read:\n%s", renderFrame(t, m))
	}
}

// Tab cycles focus through the panes that are drawn - list, detail, box -
// and the focused pane's rule title is the one that moves.
func TestTabCyclesListDetailBox(t *testing.T) {
	m := projectFrame(t, sampleTree())
	want := []pane{paneDetail, paneBox, paneList}
	for _, w := range want {
		m, _ = send(t, m, key("tab"))
		if m.focus != w {
			t.Fatalf("focus = %v, want %v", m.focus, w)
		}
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.focus != paneBox {
		t.Fatalf("shift+tab from the list = %v, want the box", m.focus)
	}
}

// A frame without a box has no box to Tab to.
func TestTabSkipsAPaneThatIsNotDrawn(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Box = query.KnownField(query.BoxView{})
	m := projectFrame(t, tree)
	m, _ = send(t, m, key("tab"))
	m, _ = send(t, m, key("tab"))
	if m.focus != paneList {
		t.Fatalf("focus after two Tabs with no box = %v, want the list again", m.focus)
	}
}

// Below 20 rows there is no room to stack: Tab swaps detail in for the
// list, and growing the split back puts the stack back.
func TestBelowTwentyRowsTabSwapsDetailInForTheList(t *testing.T) {
	m := projectFrame(t, sampleTree())
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 16})
	m, _ = send(t, m, key("tab"))
	if m.focus != paneDetail || !m.detail {
		t.Fatalf("focus=%v detail=%v at 40x16, want detail swapped in", m.focus, m.detail)
	}
	p := m.plan()
	if _, ok := p.slot(slotList); ok {
		t.Fatalf("the list is still drawn with detail swapped in: %+v", p.slots)
	}
	if !strings.Contains(renderFrame(t, m), "agent") {
		t.Fatalf("the swapped-in detail does not show the Mate's fields:\n%s", renderFrame(t, m))
	}
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 36})
	if m.detail {
		t.Fatal("growing past 20 rows must restore the stack")
	}
	if _, ok := m.plan().slot(slotList); !ok {
		t.Fatal("the list is not drawn after growing back")
	}
}

// Esc leaves the detail pane before it leaves the level.
func TestEscLeavesDetailBeforeGoingUpALevel(t *testing.T) {
	m := projectFrame(t, sampleTree())
	m, _ = send(t, m, key("tab"))
	m, _ = send(t, m, key("esc"))
	if m.focus != paneList || len(m.stack) != 2 {
		t.Fatalf("focus=%v stack=%d after Esc in detail, want the list on the same level", m.focus, len(m.stack))
	}
	m, _ = send(t, m, key("esc"))
	if len(m.stack) != 1 {
		t.Fatalf("the second Esc did not go up a level: stack %+v", m.stack)
	}
}

// Whatever the frame's size becomes, the selected row is drawn, and the
// rows cut off above and below are counted.
func TestSelectionStaysVisibleAfterPagingAndShrinking(t *testing.T) {
	tree := query.Snapshot{WorkspaceID: "ws_1"}
	for i := 0; i < 60; i++ {
		tree.Projects = append(tree.Projects, query.ProjectNode{
			ProjectID: "proj_" + itoa(i),
			Name:      "project-" + itoa(i),
			Mate:      absentMate("this project has no designated Mate"),
		})
	}
	m := loaded(t, tree, nil)
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 36}, {Width: 48, Height: 48}, {Width: 40, Height: 20}, {Width: 36, Height: 16}} {
		m, _ = send(t, m, size)
		for _, k := range []string{"pgdn", "pgdn", "down", "down", "pgup", "up"} {
			m, _ = send(t, m, key(k))
			frame := renderFrame(t, m)
			want := tree.Projects[m.cur().sel].Name
			if !strings.Contains(frame, want+" ") {
				t.Fatalf("at %dx%d after %q the selected %s is not on screen:\n%s", m.w, m.h, k, want, frame)
			}
			if m.cur().sel > 0 && !strings.Contains(frame, "↑ ") {
				t.Fatalf("at %dx%d after %q rows above the window are not counted:\n%s", m.w, m.h, k, frame)
			}
		}
	}
}

// A failed refresh keeps the snapshot on screen and says so: the rows stay,
// the list rule says stale, and the status line says what failed.
func TestAFailedRefreshKeepsTheSnapshotAndSaysSo(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, key("down"))
	wantSel := m.cur().selID

	m, _ = send(t, m, treeLoadedMsg{err: errors.New("database is locked")})
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want phaseReady: a failed refresh is not a failed load", m.phase)
	}
	if m.cur().selID != wantSel {
		t.Fatalf("selID = %q, want %q", m.cur().selID, wantSel)
	}
	if m.tree.AsOf != goldenAsOf {
		t.Fatalf("AsOf = %v, want it unchanged", m.tree.AsOf)
	}
	if !strings.Contains(m.msg.text, "database is locked") || !strings.Contains(m.msg.text, goldenAsOf.Format("15:04:05")) {
		t.Fatalf("message = %q, want the failure and the time still on screen", m.msg.text)
	}
	view := renderFrame(t, m)
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[0], "stale") {
		t.Fatalf("list rule = %q, want it to say stale", lines[0])
	}
	if !strings.Contains(view, "Refresh failed") {
		t.Fatalf("status line does not say the refresh failed:\n%s", view)
	}
	if strings.Contains(view, "can't read the workspace") {
		t.Fatalf("a failed refresh rendered the first-load error screen:\n%s", view)
	}
	for _, p := range sampleTree().Projects {
		if !strings.Contains(view, p.Name) {
			t.Fatalf("the Project %q vanished on a failed refresh:\n%s", p.Name, view)
		}
	}
}

func TestQuitStopsTheProgram(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m, cmd := send(t, m, key("q"))
	if !m.Quitting() || cmd == nil {
		t.Fatalf("q: quitting=%v cmd=%v, want both", m.Quitting(), cmd != nil)
	}
	if m.View() != "" {
		t.Fatalf("quitting view = %q, want empty", m.View())
	}
}

// Every size comes from tea.WindowSizeMsg: nothing is drawn until one
// arrives.
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

// One unreadable field does not take the whole screen to an error page:
// the crew still renders its status, and the unreadable binding says
// unknown in detail.
func TestOneUnreadableFieldDoesNotTakeTheWholeScreenToAnErrorPage(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Crews[0].Worktree = query.UnknownField[query.WorktreeValue]("worktree lookup failed")
	tree.Projects[0].Crews[0].Binding = query.UnknownField[query.BindingValue]("binding lookup failed")
	m := loaded(t, tree, nil)
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want phaseReady", m.phase)
	}
	m = toFailedAttempt(t, m)
	view := renderFrame(t, m)
	if !strings.Contains(view, string(query.CrewFailed)) {
		t.Fatalf("view does not show the recorded status:\n%s", view)
	}
	if !strings.Contains(view, "unknown") {
		t.Fatalf("view does not mark the unreadable binding unknown:\n%s", view)
	}
}

// Too small draws the too-small screen and nothing else, and answers only q.
func TestTooSmallAnswersOnlyQ(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 28, Height: 10})
	view := renderFrame(t, m)
	if !strings.Contains(view, "too small: 28×10") || !strings.Contains(view, "needs 32×14") {
		t.Fatalf("too-small view:\n%s", view)
	}
	before := m.cur()
	m, _ = send(t, m, key("down"))
	if m.cur() != before {
		t.Fatalf("a key moved the selection on the too-small screen: %+v -> %+v", before, m.cur())
	}
	if _, cmd := send(t, m, key("q")); cmd == nil {
		t.Fatal("q must still quit on the too-small screen")
	}
}
