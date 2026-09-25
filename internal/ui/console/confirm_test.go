package console

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The confirm sheet must wrap its object, scope and effect, never cut them,
// and must never draw the control characters a recorded identity can carry.

// actHostileStopConfirm is sampleTree with a hostile id on the running
// crew, driven to its stop confirmation at w x h.
func actHostileStopConfirm(t *testing.T, w, h int) Model {
	t.Helper()
	tree := sampleTree()
	tree.Projects[0].Crews[1].CrewID = "crew_\u0085修正\x1b[31mtail‼️more"
	tree.Projects[0].Crews[1].Task = "fix\nsecond \x1b[2Jline"
	m := New(func(context.Context) (query.Snapshot, error) { tree.AsOf = goldenAsOf; return tree, nil })
	m.p = plainPalette()
	m.g = unicodeGlyphs
	m, _ = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter")) // payments-api
	m, _ = send(t, m, key("down"))  // the running crew
	if r, ok := m.selectedRow(); !ok || r.kind != rowCrew {
		t.Fatalf("setup: selected %+v, want the running crew", r)
	}
	m, _ = send(t, m, key("a"))
	m, cmd := send(t, m, key("x"))
	if cmd != nil || m.confirm == nil || m.confirm.choice.action != ActionStop {
		t.Fatalf("stop on an active crew must open its confirmation without running: cmd=%v confirm=%+v", cmd != nil, m.confirm)
	}
	return m
}

func TestConfirmSheetWrapsItsProseInsteadOfCuttingIt(t *testing.T) {
	var probe Model
	_, scope, effect := probe.actionObjectDescription(actionChoice{req: ActionRequest{Action: ActionStop, TargetKind: "crew"}})
	for _, w := range []int{32, 40, 48} {
		m := actHostileStopConfirm(t, w, 40)
		frame := renderFrame(t, m)
		flat := actFlat(frame)
		for _, want := range []string{"stop crew?", "object", actFlat(scope), actFlat(effect), "cancel"} {
			if !strings.Contains(flat, want) {
				t.Fatalf("confirmation at %d cols lost %q:\n%s", w, want, frame)
			}
		}
	}
}

func TestConfirmSheetSanitizesHostileRecordedIdentity(t *testing.T) {
	for _, size := range [][2]int{{40, 36}, {32, 20}, {48, 48}} {
		m := actHostileStopConfirm(t, size[0], size[1])
		frame := renderFrame(t, m)
		assertFrameShape(t, frame, size[0], size[1])
		for _, bad := range []string{"\x1b", "\u0085", "\nsecond"} {
			if strings.Contains(frame, bad) {
				t.Fatalf("confirmation at %dx%d leaked %q:\n%q", size[0], size[1], bad, frame)
			}
		}
		if !strings.Contains(frame, "stop crew?") {
			t.Fatalf("confirmation title missing at %dx%d:\n%s", size[0], size[1], frame)
		}
	}
}

// The sheet never covers the row it acts on (design G).
func TestConfirmSheetKeepsTheSelectedRowInView(t *testing.T) {
	for _, h := range []int{20, 24, 36} {
		m := actHostileStopConfirm(t, 40, h)
		frame := renderFrame(t, m)
		lines := strings.Split(frame, "\n")
		sel := -1
		for i, l := range lines {
			if strings.HasPrefix(l, "▏") || strings.HasPrefix(l, "▌ 🤖") {
				sel = i
			}
		}
		sheet := -1
		for i, l := range lines {
			if strings.Contains(l, "stop crew?") {
				sheet = i
			}
		}
		if sel < 0 || sheet < 0 || sel >= sheet {
			t.Fatalf("at 40x%d the selected row (line %d) is not above the sheet (line %d):\n%s", h, sel, sheet, frame)
		}
	}
}
