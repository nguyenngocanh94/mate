package console

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The list pane: the left-hand region showing Projects, then a Project's
// Mate row plus its Crews. Columns are fixed widths
// (design/mate-console-design-notes.html, "Trong pane"); the Title (and, at
// the Project level, the Agent) column takes whatever is left.
//
// A pane title ("PROJECTS 3", "MATE", "CREWS 2") carries
// the same focus signal as the inspector's own title (paneTitleSpan): accent
// when the list has focus, dim otherwise. Every other column header is a
// plain columnHeaderSpan - dim regardless of focus, and never selectable.
const (
	colMateSummary = 24 // Workspace level: this Project's Mate, harness + recorded status, or "none"
	colCrewsCount  = 7  // Workspace level: this Project's Crew count
	colAttention   = 14
	colUpdated     = 10
	colHarness     = 11
	colMateStatus  = 11 // the Project's Mate row: recorded status
	// colMode is the Project's Mate row: communication mode (mvp.md
	// section 5). "supervised" is the longest value it ever holds. Its
	// twelve cells came out of the three columns beside it - each of which
	// held six or more cells of padding over its longest value - rather
	// than out of the agent name, which is the one cell on this row that
	// has to be able to show a whole `mate-<project>` at 120 columns.
	colMode    = 12
	colBinding = 12 // the Project's Mate row: binding status
	colStatus  = 17 // a Crew row: recorded status
	colCrewID  = 18 // a Crew row: abbreviated id

	// listWideMin is the list pane's own width (not the terminal's), at or
	// above which the UPDATED column appears.
	listWideMin = 90
)

func wideList(w int) bool { return w >= listWideMin }

// fitCell truncates spans to at most w cells with a hard cut - no ellipsis
// marker, because every value placed through this is a short fixed word
// (a status, a harness, a count) that is expected to fit its column - and
// pads short content with plain spaces to exactly w cells.
func fitCell(spans []span, w int) []span {
	if w <= 0 {
		return nil
	}
	out := make([]span, 0, len(spans)+1)
	used := 0
	for _, s := range spans {
		if used >= w {
			break
		}
		text := s.text
		if used+cells(text) > w {
			text = cutCells(text, w-used)
		}
		if text == "" {
			continue
		}
		used += cells(text)
		out = append(out, span{text: text, style: s.style, plain: s.plain})
	}
	if used < w {
		out = append(out, span{text: strings.Repeat(" ", w-used), plain: true})
	}
	return out
}

// titleCell is fitCell for the one column allowed to cut a value short: a
// Project name or a Task title too long for the list truncates with a
// trailing ellipsis rather than running into the next column.
func titleCell(text string, w int, style lipgloss.Style, g glyphSet) []span {
	return fitCell([]span{{text: truncateEnd(text, w, g), style: style}}, w)
}

// listLine is one line of the list pane before selection styling is
// applied. rowIdx is the index into currentRows() this line stands for, or
// -1 for a header or a blank separator - neither is selectable, and their
// marker cell always stays blank.
type listLine struct {
	rowIdx int
	build  func(selected, focused bool) *line
}

func headerListLine(build func(focused bool) *line) listLine {
	return listLine{rowIdx: -1, build: func(_ bool, focused bool) *line { return build(focused) }}
}

func blankListLine() listLine {
	return listLine{rowIdx: -1, build: func(_ bool, _ bool) *line { return newLine() }}
}

func rowListLine(idx int, build func(selected, focused bool) *line) listLine {
	return listLine{rowIdx: idx, build: build}
}

// listLines is the list pane: a header (or two, at the Project level) plus
// the rows in view, windowed to h lines with an indicator for whatever is
// off screen above or below.
func (m Model) listLines(w, h int) []*line {
	if h <= 0 {
		return nil
	}
	rows := m.currentRows()
	items := m.buildListItems(rows, w)
	f := m.cur()
	focused := m.focus == paneList

	want := indexOfListItem(items, f.sel)
	// f.top is the model's own row-space scroll hint (model.go's clampTop,
	// applied with a budget that assumes one header line - true at every
	// level except the Project's, which interleaves a second header and a
	// blank separator). Translated naively, row 0's item position is 1 (the
	// header sits at item 0), which would report the header itself as
	// "scrolled past" the moment the reader is sitting at the very top of
	// the row list. f.top == 0 means "no deliberate scroll yet", so it
	// forces the window to start at item 0 instead; a genuine scroll (top >
	// 0) is translated by position and self-corrects through clampTop/
	// window regardless of the translation's precision.
	topHint := 0
	if f.top > 0 {
		if hint := indexOfListItem(items, f.top); hint >= 0 {
			topHint = hint
		}
	}
	start, end, needUp, needDown := windowListItems(len(items), h, topHint, want)

	out := make([]*line, 0, h)
	if needUp {
		out = append(out, newLine().pad(2).add(fmt.Sprintf("%s %d more", m.g.Up, start), m.p.Dim))
	}
	for i := start; i < end; i++ {
		it := items[i]
		selected := it.rowIdx >= 0 && it.rowIdx == f.sel
		out = append(out, it.build(selected, focused))
	}
	if needDown {
		out = append(out, newLine().pad(2).add(fmt.Sprintf("%s %d more", m.g.Down, len(items)-end), m.p.Dim))
	}
	return out
}

// indexOfListItem finds the display position of the selectable item naming
// rowIdx, or -1 when there is none (an empty list, or a hint that no longer
// resolves - windowListItems treats -1 as "nothing in particular to keep
// visible" rather than an error).
func indexOfListItem(items []listLine, rowIdx int) int {
	for i, it := range items {
		if it.rowIdx == rowIdx {
			return i
		}
	}
	return -1
}

// windowListItems is clampTop/window (model.go) generalized to a display
// item list that can contain non-selectable header and separator lines: the
// same reserve-a-line-for-each-indicator-actually-needed convergence, over
// item positions rather than row positions.
func windowListItems(total, h, topHint, want int) (start, end int, needUp, needDown bool) {
	if h <= 0 {
		return 0, 0, false, false
	}
	if total <= h {
		return 0, total, false, false
	}
	lines := h
	for i := 0; i < 2; i++ {
		top := clampTop(topHint, maxInt(want, 0), total, lines)
		s, e := window(total, top, lines)
		need := 0
		if s > 0 {
			need++
		}
		if e < total {
			need++
		}
		if h-need == lines {
			return s, e, s > 0, e < total
		}
		lines = h - need
		if lines < 1 {
			lines = 1
		}
	}
	top := clampTop(topHint, maxInt(want, 0), total, lines)
	s, e := window(total, top, lines)
	return s, e, s > 0, e < total
}

// buildListItems dispatches to the current frame's column layout.
func (m Model) buildListItems(rows []row, w int) []listLine {
	switch m.cur().kind {
	case frameProject:
		return m.projectListItems(rows, w)
	default:
		return m.workspaceListItems(rows, w)
	}
}

// ---------- level 1: Workspace -> Projects ----------

func (m Model) workspaceListItems(rows []row, w int) []listLine {
	g, p := m.g, m.p
	wide := wideList(w)
	iw := w - 2
	fixed := colMateSummary + colCrewsCount + colAttention
	if wide {
		fixed += colUpdated
	}
	titleW := iw - fixed

	items := []listLine{headerListLine(func(focused bool) *line {
		l := newLine().addSpans(rowPrefix(false, false, g, p)...)
		l.addSpans(fitCell([]span{paneTitleSpan(fmt.Sprintf("PROJECTS  %d", len(rows)), focused, p)}, titleW)...)
		l.addSpans(fitCell([]span{columnHeaderSpan("MATE", p)}, colMateSummary)...)
		l.addSpans(fitCell([]span{columnHeaderSpan("CREWS", p)}, colCrewsCount)...)
		l.addSpans(fitCell([]span{columnHeaderSpan("ATTENTION", p)}, colAttention)...)
		if wide {
			l.addSpans(fitCell([]span{columnHeaderSpan("UPDATED", p)}, colUpdated)...)
		}
		return l
	})}

	if len(rows) == 0 {
		items = append(items, blankListLine())
		items = append(items, headerListLine(func(bool) *line {
			return newLine().pad(2).add("No projects recorded in workspace "+m.workspaceDisplayName()+".", p.Fg)
		}))
		items = append(items, headerListLine(func(bool) *line {
			return newLine().pad(2).add("Press n to add a Project, then s to create its Mate.", p.Dim)
		}))
		return items
	}

	for _, r := range rows {
		if r.idx < 0 || r.idx >= len(m.tree.Projects) {
			continue
		}
		proj := m.tree.Projects[r.idx]
		idx := r.idx
		items = append(items, rowListLine(idx, func(selected, focused bool) *line {
			l := selectRow(newLine(), selected, p)
			l.addSpans(rowPrefix(selected, focused, g, p)...)
			l.addSpans(titleCell(proj.Name, titleW, p.Fg, g)...)
			l.addSpans(fitCell(workspaceMateSummarySpans(proj.Mate, g, p), colMateSummary)...)
			l.addSpans(fitCell([]span{{text: fmt.Sprint(len(proj.Crews)), style: p.Fg}}, colCrewsCount)...)
			l.addSpans(fitCell(projectAttentionSpans(proj.Attention, p), colAttention)...)
			if wide {
				l.addSpans(fitCell(updatedSpans(proj.Mate.LastEvent, p), colUpdated)...)
			}
			return l
		}))
	}
	return items
}

// workspaceMateSummarySpans is the Workspace level's one-cell Mate summary:
// harness and recorded status when the designation is Known, "none" when
// there is legitimately no Mate, and an honest "unknown" when the
// designation itself could not be read - never blank, never rendered as if
// it were Absent. Distinct from seams.go's mateSummarySpans, the inspector's
// own fuller one-line Project-block summary (agent name included).
func workspaceMateSummarySpans(mate query.MateNode, g glyphSet, p palette) []span {
	switch mate.Designated.State {
	case query.Known:
		v := mate.Designated.Value
		return []span{
			{text: string(v.HarnessKind), style: p.Dim},
			{text: " " + g.Dot + " ", style: p.Dim},
			statusSpan(string(v.Status), p),
		}
	case query.Absent:
		return []span{{text: "none", style: p.Dim}}
	default:
		return []span{unknownMarkSpan("unknown", p)}
	}
}

// projectAttentionSpans renders a Field[ProjectAttention]: the Project's own
// Kind when it needs attention itself (no Mate, a stale Mate binding, an
// unreadable Mate), otherwise a Crew count when one or more Crews need
// attention, otherwise nothing - Absent draws a blank cell, never "none".
func projectAttentionSpans(a query.Field[query.ProjectAttention], p palette) []span {
	switch a.State {
	case query.Known:
		v := a.Value
		switch {
		case v.Kind == query.AttentionUnreadable:
			return []span{unknownMarkSpan(string(v.Kind), p)}
		case v.Kind != "":
			return []span{attentionSpan(string(v.Kind), p)}
		case v.CrewsNeedingAttention > 0:
			return []span{attentionSpan(plural(v.CrewsNeedingAttention, "crew", "crews"), p)}
		default:
			return nil
		}
	case query.Absent:
		return nil
	default:
		return []span{unknownMarkSpan("unknown", p)}
	}
}

// attentionSpans is projectAttentionSpans' per-Crew twin: a plain
// Field[Attention] carries one Kind directly rather than a count, and a
// Crew's own worst fact (crewAttention, internal/query/attention.go) can be
// a failure rather than merely something to look at, so that one Kind is
// drawn in red rather than amber.
func attentionSpans(a query.Field[query.Attention], p palette) []span {
	switch a.State {
	case query.Known:
		switch a.Value.Kind {
		case query.AttentionFailed:
			return []span{failureSpan(string(a.Value.Kind), p)}
		case query.AttentionUnreadable:
			return []span{unknownMarkSpan(string(a.Value.Kind), p)}
		default:
			return []span{attentionSpan(string(a.Value.Kind), p)}
		}
	case query.Absent:
		return nil
	default:
		return []span{unknownMarkSpan("unknown", p)}
	}
}

// updatedSpans is the UPDATED column's value: the time of a Known last
// event, a blank cell for a legitimate Absent (there is no event to show),
// and an honest "? unknown" when the event read itself failed. Collapsing
// Absent and Unknown to the same blank cell would render a read failure as
// "there is no update to show" - the same unsupported negative the rest of
// this file refuses to render.
func updatedSpans(e query.Field[query.EventValue], p palette) []span {
	switch e.State {
	case query.Known:
		return []span{{text: e.Value.OccurredAt.Format("15:04:05"), style: p.Dim}}
	case query.Absent:
		return nil
	default:
		return []span{unknownMarkSpan("unknown", p)}
	}
}

// ---------- level 2: Project -> Mate row, then Crews ----------

func (m Model) projectListItems(rows []row, w int) []listLine {
	g, p := m.g, m.p
	proj := m.currentProject()
	iw := w - 2
	agentW := iw - colHarness - colMateStatus - colMode - colBinding

	items := []listLine{headerListLine(func(focused bool) *line {
		l := newLine().addSpans(rowPrefix(false, false, g, p)...)
		l.addSpans(fitCell([]span{paneTitleSpan("MATE", focused, p)}, agentW)...)
		l.addSpans(fitCell([]span{columnHeaderSpan("HARNESS", p)}, colHarness)...)
		l.addSpans(fitCell([]span{columnHeaderSpan("STATUS", p)}, colMateStatus)...)
		l.addSpans(fitCell([]span{columnHeaderSpan("MODE", p)}, colMode)...)
		l.addSpans(fitCell([]span{columnHeaderSpan("BINDING", p)}, colBinding)...)
		return l
	})}

	hasMateRow := len(rows) > 0 && rows[0].kind == rowMate
	if hasMateRow {
		items = append(items, rowListLine(0, func(selected, focused bool) *line {
			l := selectRow(newLine(), selected, p)
			l.addSpans(rowPrefix(selected, focused, g, p)...)
			l.addSpans(mateRowSpans(proj.Mate, proj.Mode, agentW, g, p)...)
			return l
		}))
	}

	items = append(items, blankListLine())

	crewStart := 0
	if hasMateRow {
		crewStart = 1
	}
	crewRowsOnScreen := rows[crewStart:]
	crewsWide := wideList(w)
	// TASK is the one column allowed to cut a value short (titleCell): the
	// job a Crew was spawned for is what a reader scans this list by, so it
	// takes whatever width is left and truncates with an ellipsis rather
	// than being dropped whole the way a NOTE item is.
	taskW := iw - colCrewID - colStatus - colAttention
	if crewsWide {
		taskW -= colUpdated
	}

	// Every listed Crew is open: the snapshot already dropped the closed
	// ones (query.ProjectNode.Crews), and a `done` Crew is still in flight
	// until the Mate or the captain closes it.
	activeCrews := len(proj.Crews)
	items = append(items, headerListLine(func(focused bool) *line {
		l := newLine().addSpans(rowPrefix(false, false, g, p)...)
		l.addSpans(fitCell([]span{paneTitleSpan(fmt.Sprintf("CREWS  %d", activeCrews), focused, p)}, colCrewID)...)
		l.addSpans(fitCell([]span{columnHeaderSpan("TASK", p)}, taskW)...)
		l.addSpans(fitCell([]span{columnHeaderSpan("STATUS", p)}, colStatus)...)
		l.addSpans(fitCell([]span{columnHeaderSpan("NOTE", p)}, colAttention)...)
		if crewsWide {
			l.addSpans(fitCell([]span{columnHeaderSpan("UPDATED", p)}, colUpdated)...)
		}
		return l
	}))

	if len(crewRowsOnScreen) == 0 {
		items = append(items, blankListLine())
		items = append(items, headerListLine(func(bool) *line {
			return newLine().pad(2).add("No crews recorded for "+proj.Name+".", p.Fg)
		}))
		items = append(items, headerListLine(func(bool) *line {
			return newLine().pad(2).add("Crews are spawned by the Mate; press r to re-read.", p.Dim)
		}))
		return items
	}

	for i, r := range crewRowsOnScreen {
		idx := i + crewStart
		if r.kind == rowCompletedGroup {
			finished := finishedCrews(proj)
			items = append(items, rowListLine(idx, func(selected, focused bool) *line {
				l := selectRow(newLine(), selected, p)
				l.addSpans(rowPrefix(selected, focused, g, p)...)
				l.addSpans(fitCell([]span{{text: completedGroupTitle(len(finished), m.completedOpen[proj.ProjectID], g), style: p.Fg}}, colCrewID)...)
				l.addSpans(fitCell(nil, taskW)...)
				l.addSpans(fitCell(nil, colStatus)...)
				l.addSpans(fitCell(m.completedCrewNoteSpans(finished, colAttention), colAttention)...)
				if crewsWide {
					l.addSpans(fitCell(nil, colUpdated)...)
				}
				return l
			}))
			continue
		}
		if r.idx < 0 || r.idx >= len(proj.Crews) {
			continue
		}
		c := proj.Crews[r.idx]
		// idx must be the row's position in currentRows() (what f.sel
		// indexes), not r.idx (the Crew's position in proj.Crews): the Mate
		// row occupies position 0, and filtering finished Crews into a
		// Completed group makes r.idx differ from the displayed position.
		items = append(items, rowListLine(idx, func(selected, focused bool) *line {
			l := selectRow(newLine(), selected, p)
			l.addSpans(rowPrefix(selected, focused, g, p)...)
			l.addSpans(fitCell([]span{{text: shortID(c.CrewID, g), style: p.Fg}}, colCrewID)...)
			l.addSpans(titleCell(c.Task, taskW, p.Fg, g)...)
			l.addSpans(fitCell([]span{statusSpan(string(c.Status), p)}, colStatus)...)
			l.addSpans(fitCell(m.crewRowNoteSpans(c, colAttention), colAttention)...)
			if crewsWide {
				l.addSpans(fitCell(updatedSpans(c.LastEvent, p), colUpdated)...)
			}
			return l
		}))
	}
	return items
}

func finishedCrews(p query.ProjectNode) []query.CrewNode {
	out := make([]query.CrewNode, 0)
	for _, c := range p.Crews {
		if c.Status.IsFinished() {
			out = append(out, c)
		}
	}
	return out
}

func completedGroupTitle(n int, open bool, g glyphSet) string {
	mark := g.Down
	if open {
		mark = g.Up
	}
	return mark + " Completed (" + fmt.Sprint(n) + ")"
}

// mateRowSpans is the Project level's Mate row: Agent, Harness, recorded
// Status, Binding - or, when the Project has no designated Mate, "none
// assigned" and an attention marker in the Binding cell, matching the
// gallery's "Project without a Mate" state. A Designated read that failed
// (Unknown) is a distinct case from a Designated read that succeeded and
// found no Mate (Absent): the former must say "unknown" in the Agent cell,
// never the confident "none assigned" - an unreadable designation has not
// established that there is no Mate.
// mode is the Project's, not the Mate's: it is drawn on this row even when
// there is no Mate yet, because the flag is a Project setting that survives
// every start and stop, and a blank cell there would read as "no mode".
func mateRowSpans(mate query.MateNode, mode query.Mode, agentW int, g glyphSet, p palette) []span {
	if mate.Designated.State == query.Known && mate.Designated.Value.MateID != "" {
		v := mate.Designated.Value
		agent := v.MateID
		if mate.AgentName.IsKnown() && mate.AgentName.Value != "" {
			agent = mate.AgentName.Value
		}
		out := titleCell(agent, agentW, p.Fg, g)
		out = append(out, fitCell([]span{{text: string(v.HarnessKind), style: p.Fg}}, colHarness)...)
		out = append(out, fitCell([]span{statusSpan(string(v.Status), p)}, colMateStatus)...)
		out = append(out, fitCell(modeSpans(mode, p), colMode)...)
		out = append(out, fitCell(bindingSpans(mate.Binding, p), colBinding)...)
		return out
	}

	agentSpans := []span{{text: "none assigned", style: p.Dim}}
	if mate.Designated.State == query.Unknown {
		agentSpans = []span{unknownMarkSpan("unknown", p)}
	}
	out := fitCell(agentSpans, agentW)
	out = append(out, fitCell(nil, colHarness)...)
	out = append(out, fitCell(nil, colMateStatus)...)
	out = append(out, fitCell(modeSpans(mode, p), colMode)...)
	out = append(out, fitCell(mateMissingBindingSpans(mate, p), colBinding)...)
	return out
}

// modeSpans renders the communication mode. Auto takes the accent style
// because it is the mode in which the Console may type into the Mate's pane
// without the reader; supervised, the default, is plain.
func modeSpans(mode query.Mode, p palette) []span {
	if mode == "" {
		return nil
	}
	if mode == query.ModeAuto {
		return []span{{text: string(mode), style: p.Acc}}
	}
	return []span{{text: string(mode), style: p.Fg}}
}

// mateMissingBindingSpans is the Binding cell for a Project with no
// designated Mate: the design's own copy ("! no mate") rather than a blank
// column, since a missing Mate is exactly what the ATTENTION column
// elsewhere on this row would call out.
func mateMissingBindingSpans(mate query.MateNode, p palette) []span {
	if mate.Designated.State == query.Unknown {
		return []span{unknownMarkSpan("unknown", p)}
	}
	return []span{attentionSpan("no mate", p)}
}

// bindingSpans renders a Field[BindingValue]'s Status word - Known as its
// own status colour, Absent as dim "none", Unknown as an honest "unknown".
func bindingSpans(b query.Field[query.BindingValue], p palette) []span {
	switch b.State {
	case query.Known:
		return []span{statusSpan(string(b.Value.Status), p)}
	case query.Absent:
		return []span{{text: "none", style: p.Dim}}
	default:
		return []span{unknownMarkSpan("unknown", p)}
	}
}

func (m Model) completedCrewNoteSpans(crews []query.CrewNode, w int) []span {
	var items [][]span
	var attn query.Field[query.Attention]
	have := false
	for _, c := range crews {
		if c.Attention.State != query.Known {
			continue
		}
		attn = c.Attention
		have = true
		if c.Attention.Value.Kind == query.AttentionFailed {
			break
		}
	}
	if have {
		if s := attentionSpans(attn, m.p); len(s) > 0 {
			items = append(items, s)
		}
	}
	return packNoteItems(items, w)
}

// crewNoteItems is the Crew row's own NOTE cell items, in priority order,
// before width packing: the query layer's derived Attention, then the
// worktree's own recorded removal. Split out
// (rather than a single crewNoteSpans free function, which PR 92's
// counter-review found had drifted to zero production callers while its own
// regression tests stayed green against it - B3) so m.crewRowNoteSpans
// (below), the one production caller (list.go's taskListItems), can append
// the ADR 0019 G7-04a2 health item at its own priority without duplicating
// the packing algorithm. rows_test.go exercises this through
// m.crewRowNoteSpans with an unwired Model, which reproduces this
// function's own behaviour exactly.
func crewNoteItems(c query.CrewNode, p palette) [][]span {
	var items [][]span
	if s := attentionSpans(c.Attention, p); len(s) > 0 {
		items = append(items, s)
	}
	// crewAttention only surfaces the worktree as attention when the *read*
	// failed (Worktree.State == Unknown), not when the read succeeded and
	// recorded the worktree gone (WorktreeRecordedRemoved). Without this, a
	// worktree the discard path already removed renders byte-identical to a
	// live one at the same path in the one place a reader is looking. The
	// word is "missing" - the same recorded-worktree-status vocabulary the
	// inspector uses (design/mate-console-design-notes.html: "present
	// (clean) · present (dirty) · missing · unknown") - not "removed".
	if c.Worktree.State == query.Known && c.Worktree.Value.Status == query.WorktreeRecordedRemoved {
		items = append(items, []span{{text: "worktree missing", style: p.Red}})
	}
	return items
}

// crewRowNoteSpans is the Crew row's NOTE cell: crewNoteItems' work-outcome
// items, packed.
//
// TODO(task 18): a runtime health warning was appended here, last in the
// priority list so a narrow terminal drops it before it drops a work
// outcome. mvp.md defers the health observer to task 18.
func (m Model) crewRowNoteSpans(c query.CrewNode, w int) []span {
	return packNoteItems(crewNoteItems(c, m.p), w)
}

// packNoteItems packs a priority-ordered list of note items into w cells,
// dropping whichever whole items do not fit rather than cutting one in half
// (design/mate-console-design-notes.html, "Rút gọn").
func packNoteItems(items [][]span, w int) []span {
	var out []span
	used := 0
	for _, it := range items {
		need := spansWidth(it)
		if len(out) > 0 {
			need += 2
		}
		if used+need > w {
			break
		}
		if len(out) > 0 {
			out = append(out, span{text: "  ", plain: true})
		}
		out = append(out, it...)
		used += need
	}
	return out
}

// spansWidth is the total display width of a span slice, cells' equivalent
// for content that has not yet reached a *line.
func spansWidth(spans []span) int {
	n := 0
	for _, s := range spans {
		n += cells(s.text)
	}
	return n
}
