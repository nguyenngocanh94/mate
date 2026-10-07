package host

import (
	"context"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// Column is one sibling pane of the Console, left to right. Role names it
// ("stage", "review"); Argv is the program it runs for its whole life.
type Column struct {
	Role string
	Argv []string
}

func validColumns(cols []Column) error {
	if len(cols) == 0 {
		return observability.NewError(observability.CodeUsage, "a layout needs at least one column")
	}
	seen := map[string]bool{}
	for _, c := range cols {
		if c.Role == "" || seen[c.Role] || len(c.Argv) == 0 {
			return observability.NewError(observability.CodeUsage, "each column needs its own role and a program")
		}
		seen[c.Role] = true
		for _, a := range c.Argv {
			// Ghostty takes the program as one shell string: every word
			// must survive it unquoted.
			if a == "" || strings.ContainsAny(a, " \t\n\"'\\$`;&|<>()") {
				return observability.NewError(observability.CodeUsage, "a column's program words must be plain tokens: "+a)
			}
		}
	}
	return nil
}

// Host lays out the Console's columns and its tabs in the same window.
type Host interface {
	// Layout makes sure every column in cols exists to the right of the
	// Console, in order. A column this process made that is still there is
	// kept untouched, one that is missing is made, and a pane that is not
	// one of ours is refused rather than replaced: the captain's nvim or
	// shell stays. A column of ours that cols leaves out is left as it is;
	// Close removes it.
	Layout(ctx context.Context, cols []Column) error
	// Tab opens col as a new tab in the Console's window, not a split and
	// not another window. A tab this process already opened for col.Role
	// that is still open is left as it is. The new tab is selected. Close
	// removes it.
	Tab(ctx context.Context, col Column) error
	// Front selects the tab this process opened for role. A role with no
	// tab is left alone.
	Front(ctx context.Context, role string) error
	// Close closes the named columns and tabs this process made, or all of
	// them when no role is named. Ghostty keeps a pane whose program has
	// exited, so a column's runner ending is not enough.
	Close(ctx context.Context, roles ...string) error
}

// errForeignPane is the refusal when a pane where a column belongs is not
// one this process made.
func errForeignPane() error {
	return observability.NewError(
		observability.CodeStateConflict,
		"the pane to the right is not one of mate's columns; close it or leave it empty",
	)
}

// Console widths (the console design): mate is the left ~20% of the
// window, never narrower than consoleMinCols nor wider than consoleMaxCols.
const (
	consoleMinCols = 40
	consoleMaxCols = 48
	columnMinCols  = 10
)

// consoleCols is the Console's width in a window total columns wide.
func consoleCols(total int) int { return min(max(total/5, consoleMinCols), consoleMaxCols) }
