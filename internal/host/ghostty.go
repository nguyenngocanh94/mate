package host

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
)

type ghostty struct {
	runner    process.Runner
	osascript string
	selfCols  func() int
	settle    time.Duration

	mu sync.Mutex
	// self is the Console's own terminal, the one focused when the first
	// layout ran; ours is the terminal each column's role was made in.
	self string
	ours map[string]string
}

func newGhostty(opt Options) *ghostty {
	bin := opt.Osascript
	if bin == "" {
		bin = "osascript"
	}
	return &ghostty{
		runner:    opt.runner(),
		osascript: bin,
		selfCols:  opt.SelfCols,
		settle:    150 * time.Millisecond,
		ours:      map[string]string{},
	}
}

func (g *ghostty) Layout(ctx context.Context, cols []Column) error {
	if err := validColumns(cols); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.self == "" {
		id, err := g.terminalID(ctx, ghosttyFocusedScript)
		if err != nil {
			return err
		}
		g.self = id
	}
	present, err := g.present(ctx)
	if err != nil {
		return err
	}
	made := false
	for i, col := range cols {
		if present[col.Role] {
			continue
		}
		from, dir := g.anchor(cols, present, i)
		id, err := g.terminalID(ctx, ghosttySplitScript(from, dir, strings.Join(col.Argv, " ")))
		if err != nil {
			return err
		}
		g.ours[col.Role] = id
		present[col.Role] = true
		made = true
	}
	if !made {
		return nil
	}
	if _, err := g.script(ctx, ghosttyFocusScript(g.self)); err != nil {
		return err
	}
	g.narrow(ctx)
	return nil
}

func (g *ghostty) Close(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var first error
	for role, id := range g.ours {
		if _, err := g.script(ctx, ghosttyCloseScript(id)); err != nil && first == nil {
			first = err
		}
		delete(g.ours, role)
	}
	return first
}

// present lists the terminals of the Console's tab and says which roles
// are there. A terminal in that tab that is neither the Console nor one
// of ours is refused: Ghostty cannot say where it sits, so it may be
// exactly where a column belongs.
func (g *ghostty) present(ctx context.Context) (map[string]bool, error) {
	out, err := g.script(ctx, ghosttyTabScript(g.self))
	if err != nil {
		return nil, err
	}
	byID := map[string]string{}
	for role, id := range g.ours {
		byID[id] = role
	}
	present := map[string]bool{}
	for _, id := range strings.Fields(out) {
		if id == g.self {
			continue
		}
		role, ok := byID[id]
		if !ok {
			return nil, errForeignPane()
		}
		present[role] = true
	}
	return present, nil
}

// anchor is where cols[i] is split from: to the left of the nearest
// column to its right that is still there, else to the right of the one
// before it, or of the Console.
func (g *ghostty) anchor(cols []Column, present map[string]bool, i int) (string, string) {
	for j := i + 1; j < len(cols); j++ {
		if present[cols[j].Role] {
			return g.ours[cols[j].Role], "left"
		}
	}
	if i > 0 {
		return g.ours[cols[i-1].Role], "right"
	}
	return g.self, "right"
}

// narrow evens the columns, then moves the Console's divider left until
// the Console is its design width: Ghostty splits only in halves and
// resizes only by points, so the Console measures itself and corrects.
// Best effort: a failure leaves the columns even.
func (g *ghostty) narrow(ctx context.Context) {
	if g.selfCols == nil {
		return
	}
	if _, err := g.script(ctx, ghosttyActionScript(g.self, "equalize_splits")); err != nil {
		return
	}
	g.wait(ctx)
	have := g.selfCols()
	if have <= 0 {
		return
	}
	total := have * (len(g.ours) + 1)
	want := consoleCols(total)
	perCol := 8.0 // points per cell, corrected by the first move
	for range 3 {
		gap := have - want
		if gap <= 1 && gap >= -1 {
			return
		}
		dir, cells := "left", gap
		if gap < 0 {
			dir, cells = "right", -gap
		}
		points := int(float64(cells) * perCol)
		if _, err := g.script(ctx, ghosttyActionScript(g.self, "resize_split:"+dir+","+strconv.Itoa(points))); err != nil {
			return
		}
		g.wait(ctx)
		now := g.selfCols()
		if moved := have - now; moved != 0 && (moved > 0) == (gap > 0) {
			perCol = float64(points) / float64(abs(moved))
		}
		have = now
	}
}

func (g *ghostty) wait(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(g.settle):
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// terminalID runs a script that answers one terminal id, and checks it is
// one: every later script quotes it.
func (g *ghostty) terminalID(ctx context.Context, source string) (string, error) {
	id, err := g.script(ctx, source)
	if err != nil {
		return "", err
	}
	if _, ok := quoteID(id); !ok {
		return "", observability.NewError(observability.CodeRuntimeUnavailable, "ghostty answered "+strconv.Quote(id)+" for a terminal id")
	}
	return id, nil
}

func (g *ghostty) script(ctx context.Context, source string) (string, error) {
	return run(ctx, g.runner, g.osascript, []string{"-"}, []byte(source))
}

// Terminal ids and column programs reach AppleScript as string contents;
// validColumns keeps the programs plain tokens, and terminalID admits only
// ids that are Ghostty's hex UUIDs, so mustQuote never sees another.
func quoteID(id string) (string, bool) {
	for _, r := range id {
		if !(r == '-' || r >= '0' && r <= '9' || r >= 'A' && r <= 'F' || r >= 'a' && r <= 'f') {
			return "", false
		}
	}
	return `"` + id + `"`, id != ""
}

func mustQuote(id string) string {
	q, ok := quoteID(id)
	if !ok {
		panic(observability.NewError(observability.CodeUsage, "not a Ghostty terminal id: "+id))
	}
	return q
}

const ghosttyFocusedScript = `tell application "Ghostty"
	return id of focused terminal of selected tab of front window
end tell
`

// ghosttyTabScript lists every terminal in the tab holding self.
func ghosttyTabScript(self string) string {
	return `tell application "Ghostty"
	repeat with w in windows
		repeat with t in tabs of w
			set ids to id of every terminal of t
			if ids contains ` + mustQuote(self) + ` then
				set out to ""
				repeat with i in ids
					set out to out & i & linefeed
				end repeat
				return out
			end if
		end repeat
	end repeat
	error "the console's terminal is gone"
end tell
`
}

func ghosttySplitScript(from, direction, command string) string {
	return `tell application "Ghostty"
	set anchor to first terminal whose id is ` + mustQuote(from) + `
	set cfg to new surface configuration
	set command of cfg to "` + command + `"
	set wait after command of cfg to false
	set newTerm to split anchor direction ` + direction + ` with configuration cfg
	return id of newTerm
end tell
`
}

// ghosttyCloseScript closes a terminal if it is still there.
func ghosttyCloseScript(id string) string {
	return `tell application "Ghostty"
	repeat with t in (every terminal whose id is ` + mustQuote(id) + `)
		close t
	end repeat
end tell
`
}

func ghosttyFocusScript(id string) string {
	return `tell application "Ghostty"
	focus (first terminal whose id is ` + mustQuote(id) + `)
end tell
`
}

func ghosttyActionScript(id, action string) string {
	return `tell application "Ghostty"
	perform action "` + action + `" on (first terminal whose id is ` + mustQuote(id) + `)
end tell
`
}
