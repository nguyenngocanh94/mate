package host

import (
	"context"
	"slices"
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
	// tabs is the terminal each review tab's role was made in. Another tab
	// is not a terminal of the Console's tab and must not count as a column
	// when the Console is narrowed.
	self string
	ours map[string]string
	tabs map[string]string
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
		tabs:      map[string]string{},
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

// Tab opens col as a new tab in the Console's window. A tab already open
// for that role is kept, so a later show only swaps the program inside it.
func (g *ghostty) Tab(ctx context.Context, col Column) error {
	if err := validColumns([]Column{col}); err != nil {
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
	if id := g.tabs[col.Role]; id != "" {
		open, err := g.terminalOpen(ctx, id)
		if err != nil {
			return err
		}
		if open {
			return nil
		}
	}
	id, err := g.terminalID(ctx, ghosttyNewTabScript(g.self, strings.Join(col.Argv, " ")))
	if err != nil {
		return err
	}
	g.tabs[col.Role] = id
	return nil
}

// Front selects the review tab. The captain asked to see it.
func (g *ghostty) Front(ctx context.Context, role string) error {
	g.mu.Lock()
	id := g.tabs[role]
	g.mu.Unlock()
	if id == "" {
		return nil
	}
	_, err := g.script(ctx, ghosttySelectTabScript(id))
	return err
}

// terminalOpen reports whether id is still a terminal. The captain closing
// the tab is how a review goes away between shows.
func (g *ghostty) terminalOpen(ctx context.Context, id string) (bool, error) {
	out, err := g.script(ctx, ghosttyTerminalOpenScript(id))
	if err != nil {
		return false, err
	}
	return out == "open", nil
}

func (g *ghostty) Close(ctx context.Context, roles ...string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var first error
	for _, terms := range []map[string]string{g.ours, g.tabs} {
		for role, id := range terms {
			if len(roles) > 0 && !slices.Contains(roles, role) {
				continue
			}
			if _, err := g.script(ctx, ghosttyCloseScript(id)); err != nil && first == nil {
				first = err
			}
			delete(terms, role)
		}
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

// ghosttyNewTabScript opens a tab in the window that holds the Console.
func ghosttyNewTabScript(self, command string) string {
	return `tell application "Ghostty"
	set cfg to new surface configuration
	set command of cfg to "` + command + `"
	set wait after command of cfg to false
	set targetID to ""
	repeat with w in windows
		repeat with tb in tabs of w
			if (id of every terminal of tb) contains ` + mustQuote(self) + ` then
				set targetID to id of w
				exit repeat
			end if
		end repeat
		if targetID is not "" then exit repeat
	end repeat
	if targetID is "" then error "the console's terminal is gone"
	set nt to new tab in (first window whose id is targetID) with configuration cfg
	return id of focused terminal of nt
end tell
`
}

// ghosttySelectTabScript selects the tab that holds id.
func ghosttySelectTabScript(id string) string {
	return `tell application "Ghostty"
	repeat with w in windows
		repeat with tb in tabs of w
			if (id of every terminal of tb) contains ` + mustQuote(id) + ` then
				select tab tb
				return "ok"
			end if
		end repeat
	end repeat
	error "the review tab is gone"
end tell
`
}

func ghosttyTerminalOpenScript(id string) string {
	return `tell application "Ghostty"
	if (count of (every terminal whose id is ` + mustQuote(id) + `)) is 0 then
		return "gone"
	end if
	return "open"
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
