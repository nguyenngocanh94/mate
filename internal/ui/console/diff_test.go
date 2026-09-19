package console

// The Crew row's diff action and its overlay (mvp.md task 21). Everything
// here goes through the same seam a real Console uses - the menu builds an
// ActionRequest, the ActionFunc answers with text, the overlay shows it - so
// nothing in this file knows what git is.

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// sampleDiffText is what `matev2 diff shop k3` prints for a crew two
// commits ahead with one uncommitted file: the dirty line, the commit list,
// a blank line, then the patch. It is long enough to scroll at 80x24 and at
// 120x36, which is the point of the fixture.
const sampleDiffText = `1 uncommitted file(s) not shown
9f1c2ab add the idempotency key index
3ec77d0 add a migration for idempotency_keys

diff --git a/db/migrations/0007_idempotency_keys.sql b/db/migrations/0007_idempotency_keys.sql
new file mode 100644
index 0000000..2d4b1a9
--- /dev/null
+++ b/db/migrations/0007_idempotency_keys.sql
@@ -0,0 +1,6 @@
+CREATE TABLE idempotency_keys (
+  key         TEXT PRIMARY KEY,
+  event_id    TEXT NOT NULL,
+  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
+);
+CREATE INDEX idempotency_keys_event_id ON idempotency_keys (event_id);
diff --git a/internal/webhook/stripe.go b/internal/webhook/stripe.go
index 1c0f3ad..8b2e5c1 100644
--- a/internal/webhook/stripe.go
+++ b/internal/webhook/stripe.go
@@ -41,7 +41,14 @@ func (h *Handler) Deliver(ctx context.Context, ev Event) error {
-	if err := h.charge(ctx, ev); err != nil {
+	seen, err := h.keys.Seen(ctx, ev.IdempotencyKey)
+	if err != nil {
+		return err
+	}
+	if seen {
+		return nil
+	}
+	if err := h.charge(ctx, ev); err != nil {
 		return err
 	}
 	return nil
`

// sampleCrewBranch is the branch sampleTree's running Crew records; the
// overlay's title has to name it.
const sampleCrewBranch = "matev2/01J9P6Q6"

// toCrewMenu walks to sampleTree's running Crew and opens its Actions menu.
func toCrewMenu(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = send(t, m, key("enter")) // the payments-api Project
	m, _ = send(t, m, key("down"))  // its running Crew
	if r, ok := m.selectedRow(); !ok || r.kind != rowCrew {
		t.Fatalf("selected row = %+v (ok=%v), want a Crew row", r, ok)
	}
	m, _ = send(t, m, key("a"))
	return m
}

// diffMenuIndex is where the diff entry sits, found by name rather than by
// position so a new menu entry does not silently retarget this test.
func diffMenuIndex(t *testing.T, m Model) int {
	t.Helper()
	for i, c := range m.actionChoices {
		if c.action == ActionDiff {
			return i
		}
	}
	t.Fatalf("the Crew row's menu offers no diff entry: %+v", m.actionChoices)
	return -1
}

// openDiffOverlay runs the diff action on sampleTree's running Crew and
// returns the Console with the overlay open, plus every request the runner
// saw.
func openDiffOverlay(t *testing.T, m Model, text string, runErr error) (Model, *[]ActionRequest) {
	t.Helper()
	m, got := withRunner(m, text, runErr)
	m = toCrewMenu(t, m)
	m.actionIndex = diffMenuIndex(t, m)
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("diff did not dispatch; it writes nothing and must not ask for confirmation")
	}
	m, _ = send(t, m, cmd())
	return m, got
}

// TestCrewRowOffersDiffAndSendsProjectAndCrew is the request shape: the
// bridge has to find crews/<id>.meta, which needs both names, and a single
// string would make it guess which one it had.
func TestCrewRowOffersDiffAndSendsProjectAndCrew(t *testing.T) {
	m, got := openDiffOverlay(t, newFixture(t, sampleTree(), 120, 36, unicodeGlyphs), sampleDiffText, nil)

	if len(*got) != 1 {
		t.Fatalf("runner requests = %+v, want exactly one", *got)
	}
	req := (*got)[0]
	tree := sampleTree()
	if req.Action != ActionDiff || req.Target != tree.Projects[0].ProjectID ||
		req.Crew != tree.Projects[0].Crews[1].CrewID || req.TargetKind != "crew" {
		t.Fatalf("diff request = %+v, want the Project in Target and the Crew in Crew", req)
	}
	if !m.diff.open {
		t.Fatal("a successful diff did not open the overlay")
	}
	if m.msg.text != "" {
		t.Fatalf("a diff left %q on the message line; its whole result is the overlay", m.msg.text)
	}
}

// TestDiffIsOfferedOnACrewInEveryStateThatHasABranch: reviewing is what a
// reader does before deciding a Crew is done, so the entry may not wait for
// `wait-mate`. It is withheld only when there is no branch to compare.
func TestDiffIsOfferedOnACrewInEveryStateThatHasABranch(t *testing.T) {
	for _, status := range []query.CrewStatus{query.CrewSpawned, query.CrewWorking, query.CrewNeedsDecision, query.CrewWaitMate} {
		t.Run(string(status), func(t *testing.T) {
			tree := sampleTree()
			tree.Projects[0].Crews[1].Status = status
			m := toCrewMenu(t, newFixture(t, tree, 120, 36, unicodeGlyphs))
			choice := m.actionChoices[diffMenuIndex(t, m)]
			if !choice.enabled {
				t.Fatalf("diff on a %s Crew = %+v, want it offered", status, choice)
			}
			if !strings.Contains(choice.desc, sampleCrewBranch) {
				t.Fatalf("diff description %q does not name the branch it would show", choice.desc)
			}
		})
	}
}

func TestDiffIsRefusedWhenTheCrewRecordsNoBranch(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Crews[1].Worktree = query.AbsentField[query.WorktreeValue]("this crew has no worktree row")
	m := toCrewMenu(t, newFixture(t, tree, 120, 36, unicodeGlyphs))

	choice := m.actionChoices[diffMenuIndex(t, m)]
	if choice.enabled {
		t.Fatalf("diff on a branchless Crew = %+v, want it refused", choice)
	}
	if !strings.Contains(choice.desc, "records no branch") {
		t.Fatalf("refusal = %q, want it to say there is no branch", choice.desc)
	}
}

// TestDiffRefusalDistinguishesAnUnreadableWorktreeFromNone is
// query.FieldState's rule on this surface: a read that failed must not be
// reported as the established fact "there is no branch".
func TestDiffRefusalDistinguishesAnUnreadableWorktreeFromNone(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Crews[1].Worktree = query.UnknownField[query.WorktreeValue]("worktree read timed out")
	m := toCrewMenu(t, newFixture(t, tree, 120, 36, unicodeGlyphs))

	choice := m.actionChoices[diffMenuIndex(t, m)]
	if choice.enabled {
		t.Fatalf("diff on an unreadable worktree = %+v, want it refused", choice)
	}
	if !strings.Contains(choice.desc, "could not be read") || strings.Contains(choice.desc, "records no branch") {
		t.Fatalf("refusal = %q, want it to say the read failed", choice.desc)
	}
}

// TestDiffOverlayScrollsAndCloses is the whole keyboard contract of the
// overlay: it scrolls, it pages, and all three close keys close it. q is
// among them and must not quit the Console - a full-region reader whose q
// exits the application is the trap every pager has trained people out of.
func TestDiffOverlayScrollsAndCloses(t *testing.T) {
	base, _ := openDiffOverlay(t, newFixture(t, sampleTree(), 120, 36, unicodeGlyphs), sampleDiffText, nil)

	m, _ := send(t, base, key("down"))
	if m.diff.top != 1 {
		t.Fatalf("down did not scroll the overlay: top=%d", m.diff.top)
	}
	m, _ = send(t, m, key("up"))
	if m.diff.top != 0 {
		t.Fatalf("up did not scroll back: top=%d", m.diff.top)
	}
	m, _ = send(t, m, key("up"))
	if m.diff.top != 0 {
		t.Fatalf("up ran past the top: top=%d", m.diff.top)
	}
	m, _ = send(t, m, key("pgdn"))
	if m.diff.top < 1 {
		t.Fatalf("PgDn did not page the overlay: top=%d", m.diff.top)
	}
	paged := m.diff.top
	m, _ = send(t, m, key("pgup"))
	if m.diff.top >= paged {
		t.Fatalf("PgUp did not page back: top=%d, was %d", m.diff.top, paged)
	}

	// The tail is reachable and the offset never runs past it.
	m = base
	for i := 0; i < 200; i++ {
		m, _ = send(t, m, key("down"))
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "return nil") {
		t.Fatalf("the last line of the patch is not reachable by scrolling:\n%s", frame)
	}

	for _, closeKey := range []string{"esc", "enter", "q"} {
		m, cmd := send(t, base, key(closeKey))
		if m.diff.open {
			t.Fatalf("%s did not close the diff overlay", closeKey)
		}
		if m.quitting || cmd != nil {
			t.Fatalf("%s quit the Console instead of closing the overlay (quitting=%v cmd=%v)", closeKey, m.quitting, cmd)
		}
		// The row under the cursor is untouched: the overlay was modal, so
		// nothing behind it moved while it was open.
		if r, ok := m.selectedRow(); !ok || r.kind != rowCrew {
			t.Fatalf("after %s the selection is %+v, want the Crew row it was opened from", closeKey, r)
		}
	}

	// Ctrl+C is still the way out of the Console from inside the overlay.
	m, _ = send(t, base, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quitting {
		t.Fatal("ctrl+c inside the diff overlay did not quit")
	}
}

// TestDiffOverlaySwallowsTheKeysBehindIt: the overlay is modal, so a key
// that would move the list must not reach it - a selection change nobody
// can see is indistinguishable from a lost keystroke.
func TestDiffOverlaySwallowsTheKeysBehindIt(t *testing.T) {
	m, got := openDiffOverlay(t, newFixture(t, sampleTree(), 120, 36, unicodeGlyphs), sampleDiffText, nil)
	before, _ := m.selectedRow()

	for _, k := range []string{"a", "r", "tab", "m", "s", "n"} {
		m, _ = send(t, m, key(k))
	}
	if !m.diff.open {
		t.Fatal("a key behind the overlay closed it")
	}
	if m.actions || m.actionInputMode || m.harnessPick {
		t.Fatal("a key behind the overlay opened another surface")
	}
	if after, _ := m.selectedRow(); after != before {
		t.Fatalf("selection moved behind the overlay: %+v -> %+v", before, after)
	}
	if len(*got) != 1 {
		t.Fatalf("a key behind the overlay dispatched another action: %+v", *got)
	}
}

// TestDiffWheelScrollsTheOverlay: the wheel belongs to whatever is on top,
// and while the overlay is open that is the patch, not the box panel under
// it.
func TestDiffWheelScrollsTheOverlay(t *testing.T) {
	m, _ := openDiffOverlay(t, newFixture(t, sampleTree(), 120, 36, unicodeGlyphs), sampleDiffText, nil)

	wheel := func(b tea.MouseButton) tea.MouseMsg {
		return tea.MouseMsg(tea.MouseEvent{X: 10, Y: 10, Action: tea.MouseActionPress, Button: b})
	}
	m, _ = send(t, m, wheel(tea.MouseButtonWheelDown))
	if m.diff.top != 1 {
		t.Fatalf("wheel down did not scroll the overlay: top=%d", m.diff.top)
	}
	m, _ = send(t, m, wheel(tea.MouseButtonWheelUp))
	if m.diff.top != 0 {
		t.Fatalf("wheel up did not scroll back: top=%d", m.diff.top)
	}
}

// TestDiffFailureStaysOnTheMessageLine: there is no overlay to put a
// refusal in, and an empty frame would read as success.
func TestDiffFailureStaysOnTheMessageLine(t *testing.T) {
	m, _ := openDiffOverlay(t, newFixture(t, sampleTree(), 120, 36, unicodeGlyphs), "",
		errors.New("no crew crew_01J9P6Q6W0E5V8XK2M4B8DT is recorded for project payments-api"))

	if m.diff.open {
		t.Fatal("a failed diff opened an overlay over nothing")
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "no crew") {
		t.Fatalf("message = %+v, want the bridge's own refusal", m.msg)
	}
}

// The two golden sizes. Both are rendered with plainPalette, so the "+" and
// "-" that carry an addition and a deletion are proved legible without
// colour - the styling in diffLineStyle is only ever a second signal.
func TestGoldenDiffOverlayAt120x36(t *testing.T) {
	m, _ := openDiffOverlay(t, newFixture(t, sampleTree(), 120, 36, unicodeGlyphs), sampleDiffText, nil)
	frame := renderFrame(t, m)
	assertGolden(t, "diff-overlay-120x36-unicode", frame)
	crew := sampleTree().Projects[0].Crews[1].CrewID
	for _, want := range []string{"diff · " + crew + " · " + sampleCrewBranch, "uncommitted file(s) not shown", "Esc Close"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the overlay does not say %q:\n%s", want, frame)
		}
	}
}

func TestGoldenDiffOverlayAt80x24(t *testing.T) {
	m, _ := openDiffOverlay(t, newFixture(t, sampleTree(), 80, 24, unicodeGlyphs), sampleDiffText, nil)
	assertGolden(t, "diff-overlay-80x24-unicode", renderFrame(t, m))
}

// TestDiffOverlayKeyLineNamesItsOwnKeys: the frame's key line must never
// advertise a key the overlay swallows, and it must say Ctrl+C rather than
// q, because q closes here.
func TestDiffOverlayKeyLineNamesItsOwnKeys(t *testing.T) {
	m, _ := openDiffOverlay(t, newFixture(t, sampleTree(), 120, 36, unicodeGlyphs), sampleDiffText, nil)
	frame := renderFrame(t, m)
	last := strings.Split(frame, "\n")[m.h-1]

	for _, want := range []string{"Scroll", "Esc Close", "Ctrl+C Quit"} {
		if !strings.Contains(last, want) {
			t.Fatalf("key line %q does not name %q", last, want)
		}
	}
	if strings.Contains(last, "q Quit") {
		t.Fatalf("key line %q offers q as quit, but q closes the overlay", last)
	}
}
