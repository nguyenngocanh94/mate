package console

import (
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The list pane (design A, B, E, J, K). A row is two lines at 30+ rows -
// its name, then its harness and status - and one line below that, with
// the status in a seven-cell column. Columns never move between the two:
// the marker in col 0, the kind in cols 2-3, the name from col 5.

// listItem is one row of the list, drawn.
type listItem struct {
	row   int // index into currentRows
	lines []gline
}

// paneBody is a pane's drawn lines and, per line, the row index a click on
// it selects (-1 for none).
type paneBody struct {
	lines []gline
	rows  []int
}

func (b *paneBody) push(l gline, row int) {
	b.lines = append(b.lines, l)
	b.rows = append(b.rows, row)
}

// listNeed is the list's height without windowing, rule excluded.
func (m Model) listNeed(p framePlan) int {
	n := 0
	for _, it := range m.listItems(p) {
		n += len(it.lines)
	}
	if n == 0 {
		return 1
	}
	return n
}

// listBody windows the list into h rows. Items are drawn whole; the
// selected one is always in view; "↑ N more" and "↓ N more" take a row
// each when rows are cut off above or below.
func (m Model) listBody(p framePlan, h int) paneBody {
	var out paneBody
	if h <= 0 {
		return out
	}
	items := m.listItems(p)
	if len(items) == 0 {
		out.push(gl().pad(2).add(m.emptyListText(), tDim), -1)
		return out
	}
	start, end, ok := m.listWindow(items, h)
	if !ok {
		// Not even the selected item fits: draw what does of it.
		it := items[start]
		for _, l := range it.lines {
			if len(out.lines) < h {
				out.push(l, it.row)
			}
		}
		return out
	}
	if start > 0 {
		out.push(gl().pad(2).add(fmt.Sprintf("%s %d more", m.g.Up, start), tDim), -1)
	}
	for _, it := range items[start:end] {
		for _, l := range it.lines {
			out.push(l, it.row)
		}
	}
	if end < len(items) {
		out.push(gl().pad(2).add(fmt.Sprintf("%s %d more", m.g.Down, len(items)-end), tDim), -1)
	}
	return out
}

// listWindow is the items drawn at h rows: from the frame's scroll offset,
// moved just enough to keep the selection in view. ok is false when the
// selected item alone does not fit; start is then the selected item.
func (m Model) listWindow(items []listItem, h int) (start, end int, ok bool) {
	sel := indexOfItem(items, m.cur().sel)
	start = clampInt(indexOfItem(items, m.cur().top), 0, len(items)-1)
	if start > sel {
		start = sel
	}
	for ; start <= sel; start++ {
		end, fits := fitItems(items, start, h, start > 0)
		if fits && sel < end {
			return start, end, true
		}
	}
	return sel, sel + 1, false
}

// listTop is the scroll offset to keep: the first row the window draws.
func (m Model) listTop(p framePlan) int {
	sl, ok := p.slot(slotList)
	if !ok {
		return m.cur().top
	}
	items := m.listItems(p)
	if len(items) == 0 {
		return 0
	}
	start, _, _ := m.listWindow(items, sl.h-1)
	return items[start].row
}

// fitItems is how far from start the items fit in h rows, leaving a row
// for "↑ more" when up and for "↓ more" when not every item fits.
func fitItems(items []listItem, start, h int, up bool) (end int, fits bool) {
	room := h
	if up {
		room--
	}
	used := 0
	end = start
	for end < len(items) && used+len(items[end].lines) <= room {
		used += len(items[end].lines)
		end++
	}
	if end < len(items) {
		// Make room for the "↓ more" row.
		for end > start && used+1 > room {
			end--
			used -= len(items[end].lines)
		}
	}
	return end, end > start || len(items) == 0
}

func indexOfItem(items []listItem, row int) int {
	for i, it := range items {
		if it.row == row {
			return i
		}
	}
	return 0
}

func (m Model) emptyListText() string {
	if m.cur().kind == frameProject {
		return "no Mate and no crews yet"
	}
	return "no projects yet"
}

// listItems draws every row of the current frame.
func (m Model) listItems(p framePlan) []listItem {
	rows := m.currentRows()
	out := make([]listItem, 0, len(rows))
	focused := m.focus == paneList
	for i, r := range rows {
		selected := i == m.cur().sel
		var lines []gline
		switch r.kind {
		case rowProject:
			lines = m.projectRowLines(r, p)
		case rowMate:
			lines = m.mateRowLines(p)
		case rowCrew:
			lines = m.crewRowLines(r, p)
		case rowCompletedGroup:
			lines = m.completedRowLines(p)
		case rowHandedBackGroup:
			lines = m.handedBackRowLines(p)
		}
		for j := range lines {
			lines[j] = m.markRow(lines[j], j == 0, selected, focused, p.w)
		}
		out = append(out, listItem{row: i, lines: lines})
	}
	return out
}

// markRow puts the selection signals on a row line: the one-cell marker in
// col 0 on its first line - ▌ in the focused pane, ▏ in the others - and
// the selection background across every line (design I, "2 Four signals").
func (m Model) markRow(l gline, first, selected, focused bool, w int) gline {
	lead := gl().add(" ", tFg)
	if selected && first {
		if focused {
			lead = gl().add(m.g.Selected, tAcc)
		} else {
			lead = gl().add(m.g.Unfocused, tDim)
		}
	}
	return lead.join(l).padTo(w, m.g).selected(selected)
}

// rowLine is a row's first line after the marker cell: a blank, then the
// name block, then the right column. The right column keeps its width; the
// name is cut to what remains.
func (m Model) rowLine(name gline, right gline, w int) gline {
	return spread(gl().pad(1).join(name), right, w-1, m.g)
}

// kindName is the kind mark and the name: "👨‍💻 payments-api".
func kindName(mark, name string) gline {
	return gl().add(mark, tFg).pad(1).add(name, tFg)
}

// bangCell is the attention column: "!N", "!" for one object, or blanks.
func bangCell(n int, failed bool, single bool, width int) gline {
	if n <= 0 {
		return gl().pad(width)
	}
	text := "!"
	if !single {
		text = fmt.Sprintf("!%d", n)
	}
	return gl().add(padRight(text, width), bangTok(failed))
}

// statusCell is the one-line row's right column: the ! column, a blank,
// and the seven-cell status word (design I, "1-line: ! at 31; status 33-39").
func statusCell(bang gline, word string, t tok) gline {
	return bang.pad(1).add(padRight(shortStatus(word), shortStatusWidth), t)
}

// ---------- workspace rows ----------

func (m Model) projectRowLines(r row, p framePlan) []gline {
	proj, ok := m.projectByID(r.id)
	if !ok {
		return []gline{gl().pad(1).add(r.id, tDim)}
	}
	n, failed := projectAttention(proj)
	word := mateWord(proj.Mate)
	if !p.tall {
		// Design K: name, !N, then the Mate's status word.
		right := bangCell(n, failed, false, 0)
		if n > 0 {
			right = right.pad(1)
		}
		right = right.add(padRight(shortStatus(word), shortStatusWidth), statusTok(word))
		return []gline{m.rowLine(gl().add(proj.Name, tFg), right, p.w)}
	}
	// A workspace name starts at col 2 and keeps a blank before the !N
	// column, which is two cells wide plus two: W-8 cells of name.
	line1 := m.rowLine(gl().add(proj.Name, tFg), gl().pad(1).join(bangCell(n, failed, false, 4)), p.w)
	return []gline{line1, m.projectSummaryLine(proj)}
}

// projectSummaryLine is a workspace row's second line: 👨‍💻 mate status ·
// 🤖 crew count, then the worst problem. It stays dim unless something is
// wrong; "no mate" replaces the mate mark because attention is always
// written out (design I, "Row anatomy").
func (m Model) projectSummaryLine(proj query.ProjectNode) gline {
	l := gl().pad(3)
	word := mateWord(proj.Mate)
	if word == "no mate" {
		l = l.add(word, tAmber)
	} else {
		l = l.add(m.g.Mate, tFg).pad(1).add(word, statusTok(word))
	}
	open := 0
	for _, c := range proj.Crews {
		if !c.Closed {
			open++
		}
	}
	l = l.add(" "+m.g.Dot+" ", tDim).add(m.g.Crew, tFg).add(fmt.Sprintf(" %d", open), tDim)
	if worst, t := worstProblem(proj); worst != "" {
		l = l.add(" "+m.g.Dot+" ", tDim).add(worst, t)
	}
	return l
}

// worstProblem is the one problem a workspace row names: failed crews
// first, then crews waiting on an answer, then stale ones.
func worstProblem(proj query.ProjectNode) (string, tok) {
	var failed, waiting, stale int
	for _, c := range proj.Crews {
		if !c.Attention.IsKnown() {
			continue
		}
		switch c.Attention.Value.Kind {
		case query.AttentionFailed:
			failed++
		case query.AttentionDecision, query.AttentionBlocked:
			waiting++
		default:
			stale++
		}
	}
	switch {
	case failed > 0:
		return fmt.Sprintf("%d failed", failed), tRed
	case waiting > 0:
		return fmt.Sprintf("%d waiting", waiting), tAmber
	case stale > 0:
		return fmt.Sprintf("%d stale", stale), tAmber
	}
	return "", tDim
}

// ---------- project rows ----------

func (m Model) mateRowLines(p framePlan) []gline {
	proj := m.currentProject()
	mate := proj.Mate
	word := mateWord(mate)
	bang := 0
	if statusTok(word) == tAmber {
		bang = 1
	}
	name := kindName(m.g.Mate, proj.Name)
	return m.agentRowLines(p, name, bang, false, word, mateHarness(mate), m.mateAgent(mate), m.since(mate.Binding.Value.BoundSince))
}

func (m Model) crewRowLines(r row, p framePlan) []gline {
	c, ok := m.crewByID(r.id)
	if !ok {
		return []gline{gl().pad(1).add(r.id, tDim)}
	}
	title := strings.TrimSpace(oneLine(c.Task))
	if title == "" {
		title = shortID(c.CrewID, m.g)
	}
	bang := 0
	if crewNeedsCaptain(c) {
		bang = 1
	}
	return m.agentRowLines(p, kindName(m.g.Crew, title), bang, crewFailed(c), crewWord(c),
		string(c.HarnessKind), shortID(c.CrewID, m.g), m.since(c.CreatedAt))
}

// agentRowLines lays out a Mate or Crew row at the frame's size class.
//
//	two lines, 32-47 cols:  ▌ 🤖 Backfill ledger v2             !
//	                             ✻ needs-decision
//	two lines, 48+ cols:    ▌ 🤖 Backfill ledger v2        ! decide
//	                             ✻ · crew_01J9…B8DT · 33m
//	one line:               ▌ 🤖 Backfill ledger v2      ! decide
func (m Model) agentRowLines(p framePlan, name gline, bang int, failed bool, word, harness, agent, since string) []gline {
	icon := m.harnessIcon(harness)
	switch {
	case !p.tall:
		return []gline{m.rowLine(name, statusCell(bangCell(bang, failed, true, 1), word, statusTok(word)), p.w)}
	case p.wide:
		right := statusCell(bangCell(bang, failed, true, 1), word, statusTok(word)).pad(2)
		line2 := gl().pad(4)
		if harness != "" {
			line2 = line2.add(icon, tFg).pad(1)
		}
		if agent != "" {
			line2 = line2.add(agent, tDim)
		} else {
			line2 = line2.add(word, statusTok(word))
		}
		if since != "" {
			line2 = line2.add(" "+m.g.Dot+" "+since, tDim)
		}
		return []gline{m.rowLine(name, right, p.w), line2}
	}
	line1 := m.rowLine(name, bangCell(bang, failed, true, 4), p.w)
	line2 := gl().pad(4)
	if harness != "" {
		line2 = line2.add(icon, tFg).pad(1)
	}
	line2 = line2.add(word, statusTok(word))
	return []gline{line1, line2}
}

// mateAgent is the Mate's Herdr agent name, or "".
func (m Model) mateAgent(mate query.MateNode) string {
	if mate.AgentName.IsKnown() {
		return mate.AgentName.Value
	}
	return ""
}

// completedRowLines is the one row a Project's finished crews collapse
// into. A failure inside keeps its red "! N failed" outside (design B).
func (m Model) completedRowLines(p framePlan) []gline {
	finished := m.finishedCrews()
	failed := 0
	for _, c := range finished {
		if crewFailed(c) {
			failed++
		}
	}
	mark := m.g.Collapsed
	if m.completedOpen[m.cur().id] {
		mark = m.g.Expanded
	}
	name := gl().pad(1).add(mark, tFg).add(fmt.Sprintf(" Completed (%d)", len(finished)), tFg)
	right := gl()
	if failed > 0 {
		// Two blanks after it at 30+ rows, one below: the one-line "!" sits
		// in the ! column (col 31 at 40 cols), like every other row's.
		right = gl().add(fmt.Sprintf("! %d failed", failed), tRed).pad(2)
		if !p.tall {
			right = gl().add("! failed", tRed).pad(1)
		}
	}
	return []gline{spread(name, right, p.w-1, m.g)}
}

// finishedCrews are the current Project's closed crews, in snapshot order.
func (m Model) finishedCrews() []query.CrewNode {
	var out []query.CrewNode
	for _, c := range m.currentProject().Crews {
		if c.Closed {
			out = append(out, c)
		}
	}
	return out
}

func (m Model) handedBackCrews() []query.CrewNode {
	var out []query.CrewNode
	for _, c := range m.currentProject().Crews {
		if !c.Closed && c.Status == query.CrewWaitMate {
			out = append(out, c)
		}
	}
	return out
}

func (m Model) handedBackRowLines(p framePlan) []gline {
	mark := m.g.Collapsed
	if m.handedBackOpen[m.cur().id] {
		mark = m.g.Expanded
	}
	name := gl().pad(1).add(mark, tFg).add(fmt.Sprintf(" Handed back (%d)", len(m.handedBackCrews())), tFg)
	return []gline{spread(name, gl(), p.w-1, m.g)}
}
