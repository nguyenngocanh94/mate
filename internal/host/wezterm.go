package host

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
)

type wezTerm struct {
	runner process.Runner
	bin    string
	self   string

	mu sync.Mutex
	// ours is the pane each column's role was made in, by this process.
	ours map[string]string
}

func newWezTerm(opt Options) *wezTerm {
	self := opt.Pane
	if self == "" {
		self = opt.getenv("WEZTERM_PANE")
	}
	return &wezTerm{
		runner: opt.runner(),
		bin:    weztermCLI(opt, exec.LookPath, weztermBundles),
		self:   self,
		ours:   map[string]string{},
	}
}

// reviewShare is the review column's share of what the Console leaves, in
// percent: the stage keeps the larger part, since an agent's transcript is
// what the captain reads most.
const reviewShare = 45

func (w *wezTerm) Layout(ctx context.Context, cols []Column) error {
	if err := validColumns(cols); err != nil {
		return err
	}
	if w.self == "" {
		return observability.NewError(observability.CodeUsage, "wezterm columns need WEZTERM_PANE")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	present, err := w.present(ctx, len(cols))
	if err != nil {
		return err
	}
	made := false
	for i, col := range cols {
		if present[col.Role] {
			continue
		}
		id, err := w.makeColumn(ctx, cols, present, i)
		if err != nil {
			return err
		}
		w.ours[col.Role] = id
		present[col.Role] = true
		made = true
	}
	if made {
		return w.activate(ctx)
	}
	return nil
}

// Close kills the columns this process made. WezTerm closes a pane whose
// program exits, so this only settles what the runners left.
func (w *wezTerm) Close(ctx context.Context, roles ...string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for role, id := range w.ours {
		if len(roles) > 0 && !slices.Contains(roles, role) {
			continue
		}
		// A pane already gone makes kill-pane fail; that is the goal met.
		_, _ = run(ctx, w.runner, w.bin, []string{"cli", "kill-pane", "--pane-id", id}, nil)
		delete(w.ours, role)
	}
	return nil
}

// present walks the panes to the right of the Console, one per column, and
// says which roles are there. A pane on that walk that this process did not
// make is refused.
func (w *wezTerm) present(ctx context.Context, n int) (map[string]bool, error) {
	byID := map[string]string{}
	for role, id := range w.ours {
		byID[id] = role
	}
	present := map[string]bool{}
	cur := w.self
	for range n {
		right, err := w.neighbor(ctx, cur)
		if err != nil {
			return nil, err
		}
		if right == "" {
			break
		}
		role, ok := byID[right]
		if !ok {
			return nil, errForeignPane()
		}
		present[role] = true
		cur = right
	}
	return present, nil
}

// makeColumn splits a pane for cols[i]: to the left of the nearest column
// to its right that is still there, else to the right of the one before
// it, or of the Console.
func (w *wezTerm) makeColumn(ctx context.Context, cols []Column, present map[string]bool, i int) (string, error) {
	for j := i + 1; j < len(cols); j++ {
		if present[cols[j].Role] {
			return w.split(ctx, w.ours[cols[j].Role], "--left", []string{"--percent", strconv.Itoa(100 - reviewShare)}, cols[i].Argv)
		}
	}
	if i > 0 {
		return w.split(ctx, w.ours[cols[i-1].Role], "--right", []string{"--percent", strconv.Itoa(reviewShare)}, cols[i].Argv)
	}
	// The first column takes all the Console gives up.
	size := []string{"--percent", "80"}
	if total, ok := w.selfCols(ctx); ok {
		// One cell of the split is WezTerm's divider.
		if rest := total - consoleCols(total) - 1; rest >= columnMinCols {
			size = []string{"--cells", strconv.Itoa(rest)}
		}
	}
	return w.split(ctx, w.self, "--right", size, cols[i].Argv)
}

func (w *wezTerm) neighbor(ctx context.Context, pane string) (string, error) {
	return run(ctx, w.runner, w.bin, []string{"cli", "get-pane-direction", "--pane-id", pane, "Right"}, nil)
}

func (w *wezTerm) split(ctx context.Context, from, side string, size, prog []string) (string, error) {
	cli := append([]string{"cli", "split-pane", "--pane-id", from, side}, size...)
	cli = append(append(cli, "--"), prog...)
	return run(ctx, w.runner, w.bin, cli, nil)
}

func (w *wezTerm) activate(ctx context.Context) error {
	_, err := run(ctx, w.runner, w.bin, []string{
		"cli", "activate-pane", "--pane-id", w.self,
	}, nil)
	return err
}

// selfCols is the console pane's width now, from `cli list`: before the
// first column exists, the whole window.
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

// weztermBundles are where WezTerm.app keeps its CLI.
var weztermBundles = []string{
	"/Applications/WezTerm.app/Contents/MacOS/wezterm",
	filepath.Join(os.Getenv("HOME"), "Applications/WezTerm.app/Contents/MacOS/wezterm"),
}

// weztermCLI finds the `wezterm` CLI. WezTerm.app puts none on PATH; every
// pane it spawns gets WEZTERM_EXECUTABLE_DIR, where the CLI sits beside the
// GUI (the WEZTERM_EXECUTABLE it also sets is wezterm-gui, which has no
// `cli`). A pane that lost that variable - tmux or ssh inside WezTerm -
// falls back to PATH, then to the app bundle. The bare name is last, so a
// failure still names what was looked for.
func weztermCLI(opt Options, lookPath func(string) (string, error), bundles []string) string {
	if opt.WezTerm != "" {
		return opt.WezTerm
	}
	if dir := opt.getenv("WEZTERM_EXECUTABLE_DIR"); dir != "" {
		if p := filepath.Join(dir, "wezterm"); isExecutable(p) {
			return p
		}
	}
	if p, err := lookPath("wezterm"); err == nil {
		return p
	}
	for _, p := range bundles {
		if isExecutable(p) {
			return p
		}
	}
	return "wezterm"
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}
