package host

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
)

type wezTerm struct {
	runner  process.Runner
	bin     string
	herdr   string
	self    string
	percent int

	mu   sync.Mutex
	last StageHandle
}

func newWezTerm(opt Options) *wezTerm {
	self := opt.Pane
	if self == "" {
		self = opt.getenv("WEZTERM_PANE")
	}
	// WezTerm.app puts no `wezterm` on PATH. Every pane it spawns gets
	// WEZTERM_EXECUTABLE_DIR, where the CLI sits beside the GUI (the
	// WEZTERM_EXECUTABLE it also sets is wezterm-gui, which has no `cli`).
	bin := opt.WezTerm
	if bin == "" {
		if dir := opt.getenv("WEZTERM_EXECUTABLE_DIR"); dir != "" {
			bin = filepath.Join(dir, "wezterm")
		}
	}
	if bin == "" {
		bin = "wezterm"
	}
	return &wezTerm{
		runner:  opt.runner(),
		bin:     bin,
		herdr:   opt.herdr(),
		self:    self,
		percent: opt.percent(),
	}
}

func (w *wezTerm) EnsureSplit(ctx context.Context) (StageHandle, error) {
	if w.self == "" {
		return StageHandle{}, observability.NewError(observability.CodeUsage, "wezterm stage needs WEZTERM_PANE")
	}
	right, last, err := w.rightAndLast(ctx)
	if err != nil {
		return StageHandle{}, err
	}
	if right != "" && right == last.PaneID {
		return last, nil
	}
	if right != "" {
		return StageHandle{}, errForeignPane()
	}
	id, err := w.split(ctx, nil)
	if err != nil {
		return StageHandle{}, err
	}
	return w.commit(ctx, id)
}

func (w *wezTerm) Stage(ctx context.Context, target StageTarget) (StageHandle, error) {
	if err := target.Validate(); err != nil {
		return StageHandle{}, err
	}
	if w.self == "" {
		return StageHandle{}, observability.NewError(observability.CodeUsage, "wezterm stage needs WEZTERM_PANE")
	}
	right, last, err := w.rightAndLast(ctx)
	if err != nil {
		return StageHandle{}, err
	}
	if right != "" && right != last.PaneID {
		return StageHandle{}, errForeignPane()
	}
	if right != "" {
		if err := w.kill(ctx, right); err != nil {
			return StageHandle{}, err
		}
	}
	herdr, args := attachArgs(w.herdr, target.Session, target.AgentName)
	id, err := w.split(ctx, append([]string{herdr}, args...))
	if err != nil {
		return StageHandle{}, err
	}
	return w.commit(ctx, id)
}

func (w *wezTerm) rightAndLast(ctx context.Context) (string, StageHandle, error) {
	right, err := w.neighbor(ctx)
	if err != nil {
		return "", StageHandle{}, err
	}
	w.mu.Lock()
	last := w.last
	w.mu.Unlock()
	return right, last, nil
}

func (w *wezTerm) commit(ctx context.Context, id string) (StageHandle, error) {
	if err := w.activate(ctx); err != nil {
		return StageHandle{}, err
	}
	h := StageHandle{PaneID: id}
	w.mu.Lock()
	w.last = h
	w.mu.Unlock()
	return h, nil
}

func (w *wezTerm) neighbor(ctx context.Context) (string, error) {
	out, err := run(ctx, w.runner, w.bin, []string{
		"cli", "get-pane-direction", "--pane-id", w.self, "Right",
	}, nil)
	if err != nil {
		return "", err
	}
	return out, nil
}

func (w *wezTerm) kill(ctx context.Context, pane string) error {
	_, err := run(ctx, w.runner, w.bin, []string{
		"cli", "kill-pane", "--pane-id", pane,
	}, nil)
	return err
}

// Console widths (the console design): mate is the left ~20% of the
// window, never narrower than consoleMinCols nor wider than consoleMaxCols.
const (
	consoleMinCols = 40
	consoleMaxCols = 48
	stageMinCols   = 10
)

func (w *wezTerm) split(ctx context.Context, prog []string) (string, error) {
	size := []string{"--percent", strconv.Itoa(w.percent)}
	if cols, ok := w.selfCols(ctx); ok {
		keep := min(max(cols/5, consoleMinCols), consoleMaxCols)
		// One cell of the split is WezTerm's divider.
		if stage := cols - keep - 1; stage >= stageMinCols {
			size = []string{"--cells", strconv.Itoa(stage)}
		}
	}
	cli := append([]string{
		"cli", "split-pane",
		"--pane-id", w.self,
		"--right",
	}, size...)
	if len(prog) > 0 {
		cli = append(cli, "--")
		cli = append(cli, prog...)
	}
	return run(ctx, w.runner, w.bin, cli, nil)
}

func (w *wezTerm) activate(ctx context.Context) error {
	_, err := run(ctx, w.runner, w.bin, []string{
		"cli", "activate-pane", "--pane-id", w.self,
	}, nil)
	return err
}

// selfCols is the console pane's width now, from `cli list`. A kill of the
// last stage has just given the console back the whole window, so this is
// read at every split rather than once.
func (w *wezTerm) selfCols(ctx context.Context) (int, bool) {
	out, err := run(ctx, w.runner, w.bin, []string{"cli", "list", "--format", "json"}, nil)
	if err != nil {
		return 0, false
	}
	var panes []struct {
		PaneID int `json:"pane_id"`
		Size   struct {
			Cols int `json:"cols"`
		} `json:"size"`
	}
	if err := json.Unmarshal([]byte(out), &panes); err != nil {
		return 0, false
	}
	for _, p := range panes {
		if strconv.Itoa(p.PaneID) == w.self {
			return p.Size.Cols, p.Size.Cols > 0
		}
	}
	return 0, false
}
