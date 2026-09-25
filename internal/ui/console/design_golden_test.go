package console

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The design boards (docs/console-design.md, frames A-K and H), as
// goldens. Each one is the board's own state on the board's own grid,
// drawn from designTree(). Where a board shows a value the backend does
// not record (a tool call, "Answered today", tokens in/out), the frame
// shows what the snapshot has instead; the rest - columns, markers, rules,
// the stack of panes, the footer - is the board's.

// designStaged is the board's standing state: the payments-api Mate was
// shown in the next pane earlier.
func designStaged(t *testing.T, m Model) Model {
	t.Helper()
	m = m.WithStage(func(context.Context, StageTarget) error { return nil })
	m.staged = stagedPane{kind: StageMate, name: "payments-api"}
	return m
}

// designAt opens designTree at w x h with payments-api selected.
func designAt(t *testing.T, w, h int, g glyphSet) Model {
	t.Helper()
	// Navigate on a frame big enough to take keys, then resize: a split
	// that is too small answers only q.
	m := designStaged(t, newFixture(t, designTree(), 40, 36, g))
	for i, p := range designTree().Projects {
		if p.Name == "payments-api" {
			for ; i > 0; i-- {
				m, _ = send(t, m, key("down"))
			}
			break
		}
	}
	if r, _ := m.selectedRow(); r.id != "payments-api" {
		t.Fatalf("setup: selected %+v, want payments-api", r)
	}
	m, _ = send(t, m, sizeMsg(w, h))
	return m
}

// designProject is designAt, then Enter: the payments-api Project.
func designProject(t *testing.T, w, h int, g glyphSet) Model {
	t.Helper()
	m, _ := send(t, designAt(t, 40, 36, g), key("enter"))
	m, _ = send(t, m, sizeMsg(w, h))
	return m
}

func keys(t *testing.T, m Model, ks ...string) Model {
	t.Helper()
	for _, k := range ks {
		m, _ = send(t, m, key(k))
	}
	return m
}

func TestDesignBoards(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) Model
	}{
		{"design-a-workspace-40x36", func(t *testing.T) Model { return designAt(t, 40, 36, unicodeGlyphs) }},
		{"design-b-project-40x36", func(t *testing.T) Model { return designProject(t, 40, 36, unicodeGlyphs) }},
		{"design-c-detail-focused-40x36", func(t *testing.T) Model {
			return keys(t, designProject(t, 40, 36, unicodeGlyphs), "tab")
		}},
		{"design-d-box-focused-40x36", func(t *testing.T) Model {
			return keys(t, designProject(t, 40, 36, unicodeGlyphs), "tab", "tab")
		}},
		{"design-e-project-40x24", func(t *testing.T) Model { return designProject(t, 40, 24, unicodeGlyphs) }},
		{"design-f-actions-40x36", func(t *testing.T) Model {
			return keys(t, designProject(t, 40, 36, unicodeGlyphs), "a")
		}},
		{"design-g-stop-confirm-40x36", func(t *testing.T) Model {
			return keys(t, designProject(t, 40, 36, unicodeGlyphs), "a", "x")
		}},
		{"design-j-project-48x48", func(t *testing.T) Model { return designProject(t, 48, 48, unicodeGlyphs) }},
		{"design-k-workspace-40x24", func(t *testing.T) Model { return designAt(t, 40, 24, unicodeGlyphs) }},
		{"design-k-new-project-40x24", func(t *testing.T) Model {
			m := keys(t, designAt(t, 40, 24, unicodeGlyphs), "n")
			for _, r := range "ledger-audit" {
				m = keys(t, m, string(r))
			}
			return m
		}},
		{"design-k-new-project-taken-40x24", func(t *testing.T) Model {
			m := keys(t, designAt(t, 40, 24, unicodeGlyphs), "n")
			for _, r := range "docs-site" {
				m = keys(t, m, string(r))
			}
			return m
		}},
		{"design-keys-40x36", func(t *testing.T) Model {
			return keys(t, designProject(t, 40, 36, unicodeGlyphs), "?")
		}},
		{"design-b-project-40x36-ascii", func(t *testing.T) Model { return designProject(t, 40, 36, asciiGlyphs) }},
		{"design-b-project-40x36-nerd", func(t *testing.T) Model {
			return designProject(t, 40, 36, unicodeGlyphs.withIcons(iconsNerd))
		}},
		{"design-b-project-40x36-symbol-kinds", func(t *testing.T) Model {
			return designProject(t, 40, 36, unicodeGlyphs.withKinds(KindSymbol))
		}},
		{"design-b-project-36x24", func(t *testing.T) Model { return designProject(t, 36, 24, unicodeGlyphs) }},
		{"design-b-project-40x18-detail-swapped", func(t *testing.T) Model {
			return keys(t, designProject(t, 40, 18, unicodeGlyphs), "tab")
		}},
		{"design-h1-empty-36x24", func(t *testing.T) Model {
			tree := designTree()
			tree.Projects = nil
			return newFixture(t, tree, 36, 24, unicodeGlyphs)
		}},
		{"design-h2-unreachable-36x24", func(t *testing.T) Model {
			return newFailedFixture(t, errors.New("herdr agent list: exit 127 · not found"), 36, 24, unicodeGlyphs)
		}},
		{"design-h3-loading-36x24", func(t *testing.T) Model {
			m := New(func(context.Context) (query.Snapshot, error) { return designTree(), nil })
			m.g, m.p = unicodeGlyphs, plainPalette()
			m, _ = send(t, m, sizeMsg(36, 24))
			return m
		}},
		{"design-h4-too-small-28x10", func(t *testing.T) Model { return designAt(t, 28, 10, unicodeGlyphs) }},
		{"design-h4-tiny-18x6", func(t *testing.T) Model { return designAt(t, 18, 6, unicodeGlyphs) }},
		{"design-status-failed-40x24", func(t *testing.T) Model {
			m := designProject(t, 40, 24, unicodeGlyphs)
			m = m.onStageDone(stageDoneMsg{err: errors.New("no pane"), target: StageTarget{Kind: StageMate, ProjectID: "payments-api"}})
			return m
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertGolden(t, tc.name, renderFrame(t, tc.build(t)))
		})
	}
}

// TestEveryFrameIsExactlyTheGrid is the frame contract at every size the
// console can be split to, in every state a key can reach: exactly h lines
// of exactly w cells, with no panic.
func TestEveryFrameIsExactlyTheGrid(t *testing.T) {
	states := map[string][]string{
		"workspace": nil,
		"project":   {"enter"},
		"detail":    {"enter", "tab"},
		"box":       {"enter", "tab", "tab"},
		"actions":   {"enter", "a"},
		"confirm":   {"enter", "a", "x"},
		"new":       {"n", "l", "e", "d"},
		"keys":      {"?"},
		"expanded":  {"enter", "down", "down", "down", "enter"},
	}
	for name, ks := range states {
		for w := 16; w <= 64; w += 3 {
			for h := 6; h <= 56; h += 5 {
				m := keys(t, designAt(t, 40, 36, unicodeGlyphs), ks...)
				m, _ = send(t, m, sizeMsg(w, h))
				frame := m.View()
				assertFrameShape(t, frame, w, h)
				if t.Failed() {
					t.Fatalf("state %s at %dx%d", name, w, h)
				}
			}
		}
	}
}

// TestEveryDesignBoardKeepsTheColumns pins design I's row anatomy at 40
// columns: marker in col 0, kind in cols 2-3, the name from col 5, and
// the ! column at 36 (two lines) or 31 (one line).
func TestEveryDesignBoardKeepsTheColumns(t *testing.T) {
	b := strings.Split(renderFrame(t, designProject(t, 40, 36, unicodeGlyphs)), "\n")
	if got := cellIndex(b[1], "payments-api"); got != 5 {
		t.Errorf("B: the Mate's name starts at col %d, want 5:\n%s", got, b[1])
	}
	if got := cellIndex(b[5], "!"); got != 36 {
		t.Errorf("B: the waiting crew's ! is at col %d, want 36:\n%s", got, b[5])
	}
	if got := cellIndex(b[2], "running"); got != 7 {
		t.Errorf("B: line 2's status starts at col %d, want 7 (icon at 5):\n%s", got, b[2])
	}
	e := strings.Split(renderFrame(t, designProject(t, 40, 24, unicodeGlyphs)), "\n")
	if got := cellIndex(e[3], "!"); got != 31 {
		t.Errorf("E: the one-line ! is at col %d, want 31:\n%s", got, e[3])
	}
	if got := cellIndex(e[1], "running"); got != 33 {
		t.Errorf("E: the one-line status is at col %d, want 33:\n%s", got, e[1])
	}
}

func sizeMsg(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }
