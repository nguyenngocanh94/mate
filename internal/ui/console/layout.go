package console

// The layout engine (design I, "3 Chrome" and "5 Panes"). mate is the left
// ~20% pane of the captain's terminal: 32 to 48 columns, one stack of panes
// top to bottom - list, detail, box - never side by side, and a footer.
// Sizes come from tea.WindowSizeMsg through Model.w/Model.h and nowhere
// else. plan is the one function that places the panes; the renderer and
// the mouse hit tests both read it, so a click resolves against the frame
// that was drawn.
const (
	// minCols and minRows: below either, the too-small screen and nothing
	// else (design H4).
	minCols = 32
	minRows = 14
	// tinyCols: under this the too-small screen is one line.
	tinyCols = 20
	// tallRows: at 30 rows and more every row is two lines and the key line
	// shows; below, rows are one line and the status line ends in "? keys".
	tallRows = 30
	// stackRows: below 20 rows there is no room to stack list and detail,
	// so Tab swaps the detail in place of the list.
	stackRows = 20
	// wideCols: at 48 columns a row's status moves up to line 1 and line 2
	// gains the agent id and age (design J).
	wideCols = 48
	// comfortableRows: from here the box shows its top item in full even
	// when it is not focused (design J).
	comfortableRows = 44
	// boxFocusDetailRows: a focused box leaves detail its first 8 rows,
	// rule included (design D).
	boxFocusDetailRows = 8
)

// slotKind is what a region of the frame holds.
type slotKind int

const (
	slotList slotKind = iota
	slotDetail
	slotBox
	slotSheet
)

// slot is one pane: its rule on row top, its body below, h rows in all.
type slot struct {
	kind   slotKind
	top, h int
}

// body is the pane's first row under its rule, and how many rows follow.
func (s slot) body() (top, h int) { return s.top + 1, s.h - 1 }

// framePlan is one frame's geometry.
type framePlan struct {
	w, h     int
	tooSmall bool
	tall     bool
	wide     bool
	// footer is the rows under the panes: the status line, and the key line
	// when the frame is tall.
	footer int
	slots  []slot
}

func (p framePlan) slot(k slotKind) (slot, bool) {
	for _, s := range p.slots {
		if s.kind == k {
			return s, true
		}
	}
	return slot{}, false
}

// statusRow is the status line's frame row.
func (p framePlan) statusRow() int { return p.h - p.footer }

// sizeClass is the part of the plan that depends on the window alone.
func sizeClass(w, h int) framePlan {
	p := framePlan{w: w, h: h}
	p.tooSmall = w < minCols || h < minRows
	p.tall = h >= tallRows
	p.wide = w >= wideCols
	p.footer = 1
	if p.tall {
		p.footer = 2
	}
	return p
}

// plan places the panes for the model's current state.
func (m Model) plan() framePlan {
	p := sizeClass(m.w, m.h)
	if p.tooSmall || m.phase != phaseReady {
		return p
	}
	body := m.h - p.footer
	need := m.paneNeeds(p)
	if m.h < stackRows {
		p.slots = m.planUnstacked(p, body, need)
	} else {
		p.slots = m.planStacked(p, body, need)
	}
	return p
}

// paneNeeds is each pane's natural height, rule included; 0 for a pane the
// frame does not draw.
type paneNeeds struct {
	list, detail, box, sheet int
}

func (m Model) paneNeeds(p framePlan) paneNeeds {
	n := paneNeeds{list: 1 + m.listNeed(p)}
	if m.hasDetail() {
		n.detail = 1 + len(m.detailLines(p))
	}
	if s := m.sheetOpen(); s != sheetNone {
		n.sheet = 1 + len(m.sheetLines(p))
	} else if items := m.boxItems(); len(items) > 0 {
		n.box = 1 + len(m.boxLines(p, m.boxMode(p)).lines)
	}
	return n
}

// planStacked is the 20-row-and-taller stack. The lower pane (the box, or
// a sheet in its place) takes its natural height first, capped so the list
// keeps room for its selected row; the list then takes what its content
// needs, but at least half of the rest when it has that much; detail fills
// what is left. A focused box inverts the last two: detail keeps its first
// 8 rows and the box grows (design D).
func (m Model) planStacked(p framePlan, body int, need paneNeeds) []slot {
	listFloor := minInt(need.list, maxInt(3, body/3))
	lower, lowerKind := need.box, slotBox
	if need.sheet > 0 {
		lower, lowerKind = need.sheet, slotSheet
	}
	lower = minInt(lower, max0(body-listFloor))

	var listH, detailH int
	switch {
	case lowerKind == slotSheet:
		// A sheet keeps the list whole when it can - the sheet acts on the
		// selected row, and the rows around it are its context - and
		// detail takes what is left, if that is a pane's worth.
		rest := body - lower
		listH = m.usedListRows(p, minInt(need.list, rest))
		detailH = rest - listH
		if need.detail == 0 || detailH < 2 {
			listH, detailH = rest, 0
		}
	case lowerKind == slotBox && m.focus == paneBox && need.box > 0:
		detailH = minInt(need.detail, boxFocusDetailRows)
		listH = minInt(need.list, maxInt(listFloor, body-detailH-minInt(need.box, 4)))
		listH = m.usedListRows(p, listH)
		detailH = minInt(detailH, max0(body-listH-2))
		lower = body - listH - detailH
	default:
		rest := body - lower
		listMin := minInt(need.list, (rest+1)/2)
		detailH = minInt(need.detail, max0(rest-listMin))
		listH = m.usedListRows(p, minInt(need.list, rest-detailH))
		detailH = 0
		if need.detail > 0 {
			detailH = rest - listH
		}
		if need.detail == 0 || detailH < 2 {
			// No detail pane: the list keeps the rows, and whatever its
			// content does not use stays blank under it.
			listH, detailH = rest, 0
		}
	}
	out := make([]slot, 0, 3)
	top := 0
	add := func(k slotKind, h int) {
		if h <= 0 {
			return
		}
		out = append(out, slot{kind: k, top: top, h: h})
		top += h
	}
	add(slotList, listH)
	add(slotDetail, detailH)
	if lower > 1 {
		add(lowerKind, lower)
	}
	return out
}

// planUnstacked is under 20 rows: one main pane - the list, or detail when
// Tab swapped it in - with a sheet or the box under it when there is room.
func (m Model) planUnstacked(p framePlan, body int, need paneNeeds) []slot {
	main := slotList
	if m.detail && need.detail > 0 {
		main = slotDetail
	}
	lower, lowerKind := need.box, slotBox
	if need.sheet > 0 {
		lower, lowerKind = need.sheet, slotSheet
	}
	const mainFloor = 3
	lower = minInt(lower, max0(body-mainFloor))
	if lowerKind == slotBox {
		// The box stays compact here: its rule and one item.
		lower = minInt(lower, 3)
	}
	out := []slot{{kind: main, top: 0, h: body - lower}}
	if lower > 1 {
		out = append(out, slot{kind: lowerKind, top: body - lower, h: lower})
	}
	return out
}

// usedListRows is how many rows the list pane actually fills at most h
// rows, rule included: items are drawn whole, so a windowed list can leave
// a row unused, and that row belongs to the pane under it.
func (m Model) usedListRows(p framePlan, h int) int {
	if h <= 1 {
		return h
	}
	return 1 + len(m.listBody(p, h-1).lines)
}

// hasDetail reports whether the frame has an object to detail: the
// selected row, on either level.
func (m Model) hasDetail() bool {
	_, ok := m.selectedRow()
	return ok
}
