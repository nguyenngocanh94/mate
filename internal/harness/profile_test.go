package harness

import (
	"testing"
)

func TestParseEffortKnowsTheFiveLevels(t *testing.T) {
	for _, s := range []string{"low", "medium", "high", "xhigh", "max", " High "} {
		if _, err := ParseEffort(s); err != nil {
			t.Errorf("ParseEffort(%q): %v", s, err)
		}
	}
	for _, s := range []string{"hgih", "ultra", "-c", "low medium"} {
		if _, err := ParseEffort(s); err == nil {
			t.Errorf("ParseEffort(%q) accepted a level no harness has", s)
		}
	}
	if e, err := ParseEffort(""); err != nil || e != "" {
		t.Fatalf("ParseEffort(\"\") = %q, %v; want the harness default", e, err)
	}
}

func TestParseModelRefusesWhatCouldBeAFlag(t *testing.T) {
	for _, s := range []string{"opus", "claude-sonnet-5", "gpt-5.5", "claude-fable-5-1"} {
		if got, err := ParseModel(s); err != nil || got != s {
			t.Errorf("ParseModel(%q) = %q, %v", s, got, err)
		}
	}
	for _, s := range []string{"-m", "--dangerous", "gpt 5", "a\"b", "a=b"} {
		if _, err := ParseModel(s); err == nil {
			t.Errorf("ParseModel(%q) accepted a value that is not one model name", s)
		}
	}
}
