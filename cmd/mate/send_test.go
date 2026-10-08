package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// codexEmptyScreen and codexBusyScreen are the smallest scripted Codex
// snapshots send.ClassifyComposer recognises, mirroring
// internal/screen/fixture/testdata/screens without depending on that package's
// private fixtures.
const codexEmptyScreen = "› Ask Codex to do anything\n\n  model · cwd\n"

func codexPendingScreen(text string) string {
	return "› " + text + "\n\n  model · cwd\n"
}

const codexBusyScreen = "• Working (2s • esc to interrupt)\n› Ask Codex to do anything\n\n  model · cwd\n"

// TestSendHappyPathAppendsToSentLog is the task 13 send proof over a fake
// runtime: a line typed into an empty composer, one enter, and a busy pane
// afterwards (the harness picking the turn up) reads as "delivered" and
// leaves a record in sent.log.
func TestSendHappyPathAppendsToSentLog(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	if res.Harness != codex.KindCodex {
		t.Fatalf("harness = %q, want codex (the crew default)", res.Harness)
	}
	handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}
	rt.SetReadOutput(handle, codexEmptyScreen)
	rt.OnSendText = func(h runtime.AgentHandle, text string) {
		rt.SetReadOutput(h, codexPendingScreen(text))
	}
	rt.OnSendKeys = func(h runtime.AgentHandle, keys []string) {
		rt.SetReadOutput(h, codexBusyScreen)
	}

	report, err := sendToCrew(context.Background(), w, deps, "shop", "k3", "A", store.SourceMate, send.Options{})
	if err != nil {
		t.Fatalf("sendToCrew: %v", err)
	}
	if report.Before.State != send.StateEmpty {
		t.Fatalf("Before.State = %s, want empty", report.Before.State)
	}
	if report.After.State != send.StateBusy {
		t.Fatalf("After.State = %s, want busy (the harness picked the turn up)", report.After.State)
	}
	if report.Presses != 1 {
		t.Fatalf("Presses = %d, want 1", report.Presses)
	}
	if !report.Delivered() {
		t.Fatalf("report does not read as delivered: %+v", report)
	}
	if got, want := sendSummaryLine(report), "sent to crew-k3 (empty → typed → enter ×1 → working)"; got != want {
		t.Fatalf("sendSummaryLine = %q, want %q", got, want)
	}

	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatalf("ReadSent: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("sent.log has %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].Source != store.SourceMate || entries[0].Target != store.CrewTarget("k3") || entries[0].Text != "A" {
		t.Fatalf("sent.log entry = %+v, want mate->crew:k3 \"A\"", entries[0])
	}
}

// TestSendRefusalAppendsNothingAndFails is the negative half: a busy pane
// refuses the send, sent.log stays empty, and the error is not a usage
// error (mainRun's fallback exits 1 for exactly this shape, the same way
// every other non-usage CLI failure does).
func TestSendRefusalAppendsNothingAndFails(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}
	rt.SetReadOutput(handle, codexBusyScreen)

	_, err := sendToCrew(context.Background(), w, deps, "shop", "k3", "A", store.SourceMate, send.Options{})
	if err == nil {
		t.Fatal("sendToCrew over a busy pane must fail")
	}
	if !errors.Is(err, send.ErrAgentBusy) {
		t.Fatalf("err = %v, want it to wrap send.ErrAgentBusy", err)
	}
	var ue *usageError
	if errors.As(err, &ue) {
		t.Fatalf("a busy-pane refusal is a state conflict, not a usage error: %v", err)
	}

	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatalf("ReadSent: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("sent.log has %d entries, want 0 (nothing appended on refusal)", len(entries))
	}
}

// TestSendRefusesAStoppedCrew covers the "no agent=" refusal named in
// docs/mvp.md task 13: crews/<id>.meta with no agent is a stopped crew, and
// send must refuse before ever touching the runtime.
func TestSendRefusesAStoppedCrew(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	if err := w.WriteCrewMeta("shop", "k9", map[string]string{"harness": "codex", "stopped_at": "2026-09-17T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	_, err := sendToCrew(context.Background(), w, deps, "shop", "k9", "A", store.SourceMate, send.Options{})
	if err == nil {
		t.Fatal("sendToCrew against a stopped crew must fail")
	}
	if !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("err = %v, want it to say the crew is stopped", err)
	}
	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("sent.log has %d entries, want 0", len(entries))
	}
}

func TestResolveSendSource(t *testing.T) {
	t.Setenv("MATE_AGENT_ROLE", "")
	if got, err := resolveSendSource(""); err != nil || got != store.SourceUser {
		t.Fatalf("resolveSendSource(\"\") with no env = %q, %v, want user", got, err)
	}
	t.Setenv("MATE_AGENT_ROLE", "mate")
	if got, err := resolveSendSource(""); err != nil || got != store.SourceMate {
		t.Fatalf("resolveSendSource(\"\") with MATE_AGENT_ROLE=mate = %q, %v, want mate", got, err)
	}
	if got, err := resolveSendSource("user"); err != nil || got != store.SourceUser {
		t.Fatalf("resolveSendSource(user) = %q, %v, want user (explicit --from wins over env)", got, err)
	}
	if _, err := resolveSendSource("bogus"); err == nil {
		t.Fatal("resolveSendSource(bogus) must fail")
	}
}

func TestCrewSendUsageErrors(t *testing.T) {
	var ue *usageError
	if err := cmdSend(nil, nil, new(strings.Builder)); !errors.As(err, &ue) {
		t.Fatalf("cmdSend with no args: err = %v, want *usageError", err)
	}
	if err := cmdSend([]string{"shop"}, nil, new(strings.Builder)); !errors.As(err, &ue) {
		t.Fatalf("cmdSend with 1 arg: err = %v, want *usageError", err)
	}
	if err := cmdSend([]string{"shop", "k3", "hi", "--from", "bogus"}, nil, new(strings.Builder)); !errors.As(err, &ue) {
		t.Fatalf("cmdSend with a bad --from: err = %v, want *usageError", err)
	}
}

// A resumed send starts from the draft state, which the summary has always
// called "pending" (send.StatePending is screen.ComposerDraft underneath).
func TestSendSummaryLineCallsADraftPending(t *testing.T) {
	report := send.Report{
		Agent:   "crew-k3",
		Resumed: true,
		Presses: 1,
		Before:  send.Classification{State: send.StatePending},
		After:   send.Classification{State: send.StateEmpty},
	}
	if got, want := sendSummaryLine(report), "sent to crew-k3 (pending → resumed saved draft → enter ×1 → empty)"; got != want {
		t.Fatalf("sendSummaryLine = %q, want %q", got, want)
	}
}
