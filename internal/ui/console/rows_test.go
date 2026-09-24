package console

import (
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// renderSpans is a small test helper: render a span slice through a bare
// line at generous width, so a test can assert on the plain text a column
// builder produced without hard-coding cell offsets.
func renderSpans(spans []span, w int) string {
	return newLine().addSpans(spans...).render(w)
}

// TestAttentionSpansDistinguishesStaleBindingFromLive: a Crew's
// HeldBindingsForAgent-style status alone cannot tell a live agent from one
// mate could not confirm stopped, so the Crew row's ATTENTION
// cell must say so - crewAttention (internal/query/attention.go) already
// derives a stale binding as its own Kind, and attentionSpans (list.go) must
// carry it through rather than treating every non-error status alike.
func TestAttentionSpansDistinguishesStaleBindingFromLive(t *testing.T) {
	p := plainPalette()
	live := query.AbsentField[query.Attention]("crew c2 is recorded running and nothing about it needs attention")
	stale := query.KnownField(query.Attention{Kind: query.AttentionStaleBinding, Why: "crew c1 is recorded running but its runtime binding is stale"})
	liveText := renderSpans(attentionSpans(live, p), 40)
	staleText := renderSpans(attentionSpans(stale, p), 40)
	if liveText == staleText {
		t.Fatalf("a live and a stale-bound attempt render the same ATTENTION cell: %q", liveText)
	}
	if !strings.Contains(staleText, "stale") {
		t.Fatalf("stale attention cell = %q, want it to say stale", staleText)
	}
}

// noteModel is a Model bare enough to call crewRowNoteSpans, which is the
// TASK cell's one production entry point (list.go's projectListItems).
func noteModel() Model { return Model{p: plainPalette(), g: unicodeGlyphs} }

// TestCrewNoteSpansDistinguishesRemovedWorktreeFromLive: a worktree the
// teardown path already removed must not render byte-identically to a live
// one, in the one place a reader actually looks - the list, not only the
// inspector.
func TestCrewNoteSpansDistinguishesRemovedWorktreeFromLive(t *testing.T) {
	m := noteModel()
	live := query.CrewNode{CrewID: "c1", Worktree: query.KnownField(query.WorktreeValue{
		Path: "/w", Branch: "crew/c1", Status: query.WorktreeRecordedCreated})}
	removed := query.CrewNode{CrewID: "c1", Worktree: query.KnownField(query.WorktreeValue{
		Path: "/w", Branch: "crew/c1", Status: query.WorktreeRecordedRemoved})}
	liveText := renderSpans(m.crewRowNoteSpans(live, 40), 40)
	removedText := renderSpans(m.crewRowNoteSpans(removed, 40), 40)
	if liveText == removedText {
		t.Fatalf("a live and a removed worktree at the same path rendered identically: %q", liveText)
	}
	if !strings.Contains(removedText, "missing") {
		t.Fatalf("removed worktree note = %q, want it to say missing (the recorded worktree-status vocabulary), not \"removed\"", removedText)
	}

}

// TestCrewRowNoteSpansDrawsTheObserversHealth is the health column of
// mvp.md section 4b: a reading of the pane beside the Crew's state, in the
// words the design's NOTE cell has room for. An agent Herdr no longer has is
// the one reading that must be unmistakable.
func TestCrewRowNoteSpansDrawsTheObserversHealth(t *testing.T) {
	m := noteModel()
	crew := func(h query.CrewHealth) query.CrewNode {
		return query.CrewNode{
			CrewID:    "c1",
			Attention: query.AbsentField[query.Attention]("nothing about this crew needs attention"),
			Health:    query.KnownField(h),
		}
	}
	cases := []struct {
		name   string
		health query.CrewHealth
		want   string
	}{
		// A busy pane redraws its spinner every poll, so its quiet time is
		// always near zero; the NOTE shows how long it has been busy.
		{"busy", query.CrewHealth{AgentPresent: true, Composer: query.ComposerBusy, QuietFor: 0, ComposerFor: 12 * time.Second}, "pane busy 12s"},
		{"idle", query.CrewHealth{AgentPresent: true, Composer: query.ComposerEmpty, QuietFor: 4 * time.Minute}, "pane idle 4m"},
		{"pending is idle too", query.CrewHealth{AgentPresent: true, Composer: query.ComposerPending, QuietFor: 90 * time.Second}, "pane idle 1m"},
		// 12 cells: the NOTE column is 14 and packNoteItems drops a longer
		// item whole, which is how this read as a blank cell (2026-09-19).
		{"unclear pane", query.CrewHealth{AgentPresent: true, Composer: query.ComposerUnknown}, "pane unclear"},
		{"agent gone", query.CrewHealth{Composer: query.ComposerUnknown, QuietFor: time.Hour}, "agent gone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimRight(renderSpans(m.crewRowNoteSpans(crew(tc.health), 40), 40), " ")
			if got != tc.want {
				t.Fatalf("note = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCrewRowNoteSpansDrawsNothingWithoutAnObservation: a Crew nobody has
// observed - every one-shot read of the tree, and a console whose observer
// has not polled yet - says nothing at all. It must never render as if the
// observer had looked and found something.
func TestCrewRowNoteSpansDrawsNothingWithoutAnObservation(t *testing.T) {
	m := noteModel()
	for name, c := range map[string]query.CrewNode{
		"absent": {
			CrewID:    "c1",
			Attention: query.AbsentField[query.Attention]("nothing about this crew needs attention"),
			Health:    query.AbsentField[query.CrewHealth]("no observer has looked at this crew yet"),
		},
		"unset": {
			CrewID:    "c1",
			Attention: query.AbsentField[query.Attention]("nothing about this crew needs attention"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := strings.TrimSpace(renderSpans(m.crewRowNoteSpans(c, 40), 40)); got != "" {
				t.Fatalf("note = %q, want nothing", got)
			}
		})
	}
}

// TestCrewRowNoteSpansHealthLastMeansWorkOutcomeSurvivesAtNarrowWidth: the
// health reading is the lowest-priority item, so a column too narrow for
// both drops it and keeps the recorded outcome. The pane can be looked at
// directly; the outcome cannot.
func TestCrewRowNoteSpansHealthLastMeansWorkOutcomeSurvivesAtNarrowWidth(t *testing.T) {
	m := noteModel()
	c := query.CrewNode{
		CrewID:    "c1",
		Attention: query.AbsentField[query.Attention]("nothing about this crew needs attention"),
		Worktree: query.KnownField(query.WorktreeValue{
			Path: "/w", Branch: "crew/c1", Status: query.WorktreeRecordedRemoved,
		}),
		Health: query.KnownField(query.CrewHealth{AgentPresent: true, Composer: query.ComposerEmpty, QuietFor: 4 * time.Minute}),
	}
	full := strings.TrimRight(renderSpans(m.crewRowNoteSpans(c, 40), 40), " ")
	if !strings.Contains(full, "worktree missing") || !strings.Contains(full, "pane idle 4m") {
		t.Fatalf("full-width note = %q, want both items", full)
	}
	narrow := strings.TrimRight(renderSpans(m.crewRowNoteSpans(c, 18), 18), " ")
	if narrow != "worktree missing" {
		t.Fatalf("narrow note = %q, want the work outcome kept and the health dropped", narrow)
	}
}

// TestCrewNoteSpansDropsWholeItemsNotHalfWords is the drop-priority rule
// (design/mate-console-design-notes.html, "Rut gon"): when the ATTENTION
// cell is too narrow for every fact, whole items are dropped from the end,
// never a word cut in half.
func TestCrewNoteSpansDropsWholeItemsNotHalfWords(t *testing.T) {
	m := noteModel()
	c := query.CrewNode{
		CrewID:    "c1",
		Attention: query.KnownField(query.Attention{Kind: query.AttentionStaleBinding, Why: "stale"}),
		Worktree: query.KnownField(query.WorktreeValue{
			Path: "/w", Branch: "crew/c1", Status: query.WorktreeRecordedRemoved,
		}),
	}
	full := renderSpans(m.crewRowNoteSpans(c, 40), 40)
	if !strings.Contains(full, "stale") || !strings.Contains(full, "worktree missing") {
		t.Fatalf("full-width note = %q, want both items", full)
	}
	// 8 cells fits "! stale" (7) but not "  worktree missing" alongside it:
	// the worktree item must be dropped whole, not cut down to
	// "! stale  wor" or a similar half-word fragment.
	narrow := strings.TrimRight(renderSpans(m.crewRowNoteSpans(c, 8), 8), " ")
	if narrow != "! stale" {
		t.Fatalf("narrow note = %q, want exactly %q with the worktree item dropped whole", narrow, "! stale")
	}
}

// TestCrewNoteSpansStopsAtFirstDroppedItemRatherThanPromotingALaterOne is
// the drop-priority rule's other half: once an item does not fit, later,
// lower-priority items must not be promoted into the space it would have
// used, even when one of them would fit alone. Swapping crewNoteItems'
// `break` for `continue` left TestCrewNoteSpansDropsWholeItemsNotHalfWords
// green (PR 49 counter-review, N5) because that test's own narrow case
// only ever has one item past the cutoff. This one has two: the derived
// Attention ("! unknown read failed here", 26 cells) cannot fit in a
// 12-cell column, so - correctly - nothing is shown, even though "worktree
// missing" (16 cells) would not fit either and a shorter later item would.
func TestCrewNoteSpansStopsAtFirstDroppedItemRatherThanPromotingALaterOne(t *testing.T) {
	m := noteModel()
	c := query.CrewNode{
		CrewID:    "c1",
		Attention: query.AbsentField[query.Attention]("recorded running and nothing needs attention"),
		Worktree: query.KnownField(query.WorktreeValue{
			Path: "/repo/.worktrees/c1", Status: query.WorktreeRecordedRemoved,
		}),
	}
	if got := spansWidth([]span{{text: "worktree missing"}}); got != 16 {
		t.Fatalf("fixture assumption broken: %q is %d cells, want 16", "worktree missing", got)
	}
	got := strings.TrimRight(renderSpans(m.crewRowNoteSpans(c, 12), 12), " ")
	if got != "" {
		t.Fatalf("note in a 12-cell column = %q, want empty: the 16-cell item does not fit", got)
	}
}

// TestMateSummarySpansDistinguishesAbsentFromUnknown is the honesty rule at
// the Workspace level's MATE column: a Project read to genuinely have no
// Mate must say "none", and a Project whose Mate could not be read at all
// must say "unknown" - never the same word for both.
func TestMateSummarySpansDistinguishesAbsentFromUnknown(t *testing.T) {
	p := plainPalette()
	absent := absentMate("this project has no designated Mate")
	unknown := unknownMate("ListMates timed out")
	absentText := renderSpans(workspaceMateSummarySpans(absent, unicodeGlyphs, p), 24)
	unknownText := renderSpans(workspaceMateSummarySpans(unknown, unicodeGlyphs, p), 24)
	if absentText == unknownText {
		t.Fatalf("an absent and an unknown Mate render the same summary: %q", absentText)
	}
	if !strings.Contains(absentText, "none") {
		t.Fatalf("absent Mate summary = %q, want it to say none", absentText)
	}
	if !strings.Contains(unknownText, "unknown") {
		t.Fatalf("unknown Mate summary = %q, want it to say unknown", unknownText)
	}
}
