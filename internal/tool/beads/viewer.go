package beads

import (
	"fmt"
	"path/filepath"

	"github.com/nguyenngocanh94/mate/internal/tool"
)

// viewer is Beads Viewer in the tasks tab beside the Console.
type viewer struct{}

// Bindings is `t` on a project row, opening the tasks tab.
func (viewer) Bindings() []tool.Binding {
	return []tool.Binding{{Key: "t", Label: "tasks", Scope: tool.ScopeProject, Role: "tasks"}}
}

// Argv is bv on the project's tracker. One project has one tracker, so bv
// runs on it as it is.
func (viewer) Argv(ctx tool.ViewerContext, findTool func(string) string) ([]string, error) {
	bin := findTool(viewerBin)
	if !filepath.IsAbs(bin) {
		return nil, fmt.Errorf("a project's tasks need Beads Viewer (%s): %s", viewerBin, info.Install)
	}
	return []string{bin, "--db", data{}.Dir(ctx.ProjectDir)}, nil
}

func (viewer) Placeholder() string {
	return "mate · tasks\r\n\r\nt on a project opens Beads Viewer here."
}
