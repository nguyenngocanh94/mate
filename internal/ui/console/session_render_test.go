package console

// ADR 0025 step 3 replaces session_view_fixture_test.go's buildSessionFrame
// (a fixture-only builder that never shipped as production code) with the
// real renderer in session_render.go. This file proves that renderer
// against a SessionSnapshot delivered through FakeSessionController - never
// against hardcoded fixture text - and pins it to the same goldens step 1
// froze: session-view-contract.md is still the authority, and every fixture
// here is byte-for-byte the file step 1 committed.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

const (
	sessionTestMateAgent   = "mate-payments-api"
	sessionTestCrewAgent   = "crew-payments-api-2"
	sessionTestCrewWorkdir = ".worktrees/crew_01J9P6Q6W0E5V8XK2M4B8DT/a2"
)

func sessionTestTarget(kind SessionTargetKind) SessionTarget {
	switch kind {
	case SessionTargetMate:
		return SessionTarget{Kind: SessionTargetMate, ID: "mate_01J9M2G9N3X8D5J0B4H7V2T1WK", ProjectID: "proj_1",
			HarnessKind: query.HarnessClaude, AgentName: sessionTestMateAgent, Mode: query.ModeSupervised}
	default:
		return SessionTarget{Kind: SessionTargetCrew, ID: "crew_01J9P6Q6W0E5V8XK2M4B8DT", ProjectID: "proj_1",
			HarnessKind: query.HarnessClaude, AgentName: sessionTestCrewAgent, Worktree: sessionTestCrewWorkdir,
			Mode: query.ModeSupervised}
	}
}

func sessionTestClock(h, m int) time.Time {
	return time.Date(2026, 9, 10, h, m, 0, 0, time.UTC)
}

// sessionTestBox is task 15's pinned rail fixture, and the one mvp.md's
// task row asks for: two crew status lines - one of them the needs-decision
// that has to be visibly highlighted - and one message, which is the entry
// Enter must refuse to forward. Oldest first, the order the rail draws.
func sessionTestBox() query.Field[query.BoxView] {
	return query.KnownField(query.BoxView{
		Entries: []query.BoxEntry{
			{
				Seq: 0, At: sessionTestClock(13, 41), Kind: query.BoxStatus,
				Source: "crew", Target: "crew:k3", Crew: "k3",
				Verb: "working", Text: "reading the ticket",
				Signal: query.BoxStatusSignal("/Users/dev/work/acme/.matev2/projects/payments-api/crews/k3.status"),
			},
			{
				Seq: 1, At: sessionTestClock(13, 52), Kind: query.BoxMessage,
				Source: "user", Target: "mate",
				Text: "spawn a crew for the webhook fix",
			},
			{
				Seq: 2, At: sessionTestClock(14, 1), Kind: query.BoxStatus,
				Source: "crew", Target: "crew:k3", Crew: "k3",
				Verb: "needs-decision", Text: "migration for idempotency_keys, or key off stripe_events?",
				Attention: true, Signal: query.BoxStatusSignal("/Users/dev/work/acme/.matev2/projects/payments-api/crews/k3.status"),
			},
		},
		Crews: 1, Awaiting: 1, LastAt: sessionTestClock(14, 1),
	})
}

// sessionTestMateTranscript is the Mate sample transcript: attempt 2
// reviewing attempt 1's failure report, replying to a Crew question, then
// narrating that one question is still unanswered while waiting on the
// Crew's completion report. Built per glyph set because its content embeds
// g.Dot literally (real harness output, not something the renderer inserts).
func sessionTestMateTranscript(g glyphSet) SessionTranscript {
	entries := []SessionTranscriptEntry{
		{Kind: SessionTranscriptEntryTurn, Text: `Attempt 1 of "Fix webhook idempotency" failed on 3 tests in internal/webhook. Reading its report before reviewing attempt 2.`},
		{Kind: SessionTranscriptEntryGap},
		{Kind: SessionTranscriptEntryTurn, Text: "Read(.matev2/reports/crew-01j9p4p5v9d4.json)"},
		{Kind: SessionTranscriptEntryResult, Text: "Read 84 lines (ctrl+o to expand)"},
		{Kind: SessionTranscriptEntryGap},
		{Kind: SessionTranscriptEntryTurn, Text: fmt.Sprintf("Reply(a1 %s Upgrade database adapter)", g.Dot)},
		{Kind: SessionTranscriptEntryResult, Text: `"Keep the pool wrapper in this attempt; dropping it is a separate task."`},
		{Kind: SessionTranscriptEntryGap},
		{Kind: SessionTranscriptEntryTurn, Emphasis: true,
			Text: `1 crew question still needs an answer. Oldest first: the rebase conflict on "Upgrade database adapter".`},
		{Kind: SessionTranscriptEntryGap},
		{Kind: SessionTranscriptEntryStatus, Text: "Waiting for the completion report from " + sessionTestCrewAgent, Hint: " (esc to interrupt)"},
	}
	return SessionTranscript{Source: SessionTranscriptPolled, HarnessKind: query.HarnessClaude, Status: SessionTranscriptParsed, Entries: entries}
}

// sessionTestCrewTranscript is the Crew sample transcript: narrating the
// plan, three tool calls with their results (one of them a still-pending
// Ask to the Mate), then a running test command.
func sessionTestCrewTranscript(g glyphSet) SessionTranscript {
	entries := []SessionTranscriptEntry{
		{Kind: SessionTranscriptEntryTurn, Text: "Making the Stripe webhook handler replay-safe: an idempotency-key store keyed by event id, plus tests for a duplicate delivery."},
		{Kind: SessionTranscriptEntryGap},
		{Kind: SessionTranscriptEntryTurn, Text: "Read(internal/webhook/handler.go)"},
		{Kind: SessionTranscriptEntryResult, Text: "Read 212 lines (ctrl+o to expand)"},
		{Kind: SessionTranscriptEntryGap},
		{Kind: SessionTranscriptEntryTurn, Text: "Write(internal/webhook/idempotency.go)"},
		{Kind: SessionTranscriptEntryResult, Text: "Wrote 74 lines"},
		{Kind: SessionTranscriptEntryGap},
		{Kind: SessionTranscriptEntryTurn, Text: fmt.Sprintf("Ask(mate %s migration for idempotency_keys, or key off stripe_events?)", g.Dot)},
		{Kind: SessionTranscriptEntryResult, Text: "Waiting for the Mate to answer"},
		{Kind: SessionTranscriptEntryGap},
		{Kind: SessionTranscriptEntryTurn, Text: "Bash(go test ./internal/webhook/...)"},
		{Kind: SessionTranscriptEntryResult, Text: "ok  payments-api/internal/webhook  0.412s"},
		{Kind: SessionTranscriptEntryGap},
		{Kind: SessionTranscriptEntryStatus, Text: "Running go test ./...", Hint: " (esc to interrupt)"},
	}
	return SessionTranscript{Source: SessionTranscriptPolled, HarnessKind: query.HarnessClaude, Status: SessionTranscriptParsed, Entries: entries}
}

// sessionTestSnapshot assembles the full SessionSnapshot the golden fixtures
// were built from: a healthy runtime and a Known recorded status, so
// neither the header nor the pane draws the anomaly text those states add
// (session_render.go only appends it for a non-Known read) - exactly what
// the pinned fixtures show.
func sessionTestSnapshot(kind SessionTargetKind, g glyphSet) SessionSnapshot {
	target := sessionTestTarget(kind)
	transcript := sessionTestCrewTranscript(g)
	if kind == SessionTargetMate {
		transcript = sessionTestMateTranscript(g)
	}
	return SessionSnapshot{
		Target:         target,
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known, ObservedAt: sessionTestClock(14, 2)},
		Transcript:     transcript,
		Box:            sessionTestBox(),
		AsOf:           sessionTestClock(14, 2),
	}
}

// sessionTestReader builds a SessionReader backed by a FakeSessionController
// seeded with the golden-equivalent snapshot - the port this renderer is
// actually exercised through, per ADR 0025 step 2.
func sessionTestReader(t *testing.T, kind SessionTargetKind, g glyphSet) (SessionReader, SessionTarget) {
	t.Helper()
	target := sessionTestTarget(kind)
	fc := NewFakeSessionController()
	fc.Now = func() time.Time { return sessionTestClock(14, 2) }
	fc.Seed(target, sessionTestSnapshot(kind, g))
	return fc.Reader(), target
}

func TestSessionViewGoldens(t *testing.T) {
	sizes := []struct{ w, h int }{{160, 48}, {120, 36}, {80, 24}}
	for _, kind := range []SessionTargetKind{SessionTargetMate, SessionTargetCrew} {
		for _, sz := range sizes {
			for _, gs := range []glyphSet{unicodeGlyphs, asciiGlyphs} {
				if gs.Name == "ascii" && sz.w != 80 {
					continue // ASCII fixtures pinned at one width, matching the existing convention.
				}
				name := fmt.Sprintf("session-%s-%dx%d-%s", kind, sz.w, sz.h, gs.Name)
				t.Run(name, func(t *testing.T) {
					reader, target := sessionTestReader(t, kind, gs)
					snap, err := reader(context.Background(), target)
					if err != nil {
						t.Fatalf("Read: %v", err)
					}
					got := RenderSessionFrame(snap, "", boxRail{sel: -1}, sz.w, sz.h, gs, plainPalette())
					assertFrameShape(t, got, sz.w, sz.h)
					assertGolden(t, name, got)
				})
			}
		}
	}
}

// ---------- behaviour not exercised by the pinned goldens ----------
// The goldens above script a healthy Known runtime and a Parsed transcript,
// per session-view-contract.md. The rules in ADR 0025 about Absent/Unknown
// runtime and an unparseable transcript still apply and are not optional
// just because no fixture exercises them - so they get their own tests.

func TestSessionRuntimeMissingRendersRuntimeMissingNotLifecycle(t *testing.T) {
	fc := NewFakeSessionController()
	target := sessionTestTarget(SessionTargetMate)
	snap := sessionTestSnapshot(SessionTargetMate, unicodeGlyphs)
	snap.Runtime = SessionRuntime{Status: query.Absent, Reason: "agent_not_found"}
	fc.Seed(target, snap)

	got, err := fc.Reader()(context.Background(), target)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	frame := RenderSessionFrame(got, "", boxRail{sel: -1}, 160, 48, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 160, 48)

	banner := frameLine(frame, 2) // first pane line, right of the rail split rule
	if !strings.Contains(banner, "runtime_missing") {
		t.Fatalf("banner does not show runtime_missing for an Absent runtime: %q", banner)
	}
	for _, forbidden := range []string{"stopped", "needs_repair", "failed"} {
		if strings.Contains(banner, forbidden) {
			t.Fatalf("banner must not upgrade an Absent runtime into %q: %q", forbidden, banner)
		}
	}
}

func frameLine(frame string, i int) string {
	lines := strings.Split(frame, "\n")
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i]
}

func TestSessionRuntimeUnknownRendersUnknownNotAbsent(t *testing.T) {
	fc := NewFakeSessionController()
	target := sessionTestTarget(SessionTargetCrew)
	snap := sessionTestSnapshot(SessionTargetCrew, unicodeGlyphs)
	snap.Runtime = SessionRuntime{Status: query.Unknown, Reason: "poll timed out"}
	fc.Seed(target, snap)

	got, err := fc.Reader()(context.Background(), target)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	frame := RenderSessionFrame(got, "", boxRail{sel: -1}, 120, 36, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 120, 36)
	if !containsLine(frame, "unknown") {
		t.Fatalf("frame does not show unknown for a failed poll:\n%s", frame)
	}
	if containsLine(frame, "runtime_missing") {
		t.Fatalf("an Unknown runtime must not render as runtime_missing (that is Absent only):\n%s", frame)
	}
}

func TestSessionTranscriptUnknownRendersRawBoundedText(t *testing.T) {
	fc := NewFakeSessionController()
	target := sessionTestTarget(SessionTargetCrew)
	snap := sessionTestSnapshot(SessionTargetCrew, unicodeGlyphs)
	snap.Transcript = SessionTranscript{
		Source: SessionTranscriptPolled, HarnessKind: query.HarnessCodex,
		Status: SessionTranscriptUnknown, Raw: "some codex output\nsecond line of it",
	}
	fc.Seed(target, snap)

	got, err := fc.Reader()(context.Background(), target)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	frame := RenderSessionFrame(got, "", boxRail{sel: -1}, 120, 36, unicodeGlyphs, plainPalette())
	assertFrameShape(t, frame, 120, 36)
	if !containsLine(frame, "unknown") {
		t.Fatalf("an unparsed transcript must render the unknown marker:\n%s", frame)
	}
	if !containsLine(frame, "some codex output") {
		t.Fatalf("an unparsed transcript must still show Raw as plain bounded text:\n%s", frame)
	}
}

// TestSessionRecordedStatusUnknownIsDistinctFromAbsent pins that a
// RecordedStatus read failure is visible and worded differently from an
// Absent one, following query's own Known/Absent/Unknown convention.
// TestSessionTranscriptCapacity pins the (kind, w, h) -> capacity table
// against RenderSessionFrame's own layout arithmetic (header, rail/digest
// split, the composer's fixed 4-line chrome), so a change to any of those
// layout constants shows up here as a deliberate diff rather than only as a
// latency regression discovered from a captain's complaint (this figure
// bounds the ReadAgent request in cmd/matev2/session_bridge.go).
func TestSessionTranscriptCapacity(t *testing.T) {
	cases := []struct {
		name string
		kind SessionTargetKind
		w, h int
		want int
	}{
		{"mate rail 160x48", SessionTargetMate, 160, 48, 41},
		{"mate rail boundary 140x48", SessionTargetMate, 140, 48, 41},
		{"mate digest just below rail boundary 99x48", SessionTargetMate, 99, 48, 40},
		{"mate digest narrow 80x24", SessionTargetMate, 80, 24, 16},
		{"crew 160x48", SessionTargetCrew, 160, 48, 41},
		{"crew narrow 80x24", SessionTargetCrew, 80, 24, 17},
		{"tiny window never goes negative", SessionTargetMate, 20, 3, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SessionTranscriptCapacity(tc.kind, tc.w, tc.h)
			if got != tc.want {
				t.Fatalf("SessionTranscriptCapacity(%v, %d, %d) = %d, want %d", tc.kind, tc.w, tc.h, got, tc.want)
			}
			if got < 0 {
				t.Fatalf("capacity must never be negative, got %d", got)
			}
		})
	}
}

func TestStreamTerminalSizeReservesBannerLines(t *testing.T) {
	withoutBanner := streamTerminalSize(SessionTargetCrew, 80, 24, 0, 0)
	withBanner := streamTerminalSize(SessionTargetCrew, 80, 24, 1, 0)
	withTwoBanners := streamTerminalSize(SessionTargetCrew, 80, 24, 2, 0)

	if withBanner.Rows != withoutBanner.Rows-1 {
		t.Fatalf("one banner rows = %d, want %d", withBanner.Rows, withoutBanner.Rows-1)
	}
	if withTwoBanners.Rows != withoutBanner.Rows-2 {
		t.Fatalf("two banner rows = %d, want %d", withTwoBanners.Rows, withoutBanner.Rows-2)
	}
	if tiny := streamTerminalSize(SessionTargetCrew, 20, 3, 2, 0); tiny.Rows != 1 {
		t.Fatalf("tiny stream rows = %d, want clamped 1", tiny.Rows)
	}
}

// TestStreamTranscriptCapacity pins StreamTranscriptCapacity against the
// same (kind, w, h) table TestSessionTranscriptCapacity uses, so the two
// functions' shared sessionFrameOverhead cannot silently drift back
// together (a counter-review found no test distinguished them at all - the
// exact regression, reverting session_mode.go's streamTerminalSize back to
// SessionTranscriptCapacity, passed the whole suite). Every value here is
// SessionTranscriptCapacity's own pinned value plus 3: stream mode reserves
// only streamDetachHintHeight (1 line) where snapshot mode reserves the
// composer box's 4.
func TestStreamTranscriptCapacity(t *testing.T) {
	cases := []struct {
		name string
		kind SessionTargetKind
		w, h int
		want int
	}{
		{"mate rail 160x48", SessionTargetMate, 160, 48, 45},
		{"mate rail boundary 140x48", SessionTargetMate, 140, 48, 45},
		{"mate digest just below rail boundary 99x48", SessionTargetMate, 99, 48, 44},
		{"mate digest narrow 80x24", SessionTargetMate, 80, 24, 20},
		{"crew 160x48", SessionTargetCrew, 160, 48, 45},
		{"crew narrow 80x24", SessionTargetCrew, 80, 24, 21},
		{"tiny window never goes negative", SessionTargetMate, 20, 3, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StreamTranscriptCapacity(tc.kind, tc.w, tc.h)
			if got != tc.want {
				t.Fatalf("StreamTranscriptCapacity(%v, %d, %d) = %d, want %d", tc.kind, tc.w, tc.h, got, tc.want)
			}
			if want := SessionTranscriptCapacity(tc.kind, tc.w, tc.h) + 4; got != want && tc.want != 0 {
				t.Fatalf("StreamTranscriptCapacity(%v, %d, %d) = %d, want SessionTranscriptCapacity+4 = %d", tc.kind, tc.w, tc.h, got, want)
			}
		})
	}
}

// TestStreamTerminalSizeUsesStreamCapacityNotSessionCapacity is the direct
// regression for the mutation a counter-review found no test caught:
// reverting streamTerminalSize (session_mode.go) to call
// SessionTranscriptCapacity instead of StreamTranscriptCapacity - sizing
// the PTY 3 rows shorter than the frame actually gives it - must fail this
// test.
func TestStreamTerminalSizeUsesStreamCapacityNotSessionCapacity(t *testing.T) {
	size := streamTerminalSize(SessionTargetCrew, 160, 48, 0, 0)
	want := StreamTranscriptCapacity(SessionTargetCrew, 160, 48)
	if size.Rows != want {
		t.Fatalf("streamTerminalSize rows = %d, want StreamTranscriptCapacity's %d (not SessionTranscriptCapacity's %d)",
			size.Rows, want, SessionTranscriptCapacity(SessionTargetCrew, 160, 48))
	}
}

func TestSessionRecordedStatusUnknownIsDistinctFromAbsent(t *testing.T) {
	fc := NewFakeSessionController()
	target := sessionTestTarget(SessionTargetMate)
	snap := sessionTestSnapshot(SessionTargetMate, unicodeGlyphs)
	snap.RecordedStatus = query.UnknownField[string]("lookup timed out")
	fc.Seed(target, snap)

	got, err := fc.Reader()(context.Background(), target)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	frame := RenderSessionFrame(got, "", boxRail{sel: -1}, 160, 48, unicodeGlyphs, plainPalette())
	if !containsLine(frame, "unknown") || !containsLine(frame, "lookup timed out") {
		t.Fatalf("header must surface the RecordedStatus read failure and its reason:\n%s", frame)
	}
}

func containsLine(frame, needle string) bool {
	return strings.Contains(frame, needle)
}
