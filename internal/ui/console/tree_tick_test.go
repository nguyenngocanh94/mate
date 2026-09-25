package console

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// This file is the auto-refresh feature (2026-09-18 user feedback: "nothing
// is realtime; the crew list, statuses and Mate state only update after
// pressing r"). The tree now reloads every treeTickInterval in the
// background (model.go, update.go's onTreeTick) whatever frame is on
// screen - the chain never stops on its own once the first load lands (see
// Update's treeLoadedMsg case) - and a background reload must never disturb
// what the reader is doing: no selection, scroll, focus, open input or
// overlay moves under them just because the clock ticked.

// tickLoadSpy counts calls and hands back whatever tree/err it is told to,
// synchronously - loadCmd's own closure runs it inline, so there is nothing
// to wait on.
type tickLoadSpy struct {
	calls int
	tree  query.Snapshot
	err   error
}

func (s *tickLoadSpy) load(context.Context) (query.Snapshot, error) {
	s.calls++
	return s.tree, s.err
}

// tickFixture builds a loaded, sized Console with the tick cadence sped up
// so a test can drive the chain without waiting on the real 2s interval,
// and returns the model together with the Cmd Update handed back for the
// first load - the chain's own first tick.
func tickFixture(t *testing.T, spy *tickLoadSpy, w, h int) (Model, tea.Cmd) {
	t.Helper()
	m := New(spy.load)
	m.g = unicodeGlyphs
	m.p = plainPalette()
	m.treeTickIntervalOverride = time.Millisecond
	m, _ = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m, tick := send(t, m, m.Init()())
	if !m.treeTickStarted {
		t.Fatal("precondition: the first load must start the auto-refresh chain")
	}
	if m.treeLoadInFlight {
		t.Fatal("precondition: the first load must have resolved by now")
	}
	if tick == nil {
		t.Fatal("precondition: the first treeLoadedMsg must hand back the chain's first tick Cmd")
	}
	return m, tick
}

// fireTick invokes tickCmd, requires it produced exactly a treeTickMsg, and
// applies it to m.
func fireTick(t *testing.T, m Model, tickCmd tea.Cmd) (Model, tea.Cmd) {
	t.Helper()
	if tickCmd == nil {
		t.Fatal("fireTick: no Cmd to fire - the chain already died")
	}
	msg := tickCmd()
	tick, ok := msg.(treeTickMsg)
	if !ok {
		t.Fatalf("fireTick: Cmd produced %T, want treeTickMsg", msg)
	}
	return send(t, m, tick)
}

// splitBatch unpacks a Cmd's message into (load result, next tick Cmd) when
// it is a tea.Batch of exactly [load, next tick] - onTreeTick's shape when
// it started a load. A Cmd that is nil, or whose message is not a batch,
// means the tick did not start a load; splitBatch returns (nil, cmd)
// unchanged so the caller can still fire whatever Cmd it got (a bare
// reschedule).
func splitBatch(t *testing.T, cmd tea.Cmd) (tea.Msg, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return nil, nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return nil, func() tea.Msg { return msg }
	}
	if len(batch) != 2 {
		t.Fatalf("tea.BatchMsg has %d Cmds, want exactly 2 (load, next tick)", len(batch))
	}
	loadMsg := batch[0]()
	if _, ok := loadMsg.(treeLoadedMsg); !ok {
		t.Fatalf("first Cmd in the batch produced %T, want treeLoadedMsg", loadMsg)
	}
	return loadMsg, batch[1]
}

// driveOneTick fires m's pending tick Cmd, applies the load it starts (there
// must be one - the caller is responsible for there being nothing already
// in flight), and returns the refreshed model plus the Cmd for the chain's
// next tick.
func driveOneTick(t *testing.T, m Model, tickCmd tea.Cmd, tree query.Snapshot) (Model, tea.Cmd) {
	t.Helper()
	m, tickResult := fireTick(t, m, tickCmd)
	loadMsg, nextTick := splitBatch(t, tickResult)
	if loadMsg == nil {
		t.Fatal("driveOneTick: the tick did not start a load - is one already marked in flight?")
	}
	m, _ = send(t, m, loadMsg)
	return m, nextTick
}

// TestTreeTickIssuesOneLoadAndReschedules: the ordinary case. A tick with no
// load already outstanding starts one and reschedules itself in the same
// step, and the chain keeps doing that indefinitely.
func TestTreeTickIssuesOneLoadAndReschedules(t *testing.T) {
	tree := sampleTree()
	tree.AsOf = goldenAsOf
	spy := &tickLoadSpy{tree: tree}
	m, tick := tickFixture(t, spy, 40, 36)
	if spy.calls != 1 {
		t.Fatalf("load calls after Init = %d, want 1", spy.calls)
	}

	m, tick = driveOneTick(t, m, tick, tree)
	if spy.calls != 2 {
		t.Fatalf("load calls after one tick = %d, want 2 (Init + the tick)", spy.calls)
	}
	if m.treeLoadInFlight {
		t.Fatal("treeLoadInFlight must clear once the tick's own load resolves")
	}
	if tick == nil {
		t.Fatal("a tick that started a load must still reschedule itself")
	}

	m, tick = driveOneTick(t, m, tick, tree)
	if spy.calls != 3 {
		t.Fatalf("load calls after two ticks = %d, want 3", spy.calls)
	}
	if tick == nil {
		t.Fatal("the chain must reschedule indefinitely")
	}
}

// TestTreeTickDuringInFlightLoadDoesNotIssueASecond: a tick that fires while
// the previous load has not resolved yet must not start a second one - it
// only reschedules.
func TestTreeTickDuringInFlightLoadDoesNotIssueASecond(t *testing.T) {
	tree := sampleTree()
	spy := &tickLoadSpy{tree: tree}
	m, tick := tickFixture(t, spy, 40, 36)

	// A load is left outstanding, the way one is between being issued
	// ('r', an action's own re-read, or a tick) and its treeLoadedMsg
	// landing.
	m.treeLoadInFlight = true
	m, tickResult := fireTick(t, m, tick)
	loadMsg, nextTick := splitBatch(t, tickResult)
	if loadMsg != nil {
		t.Fatalf("a tick during an in-flight load must not start a second one, got %#v", loadMsg)
	}
	if spy.calls != 1 {
		t.Fatalf("load calls after a tick with one already in flight = %d, want still 1 (just Init)", spy.calls)
	}
	if nextTick == nil {
		t.Fatal("a tick that skipped its load must still reschedule, or the chain dies")
	}
}

// TestStaleGenTreeTickIsIgnored: a tick whose gen no longer matches the
// model's current one is dropped without issuing a load or rescheduling.
func TestStaleGenTreeTickIsIgnored(t *testing.T) {
	tree := sampleTree()
	spy := &tickLoadSpy{tree: tree}
	m, _ := tickFixture(t, spy, 40, 36)
	callsBefore := spy.calls

	m2, cmd := send(t, m, treeTickMsg{gen: m.treeGen + 1})
	if cmd != nil {
		t.Fatal("a stale-gen tick must produce no Cmd: nothing to reschedule from a chain it does not belong to")
	}
	if spy.calls != callsBefore {
		t.Fatalf("a stale-gen tick issued a load: calls = %d, want %d", spy.calls, callsBefore)
	}
	if m2.treeLoadInFlight {
		t.Fatal("a stale-gen tick must not touch treeLoadInFlight")
	}
}

// TestBackgroundTickFailureShowsStaleAndKeepsTree: the failure path a
// background tick takes is the same one 'r' already takes (onTreeLoaded),
// reached through the tick chain - the list rule says "stale", the status
// line names the last successful read's own time, and the tree stays.
func TestBackgroundTickFailureShowsStaleAndKeepsTree(t *testing.T) {
	tree := sampleTree()
	tree.AsOf = goldenAsOf
	spy := &tickLoadSpy{tree: tree}
	m, tick := tickFixture(t, spy, 40, 36)

	spy.err = errFake("database is locked")
	m, _ = driveOneTick(t, m, tick, tree)

	if m.tree.AsOf != goldenAsOf {
		t.Fatalf("tree.AsOf after a failed background tick = %v, want the last successful read's time preserved", m.tree.AsOf)
	}
	if m.lastLoadErr == nil {
		t.Fatal("lastLoadErr must be set after a failed background load")
	}
	lines := strings.Split(renderFrame(t, m), "\n")
	if !strings.Contains(lines[0], "stale") {
		t.Fatalf("list rule after a failed background tick = %q, want it to say stale", lines[0])
	}
	if !strings.Contains(m.msg.text, "database is locked") || !strings.Contains(m.msg.text, goldenAsOf.Format("15:04:05")) {
		t.Fatalf("message after a failed background tick = %q, want the error and the last good read's time", m.msg.text)
	}
	if !strings.Contains(renderFrame(t, m), sampleTree().Projects[0].Name) {
		t.Fatalf("the tree vanished on a failed background tick:\n%s", renderFrame(t, m))
	}
}

// ---------- item 2: a background reload must not disturb the reader ----------

// TestBackgroundTickPreservesListSelectionAndScroll: the reader has
// scrolled a long list and moved the selection; a tick landing behind them
// must move neither.
func TestBackgroundTickPreservesListSelectionAndScroll(t *testing.T) {
	tree := designTree()
	for i := 0; i < 20; i++ {
		tree.Projects = append(tree.Projects, query.ProjectNode{
			ProjectID: "zz-" + itoa(i), Name: "zz-" + itoa(i), Mate: absentMate("no mate"),
		})
	}
	spy := &tickLoadSpy{tree: tree}
	m, tick := tickFixture(t, spy, 40, 24)
	for i := 0; i < 25; i++ {
		m, _ = send(t, m, key("down"))
	}
	if m.cur().top == 0 {
		t.Fatal("precondition: the list must have scrolled")
	}

	wantSelID := m.cur().selID
	wantSel := m.cur().sel
	wantTop := m.cur().top

	m, _ = driveOneTick(t, m, tick, tree)

	if m.cur().selID != wantSelID || m.cur().sel != wantSel || m.cur().top != wantTop {
		t.Fatalf("selection/scroll after a background tick = %+v, want selID=%q sel=%d top=%d",
			m.cur(), wantSelID, wantSel, wantTop)
	}
}

// TestBackgroundTickPreservesFocusedPane: the reader tabbed into detail and
// walked its fields; a tick must send neither focus nor cursor back.
func TestBackgroundTickPreservesFocusedPane(t *testing.T) {
	tree := sampleTree()
	spy := &tickLoadSpy{tree: tree}
	m, tick := tickFixture(t, spy, 40, 36)
	m, _ = send(t, m, key("enter")) // the Mate row
	m, _ = send(t, m, key("tab"))   // detail
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down"))
	if m.focus != paneDetail || m.detailSel != 2 {
		t.Fatalf("precondition: focus=%v detailSel=%d, want detail at field 2", m.focus, m.detailSel)
	}

	m, _ = driveOneTick(t, m, tick, tree)

	if m.focus != paneDetail || m.detailSel != 2 {
		t.Fatalf("after a background tick focus=%v detailSel=%d, want detail at field 2", m.focus, m.detailSel)
	}
}

// TestBackgroundTickPreservesTheBoxSelection: the panel's cursor is where
// the reader has put it, and a background reload must not move it - the
// next key they press acts on the row they were looking at.
func TestBackgroundTickPreservesTheBoxSelection(t *testing.T) {
	tree := sampleTree()
	spy := &tickLoadSpy{tree: tree}
	m, tick := tickFixture(t, spy, 40, 36)
	m, _ = send(t, m, key("enter")) // into the project frame
	m, _ = send(t, m, key("tab"))
	m, _ = send(t, m, key("tab")) // the box
	m, _ = send(t, m, key("l"))   // the whole log
	m, _ = send(t, m, key("down"))
	if m.focus != paneBox || m.boxSel != 1 || !m.boxAll {
		t.Fatalf("precondition: box state focus %v sel %d all %v", m.focus, m.boxSel, m.boxAll)
	}

	m, _ = driveOneTick(t, m, tick, tree)

	if m.focus != paneBox || m.boxSel != 1 || !m.boxAll {
		t.Fatalf("box state after a background tick = focus %v sel %d all %v, want it unchanged",
			m.focus, m.boxSel, m.boxAll)
	}
}

// TestBackgroundTickPreservesOpenConfirmPrompt: a dangerous action's
// confirmation is one keystroke from running something real; a tick must
// not dismiss it out from under the reader.
func TestBackgroundTickPreservesOpenConfirmPrompt(t *testing.T) {
	tree := sampleTree()
	spy := &tickLoadSpy{tree: tree}
	m, tick := tickFixture(t, spy, 40, 36)
	m, _ = send(t, m, key("enter")) // the Mate row
	m, _ = send(t, m, key("a"))
	m, _ = send(t, m, key("x")) // Stop mate… asks first
	if m.confirm == nil {
		t.Fatal("precondition: want the stop confirmation open")
	}
	choice := m.confirm.choice

	m, _ = driveOneTick(t, m, tick, tree)

	if m.confirm == nil || m.confirm.choice.action != choice.action || m.confirm.key != "x" {
		t.Fatalf("confirm prompt after a background tick = %+v, want it unchanged", m.confirm)
	}
}

// TestBackgroundTickPreservesAllToggle: the box's [all] filter is a
// reader-chosen setting for the Console's whole run; a tick must not reset
// it.
func TestBackgroundTickPreservesAllToggle(t *testing.T) {
	tree := sampleTree()
	spy := &tickLoadSpy{tree: tree}
	m, tick := tickFixture(t, spy, 40, 36)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("tab"))
	m, _ = send(t, m, key("tab"))
	m, _ = send(t, m, key("l"))
	if !m.boxAll {
		t.Fatal("precondition: l must show the whole log")
	}

	m, _ = driveOneTick(t, m, tick, tree)

	if !m.boxAll {
		t.Fatal("the [all] toggle after a background tick = off, want it to stay on")
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
