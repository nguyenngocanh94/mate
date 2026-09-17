package mateassets

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/nguyenngocanh94/matev2/assets"
)

var (
	agentsTemplate = template.Must(template.ParseFS(assets.FS, "mate/AGENTS.md.tmpl"))
	briefTemplate  = template.Must(template.ParseFS(assets.FS, "crew/brief.md.tmpl"))
)

// Params fills assets/mate/AGENTS.md.tmpl. Every field is a value the Mate
// needs at bootstrap; every path is absolute, because the rendered file is
// the thing the Mate reads to find everything else.
type Params struct {
	// ProjectName is the project's name as registered in workspace.yaml.
	ProjectName string
	// WorkspaceRoot is the absolute path of the workspace directory.
	WorkspaceRoot string
	// ProjectRepo is the absolute path of the project's primary git checkout.
	ProjectRepo string
	// DefaultBranch is the branch Crew worktrees branch from and merge into.
	DefaultBranch string
	// Mode is the project's delivery mode (`local-only` in the MVP).
	Mode string
	// Yolo reports whether the Mate may approve merges without asking.
	Yolo bool
	// Harness names the harness the Mate itself runs on (e.g. "claude-code").
	Harness string
	// WorkspaceDoc is the absolute path of the workspace-wide WORKSPACE.md.
	WorkspaceDoc string
	// ProjectDoc is the absolute path of the project's PROJECT.md.
	ProjectDoc string
	// MemoryFile is the absolute path of the Mate's memory.md.
	MemoryFile string
	// BacklogFile is the absolute path of the Mate's backlog.md.
	BacklogFile string
	// MatevBin is the absolute path of the matev2 binary the Mate invokes.
	MatevBin string
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

// BriefParams fills assets/crew/brief.md.tmpl. The `{TASK}` placeholder in
// the rendered text is left for the Mate to replace with the task
// description; it is not a template field, because the Mate writes it after
// rendering, not at render time.
type BriefParams struct {
	// RepoPath is the absolute path of the project's primary git checkout.
	RepoPath string
	// WorktreePath is the absolute path of the Crew's own worktree.
	WorktreePath string
	// Branch is the Crew's branch name.
	Branch string
	// DefaultBranch is the branch the Crew's branch is based on and merges into.
	DefaultBranch string
}

// RenderBrief fills assets/crew/brief.md.tmpl with p and returns the result.
func RenderBrief(p BriefParams) ([]byte, error) {
	var buf bytes.Buffer
	if err := briefTemplate.Execute(&buf, p); err != nil {
		return nil, fmt.Errorf("mateassets: render brief.md: %w", err)
	}
	return buf.Bytes(), nil
}
