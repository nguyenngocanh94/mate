package mateassets

import (
	"regexp"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/facts"
	"github.com/nguyenngocanh94/mate/internal/memory"
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
	sec := section(t, renderedManual(t), "## 4. The `mate` command contract")
	for _, want := range []string{"brief check <file> [--scout]", "brief append <project> <crew>", "project facts <project>", "--scout",
		brief.OutOfScopeMarker, brief.VerifyMarker, brief.UnknownMarker, "decides: captain", brief.TaskHeading} {
		if !strings.Contains(sec, want) {
			t.Errorf("section 4 does not document %q", want)
		}
	}
}

// noRepoParams and twoRepoParams are fixedParams for a project with no repo
// and one with two (docs/mvp.md M9).
func noRepoParams() Params {
	p := fixedParams()
	p.Repos = nil
	return p
}

func twoRepoParams() Params {
	p := fixedParams()
	p.Repos = append(p.Repos, RepoParams{Name: "api", Path: "/ws/api", DefaultBranch: "develop"})
	return p
}

func renderWith(t *testing.T, p Params) string {
	t.Helper()
	got, err := Render(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// TestManualFactsExampleIsTheRealOutput: the example in section 4 is what
// `project facts` prints, byte for byte: the first repo's block while its
// branch is empty, or the no-repo lines for a project with none.
func TestManualFactsExampleIsTheRealOutput(t *testing.T) {
	for name, p := range map[string]Params{"none": noRepoParams(), "one": fixedParams(), "two": twoRepoParams()} {
		t.Run(name, func(t *testing.T) {
			want := strings.Join(facts.NoRepoLines(p.ProjectName), "\n")
			if len(p.Repos) > 0 {
				r := p.Repos[0]
				want = strings.Join(facts.Facts{Project: p.ProjectName, RepoName: r.Name, Repo: r.Path, DefaultBranch: r.DefaultBranch}.Lines(), "\n")
			}
			sec := section(t, renderWith(t, p), "## 4. The `mate` command contract")
			if !strings.Contains(sec, "```text\n"+want+"\n```") {
				t.Fatalf("section 4's project facts example is not the real output:\n%s", want)
			}
		})
	}
}

// TestManualListsTheRepos: section 1 lists every repo with its absolute
// path and default branch, or says there is none and what that means.
func TestManualListsTheRepos(t *testing.T) {
	sec := section(t, renderWith(t, twoRepoParams()), "## 1. Identity and prime directives")
	for _, want := range []string{
		"| Repo | Path | Default branch |",
		"| `shop` | `/ws/shop` | `main` |",
		"| `api` | `/ws/api` | `develop` |",
		"exactly one repo",
		"`/usr/local/bin/mate project repo list shop`",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("section 1 with two repos does not say %q", want)
		}
	}
	none := section(t, renderWith(t, noRepoParams()), "## 1. Identity and prime directives")
	for _, want := range []string{"no repo yet", "no Crew can be spawned", "add it yourself"} {
		if !strings.Contains(none, want) {
			t.Errorf("section 1 with no repo does not say %q", want)
		}
	}
	if strings.Contains(none, "| Repo |") {
		t.Error("section 1 with no repo still prints a repo table")
	}
}

// TestManualTeachesOneCrewOneRepo: the manual teaches --repo, the rule it
// follows, splitting work that spans repos into several Crews, and the
// <repo>:<branch>@<sha> anchor; no example names a repo the project lacks.
func TestManualTeachesOneCrewOneRepo(t *testing.T) {
	text := renderWith(t, twoRepoParams())
	for heading, wants := range map[string][]string{
		"## 4. The `mate` command contract": {"[--repo <name>]", "repo shop, branch mate/add-healthcheck", "<repo>:<branch>@<sha>", "shop:main@none"},
		"## 5. Task intake":                 {"one Crew per repo"},
		"## 7. Spawn":                       {"--repo <repo>", "required", "one Crew per repo", "blocked-by:"},
		"## 14. Project memory":             {"<repo>:<branch>@<sha>", "shop:main@3f2a91c"},
	} {
		sec := section(t, text, heading)
		for _, want := range wants {
			if !strings.Contains(sec, want) {
				t.Errorf("%s does not say %q", heading, want)
			}
		}
	}
	none := renderWith(t, noRepoParams())
	for _, gone := range []string{"shop:main@", "/ws/shop"} {
		if strings.Contains(none, gone) {
			t.Errorf("the manual of a project with no repo names %q", gone)
		}
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

// TestManualNamesCrewsAfterTheirTask: the captain reads a crew's id in the
// console, in crew list and in its branch, so the manual has the Mate name
// it after the task and never teaches a counter by example.
func TestManualNamesCrewsAfterTheirTask(t *testing.T) {
	text := renderedManual(t)
	sec := section(t, text, "## 4. The `mate` command contract")
	for _, want := range []string{"it names the task", "`fix-cart-total`", "no counters such as `k3`, `p1` or `m1`", "2 to 24 characters"} {
		if !strings.Contains(sec, want) {
			t.Errorf("section 4 does not say %q", want)
		}
	}
	counter := regexp.MustCompile("\\b[kpm][0-9]+\\b")
	for _, m := range counter.FindAllString(strings.Replace(text, "no counters such as `k3`, `p1` or `m1`", "", 1), -1) {
		t.Errorf("the manual still uses the counter id %q in an example", m)
	}
}

// TestManualNeverHasTheMateWaitForACrew pins docs/mvp.md task 57: the
// captain expects a Mate never to hold a long turn except to answer them. A
// live Mate on 2026-09-26 and again on 2026-09-27 polled its Crews inside one
// turn for nine minutes while the captain's messages sat queued, because the
// manual taught a `sleep 20; mate state` loop for manual mode.
func TestManualNeverHasTheMateWaitForACrew(t *testing.T) {
	text := renderedManual(t)
	if strings.Contains(text, "sleep 20") {
		t.Error("the manual still teaches a sleep loop")
	}
	sec9 := section(t, text, "## 9. Supervision, review and delivery")
	if !strings.Contains(sec9, "**You never wait for a Crew.**") {
		t.Error("section 9 does not open with the rule against waiting")
	}
	sec10 := section(t, text, "## 10. Two modes and the sentinel")
	for _, want := range []string{
		"### Ending the turn\n",
		"in either mode, after you spawn a Crew or answer one with `mate send`",
		"they have typed nothing to you for 5 minutes",
		"The captain typing to you puts the project in manual mode.",
	} {
		if !strings.Contains(sec10, want) {
			t.Errorf("section 10 does not say %q", want)
		}
	}
}

// TestManualGivesTheMateItsOwnProject pins the captain's boundary of
// 2026-09-27: everything inside the project is the Mate's to run, repos
// included, and only what is above it is the captain's. A live Mate had
// refused to add a repo the captain named, and handed them a shell line to
// paste, because the manual called `repo add` theirs.
func TestManualGivesTheMateItsOwnProject(t *testing.T) {
	for _, p := range []Params{twoRepoParams(), noRepoParams()} {
		sec := section(t, renderWith(t, p), "## 1. Identity and prime directives")
		for _, want := range []string{
			"Everything inside this project is yours to run",
			"creating or removing projects",
			"/usr/local/bin/mate project repo add shop <git-url|repo-path> [--name <name>]",
			"nothing is ever pushed",
		} {
			if !strings.Contains(sec, want) {
				t.Errorf("section 1 does not say %q", want)
			}
		}
		if strings.Contains(sec, "theirs to run, never yours") {
			t.Error("section 1 still calls repo add the captain's")
		}
	}
}
