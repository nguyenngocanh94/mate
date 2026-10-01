package mateassets

import (
	"bytes"
	"fmt"
	"text/template"
	"time"

	"github.com/nguyenngocanh94/mate/assets"
	"github.com/nguyenngocanh94/mate/internal/store"
)

var (
	agentsTemplate = template.Must(template.ParseFS(assets.FS, "mate/AGENTS.md.tmpl"))
	briefTemplate  = template.Must(template.ParseFS(assets.FS, "crew/brief.md.tmpl"))
)

// SkillNames are the skills installed beside the manual, in the order Write
// lays them down. Each one is `assets/mate/skills/<name>/SKILL.md.tmpl` in
// the embedded FS and `<mate>/<SkillsDir>/<name>/SKILL.md` on disk.
var SkillNames = []string{"harness-adapters", "crew-dispatch", "stuck-crew-recovery", "decision-authority", "diagnostic-reasoning", "stow", "mate-commands", "task-intake", "brief-writing", "crew-spawn", "review-delivery", "event-handling", "project-memory"}

// skillTemplates holds one parsed template per SkillNames entry. Parsing at
// init keeps a malformed skill a build-time failure rather than a Mate that
// starts without its playbooks.
var skillTemplates = func() map[string]*template.Template {
	out := make(map[string]*template.Template, len(SkillNames))
	for _, name := range SkillNames {
		out[name] = template.Must(template.ParseFS(assets.FS, "mate/skills/"+name+"/SKILL.md.tmpl"))
	}
	return out
}()

// Params fills assets/mate/AGENTS.md.tmpl. Every field is a value the Mate
// needs at bootstrap; every path is absolute, because the rendered file is
// the thing the Mate reads to find everything else.
type Params struct {
	// ProjectName is the project's name as registered in workspace.yaml.
	ProjectName string
	// WorkspaceRoot is the absolute path of the workspace directory.
	WorkspaceRoot string
	// Repos are the project's repos as of this start, in the project's
	// order: zero or more, each Crew working in exactly one (docs/mvp.md M9).
	Repos []RepoParams
	// Mode is the project's delivery mode (`local-only` in the MVP).
	Mode string
	// Yolo reports whether the Mate may approve merges without asking.
	Yolo bool
	// Harness names the harness the Mate itself runs on (e.g. "claude-code").
	Harness string
	// SkillsDir is where the skills are written, relative to MateDir: the
	// directory the Mate's harness discovers them in (harness.Info). The
	// manual names it for a harness that does not.
	SkillsDir string
	// WorkspaceDoc is the absolute path of the workspace-wide WORKSPACE.md.
	WorkspaceDoc string
	// ProjectDoc is the absolute path of the project's PROJECT.md.
	ProjectDoc string
	// WorkspaceCrewDoc and ProjectCrewDoc are the absolute paths of the
	// captain's two CREW.md files, which the app appends to every brief.
	WorkspaceCrewDoc string
	ProjectCrewDoc   string
	// MemoryFile is the absolute path of the Mate's memory.md.
	MemoryFile string
	// BacklogFile is the absolute path of the Mate's backlog.md.
	BacklogFile string
	// MatevBin is the absolute path of the mate binary the Mate invokes.
	MatevBin string
	// MateDir is the absolute path of the Mate's own working directory, the
	// one place it may write: its briefs, memory and backlog live there.
	MateDir string
	// CrewsDir is the absolute path of `projects/<p>/crews/`, where every
	// crew's `.meta` and `.status` file lives. The Mate reads it, never
	// writes it.
	CrewsDir string
}

// QuietAfter is how long the captain's silence has to last before the
// console turns auto mode back on, as the manual says it.
func (Params) QuietAfter() string {
	return fmt.Sprintf("%d minutes", int(store.QuietAfter/time.Minute))
}

// RepoParams is one repo of the project, as the manual lists it.
type RepoParams struct {
	// Name is the repo's name inside the project: `crew spawn --repo`, the
	// `<repo>:` of a PROJECT.md anchor.
	Name string
	// Path is the absolute path of the repo's git checkout.
	Path string
	// DefaultBranch is the branch this repo's Crew worktrees branch from and
	// merge into.
	DefaultBranch string
}

// ExampleRepo is the repo the manual's examples name: the project's first,
// or placeholders when it has none, so an example never names a repo the
// project does not have.
func (p Params) ExampleRepo() RepoParams {
	if len(p.Repos) > 0 {
		return p.Repos[0]
	}
	return RepoParams{Name: "<repo>", Path: "<repo-path>", DefaultBranch: "<branch>"}
}

// Render fills assets/mate/AGENTS.md.tmpl with p and returns the result. It
// touches no filesystem beyond the embedded template.
func Render(p Params) ([]byte, error) {
	var buf bytes.Buffer
	if err := agentsTemplate.Execute(&buf, p); err != nil {
		return nil, fmt.Errorf("mateassets: render AGENTS.md: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderSkill fills the named skill's template with p. The name must be one
// of SkillNames; anything else is a programming error and is reported as one
// rather than silently writing nothing.
func RenderSkill(name string, p Params) ([]byte, error) {
	tmpl, ok := skillTemplates[name]
	if !ok {
		return nil, fmt.Errorf("mateassets: unknown skill %q", name)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return nil, fmt.Errorf("mateassets: render skill %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

// BriefParams fills assets/crew/brief.md.tmpl: the fixed worker role, the
// Mate's `# Task` text, and the template's own sections around it
// (docs/mvp.md M7).
type BriefParams struct {
	// Task is the Mate's `# Task` body: the sections internal/brief checks,
	// inserted verbatim. A leading `# Task` line the Mate wrote is expected
	// to have been dropped already (brief.TaskBody), since the template
	// supplies the heading.
	Task string
	// Scout selects the scout shape: a report at ReportPath instead of a
	// commit and a hand-back at HandbackPath.
	Scout bool
	// RepoPath is the absolute path of the project's primary git checkout.
	RepoPath string
	// WorktreePath is the absolute path of the Crew's own worktree.
	WorktreePath string
	// Branch is the Crew's branch name.
	Branch string
	// DefaultBranch is the branch the Crew's branch is based on and merges into.
	DefaultBranch string
	// BriefPath is the absolute path of the rendered brief itself,
	// `crews/<id>/brief.md`, where `brief append` adds the captain's later
	// words.
	BriefPath string
	// ReportPath is the absolute path of a scout's report,
	// `crews/<id>/report.md`. The app knows it, so the Mate no longer has
	// to type it into the brief.
	ReportPath string
	// HandbackPath is the absolute path of a ship's hand-back,
	// `crews/<id>/handback.md`.
	HandbackPath string
	// WorkspaceCrewRules and ProjectCrewRules are the captain's CREW.md
	// texts (store.CrewRules), comments already stripped. Empty omits them;
	// both empty omits the whole section.
	WorkspaceCrewRules string
	ProjectCrewRules   string
}

// RenderBrief fills assets/crew/brief.md.tmpl with p and returns the result.
func RenderBrief(p BriefParams) ([]byte, error) {
	var buf bytes.Buffer
	if err := briefTemplate.Execute(&buf, p); err != nil {
		return nil, fmt.Errorf("mateassets: render brief.md: %w", err)
	}
	return buf.Bytes(), nil
}
