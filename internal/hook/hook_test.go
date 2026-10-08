package hook_test

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/hook"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestPromptMarkerMatchesSend keeps hook.PromptMarker (which cannot import
// internal/send without pulling the runtime/harness stack into a package
// that must stay a pure function of a JSON payload) in step with the one
// place that spells the sentinel out for a reason: send.Marker's doc
// comment records the live measurement that picked it.
func TestPromptMarkerMatchesSend(t *testing.T) {
	if hook.PromptMarker != send.Marker {
		t.Fatalf("hook.PromptMarker = %q, want send.Marker %q", hook.PromptMarker, send.Marker)
	}
}

func newWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	return w
}

func readSent(t *testing.T, w *store.Workspace, project string) []store.SentEntry {
	t.Helper()
	entries, _, err := w.ReadSent(project, 0)
	if err != nil {
		t.Fatalf("ReadSent: %v", err)
	}
	return entries
}

func TestHandlePromptTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		autoBefore bool
		wantAuto   bool
		wantSent   []store.SentEntry
		wantErr    bool
	}{
		{
			name:       "plain user prompt, no auto flag",
			payload:    `{"prompt":"ship the checkout fix","session_id":"s1"}`,
			autoBefore: false,
			wantAuto:   false,
			wantSent: []store.SentEntry{
				{Source: store.SourceUser, Target: store.TargetMate, Text: "ship the checkout fix"},
			},
		},
		{
			name:       "plain user prompt leaves auto on",
			payload:    `{"prompt":"never mind, I've got it"}`,
			autoBefore: true,
			wantAuto:   true,
			wantSent: []store.SentEntry{
				{Source: store.SourceUser, Target: store.TargetMate, Text: "never mind, I've got it"},
			},
		},
		{
			name:       "marker-prefixed prompt is recorded as app and never clears auto",
			payload:    "{\"prompt\":\"\u27e6mate\u27e7 signal: crews/k3.status\"}",
			autoBefore: true,
			wantAuto:   true,
			wantSent: []store.SentEntry{
				{Source: store.SourceApp, Target: store.TargetMate, Text: "signal: crews/k3.status"},
			},
		},
		{
			name:       "legacy 0x1f-prefixed prompt is still recognised as app for one release",
			payload:    `{"prompt":"\u001fsignal: crews/k3.status"}`,
			autoBefore: true,
			wantAuto:   true,
			wantSent: []store.SentEntry{
				{Source: store.SourceApp, Target: store.TargetMate, Text: "signal: crews/k3.status"},
			},
		},
		{
			name:       "missing prompt field is an empty user prompt, not an error",
			payload:    `{"session_id":"s1"}`,
			autoBefore: false,
			wantAuto:   false,
			wantSent: []store.SentEntry{
				{Source: store.SourceUser, Target: store.TargetMate, Text: ""},
			},
		},
		{
			name:       "malformed JSON is an error and appends nothing",
			payload:    `{not json`,
			autoBefore: true,
			wantAuto:   true,
			wantSent:   nil,
			wantErr:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorkspace(t)
			if tc.autoBefore {
				if err := w.SetAuto("shop", true); err != nil {
					t.Fatalf("SetAuto: %v", err)
				}
			}
			err := hook.HandlePrompt(w, "shop", []byte(tc.payload))
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error for malformed JSON")
				}
			} else if err != nil {
				t.Fatalf("HandlePrompt: %v", err)
			}
			if got := w.Auto("shop"); got != tc.wantAuto {
				t.Fatalf("Auto = %v, want %v", got, tc.wantAuto)
			}
			got := readSent(t, w, "shop")
			if len(got) != len(tc.wantSent) {
				t.Fatalf("sent.log = %+v, want %+v", got, tc.wantSent)
			}
			for i, want := range tc.wantSent {
				if got[i].Source != want.Source || got[i].Target != want.Target || got[i].Text != want.Text {
					t.Fatalf("entry %d = %+v, want %+v", i, got[i], want)
				}
			}
		})
	}
}

func TestHandlePromptNeverBlocksOnAStoreBoundaryRefusal(t *testing.T) {
	// An invalid project name cannot resolve inside the workspace; the hook
	// must return the store's own error rather than panic or write outside
	// the boundary.
	w := newWorkspace(t)
	if err := hook.HandlePrompt(w, "Not-A-Valid-Name", []byte(`{"prompt":"hi"}`)); err == nil {
		t.Fatal("want an error for an invalid project name")
	}
}

func TestHandleStopAppendsSentAndUpdatesMeta(t *testing.T) {
	w := newWorkspace(t)
	if err := w.WriteMateMeta("shop", map[string]string{
		"harness":    "claude",
		"agent":      "mate-shop",
		"session_id": "old-session",
	}); err != nil {
		t.Fatalf("seed WriteMateMeta: %v", err)
	}

	payload := `{"session_id":"new-session","transcript_path":"/tmp/t.jsonl","hook_event_name":"Stop","last_assistant_message":"done, PR is up"}`
	if err := hook.HandleStop(w, "shop", []byte(payload)); err != nil {
		t.Fatalf("HandleStop: %v", err)
	}

	sent := readSent(t, w, "shop")
	if len(sent) != 1 {
		t.Fatalf("sent.log = %+v, want 1 entry", sent)
	}
	if sent[0].Source != store.SourceMate || sent[0].Target != store.SourceUser || sent[0].Text != "done, PR is up" {
		t.Fatalf("sent entry = %+v", sent[0])
	}

	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatalf("ReadMateMeta: %v", err)
	}
	if meta[spawn.MetaSessionID] != "new-session" {
		t.Fatalf("meta session_id = %q, want new-session", meta[spawn.MetaSessionID])
	}
	if meta[spawn.MetaTranscript] != "/tmp/t.jsonl" {
		t.Fatalf("meta transcript = %q, want /tmp/t.jsonl", meta[spawn.MetaTranscript])
	}
	if meta["harness"] != "claude" || meta["agent"] != "mate-shop" {
		t.Fatalf("meta lost an existing key: %+v", meta)
	}
}

func TestHandleStopFlattensAndTruncatesTheAnswer(t *testing.T) {
	w := newWorkspace(t)
	long := strings.Repeat("a", 2500)
	payload := `{"session_id":"s1","transcript_path":"/tmp/t.jsonl","last_assistant_message":"line one\nline two ` + long + `"}`
	if err := hook.HandleStop(w, "shop", []byte(payload)); err != nil {
		t.Fatalf("HandleStop: %v", err)
	}
	sent := readSent(t, w, "shop")
	if len(sent) != 1 {
		t.Fatalf("sent.log = %+v", sent)
	}
	text := sent[0].Text
	if strings.Contains(text, "\n") {
		t.Fatalf("text carries a newline: %q", text)
	}
	if !strings.HasPrefix(text, "line one line two aaa") {
		t.Fatalf("text = %q, lost its flattened prefix", text)
	}
	if !strings.HasSuffix(text, "…") {
		t.Fatalf("text = %q, want a truncation ellipsis", text)
	}
	runes := []rune(text)
	if len(runes) != 2001 {
		t.Fatalf("truncated text is %d runes, want 2000 plus the ellipsis", len(runes))
	}
}

func TestHandleStopMissingFieldsAreEmptyNotAnError(t *testing.T) {
	w := newWorkspace(t)
	if err := hook.HandleStop(w, "shop", []byte(`{}`)); err != nil {
		t.Fatalf("HandleStop: %v", err)
	}
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaSessionID] != "" || meta[spawn.MetaTranscript] != "" {
		t.Fatalf("meta = %+v, want both keys present and empty", meta)
	}
	sent := readSent(t, w, "shop")
	if len(sent) != 1 || sent[0].Text != "" {
		t.Fatalf("sent = %+v, want one entry with empty text", sent)
	}
}

func TestHandleSessionStartRecordsTheIDOfARecordedMate(t *testing.T) {
	w := newWorkspace(t)
	if err := w.WriteMateMeta("shop", map[string]string{spawn.MetaHarness: "codex", spawn.MetaAgent: "mate-shop", spawn.MetaSessionID: ""}); err != nil {
		t.Fatal(err)
	}
	got, err := hook.HandleSessionStart(w, "shop", []byte(`{"source":"startup","session_id":"019a-rollout","transcript_path":"/codex/rollout.jsonl","cwd":"/m"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != hook.SourceStartup || got.SessionID != "019a-rollout" || !got.Recorded || got.LiveOnly() {
		t.Fatalf("got %+v", got)
	}
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaSessionID] != "019a-rollout" || meta[spawn.MetaTranscript] != "/codex/rollout.jsonl" || meta[spawn.MetaAgent] != "mate-shop" {
		t.Fatalf("meta = %+v", meta)
	}
	// The same id again changes nothing.
	again, err := hook.HandleSessionStart(w, "shop", []byte(`{"source":"resume","session_id":"019a-rollout","transcript_path":"/codex/rollout.jsonl"}`))
	if err != nil || again.Recorded || !again.LiveOnly() {
		t.Fatalf("again = %+v, %v", again, err)
	}
}

// TestHandleSessionStartLeavesAMetaWithNoMateAlone: a first start has not
// recorded a Mate yet, and StartMate writes the same id itself.
func TestHandleSessionStartLeavesAMetaWithNoMateAlone(t *testing.T) {
	w := newWorkspace(t)
	got, err := hook.HandleSessionStart(w, "shop", []byte(`{"source":"startup","session_id":"s1"}`))
	if err != nil || got.Recorded {
		t.Fatalf("got %+v, %v", got, err)
	}
	if meta, _ := w.ReadMateMeta("shop"); len(meta) != 0 {
		t.Fatalf("a meta was written: %+v", meta)
	}
	if _, err := hook.HandleSessionStart(w, "shop", []byte(`{not json`)); err == nil {
		t.Fatal("want an error for malformed JSON")
	}
}

func TestHandleStopMalformedJSONIsAnErrorAndAppendsNothing(t *testing.T) {
	w := newWorkspace(t)
	if err := hook.HandleStop(w, "shop", []byte(`{not json`)); err == nil {
		t.Fatal("want an error for malformed JSON")
	}
	if got := readSent(t, w, "shop"); len(got) != 0 {
		t.Fatalf("sent.log = %+v, want nothing appended on a parse failure", got)
	}
}
