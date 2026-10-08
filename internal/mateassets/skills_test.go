package mateassets

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
)

// TestRenderSkillGolden pins each installed skill's rendered text, the same
// way the manual and the brief are pinned: a skill is instructions a live
// Mate acts on, so a change to one is a change to behaviour and has to be
// reviewed as a diff.
func TestRenderSkillGolden(t *testing.T) {
	for _, name := range SkillNames {
		t.Run(name, func(t *testing.T) {
			got, err := RenderSkill(name, fixedParams())
			if err != nil {
				t.Fatal(err)
			}
			compareGolden(t, filepath.Join("testdata", "skills", name+".SKILL.md.golden"), got)
		})
	}
}

// TestRenderSkillGitHubGolden pins the skills that differ in the github mode
// (docs/mvp.md M18); TestRenderSkillGolden pins them in local-only.
func TestRenderSkillGitHubGolden(t *testing.T) {
	p := fixedParams()
	p.Mode = "github"
	for _, name := range []string{"review-delivery", "mate-commands", "event-handling", "task-intake", "brief-writing"} {
		t.Run(name, func(t *testing.T) {
			got, err := RenderSkill(name, p)
			if err != nil {
				t.Fatal(err)
			}
			compareGolden(t, filepath.Join("testdata", "skills", name+"-github.SKILL.md.golden"), got)
			if loc := unexpandedPattern.FindIndex(got); loc != nil {
				t.Fatalf("rendered skill %s has an unexpanded template marker at byte %d", name, loc[0])
			}
			checkForbidden(t, name+"/SKILL.md", got)
			local, err := RenderSkill(name, fixedParams())
			if err != nil {
				t.Fatal(err)
			}
			if string(got) == string(local) {
				t.Errorf("skill %s renders the same in both modes", name)
			}
		})
	}
}

func TestRenderSkillUnknownName(t *testing.T) {
	if _, err := RenderSkill("no-such-skill", fixedParams()); err == nil {
		t.Fatal("RenderSkill accepted an unknown skill name")
	}
}

func TestRenderSkillsNoUnexpandedVarsOrForbiddenWords(t *testing.T) {
	for _, name := range SkillNames {
		t.Run(name, func(t *testing.T) {
			got, err := RenderSkill(name, fixedParams())
			if err != nil {
				t.Fatal(err)
			}
			if loc := unexpandedPattern.FindIndex(got); loc != nil {
				t.Fatalf("rendered skill %s has an unexpanded template marker at byte %d: %q",
					name, loc[0], got[loc[0]:min(loc[0]+40, len(got))])
			}
			checkForbidden(t, name+"/SKILL.md", got)
		})
	}
}

// TestRenderSkillHasFrontmatter checks the one structural thing Claude Code
// requires of a skill file: a YAML frontmatter block whose `name` matches the
// directory the skill is installed under, and a non-empty `description`,
// which is the only text Claude sees when deciding whether to load it.
func TestRenderSkillHasFrontmatter(t *testing.T) {
	for _, name := range SkillNames {
		t.Run(name, func(t *testing.T) {
			got, err := RenderSkill(name, fixedParams())
			if err != nil {
				t.Fatal(err)
			}
			text := string(got)
			if !strings.HasPrefix(text, "---\n") {
				t.Fatalf("skill %s does not open with frontmatter: %.40q", name, text)
			}
			end := strings.Index(text[4:], "\n---\n")
			if end < 0 {
				t.Fatalf("skill %s has no closing frontmatter fence", name)
			}
			front := text[4 : 4+end]
			if !strings.Contains(front, "name: "+name+"\n") {
				t.Errorf("skill %s frontmatter does not declare name: %s\n%s", name, name, front)
			}
			var desc string
			for _, line := range strings.Split(front, "\n") {
				if strings.HasPrefix(line, "description:") {
					desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
				}
			}
			if desc == "" {
				t.Errorf("skill %s frontmatter has no description\n%s", name, front)
			}
		})
	}
}

// TestWriteInstallsSkills is the task 17 wiring proof: Write puts every skill
// where Claude Code looks for one relative to the Mate's own cwd.
func TestWriteInstallsSkills(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, fixedParams()); err != nil {
		t.Fatal(err)
	}
	for _, name := range SkillNames {
		path := filepath.Join(dir, ".claude", "skills", name, "SKILL.md")
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
		want, err := RenderSkill(name, fixedParams())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is not the rendered skill", path)
		}
	}
}

// TestWriteRegeneratesSkills proves a skill is generated content, not the
// Mate's own memory: a stale copy from an older build is replaced, never
// preserved the way memory.md is.
func TestWriteRegeneratesSkills(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "skills", SkillNames[0], "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const marker = "PRE-EXISTING-STUB-CONTENT"
	if err := os.WriteFile(path, []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, fixedParams()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte(marker)) {
		t.Errorf("%s was not regenerated: %q", path, got)
	}
}

// The harness sections come from the registry, so who runs on what follows
// the Mate's own harness: a Codex Mate is not told it runs on Claude Code.
func TestHarnessAdaptersFollowTheMatesHarness(t *testing.T) {
	p := fixedParams()
	harnesses, err := HarnessesFrom(catalog.Default(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	p.Harness, p.Harnesses = "codex", harnesses
	got, err := RenderSkill("harness-adapters", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## codex\n\nCodex is what you run on, and the default Crew harness.\n",
		"## claude\n\nA Crew runs on Claude Code when a spawn is given `--harness claude`.\n",
	} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("the skill for a Codex Mate does not say %q:\n%s", want, got)
		}
	}
	if bytes.Contains(got, []byte("Claude Code is what you run on")) {
		t.Error("the skill tells a Codex Mate it runs on Claude Code")
	}
}
