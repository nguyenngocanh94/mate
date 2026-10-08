package console

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

const bxDiffText = `9f1c2ab add the orders index

diff --git a/db/migrations/0007_orders_created_at.sql b/db/migrations/0007_orders_created_at.sql
new file mode 100644
--- /dev/null
+++ b/db/migrations/0007_orders_created_at.sql
@@ -0,0 +1,2 @@
+CREATE INDEX orders_created_at ON orders (created_at);
+ANALYZE orders;
diff --git a/internal/orders/list.go b/internal/orders/list.go
--- a/internal/orders/list.go
+++ b/internal/orders/list.go
@@ -12,3 +12,4 @@ func List(ctx context.Context) error {
-	return scan(ctx)
+	rows := scan(ctx)
+	return rows.Close()
 }
the last line of the patch
`

// bxCrewMenu opens the actions sheet on payments-api's codex crew (k7).
func bxCrewMenu(t *testing.T, tree query.Snapshot, act *bxAction) Model {
	t.Helper()
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	m.action = act.run
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	m, _ = send(t, m, key("enter"))
	var found bool
	m, found = m.jumpToCrew("k7")
	if !found {
		t.Fatal("could not locate crew k7")
	}
	if r, ok := m.selectedRow(); !ok || r.kind != rowCrew || r.id != "k7" {
		t.Fatalf("selected row = %+v, want crew k7", r)
	}
	m, _ = send(t, m, key("a"))
	return m
}

func bxDiffEntry(t *testing.T, m Model) menuEntry {
	t.Helper()
	for _, e := range m.menu {
		if e.key == "d" {
			return e
		}
	}
	t.Fatalf("the crew's actions offer no diff: %+v", m.menu)
	return menuEntry{}
}

// bxOpenDiff runs d on the crew's sheet and delivers the result.
func bxOpenDiff(t *testing.T, text string, runErr error) (Model, *bxAction) {
	t.Helper()
	act := &bxAction{out: text, err: runErr}
	m := bxCrewMenu(t, designTree(), act)
	m, cmd := send(t, m, key("d"))
	if cmd == nil {
		t.Fatal("d on the crew's actions ran nothing")
	}
	m, _ = send(t, m, cmd())
	return m, act
}

func TestDiffSendsTheProjectAndTheCrew(t *testing.T) {
	m, act := bxOpenDiff(t, bxDiffText, nil)
	if len(act.reqs) != 1 {
		t.Fatalf("requests = %+v, want one", act.reqs)
	}
	if r := act.reqs[0]; r.Action != ActionDiff || r.Target != "payments-api" || r.TargetKind != "crew" || r.Crew != "k7" {
		t.Fatalf("request = %+v", r)
	}
	if !m.diff.open || m.sheetOpen() != sheetDiff {
		t.Fatal("the diff did not open as a sheet")
	}
	frame := renderFrame(t, m)
	for _, want := range []string{"diff · k7", "CREATE INDEX orders_created_at", "esc"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("diff sheet is missing %q:\n%s", want, frame)
		}
	}
	// The sheet never covers the selected row.
	if !strings.Contains(frame, "▌ 🤖 Add index to orders.created_at") {
		t.Fatalf("the diff sheet covered the crew it was opened for:\n%s", frame)
	}
}

func TestDiffIsOfferedOnACrewInEveryStateThatHasABranch(t *testing.T) {
	for _, status := range []query.CrewStatus{query.CrewSpawned, query.CrewWorking, query.CrewNeedsDecision, query.CrewWaitMate} {
		t.Run(string(status), func(t *testing.T) {
			tree := designTree()
			tree.Projects[7].Crews[0].Status = status
			e := bxDiffEntry(t, bxCrewMenu(t, tree, &bxAction{}))
			if !e.enabled {
				t.Fatalf("diff on a %s crew = %+v, want it offered", status, e)
			}
			if !strings.Contains(e.about, "crew/k7") {
				t.Fatalf("diff description %q does not name the branch it would show", e.about)
			}
		})
	}
}

func TestDiffIsRefusedWhenTheCrewRecordsNoBranch(t *testing.T) {
	tree := designTree()
	tree.Projects[7].Crews[0].Worktree = query.AbsentField[query.WorktreeValue]("this crew has no worktree row")
	e := bxDiffEntry(t, bxCrewMenu(t, tree, &bxAction{}))
	if e.enabled || !strings.Contains(e.reason, "records no branch") {
		t.Fatalf("diff on a branchless crew = %+v, want it refused saying why", e)
	}
}

func TestDiffRefusalDistinguishesAnUnreadableWorktreeFromNone(t *testing.T) {
	tree := designTree()
	tree.Projects[7].Crews[0].Worktree = query.UnknownField[query.WorktreeValue]("worktree read timed out")
	e := bxDiffEntry(t, bxCrewMenu(t, tree, &bxAction{}))
	if e.enabled || !strings.Contains(e.about, "could not be read") || strings.Contains(e.about, "records no branch") {
		t.Fatalf("diff on an unreadable worktree = %+v, want it refused saying the read failed", e)
	}
}

func TestDiffSheetScrollsAndCloses(t *testing.T) {
	base, _ := bxOpenDiff(t, bxDiffText, nil)
	m, _ := send(t, base, key("down"))
	if m.diff.top != 1 {
		t.Fatalf("down did not scroll: top=%d", m.diff.top)
	}
	m, _ = send(t, m, key("up"))
	m, _ = send(t, m, key("up"))
	if m.diff.top != 0 {
		t.Fatalf("up ran past the top: top=%d", m.diff.top)
	}
	// The tail is reachable and the offset never runs past it.
	for i := 0; i < 100; i++ {
		m, _ = send(t, m, key("down"))
	}
	if !strings.Contains(renderFrame(t, m), "the last line of the patch") {
		t.Fatalf("the last line is not reachable:\n%s", renderFrame(t, m))
	}
	top := m.diff.top
	m, _ = send(t, m, key("down"))
	if m.diff.top != top {
		t.Fatalf("the offset ran past the end: %d -> %d", top, m.diff.top)
	}
	for _, k := range []string{"esc", "enter", "q"} {
		closed, cmd := send(t, base, key(k))
		if closed.diff.open || closed.quitting || cmd != nil {
			t.Fatalf("%s: open=%v quitting=%v cmd=%v, want the sheet closed and the Console kept", k, closed.diff.open, closed.quitting, cmd != nil)
		}
		if r, _ := closed.selectedRow(); r.id != "k7" {
			t.Fatalf("after %s the selection is %+v, want the crew it was opened from", k, r)
		}
	}
	quit, _ := send(t, base, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !quit.quitting {
		t.Fatal("ctrl+c inside the diff did not quit")
	}
}

// The sheet is modal: keys behind it neither move nor open anything.
func TestDiffSheetSwallowsTheKeysBehindIt(t *testing.T) {
	m, act := bxOpenDiff(t, bxDiffText, nil)
	before, _ := m.selectedRow()
	for _, k := range []string{"a", "r", "tab", "m", "s", "n", "?"} {
		m, _ = send(t, m, key(k))
	}
	if !m.diff.open || m.actions || m.actionInputMode || m.harnessPick || m.keysOpen {
		t.Fatalf("a key behind the diff changed the surface: diff=%v actions=%v", m.diff.open, m.actions)
	}
	if after, _ := m.selectedRow(); after != before {
		t.Fatalf("selection moved behind the diff: %+v -> %+v", before, after)
	}
	if len(act.reqs) != 1 {
		t.Fatalf("a key behind the diff dispatched another action: %+v", act.reqs)
	}
}

func TestDiffWheelScrollsTheSheet(t *testing.T) {
	m, _ := bxOpenDiff(t, bxDiffText, nil)
	sl, ok := m.plan().slot(slotSheet)
	if !ok {
		t.Fatal("no sheet slot while the diff is open")
	}
	wheel := func(b tea.MouseButton) tea.MouseMsg {
		return tea.MouseMsg(tea.MouseEvent{X: 5, Y: sl.top + 2, Action: tea.MouseActionPress, Button: b})
	}
	m, _ = send(t, m, wheel(tea.MouseButtonWheelDown))
	if m.diff.top != 1 {
		t.Fatalf("wheel down did not scroll: top=%d", m.diff.top)
	}
	m, _ = send(t, m, wheel(tea.MouseButtonWheelUp))
	if m.diff.top != 0 {
		t.Fatalf("wheel up did not scroll back: top=%d", m.diff.top)
	}
}

func TestDiffFailureStaysOnTheStatusLine(t *testing.T) {
	m, _ := bxOpenDiff(t, "", errors.New("no crew k7 is recorded for project payments-api"))
	if m.diff.open {
		t.Fatal("a failed diff opened a sheet over nothing")
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "no crew") {
		t.Fatalf("message = %+v, want the bridge's own refusal", m.msg)
	}
}

func TestDiffTintsAdditionsRemovalsAndHeaders(t *testing.T) {
	for line, want := range map[string]tok{
		"+added": tGreen, "-removed": tRed, "+++ b/x": tDim, "--- a/x": tDim,
		"@@ -1 +1 @@": tDim, "diff --git a/x b/x": tDim, " context": tFg,
	} {
		if got := diffLineTok(line); got != want {
			t.Errorf("diffLineTok(%q) = %d, want %d", line, got, want)
		}
	}
}

func TestDiffSheetKeyLineNamesItsOwnKeys(t *testing.T) {
	m, _ := bxOpenDiff(t, bxDiffText, nil)
	lines := strings.Split(renderFrame(t, m), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "scroll") || !strings.Contains(last, "esc") || strings.Contains(last, "act") {
		t.Fatalf("key line = %q, want the diff's own keys", last)
	}
}
