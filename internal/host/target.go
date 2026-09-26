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

// Host lays out the Console's columns.
type Host interface {
	// Layout makes sure every column exists to the right of the Console,
	// in order. A column this process made that is still there is kept
	// untouched, one that is missing is made again, and a pane that is not
	// one of ours is refused rather than replaced: the captain's nvim or
	// shell stays. cols is the whole layout, the same on every call.
	Layout(ctx context.Context, cols []Column) error
	// Close closes every column this process made that is still there.
	// Ghostty keeps a pane whose program has exited, so a column's runner
	// ending is not enough.
	Close(ctx context.Context) error
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
