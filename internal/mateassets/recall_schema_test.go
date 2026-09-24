package mateassets

import (
	"regexp"
	"strings"
	"testing"
)

// TestManualBootstrapIsRecall holds section 3 to task 37: a session start
// is the `mate recall` digest, not a list of files to read one by one,
// and the M7 onboarding-scout rule and the after-compaction rule stay in it.
func TestManualBootstrapIsRecall(t *testing.T) {
	p := fixedParams()
	sec := section(t, renderedManual(t), "## 3. Bootstrap checklist")
	for _, want := range []string{
		"`" + p.MatevBin + " recall shop`",
		"SessionStart hook",
		"`# mate recall shop`",
		"`recall cut to fit`",
		"`ABSENT`",
		"propose a short onboarding scout",
		"If you cannot name every In flight crew and every Held question from your context, run `" + p.MatevBin + " recall shop` before you act on anything.",
		"`⟦mate⟧ stow:`",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("section 3 does not say %s", want)
		}
	}
	if regexp.MustCompile("(?m)^\\d+\\. Read `").MatchString(sec) {
		t.Error("section 3 still lists files to read one by one; the digest frames them")
	}
}

// TestManualRecallNamesEightParts: section 4's contract lists the digest's
// parts in the order `mate recall` prints them (cmd/mate/recall_test.go
// holds the command to the same order).
func TestManualRecallNamesEightParts(t *testing.T) {
	sec := section(t, renderedManual(t), "## 4. The `mate` command contract")
	i := strings.Index(sec, "### Recall")
	if i < 0 {
		t.Fatal("section 4 has no ### Recall")
	}
	rec := sec[i:]
	if j := strings.Index(rec[len("### Recall"):], "\n### "); j >= 0 {
		rec = rec[:len("### Recall")+j]
	}
	starts := []string{"Live state", "`mate project facts`", "`/ws/.mate/projects/shop/PROJECT.md`", "`/ws/.mate/projects/shop/mate/backlog.md` without `## Done`", "`/ws/.mate/projects/shop/mate/memory.md`", "`/ws/.mate/WORKSPACE.md`", "What `memory check` says", "Only when the timeline database exists"}
	for n, want := range starts {
		item := regexp.MustCompile("(?m)^" + string(rune('1'+n)) + `\. (.*)$`).FindStringSubmatch(rec)
		if item == nil || !strings.HasPrefix(item[1], want) {
			t.Errorf("part %d of ### Recall = %q, want it to open with %s", n+1, item, want)
		}
	}
	for _, want := range []string{"`--live`", "`--max-bytes <n>`", "part 1 always survives whole"} {
		if !strings.Contains(rec, want) {
			t.Errorf("### Recall does not say %s", want)
		}
	}
}
