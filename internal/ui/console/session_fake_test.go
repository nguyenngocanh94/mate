package console

import (
	"context"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

func TestFakeSessionControllerUnseededTargetIsUnknownNotFabricated(t *testing.T) {
	f := NewFakeSessionController()
	target := SessionTarget{Kind: SessionTargetMate, ID: "mate_1", HarnessKind: query.HarnessClaude}

	snap, err := f.Reader()(context.Background(), target)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.RecordedStatus.State != query.Unknown {
		t.Fatalf("RecordedStatus.State = %v, want Unknown", snap.RecordedStatus.State)
	}
	if snap.Runtime.Status != query.Unknown {
		t.Fatalf("Runtime.Status = %v, want Unknown", snap.Runtime.Status)
	}
	if snap.Target != target {
		t.Fatalf("Target = %+v, want %+v", snap.Target, target)
	}
}

func TestFakeSessionControllerSeedRoundTrips(t *testing.T) {
	f := NewFakeSessionController()
	fixed := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	f.Now = func() time.Time { return fixed }

	target := SessionTarget{Kind: SessionTargetCrew, ID: "crew_1", HarnessKind: query.HarnessCodex}
	f.Seed(target, SessionSnapshot{
		RecordedStatus: query.KnownField("running"),
		Runtime:        SessionRuntime{Status: query.Known, ObservedAt: fixed},
		Transcript: SessionTranscript{
			Source:      SessionTranscriptPolled,
			HarnessKind: query.HarnessCodex,
			Status:      SessionTranscriptParsed,
			Entries: []SessionTranscriptEntry{
				{Kind: SessionTranscriptEntryPlain, Text: "hello"},
			},
			Raw: "hello",
		},
		Box: query.KnownField(query.BoxView{Entries: []query.BoxEntry{{
			Kind: query.BoxStatus, Crew: "k3", Source: "crew", Verb: "needs-decision",
			Text: "pick A or B", Attention: true, Signal: query.BoxStatusSignal("/Users/dev/work/acme/.matev2/projects/payments-api/crews/k3.status"),
		}}, Crews: 1, Awaiting: 1}),
	})

	snap, err := f.Reader()(context.Background(), target)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !snap.RecordedStatus.IsKnown() || snap.RecordedStatus.Value != "running" {
		t.Fatalf("RecordedStatus = %+v, want Known running", snap.RecordedStatus)
	}
	if snap.Runtime.Status != query.Known {
		t.Fatalf("Runtime.Status = %v, want Known", snap.Runtime.Status)
	}
	if snap.Transcript.Status != SessionTranscriptParsed || len(snap.Transcript.Entries) != 1 {
		t.Fatalf("Transcript = %+v, want one parsed entry", snap.Transcript)
	}
	if len(snap.Box.Value.Entries) != 1 || !snap.Box.Value.Entries[0].Attention {
		t.Fatalf("Box = %+v, want one attention entry", snap.Box)
	}
	if !snap.AsOf.Equal(fixed) {
		t.Fatalf("AsOf = %v, want %v", snap.AsOf, fixed)
	}
}

func TestFakeSessionControllerRuntimeMissingIsNeverALifecycleValue(t *testing.T) {
	f := NewFakeSessionController()
	target := SessionTarget{Kind: SessionTargetMate, ID: "mate_2"}
	f.Seed(target, SessionSnapshot{
		RecordedStatus: query.KnownField(string(query.MateRunning)),
		Runtime:        SessionRuntime{Status: query.Absent, Reason: "agent_not_found"},
	})

	snap, err := f.Reader()(context.Background(), target)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// The recorded lifecycle stays "running" even though the runtime is
	// missing - a renderer must read the two separately (ADR 0025).
	if snap.RecordedStatus.Value != string(query.MateRunning) {
		t.Fatalf("RecordedStatus.Value = %q, want %q", snap.RecordedStatus.Value, query.MateRunning)
	}
	if snap.Runtime.Status != query.Absent {
		t.Fatalf("Runtime.Status = %v, want Absent (renders runtime_missing)", snap.Runtime.Status)
	}
}

func TestFakeSessionControllerPromptRequiresSeededTarget(t *testing.T) {
	f := NewFakeSessionController()
	target := SessionTarget{Kind: SessionTargetMate, ID: "mate_3"}

	if err := f.PromptFn()(context.Background(), target, "hi"); err == nil {
		t.Fatalf("Prompt on unseeded target: want error, got nil")
	}

	f.Seed(target, SessionSnapshot{})
	if err := f.PromptFn()(context.Background(), target, "hi"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	got := f.Prompts(target)
	if len(got) != 1 || got[0] != "hi" {
		t.Fatalf("Prompts = %v, want [hi]", got)
	}
}

func TestFakeSessionControllerCloseThenReadErrors(t *testing.T) {
	f := NewFakeSessionController()
	target := SessionTarget{Kind: SessionTargetCrew, ID: "crew_2"}
	f.Seed(target, SessionSnapshot{})

	if err := f.Closer()(context.Background(), target); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := f.Reader()(context.Background(), target); err == nil {
		t.Fatalf("Read after Close: want error, got nil")
	}
	if err := f.PromptFn()(context.Background(), target, "hi"); err == nil {
		t.Fatalf("Prompt after Close: want error, got nil")
	}
}

func TestFakeSessionControllerCloseOnUnknownTargetIsANoop(t *testing.T) {
	f := NewFakeSessionController()
	target := SessionTarget{Kind: SessionTargetMate, ID: "mate_never_seeded"}
	if err := f.Closer()(context.Background(), target); err != nil {
		t.Fatalf("Close on never-seen target: %v", err)
	}
}
