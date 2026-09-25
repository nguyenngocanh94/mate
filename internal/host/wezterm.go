package host

import (
	"context"
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
	bin := opt.WezTerm
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

func (w *wezTerm) split(ctx context.Context, prog []string) (string, error) {
	cli := []string{
		"cli", "split-pane",
		"--pane-id", w.self,
		"--right",
		"--percent", strconv.Itoa(w.percent),
	}
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
