package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nguyenngocanh94/mate/internal/tool"
)

// recall is the project's work in a Mate's context at startup: a bounded
// read of the tracker itself, never of the viewer's export, which may be
// stale.
type recall struct{}

// recallLimit is how many issues of each kind recall lists.
const recallLimit = "10"

// issue is the part of a bd issue recall prints.
type issue struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Type     string `json:"issue_type"`
	Priority int    `json:"priority"`
}

// Render lists up to ten active or blocked issues and ten ready ones. A
// project with no tracker, or nothing active or ready, is absent: recall
// never makes a tracker. The caller's ctx bounds the reads.
func (recall) Render(ctx context.Context, env tool.CommandEnv, maxBytes int) (string, bool, error) {
	t, err := open(env)
	if err != nil {
		return "", false, err
	}
	ok, err := t.initialized()
	if err != nil || !ok {
		return "", false, err
	}
	unlock, err := t.lock(ctx)
	if err != nil {
		return "", false, err
	}
	defer unlock()
	active, err := t.read(ctx, "list", "--status", "in_progress,blocked", "--limit", recallLimit, "--sort", "priority", "--brief", "--json", "--readonly")
	if err != nil {
		return "", false, err
	}
	ready, err := t.read(ctx, "ready", "--limit", recallLimit, "--exclude-type", "epic", "--brief", "--json", "--readonly")
	if err != nil {
		return "", false, err
	}
	if len(active)+len(ready) == 0 {
		return "", false, nil
	}
	lines := []string{fmt.Sprintf("Beads work (up to 10 active/blocked and 10 ready; mate tasks %s --list):\n", t.name)}
	for _, i := range active {
		lines = append(lines, fmt.Sprintf("  %s [%s P%d] %s\n", i.ID, i.Status, i.Priority, clip(i.Title, 120)))
	}
	for _, i := range ready {
		lines = append(lines, fmt.Sprintf("  %s [ready P%d] %s\n", i.ID, i.Priority, clip(i.Title, 120)))
	}
	var b strings.Builder
	for _, l := range lines {
		if maxBytes > 0 && b.Len()+len(l) > maxBytes {
			break
		}
		b.WriteString(l)
	}
	return b.String(), true, nil
}

// read runs one read-only bd query and decodes its JSON.
func (t project) read(ctx context.Context, args ...string) ([]issue, error) {
	var out, diagnostic bytes.Buffer
	if err := t.run(ctx, tracker, args, nil, &out, &diagnostic); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(diagnostic.String()))
	}
	var issues []issue
	err := json.Unmarshal(out.Bytes(), &issues)
	return issues, err
}

// clip flattens s to one line and bounds it to n runes.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
