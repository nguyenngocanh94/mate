package console

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The frame: six lines of chrome around one main region, at every size.
//
//	line 0    header      - what this is, which workspace, how old the read is
//	line 1    breadcrumb  - where the reader is, and a count for this level
//	line 2    rule        - joins the inspector's divider with a tee
//	line 3..  main region - exactly h-6 lines
//	line h-3  rule        - the same tee, upside down
//	line h-2  message     - one line, or blank when there is nothing to say
//	line h-1  keys        - what the keys do here
//
// The contract this file owns is arithmetic, not content: 3 + (h-6) + 3 = h
// lines, every line exactly w cells, and the divider column in the body
// lining up with the tees above and below it. The content of the main
// region, the message and the key line belong to the other Console tasks
// (see seams.go); the frame is what they can rely on.

// render draws one frame.
func (m Model) render() string {
	if m.w <= 0 || m.h <= 0 {
		// No tea.WindowSizeMsg yet. Sizes come from that message and from
		// nowhere else, so there is nothing to draw rather than a guess.
		return ""
	}
	l := layout(m.w, m.h)
	s := newScreen(m.w, m.h)
	if l.TooSmall {
		m.pushTooSmall(s, l)
		return s.String()
	}
	s.push(m.headerLine(l))
	s.push(m.breadcrumbLine(l))
	s.push(m.ruleLine(l, m.g.TeeDown))
	m.pushBody(s, l)
	// The bottom rule joins the inspector's divider only when the divider
	// actually reaches it. The box region (mvp.md task 15) is full width and
	// sits between them, so with one drawn the tee would point at a column
	// where nothing is.
	if boxH, _ := m.boxRegion(l); boxH > 0 {
		s.push(newLine().add(strings.Repeat(m.g.HRule, l.Cols), m.p.Faint))
	} else {
		s.push(m.ruleLine(l, m.g.TeeUp))
	}
	s.push(m.messageLine(l))
	s.push(m.keysLine(l))
	return s.String()
}

// hasInspectorColumn reports whether this frame draws the inspector beside
// the list: there is an inspector width, there is a snapshot to inspect,
// and Detail has not taken the whole region instead.
func (m Model) hasInspectorColumn(l frameLayout) bool {
	return l.split(m.phase == phaseReady && !m.detail)
}

// ---------- line 0: header ----------

func (m Model) headerLine(l frameLayout) *line {
	left := newLine().add(" matev2 console", m.p.Bold)
	if m.phase == phaseReady {
		// TODO(task 18): the open-incident badge and the "? monitoring
		// error" marker lived here. Both read the health observer, which
		// mvp.md defers to task 18 (internal/watch), so the header says
		// nothing about runtime health rather than asserting silence is
		// good news.
		left.add("   workspace ", m.p.Dim)
		if l.Cols >= 100 {
			left.add(m.workspaceDisplayName(), m.p.Fg)
			if root := m.workspaceRoot(); root != "" {
				left.add("  "+root, m.p.Dim)
			}
		} else {
			left.add(m.workspaceName(), m.p.Fg)
		}
	}
	right := newLine()
	switch m.phase {
	case phaseLoading:
		right.add("Loading snapshot "+m.g.Ellipsis+" ", m.p.Dim)
	case phaseFailed:
		right.add("No snapshot loaded ", m.p.Amber)
	default:
		// The tree auto-refreshes every treeTickInterval (model.go) as well
		// as on 'r', so "live" is now an honest word for it - unlike the
		// former "Recorded snapshot", which was never wrong but is no
		// longer the whole truth. m.tree.AsOf is still the time of the last
		// *successful* load (query.Snapshot.AsOf, never a UI-owned clock):
		// on a failed background refresh the tree is not rolled back
		// (onTreeLoaded), so this is the time the picture on screen was
		// actually taken, live or stale.
		if err := m.lastLoadErr; err != nil {
			short := truncateEnd(oneLine(err.Error()), 32, m.g)
			right.add("stale "+m.g.Dot+" "+m.tree.AsOf.Format("15:04:05")+" "+m.g.Dot+" "+short+" ", m.p.Amber)
		} else {
			right.add("live "+m.g.Dot+" "+m.tree.AsOf.Format("15:04:05")+" ", m.p.Fg)
		}
	}
	return leftRight(left, right, l.Cols, m.g)
}

// workspaceName is the breadcrumb's name for this workspace, and the
// header's below 100 columns.
//
// A real id is "ws_" plus 64 hex characters, which is two thirds of a
// 120-column header on its own, so it is abbreviated by the same
// head-and-tail rule every other id in a list gets rather than shown whole.
// At 100 columns and wider the header has room for the real name instead -
// see workspaceDisplayName - but the breadcrumb stays on the abbreviated id
// at every width, so the path it draws does not grow and shrink as the
// reader resizes.
func (m Model) workspaceName() string {
	if m.tree.WorkspaceID == "" {
		return "(unknown)"
	}
	return shortID(m.tree.WorkspaceID, m.g)
}

// workspaceDisplayName is the header's name at 100 columns and wider: the
// real workspace name (Snapshot.Workspace, StateStore.WorkspaceRoot's own
// resolution - never a caller-supplied path, ADR 0015) when that field read
// successfully, the abbreviated id otherwise. There is always something
// honest to show, never a blank.
func (m Model) workspaceDisplayName() string {
	if m.tree.Workspace.IsKnown() && m.tree.Workspace.Value.Name != "" {
		return m.tree.Workspace.Value.Name
	}
	return m.workspaceName()
}

// ---------- line 1: breadcrumb ----------

func (m Model) breadcrumbLine(l frameLayout) *line {
	left := newLine()
	last := len(m.stack) - 1
	left.add(" "+m.workspaceName(), m.crumbStyle(last == 0))
	for i := 1; i < len(m.stack); i++ {
		left.add(" "+m.g.Crumb+" ", m.p.Faint)
		left.add(m.crumbLabel(i), m.crumbStyle(i == last))
	}
	right := newLine().add(m.breadcrumbCount()+" ", m.p.Dim)
	return leftRight(left, right, l.Cols, m.g)
}

// crumbStyle bolds the tail of the breadcrumb - where the reader actually
// is - and dims the path that led there.
func (m Model) crumbStyle(isLast bool) lipgloss.Style {
	if isLast {
		return m.p.Bold
	}
	return m.p.Dim
}

// crumbLabel names one frame. A frame whose entity the latest read no
// longer contains keeps its id rather than rendering blank: reconcileSelection
// drops such frames, so seeing one means the read is mid-flight.
func (m Model) crumbLabel(i int) string {
	f := m.stack[i]
	switch f.kind {
	case frameProject:
		if p, ok := m.projectByID(f.id); ok {
			return p.Name
		}
	}
	return f.id
}

// breadcrumbCount is the right-hand context for this level: how many rows
// the reader is looking at, or - in Detail - how to get back.
//
// Before the first snapshot is in hand (phaseLoading, or a first-load
// phaseFailed) m.tree is the zero Snapshot and every count derived from it
// would be a fabricated fact ("0 projects" reads as an Absent-style claim
// about the workspace, when the only established fact is that nothing has
// been read yet or the read failed). Navigation past the Workspace frame
// only exists once a snapshot has loaded, so this is the one frameWorkspace
// branch that can be reached pre-ready; it says nothing rather than guess.
func (m Model) breadcrumbCount() string {
	if m.detail {
		return "Detail view " + m.g.Dot + " Esc back to list"
	}
	if m.phase != phaseReady {
		return ""
	}
	f := m.cur()
	switch f.kind {
	case frameProject:
		return "Project " + m.g.Dot + " " + plural(len(m.currentProject().Crews), "crew", "crews")
	default:
		return "Workspace " + m.g.Dot + " " + plural(len(m.tree.Projects), "project", "projects")
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ---------- lines 2 and h-3: rules ----------

// ruleLine draws a horizontal rule, joined to the inspector's divider with
// the given tee glyph when the frame is split. The join is computed from
// the same frameLayout the body's divider comes from, so the two cannot
// drift.
func (m Model) ruleLine(l frameLayout, joint string) *line {
	if !m.hasInspectorColumn(l) {
		return newLine().add(strings.Repeat(m.g.HRule, l.Cols), m.p.Faint)
	}
	return newLine().
		add(strings.Repeat(m.g.HRule, l.List), m.p.Faint).
		add(joint, m.p.Faint).
		add(strings.Repeat(m.g.HRule, l.Inspector), m.p.Faint)
}

// ---------- lines 3..h-4: the main region ----------

// pushBody fills exactly l.Body lines. Which surface fills them is the
// only decision here; what they contain belongs to the other tasks.
func (m Model) pushBody(s *screen, l frameLayout) {
	if l.Body <= 0 {
		return
	}
	// The peek overlay ('p', box_keys.go) owns the whole region: it is a
	// crew's own terminal screen, and cropping it into a pane would misalign
	// every line the harness drew.
	if m.peek.open {
		pushAll(s, fitLines(m.peekLines(l.Cols, l.Body), l.Body))
		return
	}
	// The box panel is the project frame's third region (mvp.md task 15): it
	// sits below the list/inspector split rather than beside it, so attention
	// is visible without entering the session view. It is subtracted from the
	// body here and drawn after, which keeps every pane above it unaware of
	// it.
	if boxH, panel := m.boxRegion(l); boxH > 0 {
		inner := l
		inner.Body = l.Body - boxH
		m.pushMainRegion(s, inner)
		if panel {
			pushAll(s, m.boxPanelLines(l, boxH))
		} else {
			pushAll(s, fitLines([]*line{boxDigestLine(m.currentProject().Box, m.g, m.p)}, boxH))
		}
		return
	}
	m.pushMainRegion(s, l)
}

// pushMainRegion fills exactly l.Body lines with whichever surface owns
// them, with no knowledge of anything drawn below it.
func (m Model) pushMainRegion(s *screen, l frameLayout) {
	if l.Body <= 0 {
		return
	}
	// The failure detail overlay ('e') owns the main region while it is open.
	// It is checked before the other modals because onKey gives it the
	// keyboard first, so none of them can be open at the same time as it.
	if m.failureDetail {
		pushAll(s, fitLines(m.openFailureDetail(l), l.Body))
		return
	}
	if m.actions || m.actionInputMode || m.harnessPick || m.confirm != nil {
		pushAll(s, fitLines(m.actionLines(l.Cols, l.Body), l.Body))
		return
	}
	switch {
	case m.phase == phaseLoading:
		pushAll(s, fitLines(m.loadingLines(l), l.Body))
	case m.phase == phaseFailed:
		pushAll(s, fitLines(m.failedLines(l), l.Body))
	case m.detail:
		pushAll(s, fitLines(m.inspectorLines(l.Cols, l.detailValueWidth(), l.Body, true), l.Body))
	case m.hasInspectorColumn(l):
		left := fitLines(m.listLines(l.List, l.Body), l.Body)
		right := fitLines(m.inspectorLines(l.Inspector, l.valueWidth(), l.Body, m.focus == paneInspector), l.Body)
		divider := span{text: m.g.VRule, style: m.p.Faint}
		for i := 0; i < l.Body; i++ {
			s.pushSplit(left[i], l.List, divider, right[i], l.Inspector)
		}
	default:
		pushAll(s, fitLines(m.listLines(l.Cols, l.Body), l.Body))
	}
}

func pushAll(s *screen, lines []*line) {
	for _, l := range lines {
		s.push(l)
	}
}

// fitLines pads with blanks or drops the overflow so a pane hands the
// screen exactly n lines. A pane that miscounts is trimmed here rather than
// pushing the footer off the bottom of the frame.
func fitLines(lines []*line, n int) []*line {
	if n < 0 {
		n = 0
	}
	out := make([]*line, 0, n)
	for i := 0; i < n; i++ {
		if i < len(lines) && lines[i] != nil {
			out = append(out, lines[i])
			continue
		}
		out = append(out, newLine())
	}
	return out
}

// ---------- line h-2: message ----------

func (m Model) messageLine(l frameLayout) *line {
	msg := m.footerMessage()
	if msg.tone == toneNone || msg.text == "" {
		return newLine()
	}
	prefix, style := "", m.p.Fg
	switch msg.tone {
	case toneError:
		prefix, style = "! ", m.p.Red
	case toneWarn:
		prefix, style = "! ", m.p.Amber
	case toneUnknown:
		prefix, style = "? ", m.p.Amber
	case toneOK:
		style = m.p.Green
	}
	// cut is the safety net for a message longer than the line: the message
	// content may be arbitrary recorded text (an error's own message), and a
	// silent truncation would hide the tail of a cause the reader needs. A cut
	// is marked with the glyph set's ellipsis, so a line that was abbreviated
	// says so rather than reading as complete. The failure chain's *cause* is
	// bounded by construction (session_failure.go), but the evidence after it
	// - the error's own message - is not, and 80 columns is not the narrowest
	// frame a message can meet: it is the width the fixtures measure. So the
	// cut is a real path, not a leftover.
	return newLine().add(" "+prefix+msg.text, style).cut(l.Cols, m.g)
}

// ---------- line h-1: keys ----------

// keyHint is one entry on the key line.
//
// optional marks a hint for a convenience rather than a way through: it is
// the first thing dropped. sacrifice orders everything else - higher goes
// first when the line still does not fit. q is never given a sacrifice
// rank, because a reader who cannot read the key line still has to be able
// to leave.
type keyHint struct {
	key       string
	desc      string
	optional  bool
	sacrifice int
}

// Sacrifice ranks, one per kind of hint, applied only after the optional
// hints are gone and "(unavailable)" has been abbreviated. A higher rank is
// dropped sooner: movement first (the arrow keys are muscle memory), then
// going back, then the Enter action, then refresh. Quit's rank is never
// dropped at all.
const (
	keyQuit     = 0
	keyRefresh  = 1
	keyAction   = 2
	keyBack     = 3
	keyMovement = 4
)

func (m Model) keysLine(l frameLayout) *line {
	build := func(hs []keyHint) *line {
		out := newLine().add(" ", m.p.Dim)
		for i, h := range hs {
			if i > 0 {
				out.add("  ", m.p.Dim)
			}
			out.add(h.key, m.p.Fg).add(" "+h.desc, m.p.Dim)
		}
		return out
	}
	hints := m.keyHints(l)
	if line := build(hints); line.width() <= l.Cols {
		return line
	}
	// 1. Drop the optional hints: they name conveniences (Tab), not the keys
	//    the reader needs to get out of here. They go one at a time, from
	//    the end of the list, and only while the line still does not fit:
	//    dropping the whole group at once costs the reader hints the line
	//    had room for, and the key line is the only place some keys are
	//    ever named.
	for {
		next, ok := dropLastOptional(hints)
		if !ok {
			break
		}
		hints = next
		if line := build(hints); line.width() <= l.Cols {
			return line
		}
	}
	// 2. Shorten rather than cut a word in half: "(unavailable)" says the
	//    same thing as "(n/a)" in five cells.
	for i := range hints {
		hints[i].desc = strings.ReplaceAll(hints[i].desc, "(unavailable)", "(n/a)")
	}
	if line := build(hints); line.width() <= l.Cols {
		return line
	}
	// 3. Still too long. Drop whole hints, highest sacrifice rank first,
	//    rather than letting the line be truncated - a truncated key line
	//    loses whatever is at the end, which is the way out.
	for rank := keyMovement; rank >= keyRefresh; rank-- {
		hints = filterHints(hints, func(h keyHint) bool { return h.sacrifice != rank })
		if line := build(hints); line.width() <= l.Cols {
			return line
		}
	}
	return build(hints)
}

// dropLastOptional returns hints without its last optional entry, and
// whether there was one to drop.
func dropLastOptional(hints []keyHint) ([]keyHint, bool) {
	for i := len(hints) - 1; i >= 0; i-- {
		if !hints[i].optional {
			continue
		}
		out := make([]keyHint, 0, len(hints)-1)
		out = append(out, hints[:i]...)
		return append(out, hints[i+1:]...), true
	}
	return hints, false
}

func filterHints(hints []keyHint, keep func(keyHint) bool) []keyHint {
	out := make([]keyHint, 0, len(hints))
	for _, h := range hints {
		if keep(h) {
			out = append(out, h)
		}
	}
	return out
}

// ---------- the too-small screen ----------

// pushTooSmall draws the whole frame for a terminal below the minimum. The
// six-line chrome does not apply: there is no room for it, and drawing a
// partial frame would be less legible than a sentence. Only q responds
// here (see onKey), and the navigation stack is untouched, so growing the
// terminal back returns to exactly the previous screen.
func (m Model) pushTooSmall(s *screen, l frameLayout) {
	s.push(newLine().add(fmt.Sprintf(" Terminal too small: %dx%d", l.Cols, l.Rows), m.p.Amber))
	s.push(newLine().add(fmt.Sprintf(" matev2 console needs at least %dx%d.", minCols, minRows), m.p.Fg))
	s.push(newLine().add(" Resize the window, or press q to quit.", m.p.Dim))
}
