package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
// repo keeps it. A fixture added there grows these counts on purpose.
func TestCorpusCountsPerSource(t *testing.T) {
	got := map[string]int{}
	for _, s := range corpus(t) {
		got[s.Source]++
	}
	want := map[string]int{sourceSend: 21, sourceStartup: 31, sourceScreens: 23, sourceNotice: 12, sourceInjection: 3}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s: %d screens, want %d", k, got[k], n)
		}
	}
	if len(got) != len(want) {
		t.Errorf("sources %v, want %v", got, want)
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

// What leaves the machine keeps faint text as markers and nothing else of
// the terminal's attributes, and the key never.
func TestPrepareMarksFaintAndRedactsKey(t *testing.T) {
	ghost := "❯ \x1b[0m\x1b[2mUse checkout-express.html\x1b[0m\n\x1b[38;2;255;255;255mtyped\x1b[0m sk-test123 KEY"
	got := prepare(ghost, "KEY")
	want := "❯ " + faintOpen + "Use checkout-express.html" + faintClose + "\ntyped [redacted] [redacted]"
	if got != want {
		t.Fatalf("prepare = %q, want %q", got, want)
	}
	if plain := "› hello\n  78% context left"; prepare(plain, "") != plain {
		t.Fatalf("a plain screen changed: %q", prepare(plain, ""))
	}
}

// The measured ghost-suggestion capture reaches Jev with its suggestion
// marked faint, the evidence ClassifyComposer reads it by.
func TestGhostSuggestionCaptureIsMarked(t *testing.T) {
	for _, s := range corpus(t) {
		if strings.HasSuffix(s.File, "_ghost_suggestion.ansi") {
			if !strings.Contains(prepare(s.Text, ""), faintOpen+"Use checkout-express.html"+faintClose) {
				t.Fatalf("composer line not marked faint:\n%s", prepare(s.Text, ""))
			}
			return
		}
	}
	t.Fatal("ghost suggestion capture not in the corpus")
}

func TestRequestBodyIsStable(t *testing.T) {
	a, err := requestBody("screen")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := requestBody("screen")
	if !bytes.Equal(a, b) || hashOf(a) != hashOf(b) {
		t.Fatal("same screen, different request")
	}
	if !bytes.Contains(a, []byte(`"model":"jev-1.13.0"`)) {
		t.Fatalf("model not pinned: %s", a)
	}
}

func TestParseRefusesMalformedAnswers(t *testing.T) {
	ok := `{"model":"jev-1.13.0","usage":{"input_tokens":1,"output_tokens":1},"answers":{` +
		`"composer":{"type":"choice","choice":"busy","confidence":0.9,"probabilities":{"empty":0.05,"draft":0.02,"busy":0.9,"none":0.02,"unknown":0.01}},` +
		`"dialog":{"type":"choice","choice":"none","confidence":1,"probabilities":{"none":1,"trust":0,"update":0,"hooks_review":0,"permission":0,"other":0,"unknown":0}},` +
		`"notice":{"type":"choice","choice":"none","confidence":1,"probabilities":{"quota_warning":0,"quota_exhausted":0,"auth_required":0,"permission_required":0,"update_notice":0,"none":1,"unknown":0}}}}`
	r, err := parse([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if r.Answers[AxisComposer].Choice != "busy" || r.Answers[AxisDialog].Choice != "none" {
		t.Fatalf("parsed %+v", r)
	}
	for name, bad := range map[string]string{
		"other model":     strings.Replace(ok, "jev-1.13.0", "jev-latest", 1),
		"label outside":   strings.Replace(ok, `"choice":"busy"`, `"choice":"idle"`, 1),
		"not most likely": strings.Replace(ok, `"choice":"busy"`, `"choice":"empty"`, 1),
		"missing answer":  strings.Replace(ok, `"dialog"`, `"dialogue"`, 1),
	} {
		if _, err := parse([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The committed cassette answers every screen of the current corpus with
// the current questions, so a replay needs no network. A change to the
// corpus or the questions must re-record it.
func TestCommittedCassetteReplays(t *testing.T) {
	dir := filepath.Join("testdata", "cassette")
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal("no committed cassette:", err)
	}
	var out bytes.Buffer
	if err := run(&out, dir, true, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "### Summary") {
		t.Fatalf("no report:\n%s", out.String())
	}
}
