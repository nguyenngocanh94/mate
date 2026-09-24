package mateassets

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/autopilot"
)

// section extracts the text between a heading and the next numbered
// "## N. " heading (or end of file), so a check about section 10 cannot
// accidentally pass because the words it looks for showed up somewhere else
// in the manual. Only numbered headings end a section: since M7 the manual
// quotes brief sections such as "## Build" inside its own examples.
func section(t *testing.T, text, heading string) string {
	t.Helper()
	start := strings.Index(text, heading)
	if start < 0 {
		t.Fatalf("heading %q not found in rendered manual", heading)
	}
	rest := text[start+len(heading):]
	loc := numberedHeading.FindStringIndex(rest)
	if loc == nil {
		return rest
	}
	return rest[:loc[0]]
}

var numberedHeading = regexp.MustCompile(`\n## \d+\. `)

// TestRenderAgentsSection10TeachesDigestGrammar pins section 10's teaching of
// the `digest:` line (mvp.md section 5, internal/autopilot/digest.go) to the
// code that actually produces it, so the two cannot silently drift apart.
//
// internal/autopilot has no exported format-string constant to import: Line
// and itemText build the line with inline fmt.Sprintf verbs
// ("%s %s: %q", "%s blocked: %s, quiet for %s"), not a named template. What
// *is* exported is the three ItemKind values and the two numeric limits
// (MaxItemRunes, MaxItemsListed), so this test asserts those, plus the
// literal connective words ("blocked:", "quiet for", "+N more") that would
// have to be typed identically in both places for the grammar to actually
// match - and says so here rather than leaving the "verbatim" claim
// unverifiable.
func TestRenderAgentsSection10TeachesDigestGrammar(t *testing.T) {
	got, err := Render(fixedParams())
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	sec := section(t, text, "## 10. Two modes and the sentinel")

	// The three item shapes, and only these three (mvp.md section 5): the
	// verbs are the exported ItemKind constants, so a rename in
	// internal/autopilot fails this test at compile time, not at review
	// time.
	for _, verb := range []autopilot.ItemKind{
		autopilot.ItemNeedsDecision,
		autopilot.ItemBlocked,
		autopilot.ItemWaitMate,
	} {
		token := string(verb) + ":"
		if !strings.Contains(sec, token) {
			t.Errorf("section 10 does not teach the digest item verb %q", token)
		}
	}

	// The connective words itemText hardcodes for the blocked shape
	// (`<crew> blocked: <incident kind>, quiet for <duration>`). Not
	// exported, so pinned as a literal per this test's own doc comment.
	if !strings.Contains(sec, "quiet for") {
		t.Error(`section 10 does not teach the "quiet for" wording of a blocked item`)
	}

	// The truncation limit on a quoted crew line is a real exported
	// constant; the manual must cite the same number.
	runesWord := fmt.Sprintf("%d runes", autopilot.MaxItemRunes)
	if !strings.Contains(sec, runesWord) {
		t.Errorf("section 10 does not cite autopilot.MaxItemRunes (%d): want %q in the text", autopilot.MaxItemRunes, runesWord)
	}

	// The "at most N items, then +N more" cap is also a real exported
	// constant.
	itemsWord := fmt.Sprintf("%d items", autopilot.MaxItemsListed)
	if !strings.Contains(sec, itemsWord) {
		t.Errorf("section 10 does not cite autopilot.MaxItemsListed (%d): want %q in the text", autopilot.MaxItemsListed, itemsWord)
	}
	if !strings.Contains(sec, "+N more") {
		t.Error(`section 10 does not teach the "+N more" truncation marker`)
	}

	// <k> is documented as the true count, not the count of what is shown.
	if !strings.Contains(sec, "true number of items") && !strings.Contains(sec, "true count") {
		t.Error("section 10 does not say <k> is the true item count, independent of how many are listed")
	}

	// The order of work the policy prescribes: blocked, then
	// needs-decision, then wait-mate (mvp.md section 5 and this task).
	orderBlocked := strings.Index(sec, "blocked` first")
	orderDecision := strings.Index(sec, "needs-decision` second")
	orderWait := strings.Index(sec, "wait-mate` third")
	if orderBlocked < 0 || orderDecision < 0 || orderWait < 0 {
		t.Fatalf("section 10 does not spell out the fixed blocked/needs-decision/wait-mate order (found at %d, %d, %d)",
			orderBlocked, orderDecision, orderWait)
	}
	if !(orderBlocked < orderDecision && orderDecision < orderWait) {
		t.Errorf("section 10 lists blocked/needs-decision/wait-mate out of order (positions %d, %d, %d)",
			orderBlocked, orderDecision, orderWait)
	}

	// A wedged incident on the crew "mate" never appears in a digest and is
	// the app's own problem, per internal/autopilot/digest.go's MateCrew
	// skip and mvp.md section 5.
	if string(autopilot.MateCrew) != "mate" {
		t.Fatalf("autopilot.MateCrew = %q, want %q; update this test's wording to match", autopilot.MateCrew, "mate")
	}
	if !strings.Contains(sec, "wedged") {
		t.Error("section 10 does not mention the wedged incident on the mate crew")
	}

	// After acting, the Mate stops its turn rather than polling - the
	// opposite of section 9's manual-mode sleep loop.
	if !strings.Contains(sec, "stop your turn") {
		t.Error("section 10 does not tell the Mate to stop its turn after acting on a digest")
	}
}
