package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// renderSpans is a small test helper: render a span slice through a bare
// line at generous width, so a test can assert on the plain text a column
// builder produced without hard-coding cell offsets.
func renderSpans(spans []span, w int) string {
	return newLine().addSpans(spans...).render(w)
}

// TestAttentionSpansDistinguishesStaleBindingFromLive is N1: a Crew
// attempt's HeldBindingsForAgent-style status alone cannot tell a live agent
// from one ADR 0027 could not confirm stopped, so the Crew row's ATTENTION
// cell must say so - crewAttention (internal/query/attention.go) already
// derives a stale binding as its own Kind, and attentionSpans (list.go) must
// carry it through rather than treating every non-error status alike.
func TestAttentionSpansDistinguishesStaleBindingFromLive(t *testing.T) {
	p := plainPalette()
	live := query.AbsentField[query.Attention]("attempt 2 is recorded running and nothing about it needs attention")
	stale := query.KnownField(query.Attention{Kind: query.AttentionStaleBinding, Why: "attempt 1 is recorded running but its runtime binding is stale"})
	liveText := renderSpans(attentionSpans(live, p), 40)
	staleText := renderSpans(attentionSpans(stale, p), 40)
	if liveText == staleText {
		t.Fatalf("a live and a stale-bound attempt render the same ATTENTION cell: %q", liveText)
	}
	if !strings.Contains(staleText, "stale") {
		t.Fatalf("stale attention cell = %q, want it to say stale", staleText)
	}
}

// noteModel is a Model bare enough to call crewRowNoteSpans: no health port
// wired, so m.crewHealth always returns Absent ("not yet checked") and
// crewHealthWarningSpan always draws nothing - reproducing the pre-G7-04a2
// NOTE cell exactly. PR 92's counter-review (B3) found the previous suite
// routed around production entirely, calling a free function
// (crewNoteSpans) with zero production callers while list.go's own
// taskListItems called m.crewRowNoteSpans; these tests now call the real
// production entry point.
func noteModel() Model { return Model{p: plainPalette(), g: unicodeGlyphs} }

// TestCrewNoteSpansDistinguishesRemovedWorktreeFromLive is N2: a worktree
// the discard path already removed must not render byte-identically to a
// live one, in the one place a reader actually looks - the list, not only
// the inspector. It also covers the health-present case PR 92's
// counter-review added: the same distinction must survive when the Crew
// also carries an open health warning, since B3 moved health to the END of
// the priority list specifically so it could not silently swallow this fact.
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

	// Health-present case (PR 92 counter-review B3): a Crew whose health
	// warning is also open must still distinguish live from removed, and
	// must still show the worktree fact, not just the health one.
	withHealth := noteModel()
	withHealth.healthLastAttempt = goldenAsOf
	sample := query.KnownField(query.CrewHealthValue{Liveness: "identity_mismatch", ObservedAt: goldenAsOf})
	withHealth.health = HealthView{HealthView: query.HealthView{
		AsOf:  goldenAsOf,
		Crews: map[string]query.Field[query.CrewHealthValue]{"c1": sample},
	}}
	liveWithHealth := renderSpans(withHealth.crewRowNoteSpans(live, 60), 60)
	removedWithHealth := renderSpans(withHealth.crewRowNoteSpans(removed, 60), 60)
	if liveWithHealth == removedWithHealth {
		t.Fatalf("with a health warning also open, live and removed still rendered identically: %q", liveWithHealth)
	}
	if !strings.Contains(removedWithHealth, "missing") {
		t.Fatalf("removed+health note = %q, want it to still say missing", removedWithHealth)
	}
	if !strings.Contains(liveWithHealth, "identity mismatch") || !strings.Contains(removedWithHealth, "identity mismatch") {
		t.Fatalf("live=%q removed=%q, want both to carry the health warning too (it is appended, not swapped in)", liveWithHealth, removedWithHealth)
	}
}

// TestCrewRowNoteSpansHealthLastMeansWorkOutcomeSurvivesAtNarrowWidth is
// PR 92 counter-review B3's other half: putting the health warning FIRST in
// the priority list let it evict every work-outcome item ("! failed",
// "worktree missing", "retry of #1") at a realistic 80-column list width,
// which inverts "present health separately from work outcome" into "health
// always wins the one shared cell". Health must be dropped before work
// outcome when both cannot fit, not the other way around.
func TestCrewRowNoteSpansHealthLastMeansWorkOutcomeSurvivesAtNarrowWidth(t *testing.T) {
	m := noteModel()
	m.healthLastAttempt = goldenAsOf
	m.health = HealthView{HealthView: query.HealthView{
		AsOf: goldenAsOf,
		Crews: map[string]query.Field[query.CrewHealthValue]{
			"c1": query.KnownField(query.CrewHealthValue{Liveness: "absent", ObservedAt: goldenAsOf}),
		},
	}}
	c := query.CrewNode{
		CrewID:    "c1",
		Attention: query.KnownField(query.Attention{Kind: query.AttentionFailed, Why: "harness exited 1"}),
	}
	// "! failed" is 8 cells; "runtime missing" (the health word) is 16 - a
	// width that fits the work-outcome item alone but not both.
	got := strings.TrimRight(renderSpans(m.crewRowNoteSpans(c, 10), 10), " ")
	if !strings.Contains(got, "failed") {
		t.Fatalf("note at a width that fits only one item = %q, want the work-outcome item (failed), not the health warning", got)
	}
	if strings.Contains(got, "missing") {
		t.Fatalf("note = %q, want the health warning dropped first at this width, not shown instead of work outcome", got)
	}
	// At a generous width both are shown, in that order.
	full := renderSpans(m.crewRowNoteSpans(c, 60), 60)
	failedAt := strings.Index(full, "failed")
	missingAt := strings.Index(full, "missing")
	if failedAt < 0 || missingAt < 0 || failedAt > missingAt {
		t.Fatalf("full-width note = %q, want work outcome (failed) before the health warning (missing)", full)
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
		RetryOf:   query.KnownField(query.RetryValue{CrewID: "c0", Attempt: 1}),
	}
	full := renderSpans(m.crewRowNoteSpans(c, 40), 40)
	if !strings.Contains(full, "stale") || !strings.Contains(full, "retry of #1") {
		t.Fatalf("full-width note = %q, want both items", full)
	}
	// 8 cells fits "! stale" (7) but not ", retry of #1" alongside it: the
	// retry item must be dropped whole, not cut down to "! stale · re" or
	// similar half-word fragments.
	narrow := strings.TrimRight(renderSpans(m.crewRowNoteSpans(c, 8), 8), " ")
	if narrow != "! stale" {
		t.Fatalf("narrow note = %q, want exactly %q with the retry item dropped whole", narrow, "! stale")
	}
}

// TestCrewNoteSpansStopsAtFirstDroppedItemRatherThanPromotingALaterOne is
// the drop-priority rule's other half: once an item does not fit, later,
// lower-priority items must not be promoted into the space it would have
// used, even when one of them would fit alone. Swapping crewNoteItems'
// `break` for `continue` left TestCrewNoteSpansDropsWholeItemsNotHalfWords
// green (PR 49 counter-review, N5) because that test's own narrow case
// only ever has one item past the cutoff. This one has two: "worktree
// missing" (16 cells - the recorded worktree-status word, not "removed")
// cannot fit in a 12-cell column, so - correctly - nothing is shown, even
// though "retry of #1" (11 cells) would fit by itself.
func TestCrewNoteSpansStopsAtFirstDroppedItemRatherThanPromotingALaterOne(t *testing.T) {
	m := noteModel()
	c := query.CrewNode{
		CrewID:    "c1",
		Attention: query.AbsentField[query.Attention]("recorded running and nothing needs attention"),
		Worktree: query.KnownField(query.WorktreeValue{
			Path: "/repo/.worktrees/c1", Status: query.WorktreeRecordedRemoved,
		}),
		RetryOf: query.KnownField(query.RetryValue{CrewID: "c0", Attempt: 1}),
	}
	if got := spansWidth([]span{{text: "worktree missing"}}); got != 16 {
		t.Fatalf("fixture assumption broken: %q is %d cells, want 16", "worktree missing", got)
	}
	if got := spansWidth([]span{{text: "retry of #1"}}); got != 11 {
		t.Fatalf("fixture assumption broken: %q is %d cells, want 11", "retry of #1", got)
	}
	got := strings.TrimRight(renderSpans(m.crewRowNoteSpans(c, 12), 12), " ")
	if got != "" {
		t.Fatalf("note in a 12-cell column = %q, want empty: the 16-cell first item does not fit, and \"retry of #1\" must not be promoted into its place", got)
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
