package console

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// This file is the inspector pane's ownership of the degraded-Crew
// acceptance surface (Worktree status, Reason) that PR 46 was
// told to strip its own fixtures for rather than ship them incomplete
// (firstmate's seam ruling, 2026-09-10). It covers exactly the two gallery
// states design/mate-console-states.html names "repair-120" and
// "review-120" - a recorded worktree removal on a needs_repair Crew, and an
// Absent Reason/Binding on a Crew whose status is not an error
// state - so the distinction between "recorded as gone" and "could not be
// read" has real teeth rather than only the rendering code's say-so.

// galleryRepairTree is design's "repair-120" state: a Crew still recorded
// working
// whose worktree read succeeded and recorded the worktree removed (not a
// failed read - Worktree.State stays Known), with the durable reason a
// repair precheck records for why cleanup was not run automatically.
func galleryRepairTree() query.Snapshot {
	return oneCrewTree(query.CrewNode{
		CrewID:      "crew_01J9P4Q5R6S7T8U9V0W1X2A7CS",
		Status:      query.CrewWorking,
		HarnessKind: query.HarnessCodex,
		Repo: query.KnownField(query.RepoValue{
			RepoID: "payments-api", DisplayName: "payments-api",
			Path: "repos/payments-api", DefaultBranch: "main",
		}),
		RepoID: "payments-api",
		Worktree: query.KnownField(query.WorktreeValue{
			Path:   "/Users/dev/work/acme/repos/payments-api/.worktrees/crew_01J9P4Q5R6S7T8U9V0W1X2A7CS/a1",
			Branch: "mate/upgrade-database-adapter/a1",
			Status: query.WorktreeRecordedRemoved,
		}),
		AgentName: query.AbsentField[string]("no runtime binding has ever been held for this attempt"),
		Binding:   query.AbsentField[query.BindingValue]("no runtime binding is held for this attempt"),
		LastEvent: query.KnownField(query.EventValue{
			EventType: "crew.worktree_removed", OccurredAt: time.Date(2026, 9, 10, 11, 30, 44, 0, time.UTC),
		}),
		Error: query.KnownField(query.ErrorReason(
			"worktree path missing on disk; branch mate/upgrade-database-adapter/a1 still exists. " +
				"Cleanup not authorized while observed state is uncertain.")),
	})
}

// galleryAbsentTree is design's "review-120" state: a Crew whose recorded
// status is not an error state at all, so Reason, Retry of and Binding are
// each Absent with their own recorded reason - "none", never blank, and
// never the same rendering as a failed read.
func galleryAbsentTree() query.Snapshot {
	return oneCrewTree(query.CrewNode{
		CrewID:      "crew_01J9P8R2S3T4U5V6W7X8Y9Z0AB",
		Status:      query.CrewWaitMate,
		HarnessKind: query.HarnessClaude,
		Repo: query.KnownField(query.RepoValue{
			RepoID: "payments-api", DisplayName: "payments-api",
			Path: "repos/payments-api", DefaultBranch: "main",
		}),
		RepoID: "payments-api",
		Worktree: query.KnownField(query.WorktreeValue{
			Path:   "/Users/dev/work/acme/repos/payments-api/.worktrees/crew_01J9P8R2S3T4U5V6W7X8Y9Z0AB/a1",
			Branch: "crew/task_1/attempt-1",
			Status: query.WorktreeRecordedCreated,
		}),
		AgentName: query.KnownField("crew-payments-api-1"),
		Binding:   query.AbsentField[query.BindingValue]("session ended after completion report"),
		LastEvent: query.KnownField(query.EventValue{
			EventType: "crew.reported_done", OccurredAt: time.Date(2026, 9, 10, 12, 5, 33, 0, time.UTC),
		}),
		Error: query.AbsentField[query.ErrorReason]("the recorded status of this attempt is not an error state"),
	})
}

// TestCrewWorktreeRemovalRendersRecordedDistinctFromAFailedRead
// is B2's first required case: a Known recorded removal must render the
// design's user-facing word "missing", in red - no "r re-reads" hint and no
// Unknown warning, because the read succeeded and this is what it found, not
// a read failure.
func TestCrewWorktreeRemovalRendersRecordedDistinctFromAFailedRead(t *testing.T) {
	m := newFixture(t, galleryRepairTree(), 120, 36, unicodeGlyphs)
	m = intoFirstCrew(t, m)
	l := layout(m.w, m.h)
	fields := fieldValueText(m.inspectorLines(l.Inspector, l.valueWidth(), l.Body, true), l.Inspector)

	status, ok := fields["Worktree status"]
	if !ok {
		t.Fatalf("no Worktree status field rendered; fields = %v", fields)
	}
	if status != "missing" {
		t.Fatalf("Worktree status = %q, want exactly %q for a Known recorded removal", status, "missing")
	}
	if strings.Contains(status, "r re-reads") || strings.Contains(status, "unknown") {
		t.Fatalf("Worktree status = %q, a recorded removal must not carry the Unknown-read hint", status)
	}

	// fieldValueText re-joins a wrapped field's continuation lines with a
	// space (seams_test.go), which is not part of the rendered value itself
	// - the design wraps a path or branch after its own '/' with no space
	// inserted - so the comparison strips spaces rather than asserting on
	// the exact join, while still proving the "/a1" suffix survived whole.
	branch, ok := fields["Branch"]
	if !ok || strings.ReplaceAll(branch, " ", "") != "mate/upgrade-database-adapter/a1" {
		t.Fatalf("Branch = %q, ok=%v, want the recorded branch to survive a removed worktree", branch, ok)
	}
	worktree, ok := fields["Worktree"]
	if !ok || !strings.Contains(strings.ReplaceAll(worktree, " ", ""), "crew_01J9P4Q5R6S7T8U9V0W1X2A7CS/a1") {
		t.Fatalf("Worktree = %q, ok=%v, want the recorded path, including its /a1 suffix, to survive a removed worktree", worktree, ok)
	}

	recorded, ok := fields["Recorded status"]
	if !ok || recorded != string(query.CrewWorking) {
		t.Fatalf("Recorded status = %q, ok=%v, want %q", recorded, ok, query.CrewWorking)
	}

	reason, ok := fields["Reason"]
	if !ok {
		t.Fatalf("no Reason field rendered; fields = %v", fields)
	}
	if !strings.Contains(reason, "Cleanup not authorized") {
		t.Fatalf("Reason = %q, want the durable cleanup-not-authorized reason", reason)
	}
	if strings.Contains(reason, "r re-reads") {
		t.Fatalf("Reason = %q, a Known error reason must not carry the Unknown-read hint", reason)
	}
}

// TestCrewNotAnErrorStateRendersAbsentReasonAndBindingAsNoneWithReason
// is B2's second required case: Reason and Binding are both
// Absent - a fact the read established, not a blank and not the Known-empty
// "(no reason recorded)" sentence, which is a different fact about a
// different status (see TestErrorReasonKnownButEmptySaysSoRatherThanBlank).
func TestCrewNotAnErrorStateRendersAbsentReasonAndBindingAsNoneWithReason(t *testing.T) {
	m := newFixture(t, galleryAbsentTree(), 120, 36, unicodeGlyphs)
	m = intoFirstCrew(t, m)
	l := layout(m.w, m.h)
	fields := fieldValueText(m.inspectorLines(l.Inspector, l.valueWidth(), l.Body, true), l.Inspector)

	reason, ok := fields["Reason"]
	if !ok || !strings.HasPrefix(reason, "none") {
		t.Fatalf("Reason = %q, ok=%v, want it to start with none", reason, ok)
	}
	if !strings.Contains(reason, "not an error state") {
		t.Fatalf("Reason = %q, want the recorded reason this status is not an error state", reason)
	}
	if strings.HasPrefix(reason, "(no reason recorded)") {
		t.Fatalf("Reason = %q, an Absent reason must not render as the Known-empty sentence", reason)
	}

	binding, ok := fields["Binding"]
	if !ok || !strings.HasPrefix(binding, "none") || !strings.Contains(binding, "session ended after completion report") {
		t.Fatalf("Binding = %q, ok=%v, want none plus the recorded reason", binding, ok)
	}
	if _, ok := fields["Runtime"]; ok {
		t.Fatalf("Runtime field rendered for an Absent binding: fields = %v", fields)
	}
}

// TestGoldenFramesInspectorCrewDegradedStates is the golden coverage for
// both gallery states, at both required breakpoints, per the inspector
// task's acceptance criteria.
//
// Accept a deliberate change with:
//
//	go test ./internal/ui/console -run TestGoldenFramesInspectorCrewDegradedStates -update
func TestGoldenFramesInspectorCrewDegradedStates(t *testing.T) {
	for _, size := range []struct{ w, h int }{{160, 48}, {120, 36}} {
		suffix := fmt.Sprintf("%dx%d-unicode", size.w, size.h)

		m := newFixture(t, galleryRepairTree(), size.w, size.h, unicodeGlyphs)
		m = intoFirstCrew(t, m)
		assertGolden(t, "inspector-crew-repair-"+suffix, renderFrame(t, m))

		m2 := newFixture(t, galleryAbsentTree(), size.w, size.h, unicodeGlyphs)
		m2 = intoFirstCrew(t, m2)
		assertGolden(t, "inspector-crew-review-absent-"+suffix, renderFrame(t, m2))
	}
}
