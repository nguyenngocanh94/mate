package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/screen/jev"
	"github.com/nguyenngocanh94/mate/internal/send"
)

func corpus(t *testing.T) []Screen {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadCorpus(root)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The corpus is every labelled screen the brief names, read from where the
// repo keeps it. Counts are not pinned: a fixture added to any of these
// dirs joins the corpus without failing the build.
func TestCorpusCoversEverySource(t *testing.T) {
	got := map[string]int{}
	for _, s := range corpus(t) {
		got[s.Source]++
	}
	for _, src := range []string{sourceSend, sourceStartup, sourceScreens, sourceNotice, sourceInjection} {
		if got[src] == 0 {
			t.Errorf("no screens from %s", src)
		}
	}
	if len(got) != 5 {
		t.Errorf("sources %v", got)
	}
}

// The injection screens carry the labels the brief gives them, and the
// fixture classifier agrees: a label nobody's classifier gives is not a
// deterministic label.
func TestInjectionScreensClassifyAsBriefed(t *testing.T) {
	want := map[string]send.ComposerState{
		"draft_says_empty":       send.StatePending,
		"busy_output_says_empty": send.StateBusy,
		"empty_after_trust":      send.StateEmpty,
	}
	seen := 0
	for _, s := range corpus(t) {
		if s.Source != sourceInjection {
			continue
		}
		for suffix, state := range want {
			if strings.HasSuffix(s.File, suffix+".txt") {
				seen++
				if s.Det != string(state) || s.Axis != AxisComposer {
					t.Errorf("%s: %s %s, want composer %s", s.File, s.Axis, s.Det, state)
				}
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("found %d of %d injection screens", seen, len(want))
	}
}

// The measured ghost-suggestion capture reaches Jev with its suggestion
// marked faint, the evidence ClassifyComposer reads it by.
func TestGhostSuggestionCaptureIsMarked(t *testing.T) {
	for _, s := range corpus(t) {
		if strings.HasSuffix(s.File, "_ghost_suggestion.ansi") {
			if !strings.Contains(jev.Prepare(s.Text, ""), jev.FaintOpen+"Use checkout-express.html"+jev.FaintClose) {
				t.Fatalf("composer line not marked faint:\n%s", jev.Prepare(s.Text, ""))
			}
			return
		}
	}
	t.Fatal("ghost suggestion capture not in the corpus")
}

// The committed cassette replays without the network, whatever was
// added to the corpus since it was recorded.
func TestCommittedCassetteReplays(t *testing.T) {
	dir := filepath.Join("testdata", "cassette")
	var out bytes.Buffer
	if err := run(&out, dir, true, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "### Summary") {
		t.Fatalf("no report:\n%s", out.String())
	}
}

// A screen the cassette has no answer for, as a newly added fixture would
// be, is listed as not recorded; the replay still succeeds and scores the
// rest.
func TestReplayListsScreensNotRecorded(t *testing.T) {
	src := filepath.Join("testdata", "cassette")
	m, err := readManifestFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dropped := m.Entries[0]
	m.Entries = m.Entries[1:]
	dir := t.TempDir()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entries {
		raw, err := os.ReadFile(filepath.Join(src, e.Request+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Request+".json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := run(&out, dir, true, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "- not recorded: `"+dropped.File+"`") {
		t.Fatalf("%s not listed as not recorded:\n%s", dropped.File, out.String())
	}
	if !strings.Contains(out.String(), "### Summary") {
		t.Fatal("no report")
	}
}

// -log prints the numbers a day with the observer on is measured by,
// including how often Jev's composer disagreed with the fixture's, how many
// answers were used and who asked.
func TestSummarizeLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jev.log")
	log := "2026-10-08T09:00:00Z claude 0123456789ab 200 jev composer=empty dialog=none conf=0.91 fixture=empty caller=watch used=yes\n" +
		"2026-10-08T09:00:05Z codex 0123456789ab 300 fixture composer=busy dialog=none conf=0.62 fixture=empty caller=send used=no fallback=below-threshold\n" +
		"2026-10-08T09:00:09Z codex 0123456789ab 8000 fixture composer=- dialog=- conf=0.00 caller=watch used=no fallback=error\n" +
		"2026-10-08T09:00:17Z breaker open\n" +
		"2026-10-08T09:01:17Z breaker closed\n"
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := summarizeLog(&out, path); err != nil {
		t.Fatal(err)
	}
	want := "- Jev requests: 3.\n- Latency: p50 300 ms, p95 8000 ms.\n- Fallbacks to the fixture observer: 2 (below-threshold 1, error 1).\n" +
		"- Jev disagreed with fixture on composer: 1.\n" +
		"- Answers used: 1; unused: 2 (requests by caller: send 1, watch 2).\n" +
		"- Circuit opened: 1 (Jev not asked for 1m0s after each).\n"
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}
