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
// This file owns every way the box is drawn - the Mate session view's left
// rail, the narrow digest that replaces it, the project frame's own panel
// and its one-line digest, and the peek overlay - so the four surfaces
// cannot disagree about what an entry looks like or which one is selected.
//
// Nothing here decides what an entry *means*. The verb, whether the entry
// needs attention and whether Enter may hand it to the Mate are all fields
// on the DTO, authored in internal/query next to the merge that produced
// them (query/box.go). A renderer that re-derived them would be a second
// copy of the vocabulary, free to drift from box.Attention.

// boxEntryLine is one entry, on one line, at every surface:
//
//	<marker><!> HH:MM  <who>  <verb>  <text>
//
// The marker is the selection signal (signals.go: a glyph, never colour
// alone) and the "!" is the attention signal, for the same reason - a
// golden fixture is rendered with plainPalette, so a state carried only by
// amber would be invisible to a reader with a monochrome terminal.
func boxEntryLine(e query.BoxEntry, selected, focused bool, g glyphSet, p palette, w int) *line {
	l := selectRow(newLine(), selected, p)
	l.addSpan(markerSpan(selected, focused, g, p))
	if e.Attention {
		l.add("!", p.Amber)
	} else {
		l.add(" ", p.Dim)
	}
	l.add(" ", p.Dim)
	l.add(truncateEnd(boxEntryText(e, g), max0(w-3), g), boxEntryStyle(e, p))
	return l
}

// boxEntryText is the entry's own words, without any selection or attention
// decoration: time, who it is about, the verb, and the payload.
func boxEntryText(e query.BoxEntry, g glyphSet) string {
	parts := []string{e.At.UTC().Format("15:04"), boxEntryWho(e)}
	if e.Verb != "" {
		verb := e.Verb
		if e.Kind == query.BoxIncident {
			verb = "incident:" + verb
		}
		parts = append(parts, verb)
	}
	if e.Text != "" {
		parts = append(parts, sanitizeText(e.Text))
	}
	return strings.Join(parts, "  ")
}

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
// how many entries need eyes, out of how many there are.
func boxCountLine(v query.Field[query.BoxView], g glyphSet, p palette) *line {
	l := newLine()
	if !v.IsKnown() {
		return l.add(" ", p.Dim).addSpans(availabilitySpans(v.State, "", "", p.Fg, g, p)...)
	}
	att := v.Value.Attention()
	if att > 0 {
		l.add(fmt.Sprintf(" %d attention", att), p.Amber)
	} else {
		l.add(" none needing attention", p.Dim)
	}
	return l.add(fmt.Sprintf(" %s %s", g.Dot, plural(len(v.Value.Entries), "entry", "entries")), p.Dim)
}

// boxDigestLine is the whole box in one line, from box.Summarize's own
// figures (carried on BoxView). It is what a frame too short for the panel
// shows instead: the counts are the part a reader cannot reconstruct from
// the crews table, and the last timestamp is how stale they are.
func boxDigestLine(v query.Field[query.BoxView], g glyphSet, p palette) *line {
	l := newLine().add(" BOX  ", p.Dim)
	if !v.IsKnown() {
		l.addSpans(availabilitySpans(v.State, "", v.Reason, p.Fg, g, p)...)
		return l
	}
	att := v.Value.Attention()
	if att > 0 {
		l.add(fmt.Sprintf("%d attention", att), p.Amber)
	} else {
		l.add("none needing attention", p.Dim)
	}
	l.add(fmt.Sprintf(" %s %s %s %d awaiting %s %s",
		g.Dot, plural(len(v.Value.Entries), "entry", "entries"),
		g.Dot, v.Value.Awaiting,
		g.Dot, plural(v.Value.Crews, "crew", "crews")), p.Dim)
	if !v.Value.LastAt.IsZero() {
		l.add(" "+g.Dot+" last "+v.Value.LastAt.UTC().Format("15:04:05"), p.Dim)
	}
	return l
}

// boxBodyLines draws the entries of a box into exactly h lines, oldest at
// the top so the newest is at the bottom - a log, read the way a chat log
// is read. The scroll offset is derived here rather than kept by the
// caller: it is a pure function of the selection and the pane height, and a
// stored offset would be one more thing to reconcile every time the window
// resized or a crew appended a line.
func boxBodyLines(v query.Field[query.BoxView], sel int, focused bool, g glyphSet, p palette, w, h int) []*line {
	if h <= 0 {
		return nil
	}
	if !v.IsKnown() {
		return fitLines([]*line{newLine().add(" ", p.Dim).addSpans(availabilitySpans(v.State, "", v.Reason, p.Fg, g, p)...)}, h)
	}
	entries := v.Value.Entries
	if len(entries) == 0 {
		return fitLines([]*line{newLine().add(" no crew has written a status line yet", p.Dim)}, h)
	}
	start, end := window(len(entries), boxSelectionTop(len(entries), sel, h), h)
	out := make([]*line, 0, h)
	for i := start; i < end; i++ {
		out = append(out, boxEntryLine(entries[i], i == sel, focused, g, p, w))
	}
	// Pad at the top, not the bottom: a box with fewer entries than rows
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

// boxSelectionTop resolves the scroll offset for a box body of h lines:
// keep the selected entry visible, and otherwise show the newest end.
func boxSelectionTop(total, sel, h int) int {
	if h <= 0 || total <= h {
		return 0
	}
	top := total - h
	if sel >= 0 && sel < top {
		top = sel
	}
	return clampInt(top, 0, total-h)
}

// boxDefaultSelection is where the selection sits when a reader has not
// moved it: the newest entry, which is the one at the bottom of the rail.
func boxDefaultSelection(v query.Field[query.BoxView]) int {
	if !v.IsKnown() || len(v.Value.Entries) == 0 {
		return -1
	}
	return len(v.Value.Entries) - 1
}

// boxSelectedEntry resolves a selection index against a view, and reports
// whether there is an entry there at all.
func boxSelectedEntry(v query.Field[query.BoxView], sel int) (query.BoxEntry, bool) {
	if !v.IsKnown() || sel < 0 || sel >= len(v.Value.Entries) {
		return query.BoxEntry{}, false
	}
	return v.Value.Entries[sel], true
}

// ---------- the project frame's box panel ----------

const (
	// boxPanelRows is the panel's whole height on a project frame: one rule,
	// one header, and six entries. Six is what makes the panel worth the
	// rows it costs - fewer, and the digest line says as much in one.
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
// title line carrying the same counts the rail's header does, and the
// entries themselves.
func (m Model) boxPanelLines(l frameLayout, h int) []*line {
	v := m.currentProject().Box
	focused := m.focus == paneBox
	head := leftRight(
		newLine().pad(1).addSpan(paneTitleSpan("BOX", focused, m.p)),
		boxCountLine(v, m.g, m.p).add(" ", m.p.Dim),
		l.Cols, m.g,
	)
	out := []*line{newLine().add(strings.Repeat(m.g.HRule, l.Cols), m.p.Faint), head}
	out = append(out, boxBodyLines(v, m.projectBoxSelection(), focused, m.g, m.p, l.Cols, max0(h-len(out)))...)
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
