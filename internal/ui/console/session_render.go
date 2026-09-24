package console

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// ADR 0025 step 3: the embedded snapshot-session renderer. It draws one
// SessionSnapshot (fed by the fake controller in tests or the real snapshot
// ports built by cmd/mate) onto the Console's own cell grid, palette and glyph set - session-view-contract.md
// (step 1) is the frozen specification these functions are built against;
// where this file disagrees with a golden fixture, the fixture wins.
//
// This file never renders from hardcoded sample text: every string drawn
// here comes from the SessionSnapshot argument. Sample data belongs only in
// tests, seeded through FakeSessionController.Seed.

// RenderSessionFrame draws one session-mode frame at cols x rows: a
// one-line header, then either a rail split (Mate, >=100 cols), a digest
// (Mate, <100 cols) or nothing (Crew, every width) above the transcript and
// composer pane.
//
// composer is the operator's in-progress, unsent input text (ADR 0025 step
// 4's composer), and rail is where the reader has put the box cursor.
// Neither is part of SessionSnapshot: unlike Runtime, Transcript and Box
// they are never observed from the runtime or from committed state, and
// folding them into the snapshot would make a poll result depend on what
// the reader happened to be doing.
func RenderSessionFrame(snapshot SessionSnapshot, composer string, rail boxRail, w, h int, g glyphSet, p palette) string {
	return renderSessionFrame(snapshot, nil, false, composer, rail, w, h, g, p)
}

// RenderStreamSessionFrame renders the same session chrome (header,
// rail/digest) around an atomic TerminalBuffer frame. Unlike
// RenderSessionFrame, it never draws the composer box: the captain's ruling
// (2026-09-12, docs/phase1/session-view-contract.md's "Stream mode" section)
// is that once a real PTY owns the transcript pane, that PTY draws its own
// composer (the harness's own input line is already part of its terminal
// output) - a second, Console-drawn composer on top of it is the very "two
// input boxes" defect the ruling was written to close. Side-channel
// metadata (RecordedStatus, the runtime banner, the inbox rail) is
// unaffected; the composer box's 4 lines go to the PTY, and the one line
// of Console chrome left on the frame is sessionHintLine, which names the
// focused zone's keys (session_focus.go).
func RenderStreamSessionFrame(snapshot SessionSnapshot, buffer *TerminalBuffer, frozen bool, rail boxRail, w, h int, g glyphSet, p palette) string {
	if buffer == nil {
		return RenderSessionFrame(snapshot, "", rail, w, h, g, p)
	}
	frame := buffer.Snapshot()
	return renderSessionFrame(snapshot, &frame, frozen, "", rail, w, h, g, p)
}

// renderSessionFrame draws one session frame. frozen marks a terminal whose
// stream has died: its buffer is the agent's last live screen, kept on
// screen unchanged while the snapshot fallback's entry read is in flight
// (session_mode.go's beginStreamFallback deliberately does not resize it),
// so it is cropped from the HEAD rather than the tail when the notice
// banner leaves the frame fewer rows than the buffer holds. The frozen flag
// is ignored for a nil terminal (snapshot mode never freezes a buffer).
func renderSessionFrame(snapshot SessionSnapshot, terminal *TerminalSnapshot, frozen bool, composer string, rail boxRail, w, h int, g glyphSet, p palette) string {
	geo := sessionGeometry(snapshot, rail, terminal != nil, w, h, g)
	s := newScreen(w, h)
	s.push(sessionHeaderLine(snapshot.Target, snapshot.RecordedStatus, g, p))
	switch {
	case geo.railW > 0:
		s.push(sessionSplitRule(w, geo.railW, g, p, rail.zone == zoneBox))
		railLines := sessionRailLines(snapshot.Box, rail, geo, g, p)
		pane := sessionPaneLines(snapshot, terminal, frozen, composer, g, p, geo.paneW, geo.bodyH)
		divider := span{text: g.VRule, style: p.Faint}
		if rail.zone == zoneBox {
			divider.style = p.Acc
		}
		for i := 0; i < geo.bodyH; i++ {
			s.pushSplit(railLines[i], geo.railW, divider, pane[i], geo.paneW)
		}
	case geo.digestH > 0:
		s.push(sessionFullRule(w, g, p))
		for _, l := range sessionDigestLines(snapshot.Box, rail, g, p, w) {
			s.push(l)
		}
		s.push(sessionFullRule(w, g, p))
		for _, l := range sessionPaneLines(snapshot, terminal, frozen, composer, g, p, w, geo.bodyH) {
			s.push(l)
		}
	default: // Crew: no rail, no digest, at any width.
		s.push(newLine())
		for _, l := range sessionPaneLines(snapshot, terminal, frozen, composer, g, p, w, geo.bodyH) {
			s.push(l)
		}
	}
	// The outcome line takes the frame's full width, not the rail's: a
	// refusal from internal/send quotes the composer state and the text it
	// refused to overwrite, and 36 columns of that is a sentence cut before
	// it says anything. It is the session frame's equivalent of frame.go's
	// message line, which is why it sits on the bottom edge the same way.
	if geo.outcome {
		s.push(boxOutcomeLine(rail.outcome, g, p, w))
	}
	s.push(sessionHintLine(rail, geo, g, p, w))
	return s.String()
}

// sessionFrameOverhead is the header/rail/digest height renderSessionFrame
// always reserves above the transcript pane, before either mode's own pane
// chrome (snapshot mode's composer box; stream mode has none). Shared by
// SessionTranscriptCapacity and StreamTranscriptCapacity so the two can
// never drift apart on the part of the layout they actually share.
func sessionFrameOverhead(kind SessionTargetKind, w int) int {
	overhead := 1 // header
	switch {
	case kind == SessionTargetMate && sessionRailWidth(kind, w) > 0:
		overhead++ // split rule; rail runs alongside the pane, not above it
	case kind == SessionTargetMate:
		overhead += 2 // full rule above and below the digest
	default:
		overhead++ // Crew: one blank line in place of a rail/digest
	}
	return overhead
}

// SessionTranscriptCapacity returns the maximum number of transcript lines
// RenderSessionFrame (snapshot mode) could ever display for a session frame
// of kind at w x h, given today's fixed layout rules (header, rail/digest
// split, the composer's own fixed 4-line chrome). It ignores two things
// that can only shrink the real figure further, never grow it: the
// Inbox-dependent digest height on a narrow Mate frame, and the one-line
// runtime banner a non-Known Runtime adds. That makes this a safe upper
// bound rather than an exact figure - which is what a caller bounding a
// runtime read request needs (session_bridge.go): asking for a handful of
// lines the frame cannot actually show is harmless, asking for far fewer
// than it could show is a truncated transcript with room to spare. Never
// negative.
func SessionTranscriptCapacity(kind SessionTargetKind, w, h int) int {
	return max0(h - sessionFrameOverhead(kind, w) - sessionComposerChromeHeight - sessionHintHeight)
}

// sessionComposerChromeHeight is sessionComposerChrome's fixed height: the
// box's two edges, its input line and the harness hint row under it.
const sessionComposerChromeHeight = 4

// sessionHintHeight is the frame's single bottom key line
// (sessionHintLine), drawn in both modes at every width: it is the only
// thing on screen that says which zone the next keystroke belongs to.
const sessionHintHeight = 1

// StreamTranscriptCapacity is SessionTranscriptCapacity's stream-mode
// counterpart: the same upper bound, but reserving only
// sessionHintHeight instead of the composer box's 4 lines, since
// RenderStreamSessionFrame draws no composer at all (ADR 0026 step 6, the
// captain's ruling) - the PTY gets back most, not all, of the height a
// Console-drawn composer used to take; the one line it does not get is the
// frame's key hint line. session_mode.go's streamTerminalSize uses this,
// not SessionTranscriptCapacity, to size the actual PTY the stream opens.
func StreamTranscriptCapacity(kind SessionTargetKind, w, h int) int {
	return max0(h - sessionFrameOverhead(kind, w) - sessionHintHeight)
}

// ---------- header ----------

// harnessDisplayLabel names the harness in the header the way the harness's
// own product does ("claude-code"), not the shorter internal
// query.HarnessKind value ("claude") list.go and the inspector already
// render elsewhere - the header is naming a product to the reader, not a
// database column.
func harnessDisplayLabel(k query.HarnessKind) string {
	switch k {
	case query.HarnessClaude:
		return "claude-code"
	case query.HarnessCodex:
		return "codex"
	default:
		return string(k)
	}
}

// sessionHeaderLine names harness, agent and (for a Crew) worktree, per the
// frame-shape contract. RecordedStatus is appended only when it is not
// Known: the pinned goldens carry a Known recorded status and show no such
// suffix, and ADR 0025 never asks the header to restate a healthy, already-
// implied status - only to surface a read that could not be trusted.
func sessionHeaderLine(target SessionTarget, recorded query.Field[string], g glyphSet, p palette) *line {
	l := newLine().add(" "+harnessDisplayLabel(target.HarnessKind)+" "+g.Dot+" ", p.Dim)
	l.add(target.AgentName, p.Dim)
	if target.Kind == SessionTargetCrew && target.Worktree != "" {
		l.add(" "+g.Dot+" "+target.Worktree, p.Dim)
	}
	// The communication mode (mvp.md section 5). It is on the header rather
	// than in the rail because it governs whether anything may be typed into
	// this pane on the reader's behalf at all, which is the first thing a
	// reader looking at a live Mate needs to know.
	if target.Mode != "" {
		l.add(" "+g.Dot+" "+string(target.Mode), p.Dim)
	}
	if recorded.State != query.Known {
		l.add(" "+g.Dot+" ", p.Dim)
		l.addSpans(availabilitySpans(recorded.State, recorded.Value, recorded.Reason, p.Fg, g, p)...)
	}
	return l
}

func sessionFullRule(w int, g glyphSet, p palette) *line {
	return newLine().add(strings.Repeat(g.HRule, w), p.Faint)
}

// sessionSplitRule is frame.go's ruleLine shape, with the rail's own width
// standing in for the inspector's: the tee marks the column the vertical
// divider below it lines up with. The rail's half of the rule goes accent
// while the box has focus, which is the border signal the focus model
// promises - one zone's edge is lit, and it is the zone the next keystroke
// belongs to.
func sessionSplitRule(w, rw int, g glyphSet, p palette, boxFocused bool) *line {
	railStyle, teeStyle := p.Faint, p.Faint
	if boxFocused {
		railStyle, teeStyle = p.Acc, p.Acc
	}
	return newLine().
		add(strings.Repeat(g.HRule, rw), railStyle).
		add(g.TeeDown, teeStyle).
		add(strings.Repeat(g.HRule, w-rw-1), p.Faint)
}

// sessionRailWidth is the rail's default width: it exists only for a Mate,
// and only at the two breakpoints the Console's own inspector already uses
// (140, 100 - layout.go's inspectorWide/Narrow switch).
//
// The widths (58, 54) are sized for what the rail has to carry on one line:
// an inbox row's own words - "HH:MM  k3  stuck, quiet too long", 30 cells -
// the three-cell lead, and the action strip that row grows under the cursor
// ([assign], boxStripWidth). The defaults sit well above the sum, which is
// what keeps the need phrase whole while the button is on screen: the
// earlier 50/44 cut "needs-decision" to "need…" the moment the reader
// selected the row - the one word they were reading it for. A reader who
// wants the balance elsewhere drags the splitter (session_focus.go): these
// are defaults, not limits.
func sessionRailWidth(kind SessionTargetKind, cols int) int {
	if kind != SessionTargetMate {
		return 0
	}
	switch {
	case cols >= 140:
		return 58
	case cols >= 100:
		return 54
	default:
		return 0
	}
}

// ---------- rail (Mate, >=100 cols) and digest (Mate, <100 cols) ----------
//
// The rail is the project's message box (mvp.md task 15, section 4): every
// box entry, newest at the bottom, one line each, with the selected one
// marked and attention entries flagged. It replaces v1's interaction inbox,
// which had a lifecycle - queued, awaiting reply, answered - that mate
// deliberately does not have ("Câu hỏi của crew không có vòng đời").
// box.go owns how one entry is drawn; this file owns the pane around it.

// boxRail is the rail's interaction state. It is Console state, not part of
// a SessionSnapshot: a poll result must not depend on where the reader has
// put the cursor or what they are half-way through typing, the same reason
// the composer is a separate argument to RenderSessionFrame.
type boxRail struct {
	// all is the `[all]` toggle: draw the whole merged log instead of the
	// inbox. It is a debugging view and it is off every time the Console
	// starts (box_keys.go's toggleBoxAll).
	all bool
	// sel is the index into the rows on screen the keys act on, -1 for none.
	sel int
	// hover is the entry the pointer is over, -1 for none. It exists only
	// so an entry can show its action strip before the reader has committed
	// to selecting it; nothing else reads it.
	hover int
	// zone is which half of the view owns the keyboard (session_focus.go).
	// It decides which border is accent and which keys the hint line names.
	zone sessionZone
	// railW is the width the reader has dragged the splitter to, 0 for the
	// breakpoint default. It persists for the Console's run.
	railW int
	// outcome is the one line the last box action left behind: the Model's
	// own footer message. The session frame is not built from frame.go's
	// six-line chrome and so has no message line of its own, so
	// renderSessionFrame reserves the bottom row of the whole frame for it -
	// full width, because a refused send quotes the screen it was refused
	// from and the rail's 36 columns would cut that mid-sentence.
	outcome footerMsg
}

// sessionRailLines returns exactly geo.bodyH *line values for the rail
// pane: the two filter labels, the breadcrumb, the count line, and the box
// body under them. It draws no key hints of its own - the frame's single
// bottom hint line names the focused zone's keys and nothing else, so a
// reader is never shown two key lines and left to work out which one is
// live.
func sessionRailLines(v query.Field[query.BoxView], rail boxRail, geo sessionGeom, g glyphSet, p palette) []*line {
	w := geo.railW
	b := boxList{field: v, all: rail.all}
	header := make([]*line, 0, geo.railHdrH)
	for _, row := range packLabels(sessionHeaderLabels(), w) {
		l := newLine()
		at := 0
		for _, lab := range row {
			l.add(strings.Repeat(" ", max0(lab.x-at)), p.Dim)
			l.add(lab.text, labelStyle(lab.id, rail, p))
			at = lab.x + cells(lab.text)
		}
		header = append(header, l.cut(w, g))
	}
	title := newLine().add(" ", p.Dim).addSpan(paneTitleSpan("CREW "+g.Crumb+" MATE", rail.zone == zoneBox, p))
	header = append(header, title, boxCountLine(b, g, p), sessionFullRule(w, g, p))

	footer := []*line{sessionFullRule(w, g, p)}

	hover := -1
	if rail.zone == zoneBox {
		hover = rail.hover
	}
	body := boxBodyLines(b, rail.sel, hover, rail.zone == zoneBox, g, p, w, geo.railBodyH)
	return fitLines(append(append(header, body...), footer...), geo.bodyH)
}

// labelStyle tints one header label: the filter the box is showing carries
// the accent, the other is dim. The colour is the quick signal, not the only
// one - the count line under them says which list is on in words.
func labelStyle(id labelID, rail boxRail, p palette) lipgloss.Style {
	switch id {
	case labelAll:
		if rail.all {
			return p.Acc
		}
		return p.Dim
	case labelWaiting:
		if !rail.all {
			return p.Acc
		}
		return p.Dim
	}
	return p.Fg
}

// sessionHintLine is the frame's one key line: the keys of the zone that
// owns the keyboard, and nothing else. Naming both zones' keys at once is
// what the Ctrl+b prefix already did badly - the reader had to hold in
// their head which half of the line applied to the key they were about to
// press.
func sessionHintLine(rail boxRail, geo sessionGeom, g glyphSet, p palette, w int) *line {
	l := newLine()
	switch {
	case rail.zone == zoneBox:
		l.add(" BOX  ", p.Bold).
			add(g.UpDown, p.Fg).add(" move", p.Dim).
			add("  Enter", p.Fg).add(" open crew", p.Dim).
			add("  a", p.Fg).add(" assign", p.Dim).
			add("  l", p.Fg).add(" all", p.Dim).
			add("  m", p.Fg).add(" mode", p.Dim).
			add("  o", p.Fg).add(" actions", p.Dim).
			add("  Esc", p.Fg).add(" project", p.Dim).
			add("  F2", p.Fg).add(" terminal", p.Dim)
		return l.cut(w, g)
	default:
		l.add(" TERMINAL  ", p.Bold).add("every key goes to the agent", p.Dim)
		if geo.railW > 0 || geo.digestH > 0 {
			l.add("  "+g.Dot+"  ", p.Faint).add("F2", p.Fg).add(" box", p.Dim)
		} else {
			l.add("  "+g.Dot+"  ", p.Faint).add("F2", p.Fg).add(" console", p.Dim)
		}
		return l.cut(w, g)
	}
}

// maxDigestEntries is how many box entries the narrow-Mate digest lists
// before collapsing the rest into an "N older" line. sessionDigestHeight
// derives its own height from the same constant, so the reservation
// session_mode.go makes for the digest and the digest the renderer actually
// draws cannot drift apart.
const maxDigestEntries = 3

// sessionDigestHeight is how many rows sessionDigestLines will draw for a
// given box: always its header line, then up to maxDigestEntries entries,
// then an "N older" line when more exist. A caller that must reserve the
// digest's rows before rendering (stream mode's streamTerminalSize, so the
// PTY is never handed rows the frame then crops) needs the count without
// building the lines. A test pins len(sessionDigestLines(...)) ==
// sessionDigestHeight(...).
func sessionDigestHeight(b boxList) int {
	if !b.known() {
		return 2
	}
	total := len(b.rows())
	shown := total
	if shown > maxDigestEntries {
		shown = maxDigestEntries
	}
	if shown == 0 {
		shown = 1 // the "nothing yet" line
	}
	height := 1 + shown
	if total > maxDigestEntries {
		height++
	}
	return height
}

// sessionDigestLines is the rail below its own breakpoint: there is no room
// for a second pane, so the box collapses into a header line plus the
// newest few entries stacked above the transcript rather than beside it.
// The newest end is kept - the same end the rail anchors to - and the
// header still names how many entries need attention, so nothing is hidden
// without a count saying so.
func sessionDigestLines(v query.Field[query.BoxView], rail boxRail, g glyphSet, p palette, w int) []*line {
	b := boxList{field: v, all: rail.all}
	head := leftRight(
		newLine().add(" CREW "+g.Crumb+" MATE", p.Bold),
		boxCountLine(b, g, p).add(" ", p.Dim),
		w, g,
	)
	out := []*line{head}
	if !b.known() {
		return append(out, newLine().add(" ", p.Dim).addSpans(availabilitySpans(v.State, "", v.Reason, p.Fg, g, p)...))
	}
	entries := b.rows()
	if len(entries) == 0 {
		return append(out, newLine().add(" "+boxEmptyText(rail.all), p.Dim))
	}
	start := 0
	if len(entries) > maxDigestEntries {
		start = len(entries) - maxDigestEntries
		out = append(out, newLine().add(fmt.Sprintf(" %s %d older", g.Up, start), p.Dim))
	}
	hover := -1
	if rail.zone == zoneBox {
		hover = rail.hover
	}
	for i := start; i < len(entries); i++ {
		out = append(out, boxEntryLine(entries[i], i == rail.sel, i == hover, rail.zone == zoneBox, rail.all, g, p, w))
	}
	return out
}

// ---------- transcript + composer ----------

// sessionPaneLines is the harness's own panel: a tail-anchored transcript
// (a terminal shows the newest activity, not the oldest, so once content
// exceeds the pane the head is dropped, never the tail) over a fixed
// composer chrome pinned to the bottom edge - snapshot mode only. In stream
// mode (terminal != nil) the composer box itself is gone: the PTY's own
// screen already includes whatever composer the harness draws, and drawing
// a second one on top of it is the defect the captain's ruling
// (2026-09-12) closed. The frame's own key line (sessionHintLine) is not
// drawn here: it belongs to the whole frame, not to this pane, because it
// names the keys of whichever zone has focus. A non-Known Runtime gets one
// banner line above the transcript - "runtime_missing" (Absent) or
// "unknown" (Unknown), never upgraded into a lifecycle word (ADR 0025) -
// without clearing or replacing whatever the last successful poll recorded.
func sessionPaneLines(snapshot SessionSnapshot, terminal *TerminalSnapshot, frozen bool, composer string, g glyphSet, p palette, w, h int) []*line {
	var chrome []*line
	if terminal == nil {
		chrome = sessionComposerChrome(g, p, w, composer)
	}
	bodyH := h - len(chrome)
	if bodyH < 0 {
		bodyH = 0
	}

	var banner []*line
	if snapshot.ControllerNotice != "" {
		banner = append(banner, newLine().addSpan(unknownMarkSpan("notice", p)).add(" "+snapshot.ControllerNotice, p.Dim))
	}
	if snapshot.Runtime.Status != query.Known &&
		!(terminal != nil && snapshot.Runtime.Reason == "session metadata pending") {
		banner = append(banner, sessionRuntimeBannerLine(snapshot.Runtime, g, p))
	}
	avail := bodyH - len(banner)
	if avail < 0 {
		avail = 0
	}

	transcript := sessionTranscriptLines(snapshot.Transcript, g, p, w)
	if terminal != nil {
		transcript = terminalSnapshotLines(*terminal)
	}
	tail := transcript
	if len(tail) > avail {
		if frozen {
			// The frozen buffer is the agent's own last screen, top row first.
			// The notice banner took a frame row, so the frame now wants fewer
			// rows than the buffer holds; cropping the tail would drop the
			// agent's first line (its prompt, a shell's command echo) off the
			// top and leave a screen the reader cannot scroll. Keep the head.
			tail = tail[:avail]
		} else {
			tail = tail[len(tail)-avail:]
		}
	}
	var body []*line
	for len(body) < avail-len(tail) {
		body = append(body, newLine())
	}
	body = append(body, tail...)
	for len(body) < avail {
		body = append(body, newLine())
	}
	body = body[:avail]

	out := append(append([]*line{}, banner...), body...)
	return append(out, chrome...)
}

func sessionBannerLineCount(snapshot SessionSnapshot, terminal bool) int {
	count := 0
	if snapshot.ControllerNotice != "" {
		count++
	}
	if snapshot.Runtime.Status != query.Known &&
		!(terminal && snapshot.Runtime.Reason == "session metadata pending") {
		count++
	}
	return count
}

func terminalSnapshotLines(frame TerminalSnapshot) []*line {
	lines := make([]*line, 0, frame.Height)
	for y := 0; y < frame.Height; y++ {
		l := newLine()
		if y >= len(frame.Cells) {
			lines = append(lines, l)
			continue
		}
		row := frame.Cells[y]
		var run strings.Builder
		var runStyle lipgloss.Style
		var runEmulatorStyle uv.Style
		haveRun := false
		runHasEmulatorStyle := false
		runCursor := false
		flush := func() {
			if !haveRun {
				return
			}
			l.add(run.String(), runStyle)
			run.Reset()
			haveRun = false
		}
		for x := 0; x < frame.Width && x < len(row); {
			cell := row[x]
			if cell.Width <= 0 {
				x++
				continue
			}
			content := cell.Content
			if content == "" {
				content = " "
			}
			// Conceal intentionally replaces a wide glyph with one visible
			// space while retaining its display Width. Pad to the emulator's
			// cell width instead of measuring Content, or the next cell shifts
			// left and corrupts the frame geometry.
			if used := cells(content); used < cell.Width {
				content += strings.Repeat(" ", cell.Width-used)
			}
			cursor := frame.CursorVisible && x == frame.CursorX && y == frame.CursorY
			sameStyle := haveRun && runCursor == cursor && runHasEmulatorStyle && cell.hasEmulatorStyle &&
				runEmulatorStyle.Equal(&cell.emulatorStyle)
			if !sameStyle {
				flush()
				// The emulator-to-lipgloss translation happens here, once per
				// coalesced run, not once per cell: cellLipglossStyle builds a
				// fresh Style and allocates for every colour it sets, so calling
				// it on a cell whose run it cannot join would pay that cost
				// frame after frame for no rendering difference (see
				// TerminalCell.emulatorStyle's own note, termbuffer.go).
				style := cell.Style
				if cell.hasEmulatorStyle {
					style = cellLipglossStyle(cell.emulatorStyle)
				}
				if cursor {
					style = style.Reverse(true)
				}
				runStyle = style
				runEmulatorStyle = cell.emulatorStyle
				runHasEmulatorStyle = cell.hasEmulatorStyle
				runCursor = cursor
				haveRun = true
			}
			run.WriteString(content)
			x += cell.Width
		}
		flush()
		lines = append(lines, l)
	}
	return lines
}

// sessionRuntimeBannerLine renders what the last poll observed about the
// live process - never what recorded lifecycle status says, and never
// upgraded from Absent/Unknown into stopped/needs_repair/failed (ADR 0025).
func sessionRuntimeBannerLine(rt SessionRuntime, g glyphSet, p palette) *line {
	l := newLine()
	if rt.Status == query.Absent {
		l.addSpan(failureSpan("runtime_missing", p))
	} else {
		l.addSpan(unknownMarkSpan("unknown", p))
	}
	if rt.Reason != "" {
		l.add(" "+g.Dot+" "+rt.Reason, p.Dim)
	}
	return l
}

// sessionComposerChrome is claudeChrome in design/mate-tui.js: a rounded box
// (the Console's own corner glyphs) around one input line, plus a hint row
// naming the harness's own shortcut and mode - not Console chrome, so it
// draws with the harness's own composer shape rather than the Console's key
// line.
//
// composer is cut to the box's inner width (never wrapped: this is a
// one-line input, not a transcript entry) and the space it leaves is
// padded explicitly, matching the empty box's own hand-padding, because the
// right border glyph after it means line.render's own end-of-line padding
// cannot be relied on to keep the border aligned.
func sessionComposerChrome(g glyphSet, p palette, w int, composer string) []*line {
	inner := w - 2
	edge := func(l, r string) *line {
		return newLine().add(l+strings.Repeat(g.HRule, inner)+r, p.Faint)
	}
	textWidth := max0(inner - 3)
	text := cutCells(composer, textWidth)
	pad := max0(textWidth - cells(text))
	box := newLine().add(g.VRule, p.Faint).add(" > ", p.Fg).
		add(text, p.Fg).add(strings.Repeat(" ", pad), p.Fg).
		add(g.VRule, p.Faint)
	hint := leftRight(
		newLine().add("? for shortcuts", p.Dim),
		newLine().add(g.Cycle+" accept edits on", p.Dim),
		w, g,
	)
	return []*line{edge(g.CornerTL, g.CornerTR), box, edge(g.CornerBL, g.CornerBR), hint}
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// sessionTranscriptLines draws the transcript body: the harness-parsed
// Entries when the profile understood the output, or Raw as plain bounded
// text when it did not (SessionTranscriptUnknown) - the "unparseable
// provider output renders as plain bounded text with status unknown" rule
// from ADR 0025, which must actually degrade gracefully rather than merely
// be representable.
func sessionTranscriptLines(t SessionTranscript, g glyphSet, p palette, w int) []*line {
	if t.Status == SessionTranscriptParsed {
		return sessionParsedTranscriptLines(t.Entries, g, p, w)
	}
	return sessionRawTranscriptLines(t.Raw, g, p, w)
}

func sessionParsedTranscriptLines(entries []SessionTranscriptEntry, g glyphSet, p palette, w int) []*line {
	var out []*line
	for _, e := range entries {
		switch e.Kind {
		case SessionTranscriptEntryGap:
			out = append(out, newLine())
		case SessionTranscriptEntryResult:
			for i, ln := range wrapAfterSlash(e.Text, w-4) {
				if i == 0 {
					out = append(out, newLine().add("  "+g.Elbow+"  ", p.Faint).add(ln, p.Dim))
				} else {
					out = append(out, newLine().add("      "+ln, p.Dim))
				}
			}
		case SessionTranscriptEntryStatus:
			l := newLine().add(g.Spark+" "+e.Text, p.Fg)
			if e.Hint != "" {
				l.add(e.Hint, p.Dim)
			}
			out = append(out, l)
		default: // Turn, Plain
			style := p.Fg
			if e.Emphasis {
				style = p.Amber
			}
			for i, ln := range wrapAfterSlash(e.Text, w-2) {
				if i == 0 {
					out = append(out, newLine().add(g.Bullet+" ", p.Fg).add(ln, style))
				} else {
					out = append(out, newLine().add("  "+ln, style))
				}
			}
		}
	}
	return out
}

// sessionRawTranscriptLines is the fallback for SessionTranscriptUnknown:
// one marker line naming the state (distinct from Parsed even in
// plainPalette, per the Known/Absent/Unknown convention this package
// already uses elsewhere), then Raw wrapped verbatim rather than parsed
// into any structure this profile could not verify.
func sessionRawTranscriptLines(raw string, g glyphSet, p palette, w int) []*line {
	out := []*line{newLine().addSpan(unknownMarkSpan("unknown", p)).add(" "+g.Dot+" showing raw output", p.Dim)}
	if raw == "" {
		return out
	}
	for _, rawLine := range strings.Split(raw, "\n") {
		for _, ln := range wrapAfterSlash(sanitizeText(rawLine), w) {
			out = append(out, newLine().add(ln, p.Fg))
		}
	}
	return out
}
