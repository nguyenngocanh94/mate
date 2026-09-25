package memory

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/facts"
)

// PROJECT.md's sections (docs/research/firstmate-memory-2026-09-24.md
// §12.3). The app creates them empty at `project add`; the Mate fills them.
const (
	ProjectWhatSection      = "What this project is"
	ProjectLayoutSection    = "Layout and state"
	ProjectHowSection       = "How to work here"
	ProjectResearchSection  = "Research on file"
	ProjectDecisionsSection = "Captain's decisions"
)

// ProjectSections is PROJECT.md's sections in file order.
var ProjectSections = []string{ProjectWhatSection, ProjectLayoutSection, ProjectHowSection, ProjectResearchSection, ProjectDecisionsSection}

// RepoStateSections are the PROJECT.md sections whose lines state what the
// repository holds or how it runs. Those facts go stale by commit, not by
// calendar, so every line there carries a `<repo>:<branch>@<sha>` anchor
// from `mate project facts` (B8, M9).
var RepoStateSections = []string{ProjectLayoutSection, ProjectHowSection}

// ProjectFileName is how problems name PROJECT.md.
const ProjectFileName = "PROJECT.md"

// NoCommit is the anchor of a fact recorded while the branch had no commit.
const NoCommit = facts.NoHead

// Repo is one repo a PROJECT.md anchor may name: its name inside the
// project and the default branch its facts are read from.
type Repo struct {
	Name   string
	Branch string
}

// Anchor is one `<repo>:<branch>@<sha>` a PROJECT.md line carries. A bare
// `<branch>@<sha>` written before M9 is read as the sole repo's.
type Anchor struct {
	Line int
	Repo string
	SHA  string // abbreviated or full hex, or NoCommit
}

// anchorSHA is what follows the `@` of an anchor.
const anchorSHA = `@([0-9a-f]{7,40}|` + NoCommit + `)\b`

// anchorPattern matches `<repo>:<branch>@<sha>` for one repo.
func anchorPattern(r Repo) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^\w/.:-])` + regexp.QuoteMeta(r.Name) + `:` + regexp.QuoteMeta(r.Branch) + anchorSHA)
}

// bareAnchorPattern matches the pre-M9 `<branch>@<sha>`, not preceded by a
// repo name.
func bareAnchorPattern(branch string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^\w/.:-])` + regexp.QuoteMeta(branch) + anchorSHA)
}

// CheckProject reports every repo-state line of PROJECT.md that carries no
// anchor for one of repos, and returns the anchors it found, in line order
// and, within a line, in repos order. A line about several repos may carry
// one anchor for each. A bare `<branch>@<sha>` counts for the sole repo
// when there is exactly one (docs/mvp.md M9); with none, no repo-state line
// can be anchored at all.
func CheckProject(text string, repos []Repo) ([]Anchor, []Problem) {
	var anchors []Anchor
	var problems []Problem
	section := ""
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}
		if !strings.HasPrefix(line, "- ") || !isRepoState(section) {
			continue
		}
		found, msg := lineAnchors(line, repos)
		if msg != "" {
			problems = append(problems, Problem{File: ProjectFileName, Line: i + 1, Msg: fmt.Sprintf("`## %s` %s", section, msg)})
			continue
		}
		for _, a := range found {
			a.Line = i + 1
			anchors = append(anchors, a)
		}
	}
	return anchors, problems
}

// lineAnchors finds one repo-state line's anchors, or says what is wrong.
func lineAnchors(line string, repos []Repo) ([]Anchor, string) {
	if len(repos) == 0 {
		return nil, "line states repository state, but the project has no repo: ask the captain to add it (`mate project repo add <project> <repo-path>`), or remove the line"
	}
	var found []Anchor
	for _, r := range repos {
		for _, m := range anchorPattern(r).FindAllStringSubmatch(line, -1) {
			found = append(found, Anchor{Repo: r.Name, SHA: m[1]})
		}
	}
	if len(found) > 0 {
		return found, ""
	}
	if msg := misnamedAnchor(line, repos); msg != "" {
		return nil, msg
	}
	if len(repos) == 1 {
		if m := bareAnchorPattern(repos[0].Branch).FindStringSubmatch(line); m != nil {
			return []Anchor{{Repo: repos[0].Name, SHA: m[1]}}, ""
		}
		want := facts.Anchor(repos[0].Name, repos[0].Branch, "<sha>")
		return nil, fmt.Sprintf("line has no %s anchor: add the head `mate project facts` printed when the fact was recorded, e.g. (crews/k1/report.md §Durable facts, %s, 2026-09-24)",
			want, facts.Anchor(repos[0].Name, repos[0].Branch, "3f2a91c"))
	}
	for _, r := range repos {
		if m := bareAnchorPattern(r.Branch).FindStringSubmatch(line); m != nil {
			var fixes []string
			for _, o := range repos {
				if o.Branch == r.Branch {
					fixes = append(fixes, fmt.Sprintf("%s if the fact is about %s", facts.Anchor(o.Name, o.Branch, m[1]), o.Name))
				}
			}
			return nil, fmt.Sprintf("line's anchor %s@%s names no repo, and the project has %d: write it as %s",
				r.Branch, m[1], len(repos), strings.Join(fixes, ", or "))
		}
	}
	wants := make([]string, len(repos))
	for i, r := range repos {
		wants[i] = facts.Anchor(r.Name, r.Branch, "<sha>")
	}
	return nil, fmt.Sprintf("line has no <repo>:<branch>@<sha> anchor for one of the project's repos (%s): add the head `mate project facts` printed for that repo when the fact was recorded, e.g. (crews/k1/report.md §Durable facts, %s, 2026-09-24)",
		strings.Join(wants, ", "), facts.Anchor(repos[0].Name, repos[0].Branch, "3f2a91c"))
}

// qualifiedAnchorPattern matches any `<name>:<branch>@<sha>`, whether or
// not name is one of the project's repos.
var qualifiedAnchorPattern = regexp.MustCompile(`(?:^|[^\w/.:-])([a-z][a-z0-9-]{0,31}):([\w./-]+)` + anchorSHA)

// misnamedAnchor says what is wrong with a line's qualified anchor that
// matched none of repos: it names a repo the project does not have, or a
// branch that is not the repo's default. Empty when the line has none.
func misnamedAnchor(line string, repos []Repo) string {
	m := qualifiedAnchorPattern.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	name, branch := m[1], m[2]
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Name
		if r.Name == name {
			return fmt.Sprintf("line's anchor %s:%s@%s is on the wrong branch: %s's default branch is %s, not %s; re-read the fact against %s",
				name, branch, m[3], name, r.Branch, branch, facts.Anchor(name, r.Branch, "<sha>"))
		}
	}
	return fmt.Sprintf("line's anchor %s:%s@%s names repo %s, which this project does not have (its repos: %s): fix the repo name, or ask the captain to add the repo",
		name, branch, m[3], name, strings.Join(names, ", "))
}

func isRepoState(section string) bool {
	for _, s := range RepoStateSections {
		if s == section {
			return true
		}
	}
	return false
}

// RepoStateComment opens each repo-state section of a fresh PROJECT.md: the
// anchor its lines carry, named once where the lines are written. It names
// no repo, because the project's repos change after `project add`.
const RepoStateComment = "<!-- every line: the fact, then (source, <repo>:<branch>@<sha> from mate project facts, date) -->"

// ProjectTemplate is a fresh PROJECT.md: the title and the empty sections,
// the repo-state ones opening with RepoStateComment.
func ProjectTemplate(name string) string {
	var b strings.Builder
	b.WriteString("# " + name + "\n")
	for _, s := range ProjectSections {
		b.WriteString("\n## " + s + "\n")
		if isRepoState(s) {
			b.WriteString(RepoStateComment + "\n")
		}
	}
	return b.String()
}

// BudgetTokens is the estimated-token allowance for the always-read memory:
// memory.md, PROJECT.md and WORKSPACE.md together. It is smaller than
// firstmate's 7,500 because the Mate's manual already costs about 58 KB.
const BudgetTokens = 4000

// EstimateTokens is firstmate's conservative estimate, ceil(bytes / 3)
// (docs/configuration.md "Startup memory budget").
func EstimateTokens(bytes int) int {
	return (bytes + 2) / 3
}
