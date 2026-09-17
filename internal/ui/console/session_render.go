package console

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/query"
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
// 4's composer). It is not part of SessionSnapshot: unlike Runtime,
// Transcript and Inbox it is never observed from the runtime or from
// committed state, and folding it into the snapshot would make a poll
// result depend on what the reader happened to be typing. An empty
// composer draws byte-identical to step 3's frozen goldens.
func RenderSessionFrame(snapshot SessionSnapshot, composer string, w, h int, g glyphSet, p palette) string {
	return renderSessionFrame(snapshot, nil, false, composer, w, h, g, p)
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
// unaffected; the composer box's 4 lines shrink to streamDetachHintLine's
// single line, the one piece of chrome stream mode still draws (a Crew or
// narrow-Mate frame otherwise has no on-screen way to leave once Esc and
// Ctrl+C both go to the agent).
func RenderStreamSessionFrame(snapshot SessionSnapshot, buffer *TerminalBuffer, frozen bool, w, h int, g glyphSet, p palette) string {
	if buffer == nil {
		return RenderSessionFrame(snapshot, "", w, h, g, p)
	}
	frame := buffer.Snapshot()
	return renderSessionFrame(snapshot, &frame, frozen, "", w, h, g, p)
}

// renderSessionFrame draws one session frame. frozen marks a terminal whose
// stream has died: its buffer is the agent's last live screen, kept on
// screen unchanged while the snapshot fallback's entry read is in flight
// (session_mode.go's beginStreamFallback deliberately does not resize it),
// so it is cropped from the HEAD rather than the tail when the notice
// banner leaves the frame fewer rows than the buffer holds. The frozen flag
// is ignored for a nil terminal (snapshot mode never freezes a buffer).
func renderSessionFrame(snapshot SessionSnapshot, terminal *TerminalSnapshot, frozen bool, composer string, w, h int, g glyphSet, p palette) string {
	s := newScreen(w, h)
	s.push(sessionHeaderLine(snapshot.Target, snapshot.RecordedStatus, g, p))
	rest := h - 1

	rw := sessionRailWidth(snapshot.Target.Kind, w)
	switch {
	case snapshot.Target.Kind == SessionTargetMate && rw > 0:
		s.push(sessionSplitRule(w, rw, g, p))
		rest--
		rail := sessionRailLines(snapshot.Inbox, g, p, rw, rest)
		pane := sessionPaneLines(snapshot, terminal, frozen, composer, g, p, w-rw-1, rest)
		divider := span{text: g.VRule, style: p.Faint}
		for i := 0; i < rest; i++ {
			s.pushSplit(rail[i], rw, divider, pane[i], w-rw-1)
		}
	case snapshot.Target.Kind == SessionTargetMate:
		s.push(sessionFullRule(w, g, p))
		rest--
		digest := sessionDigestLines(snapshot.Inbox, g, p, w)
		for _, l := range digest {
			s.push(l)
		}
		rest -= len(digest)
		s.push(sessionFullRule(w, g, p))
		rest--
		for _, l := range sessionPaneLines(snapshot, terminal, frozen, composer, g, p, w, rest) {
			s.push(l)
		}
	default: // Crew: no rail, no digest, at any width.
		s.push(newLine())
		rest--
		for _, l := range sessionPaneLines(snapshot, terminal, frozen, composer, g, p, w, rest) {
			s.push(l)
		}
	}
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
	const composerChromeHeight = 4 // sessionComposerChrome is always exactly 4 lines
	return max0(h - sessionFrameOverhead(kind, w) - composerChromeHeight)
}

// streamDetachHintHeight is the one line of chrome stream mode keeps in
// place of the composer box it removes entirely (streamDetachHintLine,
// below). Named separately from composerChromeHeight in
// SessionTranscriptCapacity so the two heights can never be confused for
// one another even though both currently happen to differ from zero.
const streamDetachHintHeight = 1

// StreamTranscriptCapacity is SessionTranscriptCapacity's stream-mode
// counterpart: the same upper bound, but reserving only
// streamDetachHintHeight instead of the composer box's 4 lines, since
// RenderStreamSessionFrame draws no composer at all (ADR 0026 step 6, the
// captain's ruling) - the PTY gets back most, not all, of the height a
// Console-drawn composer used to take; the one line it does not get is
// streamDetachHintLine, the counter-review fix for a Crew or narrow-Mate
// Agent View otherwise having no on-screen way to leave once Esc and
// Ctrl+C both go to the agent. session_mode.go's streamTerminalSize uses
// this, not SessionTranscriptCapacity, to size the actual PTY the stream
// opens.
func StreamTranscriptCapacity(kind SessionTargetKind, w, h int) int {
	return max0(h - sessionFrameOverhead(kind, w) - streamDetachHintHeight)
}

// ---------- header ----------

// harnessDisplayLabel names the harness in the header the way the harness's
// own product does ("claude-code"), not the shorter internal
// domain.HarnessKind value ("claude") list.go and the inspector already
// render elsewhere - the header is naming a product to the reader, not a
// database column.
func harnessDisplayLabel(k domain.HarnessKind) string {
	switch k {
	case domain.HarnessClaude:
		return "claude-code"
	case domain.HarnessCodex:
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
// divider below it lines up with.
func sessionSplitRule(w, rw int, g glyphSet, p palette) *line {
	return newLine().
		add(strings.Repeat(g.HRule, rw), p.Faint).
		add(g.TeeDown, p.Faint).
		add(strings.Repeat(g.HRule, w-rw-1), p.Faint)
}

// sessionRailWidth mirrors railWidth in design/mate-tui.js: the rail exists
// only for a Mate, and only at the two breakpoints the Console's own
// inspector already uses (140, 100 - layout.go's inspectorWide/Narrow
// switch), with widths (42, 36) sized for this pane rather than reused from
// the inspector's own (60, 50).
func sessionRailWidth(kind SessionTargetKind, cols int) int {
	if kind != SessionTargetMate {
		return 0
	}
	switch {
	case cols >= 140:
		return 42
	case cols >= 100:
		return 36
	default:
		return 0
	}
}

// ---------- rail (Mate, >=100 cols) and digest (Mate, <100 cols) ----------

// sessionInboxKind classifies one inbox entry into the rail's three labels.
// Awaiting (domain.InteractionStatus.AwaitsReply, carried on the entry) is
// "awaiting reply"; a non-awaiting entry recorded blocked needs the same
// attention emphasis without itself awaiting a reply; every other -
// terminal - status is "recorded", the plain rest of the entry's history.
func sessionInboxKind(e SessionInboxEntry) (label string, amber bool) {
	switch {
	case e.Awaiting:
		return "awaiting reply", true
	case e.Status == domain.InteractionBlocked:
		return "attention", true
	default:
		return "recorded", false
	}
}

func sessionClock(e SessionInboxEntry) string { return e.SentAt.UTC().Format("15:04") }

// sessionRailLines returns exactly h *line values for the rail pane at
// width w: a 3-line header, packed entries, and a 2-line footer. Packing
// drops whole entries from the oldest end first (entries arrive newest-
// first, matching list.go's own delivery order) - a request is never
// clipped mid-sentence, matching design/mate-tui.js's inboxPane pack().
func sessionRailLines(entries []SessionInboxEntry, g glyphSet, p palette, w, h int) []*line {
	need := 0
	for _, e := range entries {
		if e.Awaiting {
			need++
		}
	}

	header := []*line{
		newLine().add(" CREW "+g.Crumb+" MATE", p.Bold),
		sessionRailCountLine(g, p, need, len(entries)),
		sessionFullRule(w, g, p),
	}
	footer := []*line{
		sessionFullRule(w, g, p),
		newLine().add(" Ctrl+b then q", p.Fg).add("  detach", p.Dim),
	}
	avail := h - len(header) - len(footer)
	if avail < 0 {
		avail = 0
	}

	blocks := make([][]*line, len(entries))
	for i, e := range entries {
		blocks[i] = sessionRailEntryBlock(g, p, e, w-2)
	}

	shown, used := 0, 0
	for _, b := range blocks {
		cost := len(b)
		if shown > 0 {
			cost++
		}
		if used+cost > avail {
			break
		}
		used += cost
		shown++
	}
	hidden := len(blocks) - shown
	if hidden > 0 {
		// Reserve one line for the "N more" note, re-packing so the note
		// itself never pushes a partially-shown entry past the budget.
		shown, used = 0, 0
		for _, b := range blocks {
			cost := len(b)
			if shown > 0 {
				cost++
			}
			if used+cost > avail-1 {
				break
			}
			used += cost
			shown++
		}
		hidden = len(blocks) - shown
	}

	var out []*line
	for i := 0; i < shown; i++ {
		if i > 0 {
			out = append(out, newLine())
		}
		out = append(out, blocks[i]...)
	}
	if hidden > 0 {
		word := "requests"
		if hidden == 1 {
			word = "request"
		}
		out = append(out, newLine().add(fmt.Sprintf(" %s %d older %s", g.Down, hidden, word), p.Dim))
	}
	for len(out) < avail {
		out = append(out, newLine())
	}
	out = out[:avail]

	return append(append(header, out...), footer...)
}

func sessionRailCountLine(g glyphSet, p palette, need, total int) *line {
	l := newLine()
	if need > 0 {
		l.add(fmt.Sprintf(" %d awaiting reply", need), p.Amber)
	} else {
		l.add(" none awaiting reply", p.Dim)
	}
	l.add(fmt.Sprintf(" %s %d recorded", g.Dot, total), p.Dim)
	return l
}

// sessionRailEntryBlock is one entry's lines: the timestamp/label row, the
// attempt/task row, the question (wrapped with wrapAfterSlash, the same
// wrapper the inspector uses for paths and branches, never cut with an
// ellipsis - a request is read in full or not at all), and an optional note
// row.
func sessionRailEntryBlock(g glyphSet, p palette, e SessionInboxEntry, innerWidth int) []*line {
	label, amber := sessionInboxKind(e)
	labelStyle := p.Dim
	if amber {
		labelStyle = p.Amber
	}
	head := leftRight(
		newLine().add(" "+sessionClock(e), p.Dim),
		newLine().add(label+" ", labelStyle),
		innerWidth+2, g,
	)
	var out []*line
	out = append(out, head)
	for _, ln := range wrapAfterSlash(e.Attempt+" "+g.Dot+" "+e.Task, innerWidth) {
		out = append(out, newLine().add("  "+ln, p.Fg))
	}
	textStyle := p.Fg
	if !amber {
		textStyle = p.Dim
	}
	for _, ln := range wrapAfterSlash(e.Question, innerWidth) {
		out = append(out, newLine().add("  "+ln, textStyle))
	}
	if e.Note != "" {
		for _, ln := range wrapAfterSlash(g.Dot+" "+e.Note, innerWidth) {
			out = append(out, newLine().add("  "+ln, p.Dim))
		}
	}
	return out
}

// maxDigestEntries is how many awaiting replies the narrow-Mate digest lists
// before collapsing the rest into a "+N more" line. sessionDigestHeight
// derives its own height from the same constant, so the reservation
// session_mode.go makes for the digest and the digest the renderer actually
// draws cannot drift apart.
const maxDigestEntries = 3

// sessionDigestHeight is how many rows sessionDigestLines will draw for a
// given Inbox: always its header line, then up to maxDigestEntries awaiting
// replies, then a "+N more awaiting reply" line when more are waiting. A
// caller that must reserve the digest's rows before rendering (stream mode's
// streamTerminalSize, so the PTY is never handed rows the frame then crops)
// needs the count without building the lines. A test pins
// len(sessionDigestLines(...)) == sessionDigestHeight(...).
func sessionDigestHeight(entries []SessionInboxEntry) int {
	need := 0
	for _, e := range entries {
		if e.Awaiting {
			need++
		}
	}
	shown := need
	if shown > maxDigestEntries {
		shown = maxDigestEntries
	}
	height := 1 + shown
	if need > shown {
		height++
	}
	return height
}

// sessionDigestLines is inboxDigest in design/mate-tui.js: below the rail
// breakpoint there is no room for a second pane, so the rail collapses into
// a fixed 1 (header) + up to 3 (entries) + at most 1 ("N more") lines
// stacked above the transcript, rather than beside it. Only entries still
// awaiting a reply are shown - a recorded or attention row is state a
// reader can already find on the Mate's own inbox surface, not something
// worth the three lines a narrow terminal can spare here. Entries are
// dropped oldest-first, same as the rail, so the digest and the rail never
// disagree about which requests survive a resize.
func sessionDigestLines(entries []SessionInboxEntry, g glyphSet, p palette, w int) []*line {
	var need []SessionInboxEntry
	for _, e := range entries {
		if e.Awaiting {
			need = append(need, e)
		}
	}
	shown := need
	if len(shown) > maxDigestEntries {
		shown = shown[:maxDigestEntries]
	}

	needStyle := p.Dim
	if len(need) > 0 {
		needStyle = p.Amber
	}
	out := []*line{leftRight(
		newLine().add(" CREW "+g.Crumb+" MATE", p.Bold),
		newLine().add(fmt.Sprintf("%d awaiting reply %s %d recorded ", len(need), g.Dot, len(entries)), needStyle),
		w, g,
	)}
	for _, e := range shown {
		clock := sessionClock(e)
		l := newLine().add(" "+clock+"  ", p.Dim)
		l.add(truncateEnd(e.Attempt+" "+g.Dot+" "+e.Question, w-cells(clock)-4, g), p.Fg)
		out = append(out, l)
	}
	if len(need) > len(shown) {
		out = append(out, newLine().add(fmt.Sprintf(" %d more awaiting reply", len(need)-len(shown)), p.Dim))
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
// (2026-09-12) closed. What stream mode keeps instead is one line,
// streamDetachHintLine: a Crew Agent View has no rail at any width, and a
// Mate one only gets "Ctrl+b then q  detach" from its rail footer at >=100
// columns (sessionRailLines) - without this line, once Esc and Ctrl+C both
// go to the agent, those frames would show no way to leave the Console at
// all (a counter-review finding). A non-Known Runtime gets one banner line
// above the transcript - "runtime_missing" (Absent) or "unknown" (Unknown),
// never upgraded into a lifecycle word (ADR 0025) - without clearing or
// replacing whatever the last successful poll recorded.
func sessionPaneLines(snapshot SessionSnapshot, terminal *TerminalSnapshot, frozen bool, composer string, g glyphSet, p palette, w, h int) []*line {
	var chrome []*line
	if terminal == nil {
		chrome = sessionComposerChrome(g, p, w, composer)
	} else {
		chrome = []*line{streamDetachHintLine(g, p)}
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

// streamDetachHintLine is stream mode's whole pane chrome: one line naming
// its sole detach, in the exact wording sessionRailLines' own footer
// already uses so the two never say the same thing two different ways.
// Unlike that footer it draws unconditionally, at every SessionTargetKind
// and every width - a Crew Agent View has no rail to carry it, and a Mate
// one only gets the rail at >=100 columns - because stream mode has
// forwarded both Esc and Ctrl+C to the agent, leaving Ctrl+b q as the one
// way out of the Console a reader has not already been told about
// elsewhere on screen.
func streamDetachHintLine(g glyphSet, p palette) *line {
	return newLine().add(" Ctrl+b then q", p.Fg).add("  detach", p.Dim)
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
