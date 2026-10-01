package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// runMateSession runs `mate hook mate-session [args]` from the Mate's cwd
// of the recall fixture, with a payload on stdin.
func runMateSession(t *testing.T, w *store.Workspace, payload string, args ...string) (string, string) {
	t.Helper()
	t.Setenv("MATE_WORKSPACE", w.Root())
	t.Setenv("MATE_PROJECT", "shop")
	chdir(t, w.MateDir("shop"))
	var stdout, stderr bytes.Buffer
	if err := cmdHook(append([]string{harness.SessionHookName}, args...), strings.NewReader(payload), &stdout, &stderr); err != nil {
		t.Fatalf("cmdHook %s: %v", harness.SessionHookName, err)
	}
	return stdout.String(), stderr.String()
}

// TestSessionHookPrintsWhatEachSourceCallsFor is B2's table: startup,
// clear, compact and anything unknown lost the digest (or never had it) and
// get all of it; resume kept its conversation and gets the live state only.
func TestSessionHookPrintsWhatEachSourceCallsFor(t *testing.T) {
	w, _, _ := recallFixture(t)
	for _, tc := range []struct {
		payload  string
		occasion string
		full     bool
	}{
		{`{"source":"startup","session_id":"s1"}`, "session start: startup", true},
		{`{"source":"clear","session_id":"s2"}`, "session start: clear", true},
		{`{"source":"compact","session_id":"s2"}`, "session start: compact", true},
		{`{"source":"resume","session_id":"s2"}`, "session start: resume", false},
		{`{"source":"reload","session_id":"s2"}`, "session start: reload", true},
		{`{"session_id":"s2"}`, "session start: unknown source", true},
		{``, "session start: unknown source", true},
		{`not json`, "session start: unknown source", true},
	} {
		out, errw := runMateSession(t, w, tc.payload)
		head, _, _ := strings.Cut(out, "\n")
		if !strings.HasPrefix(head, "# mate recall shop · ") || !strings.HasSuffix(head, tc.occasion) {
			t.Errorf("%s: first line %q, want the recall title for %q (stderr %q)", tc.payload, head, tc.occasion, errw)
		}
		parts := partHeadings(out)
		if tc.full && (len(parts) != 7 || parts[6] != "== 7. Memory check (mate memory check shop) ==") {
			t.Errorf("%s: parts %q, want the whole digest", tc.payload, parts)
		}
		if !tc.full && (len(parts) != 1 || !strings.Contains(out, "Contract: live state only.")) {
			t.Errorf("%s: parts %q, want part 1 only", tc.payload, parts)
		}
	}
}

// TestSessionHookRecordsTheSessionID closes task 35's /clear gap: the new
// session id and transcript are in mate.meta as soon as the session starts,
// not only after the first turn's Stop hook; every other key is kept.
func TestSessionHookRecordsTheSessionID(t *testing.T) {
	w, _, _ := recallFixture(t)
	if err := w.WriteMateMeta("shop", map[string]string{
		spawn.MetaHarness: "claude", spawn.MetaAgent: "mate-shop", spawn.MetaSessionID: "old", spawn.MetaPane: "p1",
	}); err != nil {
		t.Fatal(err)
	}
	runMateSession(t, w, `{"source":"clear","session_id":"new-id","transcript_path":"/t/new-id.jsonl"}`)
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaSessionID] != "new-id" || meta[spawn.MetaTranscript] != "/t/new-id.jsonl" || meta[spawn.MetaAgent] != "mate-shop" || meta[spawn.MetaPane] != "p1" {
		t.Fatalf("mate.meta = %+v", meta)
	}
}

// TestSessionHookFitsWhatEachHarnessReads: Claude Code keeps only a 2 KB
// preview of hook output over 10,000 characters, so its digest is cut to
// ClaudeSessionHookMaxBytes with part 1 whole and a line saying what to run;
// Codex, with its limit raised, gets the same digest whole.
func TestSessionHookFitsWhatEachHarnessReads(t *testing.T) {
	w, _, _ := recallFixture(t)
	var big strings.Builder
	big.WriteString("# Memory\n\n## Captain\n")
	for big.Len() < 20000 {
		big.WriteString("- The captain prefers answers that name the file and the line first. (captain, 2026-09-20)\n")
	}
	writeFixture(t, w.MemoryFile("shop"), big.String())

	claude, _ := runMateSession(t, w, `{"source":"startup"}`)
	if len(claude) > harness.ClaudeSessionHookMaxBytes {
		t.Fatalf("the Claude digest is %d bytes, over %d", len(claude), harness.ClaudeSessionHookMaxBytes)
	}
	if !strings.Contains(claude, "== 1. Live state ==") || !strings.Contains(claude, "recall cut to fit 9500 bytes; not shown: ") {
		t.Fatalf("the cut Claude digest lost part 1 or its notice:\n%s", claude)
	}
	codex, _ := runMateSession(t, w, `{"source":"startup"}`, "--harness", "codex")
	if strings.Contains(codex, "recall cut to fit") || len(partHeadings(codex)) != 7 {
		t.Fatalf("the Codex digest (%d bytes) was cut", len(codex))
	}
}

func TestSessionHookRefusesAnUnknownHarness(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := cmdHook([]string{harness.SessionHookName, "--harness", "pi-ish"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || stdout.Len() != 0 {
		t.Fatalf("an unknown --harness = %v, stdout %q", err, stdout.String())
	}
}

// codexWithoutHooks is Codex declaring no hooks.
type codexWithoutHooks struct{ harness.Codex }

func (c codexWithoutHooks) Capabilities() harness.Capabilities {
	caps := c.Codex.Capabilities()
	caps.Hooks = harness.Cap[harness.HookInstaller]{Status: harness.CapUnsupported, Reason: "a test harness without hooks"}
	return caps
}

// The digest's size is the harness's to say. A hook run for a harness that
// declares no verified hooks prints no digest of a guessed size: it tells
// the Mate to recall by hand and says why on stderr.
func TestSessionHookAsksTheHarnessForItsDigestSize(t *testing.T) {
	w, _, _ := recallFixture(t)
	reg, err := harness.NewRegistry(nil, codexWithoutHooks{})
	if err != nil {
		t.Fatal(err)
	}
	saved := harnesses
	harnesses = reg
	t.Cleanup(func() { harnesses = saved })
	stdout, stderr := runMateSession(t, w, `{"source":"startup"}`, "--harness", "codex")
	if strings.Contains(stdout, "== 1. Live state ==") || !strings.Contains(stdout, "recall shop") {
		t.Fatalf("stdout = %q, want only the line telling the Mate to run recall", stdout)
	}
	if !strings.Contains(stderr, "no verified Hooks") {
		t.Fatalf("stderr = %q, want the missing capability named", stderr)
	}
}
