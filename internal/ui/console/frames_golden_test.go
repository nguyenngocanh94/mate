package console

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The foundation's own golden frames: one per breakpoint, both glyph sets,
// and the screens that are not a list. They exist to make a change to the
// chrome visible in review - a shifted divider, a lost "As of", a rule that
// stopped teeing - and to give the other Console tasks worked examples of
// the harness.
//
// Accept a deliberate change with:
//
//	go test ./internal/ui/console -run TestGoldenFrames -update

func TestGoldenFramesWorkspaceAtEveryBreakpoint(t *testing.T) {
	goldenFrame(t, "workspace-160x48-unicode", sampleTree(), 160, 48, unicodeGlyphs)
	goldenFrame(t, "workspace-120x36-unicode", sampleTree(), 120, 36, unicodeGlyphs)
	goldenFrame(t, "workspace-80x24-unicode", sampleTree(), 80, 24, unicodeGlyphs)
	goldenFrame(t, "workspace-80x24-ascii", sampleTree(), 80, 24, asciiGlyphs)
	goldenFrame(t, "workspace-160x48-ascii", sampleTree(), 160, 48, asciiGlyphs)
	goldenFrame(t, "toosmall-56x14-unicode", sampleTree(), 56, 14, unicodeGlyphs)
	goldenFrame(t, "empty-120x36-unicode", query.Snapshot{WorkspaceID: "ws_acme"}, 120, 36, unicodeGlyphs)
}

func TestGoldenFramesLoadingAndFailed(t *testing.T) {
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m.phase = phaseLoading
	m.hasLoaded = false
	assertGolden(t, "loading-120x36-unicode", renderFrame(t, m))

	failed := newFailedFixture(t, errFake("open /Users/dev/work/acme/.mate/mate.db: permission denied"), 120, 36, unicodeGlyphs)
	assertGolden(t, "failed-120x36-unicode", renderFrame(t, failed))

	// A refresh that fails is a different screen: the snapshot already on
	// screen stays, and the message line says the read did not happen.
	stale := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	stale, _ = send(t, stale, treeLoadedMsg{err: errFake("database is locked")})
	assertGolden(t, "refresh-failed-120x36-unicode", renderFrame(t, stale))
}

// TestGoldenFramesDrilledIntoAProject is the Project frame with a Crew
// selected, the deepest level mate has.
//
// TODO(task 21): v1 drilled one level further, into a Task's Crew
// attempts; those attempts-*.txt fixtures went with the Task level.
func TestGoldenFramesDrilledIntoAProject(t *testing.T) {
	m := newFixture(t, sampleTree(), 140, 40, unicodeGlyphs)
	m, _ = send(t, m, key("enter")) // the payments-api Project
	m, _ = send(t, m, key("down"))  // its one active Crew
	assertGolden(t, "project-crew-140x40-unicode", renderFrame(t, m))

	m, _ = send(t, m, key("tab")) // focus the inspector on that Crew
	assertGolden(t, "project-crew-inspector-focused-140x40-unicode", renderFrame(t, m))

	narrow, _ := send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	narrow, _ = send(t, narrow, key("tab")) // Detail over the main region
	assertGolden(t, "project-crew-detail-80x24-unicode", renderFrame(t, narrow))
}
