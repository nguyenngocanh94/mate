package memory

import (
	"strings"
	"testing"
)

func TestBacklogMovesPreserveVerbatimObligationsAndMultilineEvidence(t *testing.T) {
	original := BacklogHeader() + "\n## Notes\nDo not rewrite this.\n"
	got, _, err := EditBacklog(original, "add", "checkout", BacklogHeld, `asked "Giữ cách cũ?" Waits on captain.`)
	if err != nil {
		t.Fatal(err)
	}
	got = strings.Replace(got, `Waits on captain.`, `Waits on captain.`+"\n  evidence: crews/checkout/report.md", 1)
	moved, _, err := EditBacklog(got, "move", "checkout", BacklogInFlight, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(moved, `asked "Giữ cách cũ?" Waits on captain.`+"\n  evidence: crews/checkout/report.md") || !strings.Contains(moved, "## Notes\nDo not rewrite this.") {
		t.Fatal(moved)
	}
	if _, _, err := EditBacklog(moved, "add", "checkout", BacklogQueued, "replacement"); err == nil {
		t.Fatal("duplicate silently replaced obligation")
	}
}

func TestBacklogDoneArchivesOldEntriesWithoutLosingThem(t *testing.T) {
	text := BacklogHeader()
	for _, id := range []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven"} {
		text += "- [" + id + "] resolved\n"
	}
	text, _, _ = EditBacklog(text, "add", "twelve", BacklogInFlight, "ship it")
	next, archive, err := EditBacklog(text, "done", "twelve", BacklogDone, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(archive, "eleven") || !strings.Contains(next, "twelve") {
		t.Fatalf("next=%s archive=%s", next, archive)
	}
}
