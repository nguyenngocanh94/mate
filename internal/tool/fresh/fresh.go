// Package fresh is the Fresh editor as a tool: the program the console's
// `e` opens on a crew's report, in the review tab beside the Console
// (docs/mvp.md M13). Fresh is only a viewer here. It keeps nothing of
// mate's, so every other capability is unsupported.
package fresh

import (
	"fmt"
	"path/filepath"

	"github.com/nguyenngocanh94/mate/internal/capability"
	"github.com/nguyenngocanh94/mate/internal/tool"
)

var info = tool.Info{
	Name:     "fresh",
	Title:    "Fresh",
	Binaries: []string{"fresh"},
	Install:  "brew install fresh-editor",
	Docs:     "docs/mvp.md M13",
	Measured: "fresh 0.5.2 (fresh --version, 2026-10-08)",
}

// New is Fresh's profile.
func New() tool.Profile { return profile{} }

type profile struct{}

func (profile) Name() tool.Name { return info.Name }
func (profile) Info() tool.Info { return info }

func (profile) Capabilities() tool.Capabilities {
	const none = "Fresh is an editor; it owns no data of mate's"
	return tool.Capabilities{
		Viewer: capability.Cap[tool.Viewer]{Status: capability.Verified, Impl: viewer{}, Evidence: capability.Evidence{
			Version:  "fresh 0.5.2 (read 2026-10-08; M13 recorded none)",
			Measured: "2026-09-26",
			Proof: "docs/mvp.md M13 task 54: e opened Fresh on a crew's report live in Ghostty 1.3.1 and WezTerm; " +
				"the argv is pinned by cmd/mate TestReviewFreshArgvGolden",
		}},
		Command: capability.Cap[tool.Command]{Status: capability.Unsupported, Reason: none},
		Recall:  capability.Cap[tool.Recall]{Status: capability.Unsupported, Reason: none},
		Skill:   capability.Cap[tool.Skill]{Status: capability.Unsupported, Reason: none},
		Data:    capability.Cap[tool.Data]{Status: capability.Unsupported, Reason: none},
	}
}

type viewer struct{}

// Bindings is `e` on a crew row, opening the review tab.
func (viewer) Bindings() []tool.Binding {
	return []tool.Binding{{Key: "e", Label: "report", Scope: tool.ScopeCrew, Role: "review"}}
}

// Argv is Fresh on the crew's report.md when it wrote one, else on the
// crew's folder. mate never writes report.md.
func (viewer) Argv(ctx tool.ViewerContext, findTool func(string) string) ([]string, error) {
	editor := findTool(info.Binaries[0])
	if !filepath.IsAbs(editor) {
		return nil, fmt.Errorf("a crew's report needs the Fresh editor: %s", info.Install)
	}
	if ctx.ReportPath != "" {
		return []string{editor, ctx.ReportPath}, nil
	}
	return []string{editor, ctx.CrewDir}, nil
}

func (viewer) Placeholder() string {
	return "mate · report\r\n\r\ne on a crew opens its report here."
}
