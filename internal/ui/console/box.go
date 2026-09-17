package console

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The message box (mvp.md task 15, section 4): the project's crew status
// lines, sent.log and observer incidents, merged by internal/box and handed
// to this package as query.BoxView.
//
// What is drawn is the *inbox*, not the log. The merge keeps everything -
// every `working` line, every `done`, every message - but a rail that shows
// everything is a rail where clicking a line means nothing, which is exactly
// what a reader told us after using it (2026-09-17). So every surface here
// draws query.BoxView.Inbox by default: the things a human or the Mate still
// has to decide on, and nothing else. The `[all]` header toggle swaps in the
// whole log for debugging, and that toggle is the only way to see it.
//
// This file owns every way the box is drawn - the Mate session view's left
// rail, the narrow digest that replaces it, the project frame's own panel
// and its one-line digest, and the peek overlay - so the four surfaces
// cannot disagree about what an entry looks like or which one is selected.
//
// Nothing here decides what an entry *means*. The verb, whether the entry
// needs attention, whether it is still unresolved and the exact line
// [resolve] would send are all fields on the DTO, authored in internal/query
// next to the merge that produced them (query/box.go). A renderer that
// re-derived them would be a second copy of the vocabulary, free to drift
// from box.Inbox.

// boxList is what one box surface draws: the loaded view, and whether the
// reader has toggled `[all]`. It exists so the half-dozen functions that
// need "the rows on screen" ask one thing rather than each deciding for
// itself which slice of the DTO is live - a disagreement there would put a
// selection on a row the renderer never drew.
type boxList struct {
	field query.Field[query.BoxView]
	all   bool
}

func (b boxList) known() bool { return b.field.IsKnown() }

// rows are the entries this surface shows, oldest first.
func (b boxList) rows() []query.BoxEntry {
	if !b.field.IsKnown() {
		return nil
	}
	if b.all {
		return b.field.Value.Entries
	}
	return b.field.Value.Inbox
}

// wraps reports whether the selected row shows its full text underneath.
// Only the inbox does: its rows carry no text of their own, so the question
// has to be somewhere, and `[all]` is a log a reader is scanning rather than
// one question they are answering.
func (b boxList) wraps() bool { return !b.all }

// boxEntryLine is one entry, on one line, at every surface:
//
//	inbox: <marker><!> HH:MM  <crew>  <verb>
//	all:   <marker><!> HH:MM  <who>  <verb>  <text>
//
// The inbox line carries no text because the selected item's full text is
// wrapped on the rows underneath it (boxWrapLines): one line per item keeps
// the whole inbox on screen, and the one item the reader is on is the one
// whose words they need.
//
// The marker is the selection signal (signals.go: a glyph, never colour
// alone) and the "!" is the attention signal, for the same reason - a
// golden fixture is rendered with plainPalette, so a state carried only by
// amber would be invisible to a reader with a monochrome terminal.
// An attention entry grows a trailing action strip - "[resolve] [reply]
// [peek]" - while it is selected or while the pointer is over it, so the
// three things a reader does with a waiting crew are on screen as buttons
// rather than as keys they have to remember (session_focus.go).
func boxEntryLine(e query.BoxEntry, selected, hovered, focused, all bool, g glyphSet, p palette, w int) *line {
	l := selectRow(newLine(), selected, p)
	l.addSpan(markerSpan(selected, focused, g, p))
	if e.Attention {
		l.add("!", p.Amber)
	} else {
		l.add(" ", p.Dim)
	}
	l.add(" ", p.Dim)
	strip := boxEntryHasStrip(e, selected, hovered) && boxStripFits(w, g)
	textW := max0(w - boxEntryLead)
	if strip {
		textW = max0(w - boxEntryLead - boxStripWidth(g))
	}
	text := truncateEnd(boxEntryText(e, all), textW, g)
	l.add(text, boxEntryStyle(e, p))
	if strip {
		l.add(strings.Repeat(" ", max0(textW-cells(text))+1), p.Dim)
		for i, b := range boxStripButtons(g) {
			if i > 0 {
				l.add(" ", p.Dim)
			}
			l.add(b.text, p.Acc)
		}
	}
	return l
}

// boxEntryText is the entry's own words, without any selection or attention
// decoration: time, who it is about, the verb, and - in `[all]` mode only -
// the payload.
func boxEntryText(e query.BoxEntry, all bool) string {
	parts := []string{e.At.UTC().Format("15:04"), boxEntryWho(e)}
	if e.Verb != "" {
		verb := e.Verb
		if e.Kind == query.BoxIncident {
			verb = "incident:" + verb
		}
		parts = append(parts, verb)
	}
	if all && e.Text != "" {
		parts = append(parts, sanitizeText(e.Text))
	}
	return strings.Join(parts, "  ")
}

// boxWrapLines is the selected inbox item's own words, wrapped into the rail
// under its row. This is what makes the inbox readable without `p`: the
// reader moves the cursor onto an item and the crew's question is right
// there, in full, rather than cut at the rail's edge.
//
// An item with no text of its own - a status line that was only a verb -
// gets nothing, not an empty row: a blank line under the cursor reads as a
// rendering fault.
func boxWrapLines(e query.BoxEntry, w int) []string {
	text := sanitizeText(strings.TrimSpace(e.Text))
	if text == "" {
		return nil
	}
	var out []string
	for _, chunk := range wrapAfterSlash(text, max0(w-boxWrapIndent)) {
		out = append(out, strings.Repeat(" ", boxWrapIndent)+chunk)
	}
	return out
}

// boxWrapIndent lines the wrapped text up under the entry's own words,
// past the marker and the attention mark (boxEntryLead) plus one more cell
// so the block reads as belonging to the row above it rather than as a
// second entry.
const boxWrapIndent = boxEntryLead + 1

// boxEntryWho names the entry's subject: the crew for a status line or an
// incident, and the "source->target" pair for a message, which is the same
// shape box.Line uses for `sent.log`.
func boxEntryWho(e query.BoxEntry) string {
	if e.Kind == query.BoxMessage {
		if e.Target == "" {
			return e.Source
		}
		return e.Source + "->" + e.Target
	}
	if e.Crew == "" {
		return e.Source
	}
	return e.Crew
}

// boxEntryStyle tints an entry. Attention is amber, a plain crew status is
// the foreground, and a message - which is a record of something already
// said, not something waiting on anyone - is dim.
func boxEntryStyle(e query.BoxEntry, p palette) lipgloss.Style {
	switch {
	case e.Attention:
		return p.Amber
	case e.Kind == query.BoxMessage:
		return p.Dim
	default:
		return p.Fg
	}
}

// boxCountLine is the header line both the rail and the project panel show:
// how many things are waiting on a decision. It counts the inbox, not the
// log, because that is the number a reader acts on - "3 attention" over a
// log that also held forty `working` lines was a number nobody could use.
// In `[all]` mode it says so, and gives the log's own size instead.
func boxCountLine(b boxList, g glyphSet, p palette) *line {
	l := newLine()
	if !b.known() {
		return l.add(" ", p.Dim).addSpans(availabilitySpans(b.field.State, "", "", p.Fg, g, p)...)
	}
	if b.all {
		return l.add(" all", p.Amber).
			add(fmt.Sprintf(" %s %s", g.Dot, plural(len(b.field.Value.Entries), "entry", "entries")), p.Dim)
	}
	if n := b.field.Value.ToResolve(); n > 0 {
		return l.add(fmt.Sprintf(" %d to resolve", n), p.Amber)
	}
	return l.add(" nothing to resolve", p.Dim)
}

// boxDigestLine is the whole box in one line. It is what a frame too short
// for the panel shows instead: the count is the part a reader cannot
// reconstruct from the crews table, and the last timestamp is how stale the
// project's communication is.
func boxDigestLine(v query.Field[query.BoxView], g glyphSet, p palette) *line {
	l := newLine().add(" BOX  ", p.Dim)
	if !v.IsKnown() {
		l.addSpans(availabilitySpans(v.State, "", v.Reason, p.Fg, g, p)...)
		return l
	}
	if n := v.Value.ToResolve(); n > 0 {
		l.add(fmt.Sprintf("%d to resolve", n), p.Amber)
	} else {
		l.add("nothing to resolve", p.Dim)
	}
	l.add(fmt.Sprintf(" %s %s %s %s",
		g.Dot, plural(len(v.Value.Entries), "entry", "entries"),
		g.Dot, plural(v.Value.Crews, "crew", "crews")), p.Dim)
	if !v.Value.LastAt.IsZero() {
		l.add(" "+g.Dot+" last "+v.Value.LastAt.UTC().Format("15:04:05"), p.Dim)
	}
	return l
}

// ---------- the body, and where each row came from ----------

// boxPlanRow is one drawn row of a box body: either an entry's own line, or
// one wrapped line of the selected item's text underneath it. Both carry the
// entry index, so a click anywhere in an item's block - its row or its
// question - selects that item, and neither the renderer nor the hit test
// has to re-derive where the rows went.
type boxPlanRow struct {
	index int
	wrap  string
}

// boxPlan lays a box body out as rows, before any scrolling.
func boxPlan(b boxList, sel, w int) []boxPlanRow {
	rows := b.rows()
	out := make([]boxPlanRow, 0, len(rows)+4)
	for i, e := range rows {
		out = append(out, boxPlanRow{index: i})
		if i != sel || !b.wraps() {
			continue
		}
		for _, text := range boxWrapLines(e, w) {
			out = append(out, boxPlanRow{index: i, wrap: text})
		}
	}
	return out
}

// boxBodyLines draws a box into exactly h lines, oldest at the top so the
// newest is at the bottom - a log, read the way a chat log is read. The
// scroll offset is derived here rather than kept by the caller: it is a pure
// function of the selection and the pane height, and a stored offset would
// be one more thing to reconcile every time the window resized or a crew
// appended a line.
func boxBodyLines(b boxList, sel, hover int, focused bool, g glyphSet, p palette, w, h int) []*line {
	if h <= 0 {
		return nil
	}
	if !b.known() {
		return fitLines([]*line{newLine().add(" ", p.Dim).
			addSpans(availabilitySpans(b.field.State, "", b.field.Reason, p.Fg, g, p)...)}, h)
	}
	rows := b.rows()
	if len(rows) == 0 {
		return fitLines([]*line{newLine().add(" "+boxEmptyText(b.all), p.Dim)}, h)
	}
	plan := boxPlan(b, sel, w)
	start, end := window(len(plan), boxPlanTop(plan, sel, h), h)
	out := make([]*line, 0, h)
	for i := start; i < end; i++ {
		row := plan[i]
		if row.wrap != "" {
			out = append(out, newLine().add(row.wrap, p.Fg).cut(w, g))
			continue
		}
		out = append(out, boxEntryLine(rows[row.index], row.index == sel, row.index == hover, focused, b.all, g, p, w))
	}
	// Pad at the top, not the bottom: a box with fewer rows than the pane
	// still reads newest-last if its lines sit on the bottom edge.
	if len(out) < h {
		pad := make([]*line, 0, h)
		for i := 0; i < h-len(out); i++ {
			pad = append(pad, newLine())
		}
		out = append(pad, out...)
	}
	return out
}

// boxEmptyText is the placeholder a box with no rows shows. The inbox's is
// the quiet one the reader asked for: "nothing to resolve" is a state, not a
// fault, and it must not look like a failed read.
func boxEmptyText(all bool) string {
	if all {
		return "no crew has written a status line yet"
	}
	return "nothing to resolve"
}

// boxPlanTop resolves the scroll offset for a body of h rows: keep the
// selected item's whole block visible - its row and the wrapped question
// under it - and otherwise show the newest end.
func boxPlanTop(plan []boxPlanRow, sel, h int) int {
	if h <= 0 || len(plan) <= h {
		return 0
	}
	top := len(plan) - h
	first, last := -1, -1
	for i, row := range plan {
		if row.index != sel {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first >= 0 {
		if last >= top+h {
			top = last - h + 1
		}
		if first < top {
			top = first
		}
	}
	return clampInt(top, 0, len(plan)-h)
}

// boxDefaultSelection is where the selection sits when a reader has not
// moved it: the newest row, which is the one at the bottom of the rail.
func boxDefaultSelection(b boxList) int {
	if n := len(b.rows()); n > 0 {
		return n - 1
	}
	return -1
}

// boxSelectedEntry resolves a selection index against a box, and reports
// whether there is an entry there at all.
func boxSelectedEntry(b boxList, sel int) (query.BoxEntry, bool) {
	rows := b.rows()
	if sel < 0 || sel >= len(rows) {
		return query.BoxEntry{}, false
	}
	return rows[sel], true
}

// ---------- the project frame's box panel ----------

const (
	// boxPanelRows is the panel's whole height on a project frame: one rule,
	// one header, and six rows. Six is what makes the panel worth the rows
	// it costs - fewer, and the digest line says as much in one.
	boxPanelRows = 8
	// boxPanelMinListRows is how much of the crews table the panel may never
	// take: below this the frame draws the one-line digest instead, so the
	// table a reader came to this frame for stays usable.
	boxPanelMinListRows = 14
)

// boxRegion is how many of the body's lines the box takes on a project
// frame, and whether those lines are the panel or the one-line digest.
//
// It is the one predicate: the renderer draws the region exactly when this
// says there is one, and listLayout subtracts exactly what this says it
// takes, so the rows the selection is clamped against and the rows the list
// is actually given can never disagree. Every screen that owns the whole
// body - a modal, Detail, the loading and error screens, the peek and
// failure overlays - gets no region at all, because the list is not drawn
// under any of them either.
func (m Model) boxRegion(l frameLayout) (height int, panel bool) {
	switch {
	case m.cur().kind != frameProject, l.TooSmall, l.Body <= 0:
		return 0, false
	case m.phase != phaseReady, m.detail, m.peek.open, m.failureDetail:
		return 0, false
	case m.actions, m.actionInputMode, m.harnessPick, m.confirm != nil:
		return 0, false
	}
	if l.Body-boxPanelRows >= boxPanelMinListRows {
		return boxPanelRows, true
	}
	return 1, false
}

// boxPanelLines draws the project frame's box panel: a full-width rule, a
// title line carrying the same count the rail's header does, and the inbox
// itself - the same rows, from the same DTO, so the panel and the rail can
// never disagree about what is waiting.
func (m Model) boxPanelLines(l frameLayout, h int) []*line {
	b := m.projectBoxList()
	focused := m.focus == paneBox
	head := leftRight(
		newLine().pad(1).addSpan(paneTitleSpan("BOX", focused, m.p)),
		boxCountLine(b, m.g, m.p).add(" ", m.p.Dim),
		l.Cols, m.g,
	)
	out := []*line{newLine().add(strings.Repeat(m.g.HRule, l.Cols), m.p.Faint), head}
	hover := -1
	if focused {
		hover = m.boxHover
	}
	out = append(out, boxBodyLines(b, m.projectBoxSelection(), hover, focused, m.g, m.p, l.Cols, max0(h-len(out)))...)
	return fitLines(out, h)
}

// listLayout is the layout the list pane actually gets: the frame's, less
// whatever the box region takes off the bottom. Selection clamping, paging
// and the scroll offset all measure against this rather than frameLayout's
// own Body, or the selection could sit on a row the panel has covered.
func (m Model) listLayout() frameLayout {
	l := layout(m.w, m.h)
	if boxH, _ := m.boxRegion(l); boxH > 0 {
		l.Body -= boxH
	}
	return l
}
