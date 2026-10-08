package console

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTOpensSelectedProjectFromListAndDetail(t *testing.T) {
	for _, focus := range []pane{paneList, paneDetail} {
		m := loaded(t, sampleTree(), nil)
		m.focus = focus
		var opened string
		m = m.WithTasks(func(_ context.Context, project string) error { opened = project; return nil })
		want := m.modeTarget()
		m, cmd := send(t, m, key("t"))
		if cmd == nil {
			t.Fatal("no tasks command")
		}
		m, _ = send(t, m, cmd())
		if opened != want || !strings.Contains(m.msg.text, want) || m.cur().kind != frameWorkspace {
			t.Fatalf("opened %q, expected %q, message %+v", opened, want, m.msg)
		}
	}
	m := projectFrame(t, sampleTree()).WithTasks(func(_ context.Context, project string) error { return errors.New("host failed") })
	m, cmd := send(t, m, key("t"))
	if cmd == nil {
		t.Fatal("project tasks not opened")
	}
	m, _ = send(t, m, cmd())
	if m.msg.text != "host failed" {
		t.Fatalf("failure hidden: %+v", m.msg)
	}
}

func TestTUsesBoxProjectAndKeepsInputPriority(t *testing.T) {
	m, _, _ := bxPayments(t, 80, 36)
	m = bxFocusBox(t, m)
	var opened string
	m = m.WithTasks(func(_ context.Context, p string) error { opened = p; return nil })
	items := m.boxItems()
	want := items[m.boxSelection(items)].project
	m, cmd := send(t, m, key("t"))
	if cmd == nil {
		t.Fatal("box tasks not opened")
	}
	m, _ = send(t, m, cmd())
	if opened != want {
		t.Fatalf("opened %s instead of box project %s", opened, want)
	}
	m = loaded(t, sampleTree(), nil).WithTasks(func(_ context.Context, p string) error { t.Fatal("t escaped text form"); return nil }).beginNewProject()
	m, _ = send(t, m, key("t"))
	if !m.actionInputMode {
		t.Fatal("t closed text form")
	}
}

func TestTWithoutHostGivesStandaloneCommand(t *testing.T) {
	m := projectFrame(t, sampleTree())
	m, cmd := send(t, m, key("t"))
	if cmd != nil || !strings.Contains(m.msg.text, "mate tasks "+m.currentProject().ProjectID) {
		t.Fatalf("no fallback: %+v", m.msg)
	}
}
