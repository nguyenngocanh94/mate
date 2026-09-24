package brief

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// good is a ship brief with every section in its simplest passing form. Each
// rule's failing case below is this text with exactly one thing broken, so a
// failure names the rule and nothing else.
const good = `## Captain's words
Put the opening hours in the footer: Mon-Fri 9-17.

## What we already know
- The site is static HTML with a shared footer partial (PROJECT.md, "Layout").
- Unknown: whether the footer is cached anywhere.

## Build
- Add the opening hours line to the shared footer.
- Out of scope: restyling the footer, other pages' content.

## Acceptance
- Every page's footer shows "Mon-Fri 9-17". verify: build the site and grep the output for the line; quote the command and its output.

## Open decisions
none
`

func lines(ps []Problem) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

func mustPass(t *testing.T, text string, k Kind) {
	t.Helper()
	if ps := Check(text, k); len(ps) != 0 {
		t.Fatalf("expected a clean %s brief, got:\n%s", k, strings.Join(lines(ps), "\n"))
	}
}

// mustFail asserts Check reports exactly the wanted lines, in order.
func mustFail(t *testing.T, text string, k Kind, want ...string) {
	t.Helper()
	got := lines(Check(text, k))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("problems mismatch\n--- want ---\n%s\n--- got ---\n%s", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func replace(t *testing.T, old, new string) string {
	t.Helper()
	if !strings.Contains(good, old) {
		t.Fatalf("fixture does not contain %q", old)
	}
	return strings.Replace(good, old, new, 1)
}

func TestGoodBriefPasses(t *testing.T) {
	mustPass(t, good, Ship)
	mustPass(t, "# Task\n\n"+good, Ship) // a leading # Task is the Mate's to write or not
}

func TestMissingSection(t *testing.T) {
	text := replace(t, "## Open decisions\nnone\n", "")
	mustFail(t, text, Ship, "## Open decisions: missing; every brief has one")
}

func TestScoutNeedsDeliverable(t *testing.T) {
	mustFail(t, good, Scout, "## Deliverable: missing; a scout brief names here the questions its report must answer")
	mustPass(t, good+"\n## Deliverable\n- Which pages do not use the shared footer?\n", Scout)
}

func TestShipRefusesDeliverable(t *testing.T) {
	mustFail(t, good+"\n## Deliverable\n- a report\n", Ship,
		"## Deliverable: only a scout brief has one; spawn with --scout, or move what it says into ## Build")
}

func TestDuplicateSection(t *testing.T) {
	mustFail(t, good+"\n## Build\n- more\n- Out of scope: nothing else\n", Ship,
		"## Build: appears 2 times; write each section once")
}

func TestEmptySection(t *testing.T) {
	text := replace(t, "## Open decisions\nnone\n", "## Open decisions\n\n")
	mustFail(t, text, Ship, "## Open decisions: empty")
}

func TestPlaceholder(t *testing.T) {
	mustFail(t, replace(t, "Put the opening hours in the footer: Mon-Fri 9-17.", "{TASK}"), Ship,
		"## Captain's words: still holds the template placeholder {TASK}; write the section")
	mustFail(t, replace(t, "- Every page's footer shows \"Mon-Fri 9-17\". verify: build the site and grep the output for the line; quote the command and its output.", "- TBD"), Ship,
		`## Acceptance: holds only the placeholder "TBD"; write the section`)
	// A word that merely appears in real text is not a placeholder.
	mustPass(t, replace(t, "Put the opening hours in the footer: Mon-Fri 9-17.", "Fix the TODO in footer.html: Mon-Fri 9-17."), Ship)
}

func TestCaptainsWordsSpeakerLabel(t *testing.T) {
	for _, label := range []string{
		"Captain: put the opening hours in the footer.",
		"The captain said: put the opening hours in the footer.",
		"**Captain:** put the opening hours in the footer.",
		"> Captain, put the opening hours in the footer.",
		"User: put the opening hours in the footer.",
		"[captain] put the opening hours in the footer.",
		"Người dùng nói: thêm giờ mở cửa vào footer.",
	} {
		t.Run(label, func(t *testing.T) {
			text := replace(t, "Put the opening hours in the footer: Mon-Fri 9-17.", label)
			got := lines(Check(text, Ship))
			if len(got) != 1 || !strings.HasPrefix(got[0], "## Captain's words: line 1 opens with a speaker label") {
				t.Fatalf("want one speaker-label problem, got %q", got)
			}
		})
	}
	// Provenance that is not a speaker label passes: a date line from
	// `brief append`, and a pointer to where earlier words came from.
	mustPass(t, replace(t, "Put the opening hours in the footer: Mon-Fri 9-17.",
		"Put the opening hours in the footer: Mon-Fri 9-17.\n\n(added 2026-09-24 10:00 UTC)\nAlso on the contact page.\n\nEarlier, when commissioning the footer audit (2026-09-20):\nthe footer is a mess"), Ship)
	// A label inside a fenced block the captain pasted is content.
	mustPass(t, replace(t, "Put the opening hours in the footer: Mon-Fri 9-17.",
		"Put the opening hours in the footer, like this log shows:\n```\nUser: hours?\n```"), Ship)
}

func TestAlreadyKnowEndsWithUnknown(t *testing.T) {
	mustFail(t, replace(t, "- Unknown: whether the footer is cached anywhere.\n", ""), Ship,
		`## What we already know: the last line must say what is not known, starting "Unknown:" (got "The site is static HTML with a shared footer partial (PRO...")`)
	mustFail(t, replace(t, "- Unknown: whether the footer is cached anywhere.", "- Unknown:"), Ship,
		`## What we already know: "Unknown:" is empty; say what is not known, or "Unknown: nothing material"`)
}

func TestBuildNeedsOutOfScope(t *testing.T) {
	mustFail(t, replace(t, "- Out of scope: restyling the footer, other pages' content.\n", ""), Ship,
		`## Build: no "Out of scope:" line; name what deliberately stays out of this task`)
	mustFail(t, replace(t, "- Out of scope: restyling the footer, other pages' content.", "- Out of scope:"), Ship,
		`## Build: "Out of scope:" is empty; name what stays out`)
	mustPass(t, replace(t, "- Out of scope: restyling", "**Out of scope:** restyling"), Ship)
}

func TestAcceptanceNeedsVerify(t *testing.T) {
	text := replace(t, "## Open decisions", "- It still looks the same.\n\n## Open decisions")
	mustFail(t, text, Ship,
		`## Acceptance: criterion 2 has no "verify:" ("It still looks the same."); say how the result is checked`)
	mustFail(t, replace(t, "verify: build the site and grep the output for the line; quote the command and its output.", "verify:"), Ship,
		`## Acceptance: criterion 1 has an empty "verify:"`)
	// An indented line continues the criterion above it, so its verify: counts.
	mustPass(t, replace(t, "- Every page's footer shows \"Mon-Fri 9-17\". verify: build the site and grep the output for the line; quote the command and its output.",
		"- Every page's footer shows \"Mon-Fri 9-17\".\n  verify: build the site and grep the output."), Ship)
}

func TestOpenDecisions(t *testing.T) {
	withDecisions := func(body string) string { return replace(t, "## Open decisions\nnone\n", "## Open decisions\n"+body) }
	mustPass(t, withDecisions("None.\n"), Ship)
	mustPass(t, withDecisions("1. Weekend hours are unknown. Options: omit them; show \"closed\". decides: captain\n2. Which footer partial if there are two. decides: mate\n"), Ship)
	mustFail(t, withDecisions("1. Weekend hours are unknown.\n"), Ship,
		`## Open decisions: decision 1 has no "decides: captain" or "decides: mate" ("Weekend hours are unknown.")`)
	mustFail(t, withDecisions("1. Weekend hours. decides: crew\n"), Ship,
		`## Open decisions: decision 1 says "decides: crew"; "decides:" takes captain or mate`)
	mustFail(t, withDecisions("none\n2. Weekend hours. decides: captain\n"), Ship,
		`## Open decisions: "none" must stand alone; with decisions listed, drop it`)
}

func TestOtherTopLevelHeadingRefused(t *testing.T) {
	mustFail(t, good+"\n# Setup\nrun make first\n", Ship,
		"# Setup: a brief fills only # Task's sections; every other top-level section is the template's")
}

func TestStrayTextBeforeFirstSection(t *testing.T) {
	mustFail(t, "Do the footer thing.\n\n"+good, Ship,
		`# Task: text before the first section belongs in one ("Do the footer thing.")`)
}

func TestHeadingSpellingForgiven(t *testing.T) {
	mustPass(t, replace(t, "## Captain's words", "## Captain’s Words:"), Ship)
}

// A captain's own Markdown heading, pasted verbatim, is content of
// ## Captain's words, not a section boundary.
func TestUnknownSubheadingIsContent(t *testing.T) {
	mustPass(t, replace(t, "Put the opening hours in the footer: Mon-Fri 9-17.",
		"## Footer\nPut the opening hours in the footer: Mon-Fri 9-17."), Ship)
}

// TestOldBuyesp32BriefRefused is the incident the schema exists for
// (docs/research/firstmate-prompting-2026-09-24.md section 2): the brief the
// Mate actually wrote on 2026-09-19, one paragraph under # Task.
func TestOldBuyesp32BriefRefused(t *testing.T) {
	text := readTestdata(t, "buyesp32-old-task.md")
	mustFail(t, text, Ship,
		`# Task: text before the first section belongs in one ("Implement the requested ESP32 landing-page purchase flow....")`,
		"## Captain's words: missing; every brief has one",
		"## What we already know: missing; every brief has one",
		"## Build: missing; every brief has one",
		"## Acceptance: missing; every brief has one",
		"## Open decisions: missing; every brief has one",
	)
}

// TestReportRewriteAccepted is the same task written to the schema in the
// report's section 11.
func TestReportRewriteAccepted(t *testing.T) {
	mustPass(t, readTestdata(t, "buyesp32-rewrite-task.md"), Ship)
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const rendered = RoleHeading + `
You are a Crew.

# Task
` + good + `
# How to read the task
## Captain's words is the contract.

# Setup
Run pwd -P.
`

func TestCheckFileOnRenderedBrief(t *testing.T) {
	if !IsRendered(rendered) {
		t.Fatal("a brief opening with the role heading is a rendered brief")
	}
	if ps := CheckFile(rendered, Ship); len(ps) != 0 {
		t.Fatalf("rendered brief with a good task: %q", lines(ps))
	}
	broken := strings.Replace(rendered, "- Out of scope: restyling the footer, other pages' content.\n", "", 1)
	got := lines(CheckFile(broken, Ship))
	if len(got) != 1 || !strings.HasPrefix(got[0], "## Build: no \"Out of scope:\"") {
		t.Fatalf("got %q", got)
	}
	// The Mate's own file never gets the template's sections waved through.
	if ps := CheckFile(good+"\n# Setup\nx\n", Ship); len(ps) != 1 {
		t.Fatalf("got %q", lines(ps))
	}
}

func TestTaskBody(t *testing.T) {
	for in, want := range map[string]string{
		"# Task\n\n## Build\nx\n\n": "## Build\nx",
		"\n\n## Build\nx":           "## Build\nx",
		"# Tasks\n## Build":         "# Tasks\n## Build",
	} {
		if got := TaskBody(in); got != want {
			t.Errorf("TaskBody(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAppendCaptainsWords(t *testing.T) {
	at := time.Date(2026, 9, 24, 10, 5, 0, 0, time.UTC)
	got, err := AppendCaptainsWords(rendered, "Also on the contact page.\n\n", at)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(rendered,
		"Put the opening hours in the footer: Mon-Fri 9-17.\n",
		"Put the opening hours in the footer: Mon-Fri 9-17.\n\n(added 2026-09-24 10:05 UTC)\nAlso on the contact page.\n", 1)
	if got != want {
		t.Fatalf("append mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
	// The result still has the shape, so a relaunch from it would spawn.
	if ps := CheckFile(got, Ship); len(ps) != 0 {
		t.Fatalf("appended brief fails the check: %q", lines(ps))
	}
	// Twice appends twice, in order.
	again, err := AppendCaptainsWords(got, "And Saturdays 10-12.", at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "Also on the contact page.\n\n(added 2026-09-24 11:05 UTC)\nAnd Saturdays 10-12.\n\n## What we already know") {
		t.Fatalf("second append not after the first:\n%s", again)
	}
}

func TestAppendCaptainsWordsRefusals(t *testing.T) {
	at := time.Unix(0, 0).UTC()
	if _, err := AppendCaptainsWords(rendered, "  \n", at); err == nil {
		t.Fatal("empty words accepted")
	}
	noSection := strings.Replace(rendered, "## Captain's words\n", "", 1)
	if _, err := AppendCaptainsWords(noSection, "more", at); err != ErrNoCaptainsWords {
		t.Fatalf("err = %v, want ErrNoCaptainsWords", err)
	}
}
