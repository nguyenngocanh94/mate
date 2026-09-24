package console

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// Selection, focus and status are three separate signals and never share a
// drawing method. Confusing any two of them is how a Console starts lying:
//
//	Selection - which row the keys act on. A one-cell accent marker in
//	            column 0 plus the row's background. Nothing else on the row
//	            changes colour, so a failed row that happens to be selected
//	            is still visibly failed.
//	Focus     - which pane the arrow keys move in. The focused pane's marker
//	            and title are accent; the other pane's selected row keeps a
//	            dim marker so it is still findable. Exactly one pane is
//	            accent at a time.
//	Status    - what the database recorded. Always a word, always the domain
//	            value ("awaiting_review", not "Review"). Colour tints that
//	            word and never replaces it, and never tints the background.
//
// Every constructor here returns text that carries the signal on its own,
// so the whole surface still reads correctly with colour stripped
// (plainPalette, and therefore every golden fixture).

// markerSpan is column 0 of a row: one cell.
//
//	selected in the focused pane   -> accent selection marker
//	selected in an unfocused pane  -> dim unfocused marker
//	anything else, headers included -> blank
func markerSpan(selected, paneFocused bool, g glyphSet, p palette) span {
	switch {
	case selected && paneFocused:
		return span{text: g.Selected, style: p.Acc}
	case selected:
		return span{text: g.Unfocused, style: p.Dim}
	default:
		return span{text: " ", style: p.Dim}
	}
}

// rowPrefix is the marker plus the one blank cell that separates it from
// the first column - two cells, the same on every row and every header, so
// columns line up whatever the selection is doing.
func rowPrefix(selected, paneFocused bool, g glyphSet, p palette) []span {
	return []span{markerSpan(selected, paneFocused, g, p), {text: " ", style: p.Dim}}
}

// selectRow applies the selected row's fill to a whole line, or leaves an
// unselected line alone. Selection is a row property, not a span property:
// it must reach the padding at the end of the row, and it must not touch
// any span's own foreground (see line.styleFor).
func selectRow(l *line, selected bool, p palette) *line {
	if !selected {
		return l
	}
	return l.background(p.Sel)
}

// paneTitleSpan draws a pane title: accent when the pane has focus, dim
// when it does not. Titles are the second half of the focus signal - the
// marker alone is one cell and easy to miss.
func paneTitleSpan(text string, focused bool, p palette) span {
	if focused {
		return span{text: text, style: p.Acc}
	}
	return span{text: text, style: p.Dim}
}

// columnHeaderSpan draws a dim uppercase column header. Headers share the
// row grid but are never selectable, so their marker cell stays blank.
func columnHeaderSpan(text string, p palette) span {
	return span{text: text, style: p.Dim}
}

// statusStyle maps a recorded status word to its hue. The word is the
// signal; this only tints it.
//
//	fg    - in progress or neutral: working, running, ready, starting
//	dim   - not started, or over without a result: spawned, stopped
//	amber - somebody must act: needs-decision, wait-mate, blocked, a
//	        binding recorded stale
//	red   - failed, a worktree recorded missing
//	green - finished
//
// working and running are deliberately plain fg, not green: they are
// recorded statuses, not proof that an agent is alive. wait-mate is amber
// even though it is not an inbox item (mvp.md section 4b): the crew has
// handed the task back and this column is the surface that says so, which
// is precisely why the inbox does not have to. stale and missing are here
// because the inspector routes CrewNode.Agent.Status and a worktree's
// status word through this function too (see seams.go); the design's token
// table colours both, and leaving them out renders them plain fg -
// indistinguishable from a healthy binding or worktree.
func statusStyle(word string, p palette) lipgloss.Style {
	switch word {
	case "needs-decision", "wait-mate", "blocked", "unknown", "stale":
		return p.Amber
	case "failed", "missing":
		return p.Red
	case "finished":
		return p.Green
	case "spawned", "draft", "created", "stopped", "stopping", "cancelled":
		return p.Dim
	default:
		return p.Fg
	}
}

// statusSpan renders a recorded status as its exact domain word.
func statusSpan(word string, p palette) span {
	return span{text: word, style: statusStyle(word, p)}
}

// attentionSpan renders an attention marker: "! " then the word, amber.
// The "!" is a label, not an icon, and the word is what actually says what
// is wrong.
func attentionSpan(word string, p palette) span {
	return span{text: "! " + word, style: p.Amber}
}

// failureSpan renders "! <word>" in red, for a state that is known to have
// gone wrong rather than merely needing a look.
func failureSpan(word string, p palette) span {
	return span{text: "! " + word, style: p.Red}
}

// unknownMarkSpan renders "? <word>" in amber: a read that failed, so the
// Console does not know. Distinct from attention both by the "?" and by
// where it is used.
func unknownMarkSpan(word string, p palette) span {
	return span{text: "? " + word, style: p.Amber}
}

// availabilitySpans renders one field whose read can fail, per the
// Known/Absent/Unknown rule:
//
//	Known   - the value, as it is. A known-bad value ("missing", "failed")
//	          is still Known; the caller passes the colour it deserves.
//	Absent  - "none", dim, plus why there is nothing here.
//	Unknown - "unknown", amber, plus why the read failed.
//
// Absent and Unknown never share a rendering: "none" and "unknown" are
// different words, so stripping colour still tells them apart. The reason
// text is the caller's - the query layer's field warnings decide what a
// failed read has to say for itself.
func availabilitySpans(a query.FieldState, value, reason string, valueStyle lipgloss.Style, g glyphSet, p palette) []span {
	var head span
	switch a {
	case query.Known:
		if value == "" {
			// A Known empty value is a fact, and must not collapse into
			// Absent: say it is empty rather than saying there is none.
			head = span{text: "(empty)", style: p.Dim}
		} else {
			head = span{text: value, style: valueStyle}
		}
	case query.Absent:
		head = span{text: "none", style: p.Dim}
	default:
		head = span{text: "unknown", style: p.Amber}
	}
	out := []span{head}
	if reason != "" {
		out = append(out, span{text: " " + g.Dot + " " + reason, style: p.Dim})
	}
	return out
}
