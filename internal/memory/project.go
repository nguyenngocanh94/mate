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
// calendar, so every line there carries a `<branch>@<sha>` anchor from
// `mate project facts` (B8).
var RepoStateSections = []string{ProjectLayoutSection, ProjectHowSection}

// ProjectFileName is how problems name PROJECT.md.
const ProjectFileName = "PROJECT.md"

// NoCommit is the anchor of a fact recorded while the branch had no commit.
const NoCommit = facts.NoHead

// Anchor is one `<branch>@<sha>` a PROJECT.md line carries.
type Anchor struct {
	Line int
	SHA  string // abbreviated or full hex, or NoCommit
}

func anchorPattern(branch string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^\w/.-])` + regexp.QuoteMeta(branch) + `@([0-9a-f]{7,40}|` + NoCommit + `)\b`)
}

// CheckProject reports every repo-state line of PROJECT.md that carries no
// anchor on branch, and returns the anchors it found there.
func CheckProject(text, branch string) ([]Anchor, []Problem) {
	re := anchorPattern(branch)
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
		m := re.FindStringSubmatch(line)
		if m == nil {
			problems = append(problems, Problem{File: ProjectFileName, Line: i + 1, Msg: fmt.Sprintf(
				"`## %s` line has no %s@<sha> anchor: add the head `mate project facts` printed when the fact was recorded, e.g. (crews/k1/report.md §Durable facts, %s@3f2a91c, 2026-09-24)", section, branch, branch)})
			continue
		}
		anchors = append(anchors, Anchor{Line: i + 1, SHA: m[1]})
	}
	return anchors, problems
}

func isRepoState(section string) bool {
	for _, s := range RepoStateSections {
		if s == section {
			return true
		}
	}
	return false
}

// ProjectTemplate is a fresh PROJECT.md: the title and the empty sections.
func ProjectTemplate(name string) string {
	var b strings.Builder
	b.WriteString("# " + name + "\n")
	for _, s := range ProjectSections {
		b.WriteString("\n## " + s + "\n")
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
