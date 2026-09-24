package mateassets

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief"
)

var update = flag.Bool("update", false, "update golden files in testdata/")

func fixedParams() Params {
	return Params{
		ProjectName:      "shop",
		WorkspaceRoot:    "/ws",
		ProjectRepo:      "/ws/shop",
		DefaultBranch:    "main",
		Mode:             "local-only",
		Yolo:             false,
		Harness:          "claude-code",
		WorkspaceDoc:     "/ws/.mate/WORKSPACE.md",
		ProjectDoc:       "/ws/.mate/projects/shop/PROJECT.md",
		WorkspaceCrewDoc: "/ws/.mate/CREW.md",
		ProjectCrewDoc:   "/ws/.mate/projects/shop/CREW.md",
		MemoryFile:       "/ws/.mate/projects/shop/mate/memory.md",
		BacklogFile:      "/ws/.mate/projects/shop/mate/backlog.md",
		MatevBin:         "/usr/local/bin/mate",
		MateDir:          "/ws/.mate/projects/shop/mate",
		CrewsDir:         "/ws/.mate/projects/shop/crews",
	}
}

// exampleShipTask is a small ship task written to the M7 schema, the kind
// of text a Mate passes to `crew spawn`.
const exampleShipTask = `## Captain's words
Trang About phải hiện số phiên bản của app.

## What we already know
- The site is built with Hugo; ` + "`make build`" + ` writes it to public/ (PROJECT.md, "How to work here").
- The version is the one line in VERSION at the repository root (report of scout k1, section 2).
- Unknown: whether the About page is a layout template or a content file.

## Build
- Show the text of VERSION on the About page, as "Version <text>".
- Out of scope: other pages, the footer, how VERSION is written.

## Acceptance
- The built About page contains "Version " followed by the text of VERSION. verify: ` + "`make build && grep -o 'Version [^<]*' public/about/index.html`" + `
- The site still builds. verify: ` + "`make build`" + `; quote its last lines.

## Open decisions
none`

// exampleScoutTask is a diagnostic scout, its Deliverable asking for what
// the diagnostic-reasoning skill says a diagnosis brief should ask for.
const exampleScoutTask = `## Captain's words
Sometimes the cart is empty when I open checkout, but only after I log in.

## What we already know
- The captain sees it after logging in; nobody has reproduced it yet.
- Unknown: where the cart is stored, and whether logging in replaces it.

## Build
- Reproduce the empty cart on the real login-then-checkout path and find what empties it.
- Out of scope: fixing it; any commit.

## Acceptance
- The report holds a reproduction anyone can rerun. verify: the exact commands and their output, in the report.

## Open decisions
none

## Deliverable
- The end-to-end reproduction, or the exact reason it could not be done.
- The initiating trigger, the masking condition and the visible symptom, kept apart.
- The failing path compared with a path where the cart survives, and where they first diverge.
- The relevant history: commits, migrations or earlier implementations that explain the divergence.
- The smallest counterfactual that changes the outcome, and what it showed.
- The observation that would disprove the explanation, and whether you ran it.`

func fixedBriefParams() BriefParams {
	return BriefParams{
		Task:          exampleShipTask,
		RepoPath:      "/ws/shop",
		WorktreePath:  "/ws/.worktrees/shop-k3",
		Branch:        "mate/k3",
		DefaultBranch: "main",
		BriefPath:     "/ws/.mate/projects/shop/crews/k3/brief.md",
		ReportPath:    "/ws/.mate/projects/shop/crews/k3/report.md",
		HandbackPath:  "/ws/.mate/projects/shop/crews/k3/handback.md",
		// Both CREW.md files carry a rule, so the golden pins the whole
		// section, precedence sentences included.
		WorkspaceCrewRules: "- Reproduce a bug end-to-end before you fix it.",
		ProjectCrewRules:   "- Run `make check` before you hand back.",
	}
}

func fixedScoutBriefParams() BriefParams {
	p := fixedBriefParams()
	p.Task = exampleScoutTask
	p.Scout = true
	p.WorkspaceCrewRules, p.ProjectCrewRules = "", ""
	return p
}

func compareGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run go test -run %s -update to create it)", path, err, t.Name())
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s mismatch: rerun with -update if this change is intended\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func TestRenderAgentsGolden(t *testing.T) {
	got, err := Render(fixedParams())
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, filepath.Join("testdata", "AGENTS.md.golden"), got)
}

func TestRenderBriefGolden(t *testing.T) {
	for name, p := range map[string]BriefParams{
		"brief-ship.md.golden":  fixedBriefParams(),
		"brief-scout.md.golden": fixedScoutBriefParams(),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := RenderBrief(p)
			if err != nil {
				t.Fatal(err)
			}
			compareGolden(t, filepath.Join("testdata", name), got)
		})
	}
}

// TestRenderedBriefPassesTheCheck: what the app renders from a good task is
// itself a good brief, so `mate brief check` on crews/<id>/brief.md agrees
// with the check `crew spawn` ran - and the template's own sections, which
// mention the schema's headings in prose, are never mistaken for the task's.
func TestRenderedBriefPassesTheCheck(t *testing.T) {
	for kind, p := range map[brief.Kind]BriefParams{brief.Ship: fixedBriefParams(), brief.Scout: fixedScoutBriefParams()} {
		got, err := RenderBrief(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(got), brief.RoleHeading+"\n") {
			t.Fatalf("%s brief does not open with the role heading %q", kind, brief.RoleHeading)
		}
		if ps := brief.CheckFile(string(got), kind); len(ps) != 0 {
			t.Fatalf("rendered %s brief fails its own check: %v", kind, ps)
		}
		task, ok := brief.ExtractTask(string(got))
		if !ok || strings.TrimSpace(task) != p.Task {
			t.Fatalf("rendered %s brief's # Task is not the task verbatim:\n%s", kind, task)
		}
	}
}

// TestRenderBriefShapes pins what differs between the two shapes and what
// the optional CREW.md section does, beyond the goldens.
func TestRenderBriefShapes(t *testing.T) {
	ship, err := RenderBrief(fixedBriefParams())
	if err != nil {
		t.Fatal(err)
	}
	scout, err := RenderBrief(fixedScoutBriefParams())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Before you hand back", "# Project memory", "/ws/.mate/projects/shop/crews/k3/handback.md", "wait-mate: ready in branch mate/k3", "## Deviations from Build", "## Still open", "# Captain's standing crew rules"} {
		if !strings.Contains(string(ship), want) {
			t.Errorf("ship brief lacks %q", want)
		}
		if strings.Contains(string(scout), want) {
			t.Errorf("scout brief carries ship-only %q", want)
		}
	}
	for _, want := range []string{"wait-mate: report ready at /ws/.mate/projects/shop/crews/k3/report.md", "## Durable facts", "propose the acceptance lines", "file:line references"} {
		if !strings.Contains(string(scout), want) {
			t.Errorf("scout brief lacks %q", want)
		}
	}
	// Only the project's rules: the section is there, the workspace part is not.
	p := fixedBriefParams()
	p.WorkspaceCrewRules = ""
	only, err := RenderBrief(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(only), "## Every project") || !strings.Contains(string(only), "## This project") {
		t.Errorf("project-only CREW.md rendered wrong:\n%s", only)
	}
}

// unexpandedPattern catches a template action that survived execution: a
// literal `{{` (an unexecuted action) or Go's zero-value stand-in for a
// missing field.
var unexpandedPattern = regexp.MustCompile(`\{\{|<no value>`)

func TestRenderAgentsNoUnexpandedVars(t *testing.T) {
	got, err := Render(fixedParams())
	if err != nil {
		t.Fatal(err)
	}
	if loc := unexpandedPattern.FindIndex(got); loc != nil {
		t.Fatalf("rendered AGENTS.md has an unexpanded template marker at byte %d: %q", loc[0], got[loc[0]:min(loc[0]+40, len(got))])
	}
}

func TestRenderBriefNoUnexpandedVars(t *testing.T) {
	got, err := RenderBrief(fixedBriefParams())
	if err != nil {
		t.Fatal(err)
	}
	if loc := unexpandedPattern.FindIndex(got); loc != nil {
		t.Fatalf("rendered brief.md has an unexpanded template marker at byte %d: %q", loc[0], got[loc[0]:min(loc[0]+40, len(got))])
	}
}

// forbiddenWords are v1/firstmate concepts and spellings that must never
// survive into mate's Mate manual or crew brief: tmux and treehouse are
// firstmate's terminal/worktree-pool tooling mate does not use, no-mistakes
// and secondmate are firstmate concepts mate has no equivalent of, fm- is
// firstmate's script-name prefix, gh-axi is a firstmate tool, and matev2/MATEV2_ are
// this repo's names before the 2026-09-24 rename.
var forbiddenWords = []string{
	"tmux",
	"treehouse",
	"no-mistakes",
	"secondmate",
	"fm-",
	"gh-axi",
	"matev2",
	"MATEV2_",
}

func TestRenderAgentsNoForbiddenWords(t *testing.T) {
	got, err := Render(fixedParams())
	if err != nil {
		t.Fatal(err)
	}
	checkForbidden(t, "AGENTS.md", got)
}

func TestRenderBriefNoForbiddenWords(t *testing.T) {
	got, err := RenderBrief(fixedBriefParams())
	if err != nil {
		t.Fatal(err)
	}
	checkForbidden(t, "brief.md", got)
}

func checkForbidden(t *testing.T, name string, got []byte) {
	t.Helper()
	for _, word := range forbiddenWords {
		if strings.Contains(string(got), word) {
			t.Errorf("rendered %s contains forbidden word %q", name, word)
		}
	}
}

func TestWriteCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, fixedParams()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "memory.md", "backlog.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}
	claude, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(claude) != "@AGENTS.md\n" {
		t.Errorf("CLAUDE.md = %q, want \"@AGENTS.md\\n\"", claude)
	}
}

func TestWritePreservesExistingMemory(t *testing.T) {
	dir := t.TempDir()
	const existing = "# Memory\n\n- learned something\n"
	if err := os.WriteFile(filepath.Join(dir, "memory.md"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	const existingBacklog = "# Backlog\n\n## In flight\n- task-1\n"
	if err := os.WriteFile(filepath.Join(dir, "backlog.md"), []byte(existingBacklog), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, fixedParams()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Errorf("memory.md was modified: got %q, want %q", got, existing)
	}
	gotBacklog, err := os.ReadFile(filepath.Join(dir, "backlog.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBacklog) != existingBacklog {
		t.Errorf("backlog.md was modified: got %q, want %q", gotBacklog, existingBacklog)
	}
}

func TestWriteOverwritesAgentsAndClaude(t *testing.T) {
	dir := t.TempDir()
	const marker = "PRE-EXISTING-STUB-CONTENT"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, fixedParams()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte(marker)) {
		t.Errorf("AGENTS.md was not regenerated: %q", got)
	}
}
