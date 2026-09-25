package host

import (
	"context"
	"strings"
	"sync"

	"github.com/nguyenngocanh94/mate/internal/process"
)

type ghostty struct {
	runner    process.Runner
	osascript string
	herdr     string

	mu   sync.Mutex
	last StageHandle
}

func newGhostty(opt Options) *ghostty {
	bin := opt.Osascript
	if bin == "" {
		bin = "osascript"
	}
	return &ghostty{
		runner:    opt.runner(),
		osascript: bin,
		herdr:     opt.herdr(),
	}
}

func (g *ghostty) EnsureSplit(ctx context.Context) (StageHandle, error) {
	ids, last, ours, err := g.layout(ctx)
	if err != nil {
		return StageHandle{}, err
	}
	if ours {
		return last, nil
	}
	if len(ids) > 1 {
		return StageHandle{}, errForeignPane()
	}
	id, err := g.script(ctx, ghosttySplitEmptyScript)
	if err != nil {
		return StageHandle{}, err
	}
	return g.commit(id), nil
}

func (g *ghostty) Stage(ctx context.Context, target StageTarget) (StageHandle, error) {
	if err := target.Validate(); err != nil {
		return StageHandle{}, err
	}
	ids, last, ours, err := g.layout(ctx)
	if err != nil {
		return StageHandle{}, err
	}
	if len(ids) > 1 && !ours {
		return StageHandle{}, errForeignPane()
	}
	if ours {
		if err := g.close(ctx, last.PaneID); err != nil {
			return StageHandle{}, err
		}
	}
	id, err := g.script(ctx, ghosttySplitScript(ghosttyAttachCommand(resolveExec(g.herdr), target.Session, target.AgentName)))
	if err != nil {
		return StageHandle{}, err
	}
	return g.commit(id), nil
}

func (g *ghostty) layout(ctx context.Context) ([]string, StageHandle, bool, error) {
	ids, err := g.list(ctx)
	if err != nil {
		return nil, StageHandle{}, false, err
	}
	g.mu.Lock()
	last := g.last
	g.mu.Unlock()
	ours := last.PaneID != "" && containsID(ids, last.PaneID)
	return ids, last, ours, nil
}

func (g *ghostty) commit(id string) StageHandle {
	h := StageHandle{PaneID: id}
	g.mu.Lock()
	g.last = h
	g.mu.Unlock()
	return h
}

func (g *ghostty) list(ctx context.Context) ([]string, error) {
	out, err := g.script(ctx, ghosttyListScript)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			ids = append(ids, line)
		}
	}
	return ids, nil
}

func (g *ghostty) close(ctx context.Context, id string) error {
	_, err := g.script(ctx, ghosttyCloseScript(id))
	return err
}

func (g *ghostty) script(ctx context.Context, source string) (string, error) {
	return run(ctx, g.runner, g.osascript, []string{"-"}, []byte(source))
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

const ghosttyListScript = `tell application "Ghostty"
	set tab1 to selected tab of front window
	set out to ""
	repeat with t in terminals of tab1
		set out to out & id of t & linefeed
	end repeat
	return out
end tell
`

func ghosttyCloseScript(id string) string {
	return `tell application "Ghostty"
	close (first terminal of selected tab of front window whose id is "` + id + `")
end tell
`
}

func ghosttySplitScript(command string) string {
	return `tell application "Ghostty"
	set leftPane to focused terminal of selected tab of front window
	set cfg to new surface configuration
	set command of cfg to "` + command + `"
	set wait after command of cfg to true
	set newTerm to split leftPane direction right with configuration cfg
	focus leftPane
	return id of newTerm
end tell
`
}

const ghosttySplitEmptyScript = `tell application "Ghostty"
	set leftPane to focused terminal of selected tab of front window
	set newTerm to split leftPane direction right
	focus leftPane
	return id of newTerm
end tell
`
