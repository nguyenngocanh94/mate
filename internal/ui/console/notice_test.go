package console

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNoticeIsOptInAndOpensAdvisoryWithoutChangingTree(t *testing.T) {
	m, calls := actRunner(t, sampleTree(), "Jev suggests: quota warning\nAdvisory only", nil)
	m = toRunningAttempt(t, m)
	for _, e := range m.menuFor(row{kind: rowCrew, id: m.cur().selID}, true) {
		if e.choice.action == ActionNotice {
			t.Fatal("notice offered without opt-in")
		}
	}
	m = m.WithNoticeClassifier(true)
	before := m.tree
	m, _ = send(t, m, key("a"))
	if !actEntry(t, m, "e").enabled {
		t.Fatal("notice disabled for live crew")
	}
	m, cmd := send(t, m, key("e"))
	if cmd == nil || len(*calls) != 0 || m.confirm != nil {
		t.Fatal("action must be queued once")
	}
	m, _ = send(t, m, cmd())
	if len(*calls) != 1 || (*calls)[0].Action != ActionNotice || (*calls)[0].Crew == "" || (*calls)[0].Target != m.currentProject().ProjectID {
		t.Fatalf("bad request: %+v", calls)
	}
	if !m.diff.open || !strings.Contains(m.diffTitle(), "Jev notice") || !strings.Contains(m.diff.text, "Advisory") {
		t.Fatal("missing advisory sheet")
	}
	if !reflect.DeepEqual(before, m.tree) {
		t.Fatal("classification changed snapshot")
	}
	for _, width := range []int{24, 40, 64} {
		m, _ = send(t, m, tea.WindowSizeMsg{Width: width, Height: 28})
		_ = renderFrame(t, m)
	}
}

func TestNoticeOnMateAndFailure(t *testing.T) {
	m, calls := actRunner(t, sampleTree(), "", errors.New("HTTP 429"))
	m = m.WithNoticeClassifier(true)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("a"))
	m, cmd := send(t, m, key("e"))
	if cmd == nil {
		t.Fatal("Mate notice action missing")
	}
	m, _ = send(t, m, cmd())
	if len(*calls) != 1 || (*calls)[0].TargetKind != "mate" || (*calls)[0].Crew != "" {
		t.Fatal("invalid Mate target")
	}
	if m.diff.open || m.actionBusy || !strings.Contains(m.msg.text, "Jev notice unavailable") {
		t.Fatal("error left console busy or opened a result")
	}
	_ = renderFrame(t, m)
}

func TestNoticeGolden(t *testing.T) {
	m, _ := actRunner(t, sampleTree(), "Jev suggests: quota warning\n\nCaptured: 2026-09-27T15:00:00+07:00\nAdvisory only; inspect the current pane before acting.", nil)
	m = toRunningAttempt(t, m).WithNoticeClassifier(true)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 36})
	m, _ = send(t, m, key("a"))
	assertGolden(t, "jev-notice-menu-40x36", renderFrame(t, m))
	m, cmd := send(t, m, key("e"))
	if cmd == nil {
		t.Fatal("notice action not available")
	}
	m, _ = send(t, m, cmd())
	assertGolden(t, "jev-notice-result-40x36", renderFrame(t, m))
}
