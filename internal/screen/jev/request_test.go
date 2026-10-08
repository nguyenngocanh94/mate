package jev

import (
	"bytes"
	"strings"
	"testing"
)

// The product prompt is the measured prompt: these questions are the ones
// docs/evidence/jev-observer-2026-10-08.md ran the corpus with. Changing
// them changes the evidence, and the eval must be run again
// (go run ./scripts/jeveval -cassette <dir>) before this pin moves.
func TestPromptIsTheMeasuredOne(t *testing.T) {
	if got := PromptHash(); got != "6ea1f2de62e5" {
		t.Fatalf("prompt fingerprint %s, want 6ea1f2de62e5: the questions differ from the ones measured", got)
	}
}

// What leaves the machine keeps faint text as markers and nothing else of
// the terminal's attributes, and the key never.
func TestPrepareMarksFaintAndRedactsKey(t *testing.T) {
	ghost := "❯ \x1b[0m\x1b[2mUse checkout-express.html\x1b[0m\n\x1b[38;2;255;255;255mtyped\x1b[0m sk-test123 KEY"
	got := Prepare(ghost, "KEY")
	want := "❯ " + FaintOpen + "Use checkout-express.html" + FaintClose + "\ntyped [redacted] [redacted]"
	if got != want {
		t.Fatalf("Prepare = %q, want %q", got, want)
	}
	if plain := "› hello\n  78% context left"; Prepare(plain, "") != plain {
		t.Fatalf("a plain screen changed: %q", Prepare(plain, ""))
	}
}

func TestRequestBodyIsStable(t *testing.T) {
	a, err := RequestBody("screen")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := RequestBody("screen")
	if !bytes.Equal(a, b) || HashOf(a) != HashOf(b) {
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
	r, err := Parse([]byte(ok))
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
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
