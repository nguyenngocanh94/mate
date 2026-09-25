package console

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// ---------- B7: the confirmation's Scope/Effect must wrap, never cut ----------
//
// The counter-review captured a real "stop" confirmation losing the clause
// that says the Crew/Task worktree and branch are untouched, silently, at
// 80 and 60 columns - line.render's edge truncation with no "…" marker
// (documented, and not something this fix changes). confirmField
// (actions.go) now wraps Object/Scope/Effect the way the inspector wraps its
// own fields (seams.go's field/note) instead of handing them to a plain
// .add call the pane edge then cuts.

// flattenWrap collapses a rendered frame or field back into one line for a
// content check that must not care where wrapping (or the frame's own
// trailing padding) happened to break it - only whether the words are still
// all there and in order. Never use this for a frame-shape assertion: the
// point of the h-lines/w-cells contract is that whitespace, so flattening it
// away here is deliberate and only for "does it still say X".
func flattenWrap(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// confirmFieldProbe renders one confirmField call and checks the frame
// contract holds for every line it produced, at exactly w cells - the same
// invariant renderFrame checks for a whole screen, applied to the lines this
// one helper is responsible for.
func confirmFieldProbe(t *testing.T, label, value string, w int) string {
	t.Helper()
	m := Model{p: plainPalette()}
	lines := m.confirmField(label, value, m.p.Fg, w)
	if len(lines) == 0 {
		t.Fatalf("confirmField(%q, ...) at w=%d produced no lines", label, w)
	}
	var b strings.Builder
	for _, l := range lines {
		rendered := l.render(w)
		if n := cells(rendered); n != w {
			t.Fatalf("confirmField(%q, ...) at w=%d rendered a line of %d cells:\n|%s|", label, w, n, rendered)
		}
		b.WriteString(rendered)
		b.WriteString("\n")
	}
	return b.String()
}

// TestConfirmFieldWrapsMandatoryProseInsteadOfCuttingIt reproduces the exact
// counter-review capture: the real "stop" Scope and Effect text, at the
// three widths the review's frames were taken at. Before the fix, the tail
// of each - "...are untouched." and "...its outcome." - is exactly what
// line.render cuts with no marker once the value alone is longer than what
// is left on the line.
func TestConfirmFieldWrapsMandatoryProseInsteadOfCuttingIt(t *testing.T) {
	m := Model{}
	_, scope, effect := m.actionObjectDescription(actionChoice{req: ActionRequest{Action: ActionStop}})
	for _, w := range []int{100, 80, 60} {
		got := confirmFieldProbe(t, "Scope", scope, w)
		if !strings.Contains(flattenWrap(got), "are untouched.") {
			t.Fatalf("Scope at w=%d lost its mandatory clause; got:\n%s", w, got)
		}
		got = confirmFieldProbe(t, "Effect", effect, w)
		if !strings.Contains(flattenWrap(got), "confirms its outcome.") {
			t.Fatalf("Effect at w=%d lost its mandatory clause; got:\n%s", w, got)
		}
	}
}

// TestConfirmFieldSanitizesHostileValueAtEveryWidth probes confirmField
// directly with C1 NEL (U+0085), a raw ANSI escape, wide CJK and an
// emoji-presentation sequence immediately followed by more text - the exact
// adversarial shapes hostileTree documents (frame_test.go) - inside a
// Scope-shaped value, so the probe exercises the wrapping mechanism itself
// rather than only whatever text production happens to put there today.
// This is the counter-review's own complaint about this PR's prior hostile
// sweep: a payload that never reaches the surface under test proves
// nothing.
func TestConfirmFieldSanitizesHostileValueAtEveryWidth(t *testing.T) {
	hostile := "The recorded runtime agent only; the Crew/Task\u0085worktree and\x1b[31m branch 修正 are untouched‼️tail."
	for _, w := range []int{100, 80, 60} {
		got := confirmFieldProbe(t, "Scope", hostile, w)
		if strings.Contains(got, "\x1b") || strings.Contains(got, "\u0085") {
			t.Fatalf("hostile control content leaked into the rendered Scope at w=%d:\n%q", w, got)
		}
		if !strings.Contains(got, "untouched") || !strings.Contains(got, "tail.") {
			t.Fatalf("hostile value at w=%d lost content around the sanitized control characters:\n%s", w, got)
		}
	}
}

// ---------- the gallery: a real "stop" confirmation, hostile identity ----------

// confirmGalleryState is one of the gallery's confirmation states, with the
// text it must carry - the same convention attachGallery (attach_test.go)
// established: the golden fixture and the required text live in one table,
// so a fixture regenerated after a sibling seam moves cannot silently drop
// the sentence that makes this state what it is.
type confirmGalleryState struct {
	name    string
	build   func(t *testing.T) string
	says    []string
	notSays []string
}

// hostileStopConfirm walks a hostileTree() (frame_test.go) fixture to the
// third Crew attempt - the one with a recorded active binding, so "stop" is
// enabled - after giving it a hostile CrewID (the confirmation's Object),
// and opens its confirmation. w/h sizes the frame; the confirmation stays
// open across a resize (Update's WindowSizeMsg path never touches
// m.confirm), which is how the counter-review captured the same dialog at
// three widths in the first place.
func hostileStopConfirm(t *testing.T, w, h int) Model {
	t.Helper()
	tree := hostileTree()
	tree.Projects[0].Crews[2].CrewID = "crew_\u0085修正\x1b[31mtail‼️more"
	m := New(func(context.Context) (query.Snapshot, error) { tree.AsOf = goldenAsOf; return tree, nil })
	m.p = plainPalette()
	m.g = unicodeGlyphs
	m, _ = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m, _ = send(t, m, m.Init()())
	m, _ = send(t, m, key("enter")) // the one real Project
	m, _ = send(t, m, key("down"))  // its Task row
	m, _ = send(t, m, key("enter")) // that Task's Crew attempts (needs_repair, blocked, Completed)
	m, _ = send(t, m, key("down"))  // blocked
	m, _ = send(t, m, key("down"))  // Completed group
	m, _ = send(t, m, key("enter")) // expand
	m, _ = send(t, m, key("down"))  // attempt 3: failed, binding active, stop enabled
	m, _ = send(t, m, key("a"))     // ACTIONS
	m, _ = send(t, m, key("down"))  // stop is the second entry
	m, cmd := send(t, m, key("enter"))
	if cmd != nil || m.confirm == nil {
		t.Fatalf("stop on an active-binding Crew must open a confirmation without running: cmd=%v confirm=%+v", cmd, m.confirm)
	}
	if m.confirm.choice.action != ActionStop {
		t.Fatalf("confirmation is for %q, want stop", m.confirm.choice.action)
	}
	return m
}

func confirmGallery() []confirmGalleryState {
	return []confirmGalleryState{{
		name: "confirm-stop-hostile-100x30",
		build: func(t *testing.T) string {
			return renderFrame(t, hostileStopConfirm(t, 100, 30))
		},
		says: []string{
			"CONFIRM stop?", "Object", "Scope", "Effect",
			"The recorded runtime agent only", "are untouched.",
			"released only after the service confirms its outcome.",
		},
		notSays: []string{"\x1b", "\u0085"},
	}, {
		name: "confirm-stop-hostile-80x24",
		build: func(t *testing.T) string {
			return renderFrame(t, hostileStopConfirm(t, 80, 24))
		},
		says: []string{
			"CONFIRM stop?", "Object", "Scope", "Effect",
			"The recorded runtime agent only", "are untouched.",
			"released only after the service confirms its outcome.",
		},
		notSays: []string{"\x1b", "\u0085"},
	}, {
		name: "confirm-stop-hostile-60x16",
		build: func(t *testing.T) string {
			return renderFrame(t, hostileStopConfirm(t, 60, 16))
		},
		says: []string{
			"CONFIRM stop?", "Object", "Scope", "Effect",
			"The recorded runtime agent only", "are untouched.",
			"released only after the service confirms its outcome.",
		},
		notSays: []string{"\x1b", "\u0085"},
	}}
}

// TODO(task 22): a confirm-discard-failed-crew gallery state and its
// failedCrewDiscardConfirm fixture lived here. discard is mvp.md task 22.

// TestGoldenConfirmationFrames pins the confirmation gallery byte for byte.
// Accept a deliberate change with:
//
//	go test ./internal/ui/console -run TestGoldenConfirmationFrames -update
func TestGoldenConfirmationFrames(t *testing.T) {
	for _, st := range confirmGallery() {
		t.Run(st.name, func(t *testing.T) {
			assertGolden(t, st.name, st.build(t))
		})
	}
}

// TestEveryConfirmationGalleryStateSaysWhatItMeans is the same states
// asserted as text, independent of the golden bytes: the mandatory Scope
// and Effect clauses, and the identity's control characters, sanitized.
func TestEveryConfirmationGalleryStateSaysWhatItMeans(t *testing.T) {
	for _, st := range confirmGallery() {
		t.Run(st.name, func(t *testing.T) {
			frame := st.build(t)
			// says/notSays are checked against the flattened frame: a mandatory
			// sentence that legitimately wraps across two lines must still
			// count as said, and this is a content check, not the frame-shape
			// one (that is TestGoldenConfirmationFrames' job via renderFrame).
			flat := flattenWrap(frame)
			for _, want := range st.says {
				if !strings.Contains(flat, want) {
					t.Errorf("%s does not say %q:\n%s", st.name, want, frame)
				}
			}
			for _, banned := range st.notSays {
				if strings.Contains(flat, banned) {
					t.Errorf("%s says %q, which it must not:\n%s", st.name, banned, frame)
				}
			}
		})
	}
}
