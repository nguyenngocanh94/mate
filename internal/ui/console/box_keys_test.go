package console

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// bxAction records every ActionFunc request.
type bxAction struct {
	reqs []ActionRequest
	out  string
	err  error
}

func (r *bxAction) run(_ context.Context, req ActionRequest) (string, error) {
	r.reqs = append(r.reqs, req)
	return r.out, r.err
}

// bxPayments is the design tree opened on payments-api at w x h, with an
// action recorder and a stage spy.
func bxPayments(t *testing.T, w, h int) (Model, *bxAction, *stageSpy) {
	t.Helper()
	act, spy := &bxAction{out: "done"}, &stageSpy{}
	m := newFixture(t, designTree(), w, h, unicodeGlyphs)
	m.action = act.run
	m = m.WithStage(spy.fn)
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	m, _ = send(t, m, key("enter"))
	if m.cur().kind != frameProject || m.currentProject().ProjectID != "payments-api" {
		t.Fatalf("setup: not on payments-api: %+v", m.cur())
	}
	return m, act, spy
}

// bxFocusBox tabs until the box has focus.
func bxFocusBox(t *testing.T, m Model) Model {
	t.Helper()
	for i := 0; i < 4 && m.focus != paneBox; i++ {
		m, _ = send(t, m, key("tab"))
	}
	if m.focus != paneBox {
		t.Fatalf("setup: Tab never reached the box (focus %v)", m.focus)
	}
	return m
}

// bxMessage is a message entry: it holds no question.
func bxMessage(at time.Time) query.BoxEntry {
	return query.BoxEntry{Seq: 9, At: at, Kind: query.BoxMessage, Source: "user", Target: "mate", Text: "spawn a crew"}
}

func TestBoxAssignRunsResolveWithTheEntrysOwnLine(t *testing.T) {
	m, act, _ := bxPayments(t, 40, 36)
	m = bxFocusBox(t, m)
	m, cmd := send(t, m, key("a"))
	if cmd == nil {
		t.Fatal("a on the box ran nothing")
	}
	m, _ = send(t, m, cmd())
	if len(act.reqs) != 1 {
		t.Fatalf("requests = %+v, want one", act.reqs)
	}
	got := act.reqs[0]
	want := designPayments().Box.Value.Inbox[0]
	if got.Action != ActionResolve || got.Target != "payments-api" || got.Crew != "k3" || got.Input != want.Resolve {
		t.Fatalf("request = %+v, want resolve of k3 with %q", got, want.Resolve)
	}
}

// A message holds no question: assign says so and sends nothing, rather
// than doing nothing a reader would take for a lost key.
func TestBoxAssignRefusesAMessageEntry(t *testing.T) {
	tree := designTree()
	p := &tree.Projects[7]
	msg := bxMessage(time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC))
	box := p.Box.Value
	box.Entries = append(box.Entries, msg)
	p.Box = query.KnownField(box)
	act := &bxAction{}
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	m.action = act.run
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	m, _ = send(t, m, key("enter"))
	m = bxFocusBox(t, m)
	m, _ = send(t, m, key("l")) // the whole log, messages included
	items := m.boxItems()
	if len(items) != 2 || items[0].e.Kind != query.BoxMessage {
		t.Fatalf("setup: log = %+v, want the newer message first", items)
	}
	m, cmd := send(t, m, key("a"))
	if cmd != nil || len(act.reqs) != 0 {
		t.Fatalf("assigning a message reached ActionFunc: %+v", act.reqs)
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "holds no question") {
		t.Fatalf("message = %+v, want the refusal naming why", m.msg)
	}
	if !strings.Contains(renderFrame(t, m), "Assign refused") {
		t.Fatalf("the refusal is not on the frame:\n%s", renderFrame(t, m))
	}
}

func TestBoxEnterShowsTheCrewInTheNextPane(t *testing.T) {
	m, _, spy := bxPayments(t, 40, 36)
	m = bxFocusBox(t, m)
	_, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter on the box showed nothing")
	}
	cmd()
	if len(spy.calls) != 1 || spy.calls[0].Kind != StageCrew || spy.calls[0].ID != "k3" {
		t.Fatalf("stage calls = %+v, want crew k3", spy.calls)
	}
}

// A crew closed since the last read has no pane: the refusal says so.
func TestBoxEnterRefusesACrewThatLeftTheSnapshot(t *testing.T) {
	m, _, spy := bxPayments(t, 40, 36)
	m = bxFocusBox(t, m)
	proj := &m.tree.Projects[7]
	proj.Crews = proj.Crews[:1] // k3 is gone; its question is still in the box
	m, cmd := send(t, m, key("enter"))
	if cmd != nil || len(spy.calls) != 0 {
		t.Fatalf("a gone crew reached the host: %+v", spy.calls)
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "not in this snapshot") {
		t.Fatalf("message = %+v, want the refusal saying the crew is gone", m.msg)
	}
}

func TestBoxEnterRefusesAnEntryThatNamesNoCrew(t *testing.T) {
	m, _, spy := bxPayments(t, 40, 36)
	m, _ = m.showBoxItem(boxItem{project: "payments-api", e: bxMessage(time.Now())})
	if len(spy.calls) != 0 || !strings.Contains(m.msg.text, "names no crew") {
		t.Fatalf("msg = %+v calls = %+v, want a refusal", m.msg, spy.calls)
	}
}

// Selecting a box item moves the list selection to its crew, so detail
// explains why it waits (design D).
func TestBoxSelectionMovesTheListToTheCrew(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	if r, _ := m.selectedRow(); r.kind != rowMate {
		t.Fatalf("setup: selection %+v, want the Mate", r)
	}
	m = bxFocusBox(t, m)
	m, _ = send(t, m, key("down")) // one item: stays on it, and the list follows
	if r, _ := m.selectedRow(); r.kind != rowCrew || r.id != "k3" {
		t.Fatalf("list selection after moving in the box = %+v, want crew k3", r)
	}
	if title, _ := m.detailTitle(); !strings.Contains(bxText(title), "Backfill ledger v2") {
		t.Fatalf("detail title = %q, want the waiting crew", bxText(title))
	}
}

// Box keys are live only while the box has focus: on the list, a and
// Enter mean the list's own things.
func TestBoxKeysNeedBoxFocus(t *testing.T) {
	m, act, spy := bxPayments(t, 40, 36)
	m, _ = send(t, m, key("a"))
	if !m.actions || len(act.reqs) != 0 {
		t.Fatalf("a on the list: actions=%v reqs=%+v, want the actions sheet and no assign", m.actions, act.reqs)
	}
	m, _ = send(t, m, key("esc"))
	_, cmd := send(t, m, key("enter"))
	cmd()
	if len(spy.calls) != 1 || spy.calls[0].Kind != StageMate {
		t.Fatalf("Enter on the list staged %+v, want the Mate", spy.calls)
	}
}

func TestBoxEscHandsFocusBackToTheList(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	m = bxFocusBox(t, m)
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != paneList {
		t.Fatalf("focus after Esc = %v, want the list", m.focus)
	}
}

// bxText is a line's text, without tokens.
func bxText(l gline) string {
	var b strings.Builder
	for _, s := range l.segs {
		b.WriteString(s.text)
	}
	return b.String()
}
