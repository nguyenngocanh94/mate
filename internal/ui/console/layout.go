package console

// The layout engine. Every size the Console draws with comes from
// tea.WindowSizeMsg through Model.w/Model.h and this function; nothing in
// the package asks the terminal how big it is (cmd/matev2/console.go already
// refuses a non-terminal before the program starts, and asking again here
// would answer for the process rather than for this invocation).
const (
	// minCols and minRows are the smallest frame the six-line chrome plus a
	// usable main region fits into. Below either, the Console draws the
	// too-small screen and answers only q.
	minCols = 60
	minRows = 16

	// chromeRows is the fixed six-line frame: header, breadcrumb, rule,
	// ... , rule, message, keys. The main region is exactly h - chromeRows.
	chromeRows = 6

	// inspectorWide/inspectorNarrow are the two inspector widths. 50 is
	// chosen so the value column is 31 cells: comfortable headroom over any
	// id mate actually generates (internal/domain/id.go: prefix_ plus 16 hex
	// characters, 21 cells for "crew_" - this codebase does not generate
	// ULIDs), so an id never splits across two lines at the narrower
	// breakpoint.
	inspectorWide   = 60
	inspectorNarrow = 50

	// labelWidth is the inspector's label column: 16 cells of dim label,
	// one blank, then the value; continuation lines indent past both.
	labelWidth = 16
)

// frameLayout is the geometry of one frame. Body and the pane widths are
// only meaningful when TooSmall is false; the too-small screen has no
// panes and no main region.
type frameLayout struct {
	Cols, Rows int
	// Inspector is 0 when the frame has a single main region (60-99 cols),
	// where Tab opens Detail over that region instead of splitting it.
	Inspector int
	// List is the main region's width: the full frame when there is no
	// inspector, otherwise everything left of the divider column.
	List int
	// Body is the main region's height, h - chromeRows.
	Body     int
	TooSmall bool
}

// layout resolves the design's breakpoints for a w x h terminal.
//
//	>= 140 cols: list + inspector of 60 (value column 41)
//	100-139:     list + inspector of 50 (value column 31)
//	60-99:       one main region; Tab opens Detail over it
//	< 60 cols or < 16 rows: the too-small screen
func layout(w, h int) frameLayout {
	l := frameLayout{Cols: w, Rows: h, Body: h - chromeRows}
	l.TooSmall = w < minCols || h < minRows
	switch {
	case w >= 140:
		l.Inspector = inspectorWide
	case w >= 100:
		l.Inspector = inspectorNarrow
	}
	l.List = w
	if l.Inspector > 0 {
		// One cell for the divider the horizontal rules tee into.
		l.List = w - l.Inspector - 1
	}
	return l
}

// split reports whether this frame draws two panes: an inspector column
// exists and the main region is not given over to a full-width view
// (Detail, or a loading/error screen that has no rows to inspect).
func (l frameLayout) split(hasInspector bool) bool {
	return !l.TooSmall && l.Inspector > 0 && hasInspector
}

// valueWidth is the inspector's value column: the pane minus one leading
// space, the 16-cell label, the blank between label and value, and one
// trailing space. 41 cells at 140+, 31 at 100-139.
func (l frameLayout) valueWidth() int {
	if l.Inspector <= 0 {
		return 0
	}
	return l.Inspector - labelWidth - 3
}

// detailValueWidth is valueWidth for Detail, which takes the whole main
// region and so has no fixed pane width of its own.
func (l frameLayout) detailValueWidth() int {
	return l.Cols - labelWidth - 3
}

// listRows is how many rows the list pane can show: the main region minus
// the one line the pane title and its column headers share. The list's
// scroll offset is clamped against this rather than against Body, so the
// selection the model keeps visible is the selection the pane actually
// draws.
func (l frameLayout) listRows() int {
	if l.Body <= 1 {
		return 0
	}
	return l.Body - 1
}

// page is how far PgUp/PgDn moves: a screenful less the three lines of
// overlap that keep the reader's place in a long list.
func (l frameLayout) page() int {
	if l.Body-3 < 1 {
		return 1
	}
	return l.Body - 3
}
