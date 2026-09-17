package console

import (
	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The session view's focus model and its geometry.
//
// The Mate session view has two focus zones - the box (the left rail) and
// the terminal (the agent's own PTY) - and exactly one of them owns the
// keyboard. That replaces the Ctrl+b prefix this view used to carry: a
// prefix is a mode with no indicator, and a mis-typed prefix delivered the
// key that followed it straight into the agent's composer, which is how a
// reader ended up with a Mate in a state nobody asked for.
//
// A zone is entered by clicking inside it, or with F2 - one unprefixed key
// neither Claude Code nor Codex binds. The focused zone's border is drawn
// in the accent colour and the bottom hint line names only that zone's
// keys, so at every moment the frame says where the next keystroke goes.
//
// This file is also the one place that knows where each part of the frame
// sits on screen. sessionGeometry is computed once per render and once per
// mouse event, and both the renderer and the hit test read it, so a click
// can never land on a row the renderer drew somewhere else.

// sessionZone is which half of the session view owns the keyboard.
type sessionZone int

const (
	// zoneTerminal is the default on entering the view: every key, mouse
	// wheel and click goes to the agent's PTY, exactly as a real terminal
	// would deliver it.
	zoneTerminal sessionZone = iota
	// zoneBox is the rail. Nothing reaches the PTY while it has focus.
	zoneBox
)

func (z sessionZone) other() sessionZone {
	if z == zoneBox {
		return zoneTerminal
	}
	return zoneBox
}

// railMinWidth and railMaxWidth bound the draggable splitter. Below 24 the
// rail cannot hold a timestamp, a crew name and a verb on one line; above
// 72 it is taking columns from the agent's own screen for no extra
// information - the rail's widest content is a wrapped crew question, and
// past 72 cells that wraps no better, it just reflows.
const (
	railMinWidth = 24
	railMaxWidth = 72
	// railMinPane is how much of the frame the PTY keeps whatever the
	// splitter is dragged to: a terminal narrower than this is not a
	// terminal any harness draws usefully.
	railMinPane = 40
)

// resolveRailWidth is the rail's width for this frame: the reader's own, if
// they have dragged the splitter, clamped to what the frame can carry;
// otherwise the breakpoint default (session_render.go's sessionRailWidth).
// A frame with no rail at all (a Crew at any width, a Mate below 100
// columns) stays at 0 - the splitter does not conjure one.
func resolveRailWidth(kind SessionTargetKind, cols, want int) int {
	base := sessionRailWidth(kind, cols)
	if base == 0 || want <= 0 {
		return base
	}
	hi := cols - railMinPane - 1
	if hi > railMaxWidth {
		hi = railMaxWidth
	}
	if hi < railMinWidth {
		return base
	}
	return clampInt(want, railMinWidth, hi)
}

// ---------- clickable labels ----------

// labelID names one clickable label. Every clickable thing in the session
// view is one of these, so the hit test returns an identity rather than a
// rectangle the caller has to interpret.
type labelID int

const (
	labelNone labelID = iota
	labelProject
	labelMode
	labelAll
	labelRestart
	labelClear
	labelResolve
	labelReply
	labelPeek
	labelSend
	labelCancel
	labelYes
	labelNo
)

// labelSpec is a label's identity and the exact text drawn for it. The two
// travel together so the renderer and the hit test measure the same string.
type labelSpec struct {
	id   labelID
	text string
}

// placedLabel is a labelSpec with the frame coordinates it was drawn at.
type placedLabel struct {
	labelSpec
	x, y int
}

func (l placedLabel) hit(x, y int) bool {
	return y == l.y && x >= l.x && x < l.x+cells(l.text)
}

// sessionHeaderLabels are the rail header's five affordances. They are
// labels rather than key hints because every one of them is a recovery from
// a state the reader is already unhappy in - the wrong view, the wrong
// mode, the wrong list, a wedged Mate, a composer full of junk - and a
// recovery that needs a remembered keystroke is one a reader will not find.
//
// The `[all]` label names the state it is in rather than the one it offers,
// the way `[supervised]`/`[auto]` does: "[all on]" is the only way a reader
// looking at a rail full of `working` lines can tell that they turned the
// debugging view on rather than that the inbox filter broke. The words
// carry it, not the colour, so a monochrome terminal says the same thing.
func sessionHeaderLabels(mode query.Mode, all bool, g glyphSet) []labelSpec {
	modeText := string(mode)
	if modeText == "" {
		modeText = "mode"
	}
	allText := "[all]"
	if all {
		allText = "[all on]"
	}
	return []labelSpec{
		{labelProject, "[" + g.Back + " project]"},
		{labelMode, "[" + modeText + "]"},
		{labelAll, allText},
		{labelRestart, "[restart mate]"},
		{labelClear, "[clear composer]"},
	}
}

// packLabels lays labels out into lines of at most w cells, one space
// between neighbours and one cell of left margin, wrapping greedily. A
// label wider than the whole line still gets its own line and is cut by the
// renderer; the hit test then drops it, because a label the reader cannot
// see whole is not one they can be asked to click.
func packLabels(specs []labelSpec, w int) [][]placedLabel {
	var out [][]placedLabel
	var cur []placedLabel
	x := 1
	for _, s := range specs {
		width := cells(s.text)
		if len(cur) > 0 && x+width > w {
			out = append(out, cur)
			cur, x = nil, 1
		}
		cur = append(cur, placedLabel{labelSpec: s, x: x})
		x += width + 1
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// ---------- the inline action strip ----------

// boxStripButtons is the trailing action strip an attention entry grows
// when it is selected or hovered: the three things a reader does with a
// crew that is waiting on them, as buttons rather than as remembered keys.
func boxStripButtons(g glyphSet) []labelSpec {
	return []labelSpec{
		{labelResolve, "[resolve]"},
		{labelReply, "[reply]"},
		{labelPeek, "[peek]"},
	}
}

// boxStripWidth is how many cells the strip occupies, including the single
// spaces between its buttons and the one that separates it from the entry's
// own text.
func boxStripWidth(g glyphSet) int {
	total := 1
	for i, b := range boxStripButtons(g) {
		if i > 0 {
			total++
		}
		total += cells(b.text)
	}
	return total
}

const (
	// boxEntryLead is the marker, attention mark and separating space every
	// entry line starts with (box.go's boxEntryLine).
	boxEntryLead = 3
	// boxStripMinText is how much of an entry's own words must survive for
	// the strip to be worth drawing over them.
	boxStripMinText = 10
)

// boxStripFits reports whether an entry line w cells wide can carry the
// strip and still show a readable amount of the entry's own words. The
// renderer and the hit test both ask this, so a strip that was not drawn is
// never clickable and one that was drawn always is.
func boxStripFits(w int, g glyphSet) bool {
	return w-boxEntryLead-boxStripWidth(g) >= boxStripMinText
}

// boxStripPlacement returns the strip's buttons at their frame
// coordinates, given the entry line's left edge and width. ok is false when
// the line is too narrow, which is also exactly when the renderer draws no
// strip.
func boxStripPlacement(x0, y, w int, g glyphSet) ([]placedLabel, bool) {
	if !boxStripFits(w, g) {
		return nil, false
	}
	at := x0 + w - boxStripWidth(g) + 1
	var out []placedLabel
	for i, b := range boxStripButtons(g) {
		if i > 0 {
			at++
		}
		out = append(out, placedLabel{labelSpec: b, x: at, y: y})
		at += cells(b.text)
	}
	return out, true
}

// ---------- frame geometry ----------

// sessionGeom is where every part of one session frame sits. It is derived
// from the same inputs the renderer draws from, and the renderer uses it
// rather than recomputing, so the hit test cannot disagree with the screen.
type sessionGeom struct {
	w, h int
	kind SessionTargetKind

	// railW is 0 when no rail is drawn; splitX is then -1.
	railW  int
	splitX int

	// bodyTop/bodyH is the rail-and-pane region, below the header and its
	// rule and above the outcome and hint lines.
	bodyTop, bodyH int

	railHdrH, railFootH    int
	railBodyTop, railBodyH int

	paneX, paneW int
	// paneTop/paneH is the terminal buffer itself: the pane region less the
	// notice/runtime banner above it and, in snapshot mode, the composer
	// chrome below it.
	paneTop, paneH int

	digestTop, digestH int

	outcome bool
	hintRow int

	// labels are the header and footer affordances at their frame
	// coordinates. The entry strips are not here: they depend on which
	// entry is selected or hovered and are resolved per click
	// (sessionEntryStrip).
	labels []placedLabel
}

func (g sessionGeom) inRail(x, y int) bool {
	return g.railW > 0 && x >= 0 && x < g.railW && y >= g.bodyTop && y < g.bodyTop+g.bodyH
}

func (g sessionGeom) inSplitter(x, y int) bool {
	return g.splitX >= 0 && x == g.splitX && y >= g.bodyTop && y < g.bodyTop+g.bodyH
}

func (g sessionGeom) inDigest(x, y int) bool {
	return g.digestH > 0 && y >= g.digestTop && y < g.digestTop+g.digestH
}

func (g sessionGeom) inPane(x, y int) bool {
	return x >= g.paneX && x < g.paneX+g.paneW && y >= g.bodyTop && y < g.bodyTop+g.bodyH
}

func (g sessionGeom) labelAt(x, y int) (labelID, bool) {
	for _, l := range g.labels {
		if l.hit(x, y) {
			return l.id, true
		}
	}
	return labelNone, false
}

// sessionGeometry lays one session frame out. stream says whether the
// agent's own PTY owns the pane, which is what decides whether the pane
// carries the Console's composer chrome underneath it.
func sessionGeometry(snapshot SessionSnapshot, rail boxRail, stream bool, w, h int, g glyphSet) sessionGeom {
	geo := sessionGeom{w: w, h: h, kind: snapshot.Target.Kind, splitX: -1, hintRow: h - 1}
	geo.outcome = rail.outcome.tone != toneNone && rail.outcome.text != ""
	rest := h - 3 // the header, the rule under it, and the hint line
	if geo.outcome {
		rest--
	}
	geo.bodyTop = 2
	geo.paneW = w
	railW := resolveRailWidth(snapshot.Target.Kind, w, rail.railW)
	switch {
	case railW > 0:
		geo.railW, geo.splitX = railW, railW
		geo.paneX, geo.paneW = railW+1, w-railW-1
		geo.bodyH = max0(rest)
		labelRows := packLabels(sessionHeaderLabels(snapshot.Target.Mode, rail.all, g), railW)
		for y, row := range labelRows {
			for _, l := range row {
				if l.x+cells(l.text) > railW {
					continue // cut by the renderer; not offered to the mouse
				}
				l.y = geo.bodyTop + y
				geo.labels = append(geo.labels, l)
			}
		}
		geo.railHdrH = len(labelRows) + 3 // title, counts, rule
		geo.railFootH = 1                 // the rule above the footer
		if rail.confirm {
			geo.railFootH++
		}
		if rail.reply {
			geo.railFootH++
		}
		geo.railBodyTop = geo.bodyTop + geo.railHdrH
		geo.railBodyH = max0(geo.bodyH - geo.railHdrH - geo.railFootH)
		geo.labels = append(geo.labels, sessionFooterLabels(geo, rail)...)
	case snapshot.Target.Kind == SessionTargetMate:
		digest := sessionDigestHeight(boxList{field: snapshot.Box, all: rail.all})
		geo.digestTop, geo.digestH = geo.bodyTop, digest
		geo.bodyTop = 2 + digest + 1
		geo.bodyH = max0(rest - digest - 1)
	default:
		geo.bodyH = max0(rest)
	}
	banner := sessionBannerLineCount(snapshot, stream)
	geo.paneTop = geo.bodyTop + banner
	geo.paneH = max0(geo.bodyH - banner)
	if !stream {
		geo.paneH = max0(geo.paneH - sessionComposerChromeHeight)
	}
	return geo
}

// sessionFooterLabels are the rail footer's buttons: the reply input's
// [send]/[cancel] and the restart confirmation's [yes]/[no]. They sit on
// the same line as the field or the question they answer, right-aligned,
// so a reader's eye does not have to leave the thing they are deciding.
func sessionFooterLabels(geo sessionGeom, rail boxRail) []placedLabel {
	row := geo.bodyTop + geo.bodyH - 1
	var specs []labelSpec
	switch {
	case rail.reply:
		specs = []labelSpec{{labelSend, "[send]"}, {labelCancel, "[cancel]"}}
	case rail.confirm:
		specs = []labelSpec{{labelYes, "[yes]"}, {labelNo, "[no]"}}
	default:
		return nil
	}
	return rightAlignLabels(specs, 0, row, geo.railW)
}

// rightAlignLabels places labels against the right edge of a w-cell line
// starting at x0, one space between them and one before the edge.
func rightAlignLabels(specs []labelSpec, x0, y, w int) []placedLabel {
	total := 1
	for i, s := range specs {
		if i > 0 {
			total++
		}
		total += cells(s.text)
	}
	at := x0 + w - total + 1
	if at < x0 {
		return nil
	}
	out := make([]placedLabel, 0, len(specs))
	for i, s := range specs {
		if i > 0 {
			at++
		}
		out = append(out, placedLabel{labelSpec: s, x: at, y: y})
		at += cells(s.text)
	}
	return out
}

// sessionEntryAt maps a body row of a box pane back to the entry drawn on
// it. It mirrors boxBodyLines' own windowing - the same plan, the same
// scroll offset, the same top padding - rather than storing what was drawn,
// so a click resolves against the frame the reader is actually looking at.
// A click on one of the wrapped question lines under the selected item
// resolves to that item, because the block is one thing on screen and a
// reader who clicks the words they are reading means the row they belong to.
func sessionEntryAt(b boxList, sel, h, row, w int) (int, bool) {
	if h <= 0 || row < 0 || row >= h || !b.known() || len(b.rows()) == 0 {
		return 0, false
	}
	plan := boxPlan(b, sel, w)
	start, end := window(len(plan), boxPlanTop(plan, sel, h), h)
	pad := h - (end - start)
	if row < pad {
		return 0, false
	}
	i := start + (row - pad)
	if i >= end {
		return 0, false
	}
	return plan[i].index, true
}

// sessionEntryStrip is the action strip of the entry at one index, when
// that entry has one: only an attention entry that is selected or hovered
// grows buttons, which is the same condition boxEntryLine draws them under.
func sessionEntryStrip(b boxList, sel, hover, index, x0, row, w int, g glyphSet) ([]placedLabel, bool) {
	e, ok := boxSelectedEntry(b, index)
	if !ok || !boxEntryHasStrip(e, index == sel, index == hover) {
		return nil, false
	}
	return boxStripPlacement(x0, row, w, g)
}

// boxEntryHasStrip is the one predicate the renderer and the hit test share
// for "does this entry show its buttons": an entry that needs a decision,
// is blocked, finished or failed, or is an incident - which is exactly what
// internal/query already decided when it set Attention - and only while the
// reader's cursor or pointer is on it.
func boxEntryHasStrip(e query.BoxEntry, selected, hovered bool) bool {
	return e.Attention && (selected || hovered)
}
