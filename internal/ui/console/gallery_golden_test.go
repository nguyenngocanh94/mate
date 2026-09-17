package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The states gallery's own two groups, wired to fixtures: "Empty, loading,
// lỗi tải toàn bộ" (design/mate-console-states.html#empty) and "Known ·
// Absent · Unknown" (design/mate-console-states.html#unknown). These are
// this task's own surface - the chrome and the screens with little or
// nothing in the main region - so each fixture below exercises the frame at
// the gallery's own state, not a hand-picked one.
//
// The list and inspector content these render is the foundation's own
// (rows.go/seams.go): the fuller field set (worktree status word, a Crew's
// Reason field) is the inspector-pane task's own surface and is not yet
// wired. These fixtures capture today's frame around that state; expect
// them to need `-update` once that field set lands.

// galleryWorkspaceRoot matches the reference sample data's root
// (design/mate-tui-data.js) so a header rendered from these fixtures reads
// the same as the states gallery's own screenshots.
const galleryWorkspaceRoot = "/Users/dev/work/acme"

func galleryWorkspace() query.Field[query.WorkspaceValue] {
	return query.KnownField(query.WorkspaceValue{
		Name: "acme", Root: galleryWorkspaceRoot, DatabasePath: galleryWorkspaceRoot + "/.matev2/matev2.db",
	})
}

// TestGalleryEmptyLoadingAndError covers the gallery's "Empty, loading,
// lỗi tải toàn bộ" group: a workspace with no Projects, a Project with no
// Mate, a Project with no Tasks, Loading, and a whole-snapshot read error
// with its retry affordance. Header and footer must stay present and the
// three screens must read as three different facts, never as each other.
func TestGalleryEmptyLoadingAndError(t *testing.T) {
	t.Run("workspace without projects", func(t *testing.T) {
		tree := query.Snapshot{WorkspaceID: "ws_acme", Workspace: galleryWorkspace()}
		m := goldenFrame(t, "gallery-empty-workspace-80x24-unicode", tree, 80, 24, unicodeGlyphs)
		frame := renderFrame(t, m)
		if !strings.Contains(frame, "No projects recorded in workspace acme.") {
			t.Fatalf("empty workspace does not say why the list is empty:\n%s", frame)
		}
	})

	t.Run("project without a mate", func(t *testing.T) {
		tree := query.Snapshot{
			WorkspaceID: "ws_acme",
			Workspace:   galleryWorkspace(),
			Projects: []query.ProjectNode{{
				ProjectID: "proj_01J9M1C5H9S4Z1E6X0D3Q8P7RF",
				Name:      "notifications-service",
				Mate:      absentMate("this project has no designated Mate"),
				Tasks: []query.TaskNode{{
					TaskID: "task_01J9N3N4T8C3J0Q5G9P2A7Z6BR",
					Title:  "Migrate email delivery to SES",
					Status: query.TaskReady,
					Error:  query.AbsentField[query.ErrorReason](notErrorState),
				}},
			}},
		}
		m := newFixture(t, tree, 120, 36, unicodeGlyphs)
		m, _ = send(t, m, key("down")) // select the Project row
		m, _ = send(t, m, key("enter"))
		assertGolden(t, "gallery-project-no-mate-120x36-unicode", renderFrame(t, m))
	})

	t.Run("project without tasks", func(t *testing.T) {
		tree := query.Snapshot{
			WorkspaceID: "ws_acme",
			Workspace:   galleryWorkspace(),
			Projects: []query.ProjectNode{{
				ProjectID: "proj_01J9M1D6J0T5A2F7Y1E4R9Q8SG",
				Name:      "docs-site",
				Mate: query.MateNode{
					Designated: query.KnownField(query.MateIdentity{
						MateID: "mate_01J9M2H0P4Y9E6K1C5J8W3V2XM", HarnessKind: query.HarnessClaude, Status: query.MateStopped,
					}),
					AgentName: query.KnownField("mate-docs-site"),
					Binding:   query.AbsentField[query.BindingValue]("stopped at 2026-09-09 18:02; binding kept in audit only"),
					Error:     query.AbsentField[query.ErrorReason](notErrorState),
				},
			}},
		}
		m := newFixture(t, tree, 80, 24, unicodeGlyphs)
		m, _ = send(t, m, key("down")) // select the Project row
		m, _ = send(t, m, key("enter"))
		frame := renderFrame(t, m)
		// The Mate row is always present - "Mate ở hàng đầu, luôn tìm thấy"
		// (design notes) - so a Project with no Tasks is not an empty list;
		// it is a list of exactly one row, the Mate, and no Task rows below
		// it. emptyMessage's "No Tasks yet" is for a Project frame with zero
		// rows at all, which a designated Mate always prevents.
		if strings.Contains(frame, "No Tasks yet") {
			t.Fatalf("a Project with a Mate row must not show the empty-list message:\n%s", frame)
		}
		assertGolden(t, "gallery-project-no-tasks-80x24-unicode", frame)
	})

	t.Run("loading", func(t *testing.T) {
		m := newFixture(t, sampleTree(), 80, 24, unicodeGlyphs)
		m.phase = phaseLoading
		m.hasLoaded = false
		assertGolden(t, "gallery-loading-80x24-unicode", renderFrame(t, m))
	})

	t.Run("whole-snapshot read error with retry", func(t *testing.T) {
		// newFailedFixture's tree is the zero Snapshot: no Projects were ever
		// read, so this is also the "empty workspace plus failed read" case -
		// the reproduction a counter-review used
		// (newFailedFixture(errFake("sqlite busy"), 80, 24, unicodeGlyphs)) to
		// show the breadcrumb falsely claiming "0 projects". The only fact a
		// failed first load establishes is that the read failed, not how many
		// Projects the workspace has.
		err := errFake("runtime_unavailable: sqlite: database is locked (SQLITE_BUSY) after 5s (" + galleryWorkspaceRoot + "/.matev2/matev2.db)")
		failed := newFailedFixture(t, err, 80, 24, unicodeGlyphs)
		frame := renderFrame(t, failed)
		if !strings.Contains(frame, "r retries the read; q quits.") {
			t.Fatalf("read-error screen does not offer the retry affordance:\n%s", frame)
		}
		breadcrumb := strings.Split(frame, "\n")[1]
		if strings.Contains(breadcrumb, "project") {
			t.Fatalf("a failed read must not fabricate a project count - the only established fact is that the read failed:\n%q", breadcrumb)
		}
		if !strings.Contains(frame, "(unknown)") {
			t.Fatalf("a failed read with no prior snapshot must show the workspace as unknown, not a fabricated identity:\n%s", frame)
		}
		assertGolden(t, "gallery-error-retry-80x24-unicode", frame)
	})

	// The three must never render alike: an empty list, a screen with
	// nothing loaded yet, and a screen that could not load anything are
	// three different facts about the reader's situation.
	empty := renderFrame(t, newFixture(t, query.Snapshot{WorkspaceID: "ws_acme", Workspace: galleryWorkspace()}, 80, 24, unicodeGlyphs))
	loadingM := newFixture(t, sampleTree(), 80, 24, unicodeGlyphs)
	loadingM.phase, loadingM.hasLoaded = phaseLoading, false
	loading := renderFrame(t, loadingM)
	errored := renderFrame(t, newFailedFixture(t, errFake("read failed"), 80, 24, unicodeGlyphs))
	if empty == loading || empty == errored || loading == errored {
		t.Fatalf("empty, loading and error must render distinctly from one another")
	}
	// Distinctness alone does not rule out three different wrong claims: an
	// empty workspace genuinely has "0 projects", but loading and error must
	// not print that same fact about a workspace nothing has been read from
	// yet.
	if breadcrumb := strings.Split(loading, "\n")[1]; strings.Contains(breadcrumb, "project") {
		t.Fatalf("loading breadcrumb must not claim a project count before the first read completes:\n%q", breadcrumb)
	}
	if breadcrumb := strings.Split(errored, "\n")[1]; strings.Contains(breadcrumb, "project") {
		t.Fatalf("errored breadcrumb must not claim a project count when the read failed:\n%q", breadcrumb)
	}
}

// galleryUnknownTree is sampleTree with its running attempt's worktree read
// failed instead of succeeding, and Snapshot.Warnings carrying exactly that
// one field - the shape query.LoadSnapshot itself produces for a partial
// read failure (see internal/query/read.go's note calls).
func galleryUnknownTree() query.Snapshot {
	tree := sampleTree()
	const reason = "lookup timed out (2s)"
	crew := &tree.Projects[0].Tasks[0].Crews[1]
	row := query.RowRef{Kind: query.RowCrew, ID: crew.CrewID, Label: "attempt 2"}
	crew.Worktree = query.UnknownField[query.WorktreeValue](reason)
	tree.Warnings = []query.FieldWarning{{Field: "worktree", Row: row, Reason: reason}}
	return tree
}

// TestGalleryKnownAbsentUnknown covers the gallery's "Known · Absent ·
// Unknown" group. The footer's standing warning line is this task's own
// surface (see warningsFooterMsg); it must name the exact field, row and
// reason the read layer reported, and it must not fire at all on a Known
// bad value or a legitimate Absent one.
func TestGalleryKnownAbsentUnknown(t *testing.T) {
	t.Run("one field unknown", func(t *testing.T) {
		tree := galleryUnknownTree()
		m := newFixture(t, tree, 120, 36, unicodeGlyphs)
		m, _ = send(t, m, key("enter")) // payments-api
		m, _ = send(t, m, key("down"))  // the webhook Task
		m, _ = send(t, m, key("enter")) // its attempts
		m, _ = send(t, m, key("down"))  // the attempt whose worktree failed to read
		frame := renderFrame(t, m)
		want := "? 1 field unknown: worktree of attempt 2 (lookup timed out (2s))"
		if !strings.Contains(frame, want) {
			t.Fatalf("frame does not carry the standing unknown-field line %q:\n%s", want, frame)
		}
		assertGolden(t, "gallery-unknown-field-120x36-unicode", frame)
	})

	// The gallery's other two states in this group - a needs_repair Crew
	// with a missing recorded worktree and its reason, and an
	// awaiting_review Crew whose Binding/RetryOf/Error are legitimately
	// Absent with the reason each is none - render Worktree status, Reason
	// and Retry of. Those are inspector-pane fields (PR 48's surface, per
	// firstmate's ruling on this PR's fix round), not this task's chrome and
	// footer surface, so they are not fixtured here. Rendering them
	// incompletely - as this PR previously did, showing only a path and
	// branch under a needs_repair status with no explanation - would bless
	// output the design's own honesty rules call out by name.
}
