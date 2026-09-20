package console

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// ---------- golden fixtures: the Project level (Mate row + Crews) ----------
//
// frames_golden_test.go's own golden suite (the foundation's) covers the
// Workspace list but never drills into a Project - the one level with two
// stacked tables (design/mate-console-design-notes.html, "Trong pane").
// These fixtures close that gap.

func intoProject(t *testing.T, w, h int, g glyphSet) Model {
	t.Helper()
	m := newFixture(t, sampleTree(), w, h, g)
	m, _ = send(t, m, key("enter")) // payments-api: Mate row + its Crews
	return m
}

func TestGoldenProjectLevelAtEveryBreakpoint(t *testing.T) {
	assertGolden(t, "project-160x48-unicode", renderFrame(t, intoProject(t, 160, 48, unicodeGlyphs)))
	assertGolden(t, "project-120x36-unicode", renderFrame(t, intoProject(t, 120, 36, unicodeGlyphs)))
	assertGolden(t, "project-80x24-unicode", renderFrame(t, intoProject(t, 80, 24, unicodeGlyphs)))
	assertGolden(t, "project-80x24-ascii", renderFrame(t, intoProject(t, 80, 24, asciiGlyphs)))
	assertGolden(t, "project-140x40-ascii", renderFrame(t, intoProject(t, 140, 40, asciiGlyphs)))
}

// TestTokensColumnRendersHumanisedTotals (mvp.md M5 task 27): the Mate row
// and a Crew row both show the TOKENS column once query.TokenValue is
// Known, humanised the way `matev2 usage` renders the same numbers - a
// total alone when the model has no price, a total plus cost once it does.
func TestTokensColumnRendersHumanisedTotals(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Mate.Tokens = query.KnownField(query.TokenValue{Total: 96_300})
	cost := 0.12
	tree.Projects[0].Crews[1].Tokens = query.KnownField(query.TokenValue{Total: 1_200_000, Cost: &cost})

	m := newFixture(t, tree, 160, 48, unicodeGlyphs)
	m, _ = send(t, m, key("enter")) // into payments-api
	frame := renderFrame(t, m)

	if !strings.Contains(frame, "mate-payments-api") || !frameHasOnSameLine(frame, "mate-payments-api", "96.3k") {
		t.Fatalf("the Mate row does not show its token total:\n%s", frame)
	}
	if !frameHasOnSameLine(frame, "Add idempotency-key", "1.2M $0.12") {
		t.Fatalf("the Crew row does not show its token total and cost:\n%s", frame)
	}
}

func frameHasOnSameLine(frame, a, b string) bool {
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, a) && strings.Contains(l, b) {
			return true
		}
	}
	return false
}

// TestGoldenProjectWithoutMateAndWithoutCrews pins the two empty-ish
// Project states the gallery names explicitly: a Project with no designated
// Mate ("! no mate" in the Binding cell) and a Project with a Mate but zero
// Crews (the two-line empty message under the Crews header).
func TestGoldenProjectWithoutMateAndWithoutCrews(t *testing.T) {
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("down")) // ledger-worker: no designated Mate
	m, _ = send(t, m, key("enter"))
	assertGolden(t, "project-nomate-120x36-unicode", renderFrame(t, m))

	withoutCrews := noCrewsTree()
	m2 := newFixture(t, withoutCrews, 80, 24, unicodeGlyphs)
	m2, _ = send(t, m2, key("enter"))
	assertGolden(t, "project-nocrews-80x24-unicode", renderFrame(t, m2))
}

func noCrewsTree() query.Snapshot {
	return query.Snapshot{
		WorkspaceID: "ws_acme",
		Workspace:   query.KnownField(query.WorkspaceValue{Name: "acme"}),
		Projects: []query.ProjectNode{{
			ProjectID: "proj_docs",
			Name:      "docs-site",
			Mate: query.MateNode{
				Designated: query.KnownField(query.MateIdentity{MateID: "mate_docs", HarnessKind: query.HarnessClaude, Status: query.MateStopped}),
				AgentName:  query.KnownField("mate-docs-site"),
				Binding:    query.AbsentField[query.BindingValue]("no runtime binding is held"),
				Error:      query.AbsentField[query.ErrorReason](notErrorState),
			},
			Attention: query.AbsentField[query.ProjectAttention]("no crew in this project needs attention and its Mate is recorded healthy"),
		}},
	}
}

// ---------- selection identity at the Project level ----------

// TestProjectLevelSelectionMarksTheRightRow: the Project level's
// currentRows() puts the Mate row at position 0 and each Crew after it
// (rows.go's projectDetailRows). projectListItems must label a Crew's list
// item with that same position - not the Crew's own index into proj.Crews -
// or the Mate row and the first Crew collide on rowIdx 0 (both painted
// selected at once) while every later Crew's marker is drawn one row off
// from the selection Up/Down and Enter actually act on.
func TestProjectLevelSelectionMarksTheRightRow(t *testing.T) {
	m := newFixture(t, sampleTree(), 160, 48, unicodeGlyphs)
	m, _ = send(t, m, key("enter")) // payments-api: Mate row + its Crews
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

	running := sampleTree().Projects[0].Crews[1]
	m, _ = send(t, m, key("down")) // sel == 1: the one active Crew
	// "Add idempotency-key index" itself: the TOKENS column (mvp.md M5 task
	// 27) takes some of the width the TASK cell used to have at this pane
	// size, so only a prefix survives the cut.
	if got := markedLine(m); !strings.Contains(got, "Add idempotency-key") {
		t.Fatalf("sel=1 marked %q, want the running Crew's task line", got)
	}
	if got := markedLine(m); strings.Contains(got, "mate-payments-api") {
		t.Fatalf("sel=1 still marked the Mate row: %q", got)
	}
	if r, ok := m.selectedRow(); !ok || r.kind != rowCrew || r.id != running.CrewID {
		t.Fatalf("selectedRow() at sel=1 = %+v, want the running Crew", r)
	}

	m, _ = send(t, m, key("down")) // sel == 2: the Completed group
	if r, ok := m.selectedRow(); !ok || r.kind != rowCompletedGroup {
		t.Fatalf("selectedRow() at sel=2 = %+v, want the Completed group", r)
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
// (HARNESS 11, STATUS 11/17, MODE 12, ATTENTION 14) directly against the
// rendered header line, by locating each header word and measuring the gap
// to the next one - not by re-deriving the constants list.go already uses,
// which would just check the code against itself. The expected widths below
// are literal numbers, not colStatus/colCrewID/etc: a mutation that changes
// one of those constants (e.g. colStatus 17 -> 16) must fail this test,
// which it cannot do if the test's own expectation is the same constant.
func TestFixedColumnWidthsHoldAcrossLevels(t *testing.T) {
	m := newFixture(t, sampleTree(), 160, 48, unicodeGlyphs)
	l := layout(m.w, m.h)

	// Workspace level: MATE 24, CREWS 7, ATTENTION 14.
	wsHeader := m.listLines(l.List, l.Body)[0].render(l.List)
	requireColumnGap(t, wsHeader, "MATE", "CREWS", 24)
	requireColumnGap(t, wsHeader, "CREWS", "ATTENTION", 7)
	requireColumnGap(t, wsHeader, "ATTENTION", "UPDATED", 14)

	// Project level: HARNESS 8, STATUS 10, MODE 21 (its own Mate-row table -
	// MODE is the wide one because it carries "auto · sent 14:32:10"), then
	// blank, then the Crew table's STATUS 17, NOTE 14.
	proj, _ := send(t, m, key("enter"))
	header := proj.listLines(l.List, l.Body)[0].render(l.List)
	requireColumnGap(t, header, "HARNESS", "STATUS", 8)
	requireColumnGap(t, header, "STATUS", "MODE", 10)
	requireColumnGap(t, header, "MODE", "BINDING", 21)

	// items: [0] mate header, [1] mate row, [2] blank separator, [3] crews header.
	crewHeader := proj.listLines(l.List, l.Body)[3].render(l.List)
	requireColumnGap(t, crewHeader, "STATE", "NOTE", 17)
	requireColumnGap(t, crewHeader, "NOTE", "UPDATED", 14)
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

func manyCrewsTree(n int) query.Snapshot {
	var crews []query.CrewNode
	for i := 0; i < n; i++ {
		crews = append(crews, query.CrewNode{
			CrewID:      fmt.Sprintf("crew_%03d", i),
			Task:        fmt.Sprintf("task-%03d", i),
			Status:      query.CrewWorking,
			HarnessKind: query.HarnessClaude,
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
				Designated: query.KnownField(query.MateIdentity{MateID: "mate_big", HarnessKind: query.HarnessClaude, Status: query.MateRunning}),
				AgentName:  query.KnownField("mate-big"),
				Binding:    query.KnownField(query.BindingValue{Status: query.BindingActive}),
				Error:      query.AbsentField[query.ErrorReason](notErrorState),
			},
			Crews:     crews,
			Attention: query.AbsentField[query.ProjectAttention]("no crew in this project needs attention and its Mate is recorded healthy"),
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

// TestProjectLevelCrewsScrollThroughTheCombinedList: the Project level's
// Mate row and its Crews are one selection sequence (the Mate row is
// currentRows() index 0), so scrolling far enough into a long Crew list
// scrolls the Mate section off screen along with it - the same "whatever is
// off screen shows as N more" behaviour as any other level, not a special
// pinned header.
func TestProjectLevelCrewsScrollThroughTheCombinedList(t *testing.T) {
	m := loaded(t, manyCrewsTree(60), nil)
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
		t.Fatalf("after paging to the last Crew, it should be on screen:\n%s", frame)
	}
	if !strings.Contains(listPaneText(frame, m.g), "  "+m.g.Up+" ") {
		t.Fatalf("scrolled past the Mate section and most Crews, an up indicator is expected:\n%s", frame)
	}
	if got := m.currentRows()[m.cur().sel].id; got != "crew_059" {
		t.Fatalf("sel after 60 downs = row %q, want the last Crew crew_059", got)
	}
}

// TestProjectLevelScrollIndicators exercises the up/down indicator logic
// against a Project's 30 Crews.
func TestProjectLevelScrollIndicators(t *testing.T) {
	m := loaded(t, manyCrewsTree(30), nil)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m, _ = send(t, m, key("enter"))

	frame := renderFrame(t, m)
	list := listPaneText(frame, m.g)
	if strings.Contains(list, "  "+m.g.Up+" ") {
		t.Fatalf("at the top of the Crew list, an up indicator should not appear:\n%s", frame)
	}
	if !strings.Contains(list, "  "+m.g.Down+" ") {
		t.Fatalf("with 30 crews in a short body, a down indicator is expected:\n%s", frame)
	}
	for i := 0; i < 30; i++ {
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
				Designated: query.KnownField(query.MateIdentity{MateID: "mate_1", HarnessKind: query.HarnessClaude, Status: query.MateRunning}),
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

// TestUnknownCrewLastEventRendersHonestlyInTheProjectCrewList is the
// Crew-row counterpart: a Crew row's UPDATED cell at the Project level
// follows the same Unknown-must-not-render-blank rule as the Mate row's at
// the Workspace level.
func TestUnknownCrewLastEventRendersHonestlyInTheProjectCrewList(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Crews[1].LastEvent = query.UnknownField[query.EventValue]("event lookup timed out")
	m := newFixture(t, tree, 160, 48, unicodeGlyphs) // 160 wide: list pane clears the UPDATED boundary
	m, _ = send(t, m, key("enter"))                  // into payments-api
	frame := renderFrame(t, m)
	// A prefix, not the whole title: the TOKENS column (mvp.md M5 task 27)
	// leaves less room for TASK at this pane width, so the title is cut.
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "Add idempotency-key") {
			if !strings.Contains(l, "? unknown") {
				t.Fatalf("an unreadable Crew event should render as unknown in the UPDATED column:\n%s", l)
			}
			return
		}
	}
	t.Fatalf("the active Crew row is not on the frame:\n%s", frame)
}
