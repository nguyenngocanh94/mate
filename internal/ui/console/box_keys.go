package console

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The three box keys (mvp.md section 5, task 15) and the peek overlay.
//
//	Enter  hand the selected entry to the Mate - one verified line into its
//	       composer, `signal: crews/<id>.status` or `signal: incident ...`.
//	r      reply to the crew directly, through the same send path
//	       `matev2 send` uses, recorded in sent.log with Source: user.
//	p      peek: the crew's own pane, 40 lines, in a scrollable overlay.
//	j/k    move the selection.
//
// Where they live depends on who owns the keyboard. Stream mode forwards
// every unprefixed key to the agent's PTY (ADR 0026, the captain's ruling),
// so there they sit behind the same Ctrl+b prefix as the detach; in
// snapshot mode and on the project frame's box panel the Console owns the
// keyboard and the bare keys work. sessionRailKeyLines and keyHints say
// which, so a reader is never told about a key that would land in the
// harness instead.
//
// None of them acts on anything itself: each builds an actionChoice and
// goes through runAction, so the whole existing machinery - the busy flag,
// the cancellable context, the one outcome line, the re-read afterwards -
// applies unchanged, and this package still reaches state only through
// ActionFunc.

// boxOutcomeLine draws the last box action's result inside the rail, which
// is where a session frame has to put it: unlike the project frame, the
// session frame is not built from frame.go's six-line chrome and so has no
// message line at all. The tone prefixes ("!", "?") are frame.go's own, so
// the two surfaces read the same way with colour stripped.
func boxOutcomeLine(msg footerMsg, g glyphSet, p palette, w int) *line {
	prefix, style := "", p.Fg
	switch msg.tone {
	case toneError:
		prefix, style = "! ", p.Red
	case toneWarn:
		prefix, style = "! ", p.Amber
	case toneUnknown:
		prefix, style = "? ", p.Amber
	case toneOK:
		style = p.Green
	}
	return newLine().add(" "+prefix+msg.text, style).cut(w, g)
}

// sessionBoxView is the box the session rail is showing, and whether there
// is one to act on at all.
func (m Model) sessionBoxView() (query.Field[query.BoxView], bool) {
	if m.sess.target.Kind != SessionTargetMate {
		return query.Field[query.BoxView]{}, false
	}
	v := m.sess.snapshot.Box
	return v, v.IsKnown() && len(v.Value.Entries) > 0
}

// sessionRailState assembles what the renderer needs from the flow. The
// selection is resolved here rather than stored resolved, so "follow the
// newest" (-1) keeps following as a crew appends.
func (m Model) sessionRailState() boxRail {
	v, _ := m.sessionBoxView()
	sel := m.sess.boxSel
	if sel < 0 {
		sel = boxDefaultSelection(v)
	} else if v.IsKnown() {
		sel = clampInt(sel, 0, len(v.Value.Entries)-1)
	}
	return boxRail{
		sel:       sel,
		outcome:   m.msg,
		reply:     m.boxReply,
		replyCrew: m.boxReplyCrew,
		replyText: m.boxReplyText,
		stream:    m.sess.stream != nil,
	}
}

// onSessionBoxKey handles one key aimed at the session rail. The third
// return says whether the key was consumed; false means the caller's own
// handling (the composer, or the PTY) still applies.
func (m Model) onSessionBoxKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.boxReply {
		model, cmd := m.onBoxReplyKey(msg)
		return model, cmd, true
	}
	v, ok := m.sessionBoxView()
	if !ok {
		return m, nil, false
	}
	rail := m.sessionRailState()
	switch msg.String() {
	case "j", "down":
		m.sess.boxSel = clampInt(rail.sel+1, 0, len(v.Value.Entries)-1)
		m.msg = footerMsg{}
		return m, nil, true
	case "k", "up":
		m.sess.boxSel = clampInt(rail.sel-1, 0, len(v.Value.Entries)-1)
		m.msg = footerMsg{}
		return m, nil, true
	case "enter":
		model, cmd := m.beginBoxForward(m.sess.target.ProjectID, v, rail.sel)
		return model, cmd, true
	case "r":
		return m.beginBoxReply(m.sess.target.ProjectID, v, rail.sel), nil, true
	case "p":
		model, cmd := m.beginBoxPeek(m.sess.target.ProjectID, v, rail.sel)
		return model, cmd, true
	}
	return m, nil, false
}

// onBoxReplyKey is the one-line reply input. It is the same shape as the
// new-project name input (actions.go's onActionInputKey): Enter submits,
// Esc cancels, backspace deletes one grapheme cluster, and printable runes
// accumulate. A reply is one line by protocol (mvp.md section 4), so there
// is nothing here that could produce a second one.
func (m Model) onBoxReplyKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.boxReply, m.boxReplyText, m.boxReplyCrew, m.boxReplyProject = false, "", "", ""
		return m, nil
	case "backspace":
		m.boxReplyText = trimLastCluster(m.boxReplyText)
		return m, nil
	case "enter":
		crew, project, text := m.boxReplyCrew, m.boxReplyProject, strings.TrimSpace(m.boxReplyText)
		m.boxReply, m.boxReplyText, m.boxReplyCrew, m.boxReplyProject = false, "", "", ""
		if text == "" {
			m.msg = errMsg("Reply refused: a reply is one non-empty line " + m.g.Dot + " nothing was sent")
			return m, nil
		}
		return m.runAction(boxReplyChoice(project, crew, text))
	}
	if len(msg.Runes) > 0 && len([]rune(m.boxReplyText)) < 500 {
		m.boxReplyText += string(msg.Runes)
	}
	return m, nil
}

// beginBoxForward is Enter: hand the selected entry to the Mate. A message
// entry is not forwardable - the Mate either sent it or was sent it, so
// handing it back says nothing - and the refusal says so rather than
// silently doing nothing, which is indistinguishable from a lost keystroke.
func (m Model) beginBoxForward(project string, v query.Field[query.BoxView], sel int) (Model, tea.Cmd) {
	e, ok := boxSelectedEntry(v, sel)
	if !ok {
		return m, nil
	}
	if !e.Forwardable() {
		m.msg = errMsg("Send refused: a " + string(e.Kind) + " entry is not forwardable; only a crew status or an incident is " +
			m.g.Dot + " nothing was sent")
		return m, nil
	}
	if m.actionBusy {
		return m, nil
	}
	return m.runAction(boxForwardChoice(project, e))
}

// beginBoxReply is `r`: open the one-line input. It refuses on an entry
// with no crew (a message to the Mate's own pane) rather than opening an
// input with nowhere to send.
func (m Model) beginBoxReply(project string, v query.Field[query.BoxView], sel int) Model {
	e, ok := boxSelectedEntry(v, sel)
	if !ok {
		return m
	}
	if e.Crew == "" {
		m.msg = errMsg("Reply refused: this entry names no crew " + m.g.Dot + " nothing was sent")
		return m
	}
	m.boxReply, m.boxReplyCrew, m.boxReplyProject, m.boxReplyText = true, e.Crew, project, ""
	m.msg = footerMsg{}
	return m
}

// beginBoxPeek is `p`: read the crew's own pane into the overlay.
func (m Model) beginBoxPeek(project string, v query.Field[query.BoxView], sel int) (Model, tea.Cmd) {
	e, ok := boxSelectedEntry(v, sel)
	if !ok {
		return m, nil
	}
	if e.Crew == "" {
		m.msg = errMsg("Peek refused: this entry names no crew " + m.g.Dot + " nothing was read")
		return m, nil
	}
	if m.actionBusy {
		return m, nil
	}
	return m.runAction(boxPeekChoice(project, e.Crew))
}

// The three choices. They are built here rather than in actionChoicesForSelected
// because none of them acts on the *selected row* of a frame: they act on a
// box entry, which is a different selection entirely.

func boxForwardChoice(project string, e query.BoxEntry) actionChoice {
	return actionChoice{
		action: ActionForward, enabled: true,
		desc: "Hand " + e.Crew + "'s " + e.Verb + " to the Mate",
		req: ActionRequest{
			Action: ActionForward, Target: project, TargetKind: "project",
			Crew: e.Crew, Input: e.Signal,
		},
	}
}

func boxReplyChoice(project, crew, text string) actionChoice {
	return actionChoice{
		action: ActionReply, enabled: true,
		desc: "Reply to crew " + crew,
		req: ActionRequest{
			Action: ActionReply, Target: project, TargetKind: "project",
			Crew: crew, Input: text,
		},
	}
}

func boxPeekChoice(project, crew string) actionChoice {
	return actionChoice{
		action: ActionPeek, enabled: true,
		desc: "Read crew " + crew + "'s pane",
		req: ActionRequest{
			Action: ActionPeek, Target: project, TargetKind: "project", Crew: crew,
		},
	}
}

// ---------- the peek overlay ----------

// peekFlow is `p`'s result: the crew's own pane, as it was read, kept on
// screen until Esc. It is never merged into the box or the transcript - it
// is a live screen scrape, and mvp.md decision 8 keeps screen scraping out
// of anything that looks like recorded state.
type peekFlow struct {
	open bool
	crew string
	text string
	top  int
}

// peekLines renders the overlay over a whole region: a title, a rule, and
// the pane's own lines, scrolled. Nothing is wrapped - a terminal scrape is
// already laid out in columns, and re-wrapping it would misalign whatever
// the harness drew.
func (m Model) peekLines(w, h int) []*line {
	out := []*line{
		leftRight(
			newLine().pad(1).add("PEEK  ", m.p.Bold).add("crew "+m.peek.crew, m.p.Fg),
			newLine().add("live pane read, not recorded state ", m.p.Dim),
			w, m.g,
		),
		newLine().add(strings.Repeat(m.g.HRule, max0(w)), m.p.Faint),
	}
	body := max0(h - len(out) - 2)
	raw := strings.Split(m.peek.text, "\n")
	if strings.TrimSpace(m.peek.text) == "" {
		raw = []string{"the pane returned nothing"}
	}
	start, end := window(len(raw), clampInt(m.peek.top, 0, max0(len(raw)-body)), body)
	for i := start; i < end; i++ {
		out = append(out, newLine().add(" "+sanitizeText(raw[i]), m.p.Fg))
	}
	for len(out) < h-2 {
		out = append(out, newLine())
	}
	out = append(out,
		newLine().add(strings.Repeat(m.g.HRule, max0(w)), m.p.Faint),
		newLine().add(" "+m.g.UpDown, m.p.Fg).add(" Scroll", m.p.Dim).
			add("  Esc", m.p.Fg).add(" Close", m.p.Dim).
			add("  "+m.g.Dot+" ", m.p.Faint).
			add(plural(len(raw), "line", "lines"), m.p.Dim),
	)
	return fitLines(out, h)
}

// onPeekKey is the overlay's own keyboard. It is modal: only scroll and
// close respond, so no key can move a selection behind it.
func (m Model) onPeekKey(key string) Model {
	switch key {
	case "esc", "q", "p":
		m.peek = peekFlow{}
	case "j", "down":
		m.peek.top++
	case "k", "up":
		if m.peek.top > 0 {
			m.peek.top--
		}
	}
	return m
}

// ---------- the project frame's box panel ----------
//
// The panel exists so attention is visible without entering the session
// view, which is the whole point of showing it here. Its keys are the same
// three, bare - the Console owns the keyboard on this frame - but they are
// live only while Tab has moved focus onto the panel: Enter already opens
// the Agent View on this frame and `r` already refreshes, and rebinding
// either of them under the list would be exactly the trap the `n` key's own
// note (seams.go's actionHints) refuses to lay.

// projectBox is the box of the project frame the reader is on, and whether
// there is one with entries in it.
func (m Model) projectBox() (query.Field[query.BoxView], bool) {
	if m.cur().kind != frameProject {
		return query.Field[query.BoxView]{}, false
	}
	v := m.currentProject().Box
	return v, v.IsKnown() && len(v.Value.Entries) > 0
}

// projectBoxSelection resolves the panel's selection the same way
// sessionRailState resolves the rail's.
func (m Model) projectBoxSelection() int {
	v, _ := m.projectBox()
	if m.boxSel < 0 {
		return boxDefaultSelection(v)
	}
	if v.IsKnown() {
		return clampInt(m.boxSel, 0, len(v.Value.Entries)-1)
	}
	return m.boxSel
}

// onProjectBoxKey is the panel's keyboard, live only while focus is on it.
func (m Model) onProjectBoxKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.boxReply {
		return m.onBoxReplyKey(msg)
	}
	project := m.currentProject().ProjectID
	v, ok := m.projectBox()
	sel := m.projectBoxSelection()
	switch msg.String() {
	case "esc", "tab":
		m.focus = paneList
		m.msg = footerMsg{}
		return m, nil
	}
	if !ok {
		return m, nil
	}
	switch msg.String() {
	case "j", "down":
		m.boxSel = clampInt(sel+1, 0, len(v.Value.Entries)-1)
		m.msg = footerMsg{}
	case "k", "up":
		m.boxSel = clampInt(sel-1, 0, len(v.Value.Entries)-1)
		m.msg = footerMsg{}
	case "enter":
		return m.beginBoxForward(project, v, sel)
	case "r":
		return m.beginBoxReply(project, v, sel), nil
	case "p":
		return m.beginBoxPeek(project, v, sel)
	}
	return m, nil
}
