package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// bodyLines is the frame's main region on its own, with the six lines of
// chrome (header, breadcrumb, rule, rule, message, keys) stripped off - so a
// test asserting on scroll indicators does not confuse the key line's own
// "↑↓ Scroll" hint for one.
func bodyLines(t *testing.T, frame string) string {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) < chromeRows {
		t.Fatalf("frame has %d lines, want at least %d", len(lines), chromeRows)
	}
	return strings.Join(lines[3:len(lines)-3], "\n")
}

// oneCrewTree is a minimal Workspace -> Project -> Task -> Crew snapshot for
// tests that only care about one Crew's own inspector block.
func oneCrewTree(c query.CrewNode) query.Snapshot {
	return query.Snapshot{
		WorkspaceID: "ws_1",
		Projects: []query.ProjectNode{{
			ProjectID: "proj_1", Name: "acme",
			Mate:  absentMate("this project has no designated Mate"),
			Crews: []query.CrewNode{c},
		}},
	}
}

// intoFirstCrew drills from the Workspace frame down to the first Crew row
// of the first Project: enter the Project (row 0 is the Mate row), then
// move down onto its first Crew.
func intoFirstCrew(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	return m
}

// TestUnknownFieldsHintAtRefresh: the design's own rule for every Unknown
// field in the inspector - amber "unknown", the read's own reason, and a
// pointer at the one key that might fix it - applies across the Crew
// block's Worktree-backed fields (they share one read, so all three carry
// the same Unknown state) and not only to the Mate row F1 first fixed.
func TestUnknownFieldsHintAtRefresh(t *testing.T) {
	const reason = "lookup timed out (2s)"
	tree := oneCrewTree(query.CrewNode{
		CrewID: "crew_1", Status: query.CrewWorking,
		Worktree: query.UnknownField[query.WorktreeValue](reason),
		Error:    query.AbsentField[query.ErrorReason](notErrorState),
	})
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	m = intoFirstCrew(t, m)
	l := layout(m.w, m.h)
	fields := fieldValueText(m.inspectorLines(l.Inspector, l.valueWidth(), l.Body, true), l.Inspector)
	for _, label := range []string{"Branch", "Worktree", "Worktree status"} {
		val, ok := fields[label]
		if !ok {
			t.Fatalf("no %q field rendered; fields = %v", label, fields)
		}
		if !strings.HasPrefix(val, "unknown") {
			t.Fatalf("%q field = %q, want it to say unknown", label, val)
		}
		if !strings.Contains(val, reason) {
			t.Fatalf("%q field = %q, want the read failure's own reason %q", label, val, reason)
		}
		if !strings.Contains(val, "r re-reads") {
			t.Fatalf("%q field = %q, want the r-re-reads hint the design calls for on every Unknown field", label, val)
		}
	}
}

// TODO(task 21): TestRetryOfRendersFirstAttemptAndLinkedAttempt lived
// here. matev2 has no retry: a Crew runs once.

// TestErrorReasonKnownButEmptySaysSoRatherThanBlank is query.ErrorReason's
// own Known-but-empty case: the status is an error state, the event read
// succeeded, and no reason was recorded on it - a fact, not a blank cell
// and not the same sentence as Absent's "not an error state".
func TestErrorReasonKnownButEmptySaysSoRatherThanBlank(t *testing.T) {
	tree := oneCrewTree(query.CrewNode{
		CrewID: "crew_1", Status: query.CrewWorking,
		Error: query.KnownField(query.ErrorReason("")),
	})
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	m = intoFirstCrew(t, m)
	l := layout(m.w, m.h)
	fields := fieldValueText(m.inspectorLines(l.Inspector, l.valueWidth(), l.Body, true), l.Inspector)
	got := fields["Reason"]
	if got == "" {
		t.Fatalf("Reason field is blank, want it to say no reason was recorded")
	}
	if strings.HasPrefix(got, "none") {
		t.Fatalf("Reason = %q, a Known-but-empty reason must not render the same as Absent's none", got)
	}
}

// TestBindingAbsentShowsOneFieldNoRuntimeOrBoundSince: an Absent or Unknown
// binding has nothing to run Runtime or Bound since off, so the block
// renders as a single field rather than blank continuation rows under
// labels that do not apply.
func TestBindingAbsentShowsOneFieldNoRuntimeOrBoundSince(t *testing.T) {
	tree := oneCrewTree(query.CrewNode{
		CrewID: "crew_1", Status: query.CrewWorking,
		Binding: query.AbsentField[query.BindingValue]("session ended when rebase was required"),
		Error:   query.AbsentField[query.ErrorReason](notErrorState),
	})
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	m = intoFirstCrew(t, m)
	l := layout(m.w, m.h)
	fields := fieldValueText(m.inspectorLines(l.Inspector, l.valueWidth(), l.Body, true), l.Inspector)
	binding, ok := fields["Binding"]
	if !ok || !strings.HasPrefix(binding, "none") || !strings.Contains(binding, "session ended when rebase was required") {
		t.Fatalf("Binding = %q, ok=%v, want none plus the reason", binding, ok)
	}
	if _, ok := fields["Runtime"]; ok {
		t.Fatalf("Runtime field rendered for a binding that is not Known: fields = %v", fields)
	}
	if _, ok := fields["Bound since"]; ok {
		t.Fatalf("Bound since field rendered for a binding that is not Known: fields = %v", fields)
	}
}

// TestCrewAttentionRendersKindAndWhyOrNoneWithReason is the design's own
// rule that the ATTENTION column's one word gets its full sentence in the
// inspector (design/mate-console-design-notes.html, "Trong pane").
func TestCrewAttentionRendersKindAndWhyOrNoneWithReason(t *testing.T) {
	needsAttention := query.Snapshot{
		WorkspaceID: "ws_1",
		Projects: []query.ProjectNode{{
			ProjectID: "proj_1", Name: "acme",
			Mate: absentMate("no mate"),
			Crews: []query.CrewNode{{
				CrewID: "crew_1", Task: "Add refund audit trail", Status: query.CrewNeedsDecision,
				Error:     query.AbsentField[query.ErrorReason](notErrorState),
				Attention: query.KnownField(query.Attention{Kind: query.AttentionDecision, Why: "crew crew_1 asked a question and stopped its turn; it waits on an answer"}),
			}},
		}},
	}
	m := newFixture(t, needsAttention, 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter")) // Project
	m, _ = send(t, m, key("down"))  // the Crew row
	l := layout(m.w, m.h)
	fields := fieldValueText(m.inspectorLines(l.Inspector, l.valueWidth(), l.Body, true), l.Inspector)
	got := fields["Attention"]
	if !strings.HasPrefix(got, "needs-decision") || !strings.Contains(got, "waits on an answer") {
		t.Fatalf("Attention = %q, want the kind and the full why sentence", got)
	}

	healthy := query.Snapshot{
		WorkspaceID: "ws_1",
		Projects: []query.ProjectNode{{
			ProjectID: "proj_1", Name: "acme",
			Mate: absentMate("no mate"),
			Crews: []query.CrewNode{{
				CrewID: "crew_1", Task: "Add refund audit trail", Status: query.CrewSpawned,
				Error:     query.AbsentField[query.ErrorReason](notErrorState),
				Attention: query.AbsentField[query.Attention]("no agent has been started and the crew is recorded reserved"),
			}},
		}},
	}
	m2 := newFixture(t, healthy, 120, 36, unicodeGlyphs)
	m2, _ = send(t, m2, key("enter"))
	m2, _ = send(t, m2, key("down"))
	fields2 := fieldValueText(m2.inspectorLines(l.Inspector, l.valueWidth(), l.Body, true), l.Inspector)
	got2 := fields2["Attention"]
	if !strings.HasPrefix(got2, "none") || !strings.Contains(got2, "recorded reserved") {
		t.Fatalf("Attention = %q, want none plus the recorded reason", got2)
	}
}

// TestMateInspectorTitleNamesItsProject: the design's Mate block is titled
// "MATE  <project name>" (design/mate-console-states.html, project-120),
// not the bare "MATE" a reader would not be able to place among several
// Projects each with their own Mate row.
func TestMateInspectorTitleNamesItsProject(t *testing.T) {
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter")) // payments-api; its Mate row is selected by default
	l := layout(m.w, m.h)
	title := m.inspectorLines(l.Inspector, l.valueWidth(), l.Body, true)[0].render(l.Inspector)
	if !strings.Contains(title, "MATE") || !strings.Contains(title, "payments-api") {
		t.Fatalf("inspector title = %q, want MATE and the project name", title)
	}
}

// TestInspectorScrollIndicatorsAtBothEndsAndInTheMiddle: no upward
// indicator at the top, a downward one once content overflows the pane, both
// together once scrolled into the middle, and only the upward one once
// scrolled past the end - with the last field still on screen.
func TestInspectorScrollIndicatorsAtBothEndsAndInTheMiddle(t *testing.T) {
	m := newFixture(t, sampleTree(), 80, 24, unicodeGlyphs)
	m, _ = send(t, m, key("enter")) // Project
	m, _ = send(t, m, key("down"))  // its active Crew
	m, _ = send(t, m, key("tab"))   // Detail: 80 cols has no inspector column

	body := bodyLines(t, renderFrame(t, m))
	if strings.Contains(body, unicodeGlyphs.Up) {
		t.Fatalf("at the top, want no upward indicator:\n%s", body)
	}
	if !strings.Contains(body, unicodeGlyphs.Down+" ") {
		t.Fatalf("long content at the top, want a downward indicator:\n%s", body)
	}

	for i := 0; i < 5; i++ {
		m, _ = send(t, m, key("down"))
	}
	body = bodyLines(t, renderFrame(t, m))
	if !strings.Contains(body, unicodeGlyphs.Up+" ") || !strings.Contains(body, unicodeGlyphs.Down+" ") {
		t.Fatalf("scrolled to the middle, want both indicators:\n%s", body)
	}

	for i := 0; i < 60; i++ {
		m, _ = send(t, m, key("down"))
	}
	body = bodyLines(t, renderFrame(t, m))
	if strings.Contains(body, unicodeGlyphs.Down+" ") {
		t.Fatalf("scrolled past the end, want no downward indicator:\n%s", body)
	}
	if !strings.Contains(body, unicodeGlyphs.Up+" ") {
		t.Fatalf("scrolled past the end, want the upward indicator:\n%s", body)
	}
	if !strings.Contains(body, "Reason") {
		t.Fatalf("scrolled past the end, want the last field (Reason) on screen:\n%s", body)
	}
}

// TestUnknownFieldWithNoReasonNeverDoublesTheDot regresses a bug this task
// introduced and fixed before it shipped: attentionFieldSpans, retryOfSpans
// and errorReasonSpans each built their Absent/Unknown text as one
// concatenated string, so a Field whose Reason happened to be empty
// rendered "unknown ·  · r re-reads" - a bare " · " where the reason should
// have been. reasonSpan/rereadsSpan (seams.go) fix this by treating the
// reason and the re-reads hint as independently-guarded spans, mirroring
// availabilitySpans' own guard. This is not a state the store-backed loader ever
// produces (every Unknown field it builds carries a reason), but the
// rendering code must not assume that - an empty reason is still legal
// input to a Field[T].
func TestUnknownFieldWithNoReasonNeverDoublesTheDot(t *testing.T) {
	empty := ""
	got := attentionFieldSpans(query.UnknownField[query.Attention](empty), unicodeGlyphs, plainPalette())
	text := ""
	for _, s := range got {
		text += s.text
	}
	if strings.Contains(text, "·  ·") || strings.Contains(text, "·  ") {
		t.Fatalf("attentionFieldSpans with an empty reason = %q, want no bare dot for the missing reason", text)
	}
	if text != "unknown "+unicodeGlyphs.Dot+" r re-reads" {
		t.Fatalf("attentionFieldSpans with an empty reason = %q, want exactly %q", text, "unknown "+unicodeGlyphs.Dot+" r re-reads")
	}
}
