package mateassets

import (
	"regexp"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/brief"
	"github.com/nguyenngocanh94/matev2/internal/facts"
	"github.com/nguyenngocanh94/matev2/internal/memory"
)

// The brief schema has one source of truth, internal/brief. These tests
// hold the manual, the skills and the crew template to it, so a section
// renamed in the check cannot leave the Mate taught the old name.

func renderedManual(t *testing.T) string {
	t.Helper()
	got, err := Render(fixedParams())
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// schemaItem is a section 6 list item that opens with a `## Name`.
var schemaItem = regexp.MustCompile("(?m)^- `## ([^`]+)` - ")

// TestManualSection6SchemaMatchesTheCheck: section 6 teaches exactly the
// sections `brief check` enforces, in the check's order.
func TestManualSection6SchemaMatchesTheCheck(t *testing.T) {
	sec := section(t, renderedManual(t), "## 6. Writing the brief")
	var got []string
	for _, m := range schemaItem.FindAllStringSubmatch(sec, -1) {
		got = append(got, m[1])
	}
	want := brief.AllSections()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("section 6 teaches sections %q, brief check enforces %q", got, want)
	}
	for _, marker := range []string{brief.OutOfScopeMarker, brief.VerifyMarker, brief.UnknownMarker, "decides: captain", "decides: mate", "`" + brief.NoneWord + "`"} {
		if !strings.Contains(sec, marker) {
			t.Errorf("section 6 never teaches the marker %q", marker)
		}
	}
}

// TestManualSection6ExamplePassesTheCheck: the one example brief the manual
// shows is a brief `crew spawn` accepts.
func TestManualSection6ExamplePassesTheCheck(t *testing.T) {
	sec := section(t, renderedManual(t), "## 6. Writing the brief")
	start := strings.Index(sec, "```markdown\n")
	if start < 0 {
		t.Fatal("section 6 has no ```markdown example")
	}
	body := sec[start+len("```markdown\n"):]
	end := strings.Index(body, "\n```\n")
	if end < 0 {
		t.Fatal("section 6's example is not closed")
	}
	example := body[:end]
	if ps := brief.Check(example, brief.Ship); len(ps) != 0 {
		t.Fatalf("section 6's example fails brief check: %v\n%s", ps, example)
	}
	if strings.Contains(example, "ESP32") || strings.Contains(example, "Mua ngay") {
		t.Error("section 6's example is the buyesp32 brief; it should be a neutral one")
	}
}

// TestManualSection4TeachesTheCheckRules: section 4's list of what `brief
// check` checks names every marker the check looks for.
func TestManualSection4TeachesTheCheckRules(t *testing.T) {
	sec := section(t, renderedManual(t), "## 4. The `matev2` command contract")
	for _, want := range []string{"brief check <file> [--scout]", "brief append <project> <crew>", "project facts <project>", "--scout",
		brief.OutOfScopeMarker, brief.VerifyMarker, brief.UnknownMarker, "decides: captain", brief.TaskHeading} {
		if !strings.Contains(sec, want) {
			t.Errorf("section 4 does not document %q", want)
		}
	}
}

// TestManualFactsExampleIsTheRealOutput: the empty-repository example in
// section 4 is what `project facts` prints, byte for byte.
func TestManualFactsExampleIsTheRealOutput(t *testing.T) {
	p := fixedParams()
	want := strings.Join(facts.Facts{Project: p.ProjectName, Repo: p.ProjectRepo, DefaultBranch: p.DefaultBranch}.Lines(), "\n")
	if !strings.Contains(renderedManual(t), "```text\n"+want+"\n```") {
		t.Fatalf("section 4's project facts example is not the real output:\n%s", want)
	}
}

// headingMention is a backquoted `## Name` anywhere in prose.
var headingMention = regexp.MustCompile("`## ([^`]+)`")

// templateHeadings are the `##` headings the crew template itself asks a
// Crew to write, in a hand-back or a report, and the sections of the Mate's
// own files (internal/memory); they are not brief sections.
var templateHeadings = func() map[string]bool {
	m := map[string]bool{"Deviations from Build": true, "Still open": true, "Durable facts": true}
	for _, set := range [][]string{memory.Sections, memory.ProjectSections, memory.BacklogSections} {
		for _, s := range set {
			m[s] = true
		}
	}
	return m
}()

// TestEveryBriefHeadingMentionIsKnown: wherever the manual, a skill or the
// crew template names a `## ` heading, it is one the check enforces or one
// the template defines, so a typo in prose is a test failure rather than a
// Mate writing a section the check will not recognise.
func TestEveryBriefHeadingMentionIsKnown(t *testing.T) {
	known := map[string]bool{}
	for _, s := range brief.AllSections() {
		known[s] = true
	}
	texts := map[string]string{"AGENTS.md": renderedManual(t)}
	for _, name := range SkillNames {
		got, err := RenderSkill(name, fixedParams())
		if err != nil {
			t.Fatal(err)
		}
		texts[name] = string(got)
	}
	for _, p := range []BriefParams{fixedBriefParams(), fixedScoutBriefParams()} {
		got, err := RenderBrief(p)
		if err != nil {
			t.Fatal(err)
		}
		texts["brief"] += string(got)
	}
	for file, text := range texts {
		for _, m := range headingMention.FindAllStringSubmatch(text, -1) {
			if !known[m[1]] && !templateHeadings[m[1]] {
				t.Errorf("%s mentions `## %s`, which is neither a brief section nor a template heading", file, m[1])
			}
		}
	}
}

// TestManualNoLongerParaphrasesTheCaptain: the pre-M7 rule told the Mate to
// write the task "in your own words and not by pasting the captain's
// message"; M7 reverses it.
func TestManualNoLongerParaphrasesTheCaptain(t *testing.T) {
	text := renderedManual(t)
	for _, gone := range []string{"not by pasting", "in your own words", "The template cannot supply that path"} {
		if strings.Contains(text, gone) {
			t.Errorf("the manual still says %q", gone)
		}
	}
	if !strings.Contains(section(t, text, "## 6. Writing the brief"), "verbatim, always") {
		t.Error("section 6 does not say the captain's words are copied verbatim, always")
	}
}
