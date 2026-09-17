package console

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/query"
)

// ---------- golden fixtures: the Project level (Mate row + Tasks) ----------
//
// frames_golden_test.go's own golden suite (the foundation's) covers the
// Workspace list and a Task's Crew attempts, but never drills into a
// Project - the one level with two stacked tables (design/mate-console-
// design-notes.html, "Trong pane"). These fixtures close that gap.

func intoProject(t *testing.T, w, h int, g glyphSet) Model {
	t.Helper()
	m := newFixture(t, sampleTree(), w, h, g)
	m, _ = send(t, m, key("enter")) // payments-api: Mate row + two Tasks
	return m
}

func TestGoldenProjectLevelAtEveryBreakpoint(t *testing.T) {
	assertGolden(t, "project-160x48-unicode", renderFrame(t, intoProject(t, 160, 48, unicodeGlyphs)))
	assertGolden(t, "project-120x36-unicode", renderFrame(t, intoProject(t, 120, 36, unicodeGlyphs)))
	assertGolden(t, "project-80x24-unicode", renderFrame(t, intoProject(t, 80, 24, unicodeGlyphs)))
	assertGolden(t, "project-80x24-ascii", renderFrame(t, intoProject(t, 80, 24, asciiGlyphs)))
}

// TestGoldenTaskLevelAsciiGlyphs: frames_golden_test.go's own attempts-*
// fixtures cover the Task level in Unicode only. The list pane's own
// acceptance bar asks for both glyph sets at every level.
func TestGoldenTaskLevelAsciiGlyphs(t *testing.T) {
	m := newFixture(t, sampleTree(), 140, 40, asciiGlyphs)
	m, _ = send(t, m, key("enter")) // payments-api
	m, _ = send(t, m, key("down"))  // its first Task
	m, _ = send(t, m, key("enter")) // that Task's attempts
	assertGolden(t, "attempts-140x40-ascii", renderFrame(t, m))
}

// TestGoldenProjectWithoutMateAndWithoutTasks pins the two empty-ish
// Project states the gallery names explicitly: a Project with no designated
// Mate ("! no mate" in the Binding cell) and a Project with a Mate but zero
// Tasks (the two-line empty message under the Tasks header).
func TestGoldenProjectWithoutMateAndWithoutTasks(t *testing.T) {
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("down")) // ledger-worker: no designated Mate
	m, _ = send(t, m, key("enter"))
	assertGolden(t, "project-nomate-120x36-unicode", renderFrame(t, m))

	withoutTasks := noTasksTree()
	m2 := newFixture(t, withoutTasks, 80, 24, unicodeGlyphs)
	m2, _ = send(t, m2, key("enter"))
	assertGolden(t, "project-notasks-80x24-unicode", renderFrame(t, m2))
}

func noTasksTree() query.Snapshot {
	return query.Snapshot{
		WorkspaceID: "ws_acme",
		Workspace:   query.KnownField(query.WorkspaceValue{Name: "acme"}),
		Projects: []query.ProjectNode{{
			ProjectID: "proj_docs",
			Name:      "docs-site",
			Mate: query.MateNode{
				Designated: query.KnownField(query.MateIdentity{MateID: "mate_docs", HarnessKind: domain.HarnessClaude, Status: domain.MateStopped}),
				AgentName:  query.KnownField("mate-docs-site"),
				Binding:    query.AbsentField[query.BindingValue]("no runtime binding is held"),
				Error:      query.AbsentField[query.ErrorReason](notErrorState),
			},
			Attention: query.AbsentField[query.ProjectAttention]("no task in this project needs attention and its Mate is recorded healthy"),
		}},
	}
}

// ---------- selection identity at the Project level (B1) ----------

// TestProjectLevelSelectionMarksTheRightRow: the Project level's currentRows()
// puts the Mate row at position 0 and each Task at position idx+1 (rows.go's
// projectDetailRows). projectListItems must label a Task's list item with
// that same position - not the Task's own index into proj.Tasks - or the
// Mate row and the first Task collide on rowIdx 0 (both painted selected at
// once) while every later Task's marker is drawn one row off from the
// selection Up/Down and Enter actually act on.
func TestProjectLevelSelectionMarksTheRightRow(t *testing.T) {
	m := newFixture(t, sampleTree(), 160, 48, unicodeGlyphs)
	m, _ = send(t, m, key("enter")) // payments-api: Mate row + two Tasks
	l := layout(m.w, m.h)

	markedLine := func(m Model) string {
		t.Helper()
		focused := m.focus == paneList
		lines := m.buildListItems(m.currentRows(), l.List)
		for _, it := range lines {
			if it.rowIdx < 0 {
				continue
			}
			selected := it.rowIdx == m.cur().sel
			rendered := it.build(selected, focused).render(l.List)
			if selected {
				return rendered
			}
		}
		return ""
	}

	// sel == 0: only the Mate row is selected.
	if got := markedLine(m); !strings.Contains(got, "mate-payments-api") {
		t.Fatalf("initial selection marked %q, want the Mate row (mate-payments-api)", got)
	}

	m, _ = send(t, m, key("down")) // sel == 1: first Task
	if got := markedLine(m); !strings.Contains(got, "Fix webhook") {
		t.Fatalf("sel=1 marked %q, want the first Task (Fix webhook…)", got)
	}
	if got := markedLine(m); strings.Contains(got, "mate-payments-api") {
		t.Fatalf("sel=1 still marked the Mate row: %q", got)
	}
	if r, ok := m.selectedRow(); !ok || r.kind != rowTask || r.id != "task_01J9P2B4C6D8E0F2G4H6J8K0LM" {
		t.Fatalf("selectedRow() at sel=1 = %+v, want the first Task", r)
	}

	m, _ = send(t, m, key("down")) // sel == 2: second Task
	if got := markedLine(m); !strings.Contains(got, "Add idempotency-key index") {
		t.Fatalf("sel=2 marked %q, want the second Task (Add idempotency-key index)", got)
	}
	if r, ok := m.selectedRow(); !ok || r.kind != rowTask || r.id != "task_01J9P7N8P9Q0R1S2T3U4V5W6XY" {
		t.Fatalf("selectedRow() at sel=2 = %+v, want the second Task", r)
	}

	// Exactly one row is ever marked selected - never zero, never two.
	for sel := 0; sel < 3; sel++ {
		count := 0
		for _, it := range m.buildListItems(m.currentRows(), l.List) {
			if it.rowIdx == sel {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("rowIdx %d appears on %d list items, want exactly 1", sel, count)
		}
	}
}

// ---------- column widths ----------

// TestFixedColumnWidthsHoldAcrossLevels checks the design's own numbers
// (STATUS 17, ATTEMPTS 10, ATTENTION 14, HARNESS 13) directly against the
// rendered header line, by locating each header word and measuring the gap
// to the next one - not by re-deriving the constants list.go already uses,
// which would just check the code against itself. The expected widths below
// are literal numbers, not colStatus/colAttempts/etc: a mutation that
// changes one of those constants (e.g. colStatus 17 -> 16) must fail this
// test, which it cannot do if the test's own expectation is the same
// constant (N4, PR 49 counter-review).
func TestFixedColumnWidthsHoldAcrossLevels(t *testing.T) {
	m := newFixture(t, sampleTree(), 160, 48, unicodeGlyphs)
	l := layout(m.w, m.h)

	// Project level: HARNESS 13, STATUS 15, BINDING 18 (its own Mate-row
	// table), then blank, then TASKS with STATUS 17, ATTEMPTS 10, ATTENTION
	// 14, UPDATED 10.
	proj, _ := send(t, m, key("enter"))
	header := proj.listLines(l.List, l.Body)[0].render(l.List)
	requireColumnGap(t, header, "HARNESS", "STATUS", 13)
	requireColumnGap(t, header, "STATUS", "BINDING", 15)

	// items: [0] mate header, [1] mate row, [2] blank separator, [3] tasks header.
	tasksHeaderLine := proj.listLines(l.List, l.Body)[3].render(l.List)
	requireColumnGap(t, tasksHeaderLine, "STATUS", "ATTEMPTS", 17)
	requireColumnGap(t, tasksHeaderLine, "ATTEMPTS", "ATTENTION", 10)
	requireColumnGap(t, tasksHeaderLine, "ATTENTION", "UPDATED", 14)

	// Task level: STATUS 17, HARNESS 13.
	task, _ := send(t, proj, key("down"))
	task, _ = send(t, task, key("enter"))
	crewHeader := task.listLines(l.List, l.Body)[0].render(l.List)
	requireColumnGap(t, crewHeader, "STATUS", "HARNESS", 17)
}

// TestUpdatedColumnBoundaryAt89And90 (N4): the UPDATED column must be absent
// at list width 89 and present at list width 90 - the exact boundary
// wideList (list.go) draws. A mutation that shifts wideList's own w >=
// listWideMin comparison (e.g. to w >= 91) left the whole package green
// before this test existed, because nothing rendered a frame at either edge
// of the boundary; this pins both edges with literal terminal widths, not a
// value solved from wideList or listWideMin.
//
// layout.go gives a >=140-column terminal a 60-cell inspector, so its list
// pane is w-61: terminal width 150 is list width 89, and 151 is list width
// 90. Each width assertion below is a guard that the fixture still matches
// that arithmetic, not the thing under test.
func TestUpdatedColumnBoundaryAt89And90(t *testing.T) {
	below := newFixture(t, sampleTree(), 150, 40, unicodeGlyphs)
	l := layout(below.w, below.h)
	if l.List != 89 {
		t.Fatalf("terminal width 150 produced list width %d, want 89", l.List)
	}
	header := below.listLines(l.List, l.Body)[0].render(l.List)
	if strings.Contains(header, "UPDATED") {
		t.Fatalf("UPDATED header present at list width 89, want it hidden below the boundary:\n%s", header)
	}

	atBoundary := newFixture(t, sampleTree(), 151, 40, unicodeGlyphs)
	l2 := layout(atBoundary.w, atBoundary.h)
	if l2.List != 90 {
		t.Fatalf("terminal width 151 produced list width %d, want 90", l2.List)
	}
	header2 := atBoundary.listLines(l2.List, l2.Body)[0].render(l2.List)
	if !strings.Contains(header2, "UPDATED") {
		t.Fatalf("UPDATED header missing at list width 90, want it shown at the boundary:\n%s", header2)
	}
}

// requireColumnGap asserts that the header word `to` starts exactly w cells
// after `from` starts, which is what a fixed column width of w means for
// two adjacent headers - independent of anything list.go computed to get
// there.
func requireColumnGap(t *testing.T, line, from, to string, w int) {
	t.Helper()
	fromCol := cellIndex(line, from)
	toCol := cellIndex(line, to)
	if fromCol < 0 || toCol < 0 {
		t.Fatalf("header %q does not contain both %q and %q", line, from, to)
	}
	if got := toCol - fromCol; got != w {
		t.Fatalf("gap between %q and %q = %d cells, want the fixed column width %d\n%s", from, to, got, w, line)
	}
}

// ---------- many-row fixtures for scrolling ----------

func manyProjectsTree(n int) query.Snapshot {
	tree := query.Snapshot{WorkspaceID: "ws_many", Workspace: query.KnownField(query.WorkspaceValue{Name: "many"})}
	for i := 0; i < n; i++ {
		tree.Projects = append(tree.Projects, query.ProjectNode{
			ProjectID: fmt.Sprintf("proj_%03d", i),
			Name:      fmt.Sprintf("project-%03d", i),
			Mate:      absentMate("this project has no designated Mate"),
			Attention: query.KnownField(query.ProjectAttention{Kind: query.AttentionNoMate, Why: "no mate"}),
		})
	}
	return tree
}

func manyTasksTree(n int) query.Snapshot {
	var tasks []query.TaskNode
	for i := 0; i < n; i++ {
		tasks = append(tasks, query.TaskNode{
			TaskID:    fmt.Sprintf("task_%03d", i),
			Title:     fmt.Sprintf("task-%03d", i),
			Status:    domain.TaskReady,
			Error:     query.AbsentField[query.ErrorReason](notErrorState),
			Attention: query.AbsentField[query.Attention]("no attempt has been started and the task is recorded ready"),
		})
	}
	return query.Snapshot{
		WorkspaceID: "ws_many",
		Workspace:   query.KnownField(query.WorkspaceValue{Name: "many"}),
		Projects: []query.ProjectNode{{
			ProjectID: "proj_big", Name: "big-project",
			Mate: query.MateNode{
				Designated: query.KnownField(query.MateIdentity{MateID: "mate_big", HarnessKind: domain.HarnessClaude, Status: domain.MateRunning}),
				AgentName:  query.KnownField("mate-big"),
				Binding:    query.KnownField(query.BindingValue{Status: query.BindingActive}),
				Error:      query.AbsentField[query.ErrorReason](notErrorState),
			},
			Tasks:     tasks,
			Attention: query.AbsentField[query.ProjectAttention]("no task in this project needs attention and its Mate is recorded healthy"),
		}},
	}
}

func manyCrewsTree(n int) query.Snapshot {
	var crews []query.CrewNode
	for i := 0; i < n; i++ {
		crews = append(crews, query.CrewNode{
			CrewID:      fmt.Sprintf("crew_%03d", i),
			Attempt:     i + 1,
			Status:      domain.CrewRunning,
			HarnessKind: domain.HarnessClaude,
			RetryOf:     query.AbsentField[query.RetryValue]("first attempt"),
			Error:       query.AbsentField[query.ErrorReason](notErrorState),
			Attention:   query.AbsentField[query.Attention]("nothing about it needs attention"),
		})
	}
	return query.Snapshot{
		WorkspaceID: "ws_many",
		Workspace:   query.KnownField(query.WorkspaceValue{Name: "many"}),
		Projects: []query.ProjectNode{{
			ProjectID: "proj_big", Name: "big-project",
			Mate: query.MateNode{
				Designated: query.KnownField(query.MateIdentity{MateID: "mate_big", HarnessKind: domain.HarnessClaude, Status: domain.MateRunning}),
				AgentName:  query.KnownField("mate-big"),
				Binding:    query.KnownField(query.BindingValue{Status: query.BindingActive}),
				Error:      query.AbsentField[query.ErrorReason](notErrorState),
			},
			Tasks: []query.TaskNode{{
				TaskID: "task_big", Title: "big-task", Status: domain.TaskRunning,
				Error:     query.AbsentField[query.ErrorReason](notErrorState),
				Attention: query.AbsentField[query.Attention]("nothing about it needs attention"),
				Crews:     crews,
			}},
			Attention: query.AbsentField[query.ProjectAttention]("no task in this project needs attention and its Mate is recorded healthy"),
		}},
	}
}

// ---------- paging and scroll indicators ----------

// TestScrollIndicatorsAtTopMiddleAndBottom drives the Workspace list (40
// Projects, far more than any tested body height fits) to the top, the
// middle and the bottom, and checks the "n more" indicators at each stop:
// none above at the top, both above and below in the middle, none below at
// the bottom.
func TestScrollIndicatorsAtTopMiddleAndBottom(t *testing.T) {
	m := loaded(t, manyProjectsTree(40), nil)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})

	frame := renderFrame(t, m)
	if strings.Contains(frame, "  "+m.g.Up+" ") {
		t.Fatalf("at the top of the list, an up indicator should not appear:\n%s", frame)
	}
	if !strings.Contains(frame, "  "+m.g.Down+" ") {
		t.Fatalf("with 40 rows in a short body, a down indicator is expected:\n%s", frame)
	}

	for i := 0; i < 15; i++ {
		m, _ = send(t, m, key("down"))
	}
	frame = renderFrame(t, m)
	if !strings.Contains(frame, "  "+m.g.Up+" ") || !strings.Contains(frame, "  "+m.g.Down+" ") {
		t.Fatalf("in the middle of a long list, both indicators are expected:\n%s", frame)
	}

	for i := 0; i < 40; i++ {
		m, _ = send(t, m, key("down"))
	}
	frame = renderFrame(t, m)
	if strings.Contains(frame, "  "+m.g.Down+" ") {
		t.Fatalf("at the bottom of the list, a down indicator should not appear:\n%s", frame)
	}
	if !strings.Contains(frame, "  "+m.g.Up+" ") {
		t.Fatalf("scrolled to the bottom of a long list, an up indicator is expected:\n%s", frame)
	}
	if f := m.cur(); f.sel != 39 {
		t.Fatalf("sel = %d, want the last row (39) after moving past the end", f.sel)
	}
}

// TestPgDnPgUpMoveByAFullPage: PgDn/PgUp move the selection by roughly a
// screenful rather than one row at a time.
func TestPgDnPgUpMoveByAFullPage(t *testing.T) {
	m := loaded(t, manyProjectsTree(40), nil)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	start := m.cur().sel
	m, _ = send(t, m, key("pgdn"))
	afterOne := m.cur().sel
	if afterOne <= start+1 {
		t.Fatalf("pgdn moved sel from %d to %d, want a jump bigger than a single row", start, afterOne)
	}
	m, _ = send(t, m, key("pgup"))
	if got := m.cur().sel; got >= afterOne {
		t.Fatalf("pgup did not move the selection back up: %d -> %d", afterOne, got)
	}
}

// TestJKMoveTheListSelectionLikeArrowKeys: j/k are the vim-style aliases for
// down/up in the list (design/mate-console-design-notes.html, key table).
func TestJKMoveTheListSelectionLikeArrowKeys(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, key("j"))
	if got := m.cur().sel; got != 1 {
		t.Fatalf("sel after j = %d, want 1", got)
	}
	m, _ = send(t, m, key("k"))
	if got := m.cur().sel; got != 0 {
		t.Fatalf("sel after k = %d, want 0", got)
	}
}

// listPaneText strips everything from the divider column onward on every
// line of a rendered frame, so a check for the list pane's own "N more"
// indicator cannot false-positive on the inspector's independent scroll
// indicator (seams.go's windowContent draws the identical glyph) when a
// selected row's field set happens to overflow the inspector column too.
func listPaneText(frame string, g glyphSet) string {
	lines := strings.Split(frame, "\n")
	for i, l := range lines {
		if idx := strings.Index(l, g.VRule); idx >= 0 {
			lines[i] = l[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

// TestTaskLevelScrollIndicators exercises the same up/down indicator logic
// against a Task's Crew attempts (30 attempts), a single flat table with no
// interleaved headers - the simpler of the two shapes the list pane draws.
func TestTaskLevelScrollIndicators(t *testing.T) {
	m := loaded(t, manyCrewsTree(30), nil)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m, _ = send(t, m, key("enter")) // the Project's Mate row + one Task
	m, _ = send(t, m, key("down"))  // the Task row
	m, _ = send(t, m, key("enter"))

	frame := renderFrame(t, m)
	list := listPaneText(frame, m.g)
	if strings.Contains(list, "  "+m.g.Up+" ") {
		t.Fatalf("at the top of the Crew list, an up indicator should not appear:\n%s", frame)
	}
	if !strings.Contains(list, "  "+m.g.Down+" ") {
		t.Fatalf("with 30 attempts in a short body, a down indicator is expected:\n%s", frame)
	}
	for i := 0; i < 29; i++ {
		m, _ = send(t, m, key("down"))
	}
	frame = renderFrame(t, m)
	list = listPaneText(frame, m.g)
	if strings.Contains(list, "  "+m.g.Down+" ") {
		t.Fatalf("at the bottom of the Crew list, a down indicator should not appear:\n%s", frame)
	}
	if !strings.Contains(list, "  "+m.g.Up+" ") {
		t.Fatalf("scrolled to the bottom, an up indicator is expected:\n%s", frame)
	}
}

// TestProjectLevelTasksScrollThroughTheCombinedList: the Project level's
// Mate row and its Tasks are one selection sequence (the Mate row is
// currentRows() index 0), so scrolling far enough into a long Tasks list
// scrolls the Mate section off screen along with it - the same "whatever is
// off screen shows as N more" behaviour as any other level, not a special
// pinned header.
func TestProjectLevelTasksScrollThroughTheCombinedList(t *testing.T) {
	m := loaded(t, manyTasksTree(60), nil)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	m, _ = send(t, m, key("enter"))

	frame := renderFrame(t, m)
	if !strings.Contains(frame, "MATE") || !strings.Contains(frame, "mate-big") {
		t.Fatalf("the Mate section should be visible before any scrolling:\n%s", frame)
	}

	for i := 0; i < 60; i++ {
		m, _ = send(t, m, key("down"))
	}
	frame = renderFrame(t, m)
	if !strings.Contains(frame, "task-059") {
		t.Fatalf("after paging to the last Task, it should be on screen:\n%s", frame)
	}
	if !strings.Contains(frame, "  "+m.g.Up+" ") {
		t.Fatalf("scrolled past the Mate section and most Tasks, an up indicator is expected:\n%s", frame)
	}
	if got := m.currentRows()[m.cur().sel].id; got != "task_059" {
		t.Fatalf("sel after 60 downs = row %q, want the last Task task_059", got)
	}
}

// ---------- selection across a refresh ----------

// TestSelectionSurvivesRefreshReorder: a refresh that changes row order (a
// new Project inserted ahead of the selected one) keeps the selection on
// the same Project by id, not by index.
func TestSelectionSurvivesRefreshReorder(t *testing.T) {
	tree := manyProjectsTree(5)
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("down")) // proj_001
	m, _ = send(t, m, key("down")) // proj_002
	if got := m.cur().sel; got != 2 {
		t.Fatalf("sel = %d, want 2", got)
	}
	selID := m.tree.Projects[2].ProjectID

	reordered := manyProjectsTree(5)
	reordered.Projects = append([]query.ProjectNode{{
		ProjectID: "proj_new", Name: "project-new", Mate: absentMate("no mate"),
		Attention: query.KnownField(query.ProjectAttention{Kind: query.AttentionNoMate}),
	}}, reordered.Projects...)
	m, _ = send(t, m, treeLoadedMsg{tree: reordered})

	f := m.cur()
	if f.selID != selID {
		t.Fatalf("selID after reorder = %q, want it to stay %q", f.selID, selID)
	}
	if got := m.currentRows()[f.sel].id; got != selID {
		t.Fatalf("sel after reorder points at row %q, want %q", got, selID)
	}
}

// TestSelectionClampsWhenRowDisappears: a refresh that removes the selected
// row clamps to the nearest surviving neighbour instead of pointing at
// nothing.
func TestSelectionClampsWhenRowDisappears(t *testing.T) {
	m := loaded(t, manyProjectsTree(5), nil)
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down")) // proj_004, the last row

	shrunk := manyProjectsTree(2) // proj_000, proj_001 only
	m, _ = send(t, m, treeLoadedMsg{tree: shrunk})

	f := m.cur()
	rows := m.currentRows()
	if f.sel < 0 || f.sel >= len(rows) {
		t.Fatalf("sel = %d after the selected row disappeared, want it clamped inside [0,%d)", f.sel, len(rows))
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, m.currentRows()[f.sel].id) {
		// The row id itself is not painted for a Project row (only its
		// Name is); assert the clamp landed on the last surviving row.
		if f.sel != len(rows)-1 {
			t.Fatalf("sel = %d, want the clamp to land on the last surviving row (%d)", f.sel, len(rows)-1)
		}
	}
}

// ---------- honesty: Unknown renders as unknown, never blank or absent ----------

// TestUnknownAttentionFieldNeverRendersBlank: a Field[Attention] left at its
// Go zero value (State == "") is neither Known nor Absent - attentionSpans
// must still say unknown rather than leaving the cell blank, which the
// zero-value struct would otherwise silently produce.
func TestUnknownAttentionFieldNeverRendersBlank(t *testing.T) {
	p := plainPalette()
	var zero query.Field[query.Attention]
	got := renderSpans(attentionSpans(zero, p), 20)
	if strings.TrimSpace(got) == "" {
		t.Fatalf("an unset Attention field rendered blank, want an honest unknown marker")
	}
	if !strings.Contains(got, "unknown") {
		t.Fatalf("unset Attention field = %q, want it to say unknown", got)
	}
}

// TestUnknownMateBindingRendersHonestlyInTheProjectList: a Mate whose
// binding read failed must say "unknown" in the Binding cell of the
// Project-level Mate row, never "none" (which would claim there is
// definitely no binding) and never a blank cell.
func TestUnknownMateBindingRendersHonestlyInTheProjectList(t *testing.T) {
	tree := query.Snapshot{
		WorkspaceID: "ws_1",
		Projects: []query.ProjectNode{{
			ProjectID: "proj_1", Name: "acme",
			Mate: query.MateNode{
				Designated: query.KnownField(query.MateIdentity{MateID: "mate_1", HarnessKind: domain.HarnessClaude, Status: domain.MateRunning}),
				AgentName:  query.KnownField("mate-acme"),
				Binding:    query.UnknownField[query.BindingValue]("lookup timed out (2s)"),
				Error:      query.AbsentField[query.ErrorReason](notErrorState),
			},
			Attention: query.AbsentField[query.ProjectAttention]("healthy"),
		}},
	}
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "unknown") {
		t.Fatalf("an unreadable binding should render as unknown somewhere on screen:\n%s", frame)
	}
}

// TestUnknownMateDesignationRendersHonestlyInTheProjectList (B2): a Project
// whose Mate *designation* lookup itself failed must say "unknown" in the
// Agent cell of the Project-level Mate row, never the confident "none
// assigned" - that phrase asserts the read succeeded and found no Mate,
// which is exactly what did not happen here.
func TestUnknownMateDesignationRendersHonestlyInTheProjectList(t *testing.T) {
	tree := query.Snapshot{
		WorkspaceID: "ws_1",
		Projects: []query.ProjectNode{{
			ProjectID: "proj_1", Name: "acme",
			Mate: query.MateNode{
				Designated: query.UnknownField[query.MateIdentity]("designation lookup timed out"),
				AgentName:  query.UnknownField[string]("designation lookup timed out"),
				Binding:    query.UnknownField[query.BindingValue]("designation lookup timed out"),
				Error:      query.AbsentField[query.ErrorReason](notErrorState),
			},
			Attention: query.KnownField(query.ProjectAttention{Kind: query.AttentionUnreadable, Why: "designation lookup timed out"}),
		}},
	}
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	frame := renderFrame(t, m)
	if strings.Contains(frame, "none assigned") {
		t.Fatalf("an unreadable Mate designation rendered as the confident \"none assigned\":\n%s", frame)
	}
	if !strings.Contains(frame, "unknown") {
		t.Fatalf("an unreadable Mate designation should render as unknown somewhere on screen:\n%s", frame)
	}
}

// TestMateWithNoDesignatedMateStillSaysNoneAssigned pins the Absent
// counterpart of the test above: a Project the read layer has confirmed has
// no Mate keeps saying "none assigned" - B2's fix must not turn that
// legitimate, Known-absence case into an unknown marker too.
func TestMateWithNoDesignatedMateStillSaysNoneAssigned(t *testing.T) {
	tree := query.Snapshot{
		WorkspaceID: "ws_1",
		Projects: []query.ProjectNode{{
			ProjectID: "proj_1", Name: "acme",
			Mate:      absentMate("this project has no designated Mate"),
			Attention: query.KnownField(query.ProjectAttention{Kind: query.AttentionNoMate, Why: "no mate"}),
		}},
	}
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "none assigned") {
		t.Fatalf("a Project confirmed to have no Mate should still say \"none assigned\":\n%s", frame)
	}
	if strings.Contains(frame, "? unknown") {
		t.Fatalf("a confirmed-absent Mate should not render as unknown:\n%s", frame)
	}
}

// TestUnknownLastEventRendersHonestlyInTheUpdatedColumn (B3): an event read
// failure in the UPDATED column must say "unknown", never collapse to the
// same blank cell a legitimate "no event yet" (Absent) renders as.
func TestUnknownLastEventRendersHonestlyInTheUpdatedColumn(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Mate.LastEvent = query.UnknownField[query.EventValue]("event lookup timed out")
	m := newFixture(t, tree, 160, 48, unicodeGlyphs) // 160 wide: list pane clears the UPDATED boundary
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "? unknown") {
		t.Fatalf("an unreadable last-event should render as unknown in the UPDATED column:\n%s", frame)
	}
}

// TestUnknownLatestCrewLastEventRendersHonestlyInTheProjectTaskList is B3's
// Task-row counterpart: latestCrewLastEvent (list.go) supplies the UPDATED
// cell for a Task row at the Project level exactly the way the Mate row
// supplies it at the Workspace level, and the same Unknown-must-not-render-
// blank rule applies there too. Reverting latestCrewLastEvent to fold
// Unknown into Absent (restoring a blank UPDATED cell for a read failure,
// while leaving Known timestamps untouched) previously left the whole
// console package green, because no test entered a Project and gave its
// latest Crew an unknown event.
func TestUnknownLatestCrewLastEventRendersHonestlyInTheProjectTaskList(t *testing.T) {
	tree := sampleTree()
	// task_01J9P2B4... has two Crews; index len-1 (attempt 2) is what
	// latestCrewLastEvent reads.
	tree.Projects[0].Tasks[0].Crews[1].LastEvent = query.UnknownField[query.EventValue]("event lookup timed out")
	m := newFixture(t, tree, 160, 48, unicodeGlyphs) // 160 wide: list pane clears the UPDATED boundary
	m, _ = send(t, m, key("enter"))                  // into payments-api
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "? unknown") {
		t.Fatalf("an unreadable latest-Crew event should render as unknown in a Task row's UPDATED column:\n%s", frame)
	}
	// The second Task (task_01J9P7N8...) has no Crews at all: its UPDATED
	// cell is a legitimate Absent blank, distinct from the Unknown case
	// above - the frame must carry both without collapsing them.
	if strings.Contains(frame, "Add idempotency-key index") {
		lines := strings.Split(frame, "\n")
		for _, l := range lines {
			if strings.Contains(l, "Add idempotency-key index") && strings.Contains(l, "? unknown") {
				t.Fatalf("a Task with no attempts must not render \"? unknown\" in UPDATED:\n%s", l)
			}
		}
	}
}
