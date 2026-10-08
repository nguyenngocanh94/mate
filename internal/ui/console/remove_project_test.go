package console

import (
	"strings"
	"testing"
)

// The Project row's Actions menu offers `Remove project…`; it asks first,
// says what it stops and what stays, and `y` runs it (mvp.md task 75).
func TestRemoveProjectEntryAsksThenRunsOnY(t *testing.T) {
	m, got := actRunner(t, sampleTree(), "removed", nil)
	if r, ok := m.selectedRow(); !ok || r.kind != rowProject {
		t.Fatalf("setup: selected %+v, want the project row", r)
	}
	m, _ = send(t, m, key("a"))
	if view := renderFrame(t, m); !strings.Contains(view, "Remove project…") {
		t.Fatalf("project row menu has no Remove project entry:\n%s", view)
	}
	m, cmd := send(t, m, key("x"))
	if cmd != nil || m.confirm == nil || m.confirm.choice.action != ActionRemoveProject || len(*got) != 0 {
		t.Fatalf("x must open the confirmation without running: cmd=%v confirm=%+v calls=%d", cmd != nil, m.confirm, len(*got))
	}
	flat := actFlat(renderFrame(t, m))
	for _, want := range []string{"Remove project payments-api?", "stops its Mate and", "stay on disk", "cancel"} {
		if !strings.Contains(flat, actFlat(want)) {
			t.Fatalf("confirmation lost %q:\n%s", want, renderFrame(t, m))
		}
	}
	// Esc goes back to the menu; nothing ran. The asking key does not confirm.
	m, _ = send(t, m, key("x"))
	if len(*got) != 0 || m.confirm == nil {
		t.Fatalf("the menu key confirmed: confirm=%+v calls=%d", m.confirm, len(*got))
	}
	m, cmd = send(t, m, key("y"))
	if cmd == nil {
		t.Fatal("y did not run the removal")
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 {
		t.Fatalf("runner calls = %d, want one", len(*got))
	}
	if req := (*got)[0]; req.Action != ActionRemoveProject || req.Target != sampleTree().Projects[0].ProjectID || req.TargetKind != "project" {
		t.Fatalf("request = %+v", req)
	}
	if !strings.Contains(m.msg.text, "removed") {
		t.Fatalf("message = %+v", m.msg)
	}
}

func TestRemoveProjectEscCancelsAndEnterToo(t *testing.T) {
	for _, cancel := range []string{"esc", "enter"} {
		m, got := actRunner(t, sampleTree(), "removed", nil)
		m, _ = send(t, m, key("a"))
		m, _ = send(t, m, key("x"))
		m, cmd := send(t, m, key(cancel))
		if cmd != nil || len(*got) != 0 {
			t.Fatalf("%s ran something", cancel)
		}
	}
}
