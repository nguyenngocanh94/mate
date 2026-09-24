package mateassets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/memory"
	"github.com/nguyenngocanh94/matev2/internal/send"
)

// The memory shape has one source of truth, internal/memory. These tests
// hold the manual and the stow skill to it (docs/mvp.md task 36).

// memoryExampleDay is the date the manual's and the skill's memory examples
// are written on, so the shape check judges their ages as of that day.
var memoryExampleDay = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// markdownBlock returns the first ```markdown block in text.
func markdownBlock(t *testing.T, what, text string) string {
	t.Helper()
	start := strings.Index(text, "```markdown\n")
	if start < 0 {
		t.Fatalf("%s has no ```markdown example", what)
	}
	body := text[start+len("```markdown\n"):]
	end := strings.Index(body, "\n```")
	if end < 0 {
		t.Fatalf("%s's example is not closed", what)
	}
	return body[:end]
}

// TestManualSection14MemoryShapeIsWhatRememberWrites: the entry shape
// section 14 documents is the one `matev2 remember` writes, byte for byte,
// for all three tiers, and it passes `memory check`.
func TestManualSection14MemoryShapeIsWhatRememberWrites(t *testing.T) {
	sec := section(t, renderedManual(t), "## 14. Project memory")
	example := markdownBlock(t, "section 14", sec)
	entries, problems := memory.Check(example, memoryExampleDay, "/ws", "/ws/.matev2/projects/shop")
	if len(problems) != 0 {
		t.Fatalf("section 14's memory example fails memory check: %v\n%s", problems, example)
	}
	lines := strings.Split(example, "\n")
	seen := map[memory.Tier]bool{}
	for _, e := range entries {
		written, err := memory.NewEntry(e.Section, e.Text, e.Source, e.Expiry, memoryExampleDay)
		if err != nil {
			t.Fatalf("remember refuses section 14's entry %q: %v", e.Text, err)
		}
		if got := lines[e.Line-1]; written.Line() != got {
			t.Errorf("remember writes\n%s\nbut section 14 documents\n%s", written.Line(), got)
		}
		seen[e.Tier] = true
	}
	for _, tier := range []memory.Tier{memory.Pinned, memory.Aging, memory.Perishable} {
		if !seen[tier] {
			t.Errorf("section 14's example has no %s entry", tier)
		}
	}
	for _, want := range []string{
		fmt.Sprintf("stale %d days", memory.AgingDays),
		fmt.Sprintf("stale after %d", memory.PerishableDays),
		"(" + memory.ExpiresPrefix + "<condition>)",
		fmt.Sprintf("Keep the %d most recent", memory.DoneKeep),
		memory.BacklogArchiveName,
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("section 14 does not say %q", want)
		}
	}
}

// TestManualSection4DocumentsTheMemoryCommands: remember and memory check
// are documented with what they really print and enforce, and the old
// "append it yourself" stop-gap is gone.
func TestManualSection4DocumentsTheMemoryCommands(t *testing.T) {
	text := renderedManual(t)
	sec := section(t, text, "## 4. The `matev2` command contract")
	for _, want := range []string{
		`matev2 remember <project> --captain|--lesson [--perishable "<expiry condition>"] --source <src> "<one line>"`,
		"matev2 memory check <project>",
		fmt.Sprintf("%d estimated tokens", memory.BudgetTokens),
		"ceil(bytes / 3)",
		"head: <sha>",
		fmt.Sprintf("%d or more days ago", memory.AgingDays),
		"record: update backlog.md Done; if this task taught you anything durable, route it (skill stow)",
		"if this correction applies to future crews, route it: memory.md Lessons or propose it for CREW.md",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("section 4 does not document %q", want)
		}
	}
	e, err := memory.NewEntry(memory.LessonsSection,
		"Scout Crews commit their report files unless the brief says to keep every file under crews/<id>/ and commit nothing.",
		"sent.log to rpi35 2026-09-18", "", memoryExampleDay)
	if err != nil {
		t.Fatal(err)
	}
	if want := "under ## " + memory.LessonsSection + ": " + e.Line() + "\n"; !strings.Contains(sec, want) {
		t.Errorf("section 4's remember example is not what remember prints; want a line ending %q", want)
	}
	for _, gone := range []string{"Not yet available", "lands in a later task", "you append to"} {
		if strings.Contains(text, gone) {
			t.Errorf("the manual still says %q", gone)
		}
	}
}

// TestManualSection2RoutesEveryKindToOneOwner: A1 and A4 are in section 2.
func TestManualSection2RoutesEveryKindToOneOwner(t *testing.T) {
	sec := section(t, renderedManual(t), "## 2. File layout you read at bootstrap")
	for _, want := range []string{
		"You just learned", "is your only memory", "it is never a source",
		"`## " + memory.CaptainSection + "`", "`## " + memory.LessonsSection + "`", "`## " + memory.BacklogHeld + "`",
		"`## " + memory.ProjectDecisionsSection + "`", "A proposal to the captain", memory.ArchiveName, memory.BacklogArchiveName,
		"- `stow` - ",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("section 2 does not say %q", want)
		}
	}
	if strings.Contains(sec, "| Layer |") {
		t.Error("section 2 still has the layers table A1 replaces")
	}
}

// TestManualSection13RecordsHeldQuestions: A3.
func TestManualSection13RecordsHeldQuestions(t *testing.T) {
	sec := section(t, renderedManual(t), "## 13. Escalation and captain etiquette")
	for _, want := range []string{"`## " + memory.BacklogHeld + "`", "exactly as you sent it", "what it waits on", "verbatim", "only then remove the line"} {
		if !strings.Contains(sec, want) {
			t.Errorf("section 13 does not say %q", want)
		}
	}
}

// TestWriteSeedsMemoryAndBacklogWithTheirSections: a fresh Mate gets the
// sections the manual names, and memory check accepts the empty file.
func TestWriteSeedsMemoryAndBacklogWithTheirSections(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, fixedParams()); err != nil {
		t.Fatal(err)
	}
	mem, err := os.ReadFile(filepath.Join(dir, "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(mem) != memory.Header {
		t.Fatalf("memory.md = %q, want %q", mem, memory.Header)
	}
	if _, problems := memory.Check(string(mem), memoryExampleDay, "/ws", "/ws/.matev2/projects/shop"); len(problems) != 0 {
		t.Fatalf("a fresh memory.md fails memory check: %v", problems)
	}
	backlog, err := os.ReadFile(filepath.Join(dir, "backlog.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backlog) != memory.BacklogHeader() {
		t.Fatalf("backlog.md = %q", backlog)
	}
}

// TestStowSkillQuotesTheStowLine: the line the app sends before a restart
// (task 37) is the one the skill teaches, and the skill's example passes
// memory check.
func TestStowSkillQuotesTheStowLine(t *testing.T) {
	got, err := RenderSkill("stow", fixedParams())
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, send.Marker+memory.StowLine+"\n") {
		t.Errorf("the stow skill does not quote the stow line %q", send.Marker+memory.StowLine)
	}
	if !strings.Contains(renderedManual(t), "`"+send.Marker+"stow:`") {
		t.Error("the manual's skill list does not name the stow: line")
	}
	example := markdownBlock(t, "stow skill", text)
	if _, problems := memory.Check(example, memoryExampleDay, "/ws", "/ws/.matev2/projects/shop"); len(problems) != 0 {
		t.Fatalf("the stow skill's example fails memory check: %v", problems)
	}
	for _, want := range []string{
		"`unchanged`", "`added`", "`rewritten`", "`pruned`", "`routed`", "`archived`",
		"nothing this session knew has been lost", memory.HeaderPointer, memory.ArchiveName,
		fmt.Sprintf("%d days since its last-reinforced date", memory.AgingDays),
		fmt.Sprintf("%d days since its last-reinforced date", memory.PerishableDays),
		"never write it",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the stow skill does not say %s", want)
		}
	}
}
