package console

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// Chrome (design I, "3 Chrome"): one titled rule per pane, a status line,
// and at 30+ rows a key line. Never a title bar, "mate console", the
// workspace path, a clock, a window frame or an embedded terminal.

// ruleLine is a pane rule: "─ title ──── meta ─". The title is tinted when
// the pane has focus; meta is !N, a count or "esc".
func (m Model) ruleLine(title gline, meta gline, w int) gline {
	head := gl().add(m.g.HRule+" ", tFaint).join(title)
	tail := gl()
	if len(meta.segs) > 0 {
		tail = gl().pad(1).join(meta).add(" "+m.g.HRule, tFaint)
	}
	fill := w - head.width() - tail.width() - 1
	if len(tail.segs) == 0 {
		fill++
	}
	if fill < 1 {
		return head.cut(w-tail.width(), m.g).join(tail).padTo(w, m.g)
	}
	if len(tail.segs) == 0 {
		return head.add(" "+strings.Repeat(m.g.HRule, fill-1), tFaint)
	}
	return head.add(" "+strings.Repeat(m.g.HRule, fill), tFaint).join(tail)
}

// focusTitle tints a title that has focus: acc and bold, whatever tokens
// it was built in. Emoji keep their own colours.
func focusTitle(l gline, focused bool) gline {
	out := gl()
	for _, s := range l.segs {
		t := tDim
		if focused {
			t = tAcc
		}
		out = out.add(s.text, t)
	}
	return out
}

// listRule is the list's rule: the breadcrumb, and the level's !N.
func (m Model) listRule(w int) gline {
	focused := m.focus == paneList
	ws := m.workspaceName()
	var title gline
	var n int
	var failed bool
	if m.cur().kind == frameProject {
		proj := m.currentProject()
		title = gl().add(ws+" "+m.g.Crumb, tDim).join(focusTitle(gl().add(" "+proj.Name, tFg), focused))
		n, failed = projectAttention(proj)
	} else {
		title = focusTitle(gl().add(ws, tFg), focused)
		for _, p := range m.tree.Projects {
			pn, pf := projectAttention(p)
			n += pn
			failed = failed || pf
		}
	}
	meta := gl()
	switch {
	case m.lastLoadErr != nil:
		meta = gl().add("stale", tAmber)
	case n > 0:
		meta = gl().add(fmt.Sprintf("%s%d", m.g.Bang, n), bangTok(failed))
	case len(m.tree.Projects) == 0:
		meta = gl().add("0", tDim)
	}
	return m.ruleLine(title, meta, w)
}

// workspaceName is the workspace's own name, or its abbreviated id when
// the name could not be read.
func (m Model) workspaceName() string {
	if m.tree.Workspace.IsKnown() && m.tree.Workspace.Value.Name != "" {
		return m.tree.Workspace.Value.Name
	}
	if m.tree.WorkspaceID == "" {
		return "mate"
	}
	return shortID(m.tree.WorkspaceID, m.g)
}

// paneRule is the rule over a pane.
func (m Model) paneRule(k slotKind, w int) gline {
	switch k {
	case slotList:
		return m.listRule(w)
	case slotDetail:
		title, meta := m.detailTitle()
		metaLine := gl()
		if meta != "" {
			metaLine = gl().add(meta, tDim)
		}
		return m.ruleLine(focusTitle(title, m.focus == paneDetail), metaLine, w)
	case slotBox:
		items := m.boxItems()
		return m.ruleLine(focusTitle(gl().add("box", tFg), m.focus == paneBox), gl().add(m.boxRuleMeta(items), tAmber), w)
	case slotSheet:
		title, meta := m.sheetTitle()
		return m.ruleLine(title, gl().add(meta, tDim), w)
	}
	return m.ruleLine(gl(), gl(), w)
}

// ---------- the status line ----------

// statusLine says what the next pane shows (design I, "6"), or the last
// thing that happened when there is something to say. On a short frame it
// ends in "? keys".
func (m Model) statusLine(p framePlan) gline {
	right := gl()
	if !p.tall {
		right = gl().add("? keys", tDim)
	}
	var left gline
	switch msg := m.footerMessage(); {
	case msg.text != "":
		left = m.messageLine(msg)
	case m.staged.err != "":
		// "r retry" keeps its cells: the reason is what gets cut.
		left = gl().add(m.g.Arrow+" next pane "+m.g.Dot+" ", tDim).add("failed: "+m.staged.err, tRed)
		right = gl().add("r", tFg).add(" retry", tDim)
	case m.staged.name != "":
		left = gl().add(m.g.Arrow+" next pane  ", tDim).join(kindName(m.stagedMark(), m.staged.name))
	case m.stage == nil:
		left = gl().add(m.g.Arrow+" next pane "+m.g.Dot+" no host", tDim)
	default:
		left = gl().add(m.g.Arrow+" next pane "+m.g.Dot+" nothing shown", tDim)
	}
	if len(right.segs) == 0 {
		return left.padTo(p.w, m.g)
	}
	return spread(left, right, p.w, m.g)
}

// stagedMark is the kind mark of what the next pane shows.
func (m Model) stagedMark() string {
	if m.staged.kind == StageCrew {
		return m.g.Crew
	}
	return m.g.Mate
}

// messageLine draws a message in its tone. The tone never travels alone:
// errors and attention start "! ", unknown "? ", so the line reads with
// colour stripped.
func (m Model) messageLine(msg footerMsg) gline {
	switch msg.tone {
	case toneError:
		return gl().add(m.g.Bang+" "+msg.text, tRed)
	case toneWarn:
		return gl().add(m.g.Bang+" "+msg.text, tAmber)
	case toneUnknown:
		return gl().add("? "+msg.text, tAmber)
	case toneOK:
		return gl().add(msg.text, tGreen)
	}
	return gl().add(msg.text, tDim)
}

// footerMessage is the message to show: an explicit one first, then the
// auto daemon's notice, then load warnings, then a stale refresh.
func (m Model) footerMessage() footerMsg {
	if m.msg.text != "" {
		return m.msg
	}
	// Recovery outranks the opening notice and the runtime notice: while it
	// runs, Herdr being down is the thing it is fixing, and afterwards its
	// summary is the news.
	if r := m.tree.Recovery; r.Line != "" {
		switch {
		case r.Failed:
			return warnMsg(r.Line)
		case r.Active:
			return infoMsg(r.Line)
		}
		return okMsg(r.Line)
	}
	if m.notice != "" {
		return warnMsg(m.notice)
	}
	// The runtime being down outranks the daemon's and the loader's own
	// notices: while Herdr cannot be reached, both are describing a picture
	// nobody can refresh, and the reader's first need is to know that.
	if m.tree.Runtime.Notice != "" {
		return warnMsg(m.tree.Runtime.Notice)
	}
	if d := daemonFooterMsg(m.tree.Projects); d.text != "" {
		return d
	}
	if w := warningsFooterMsg(m.tree.Warnings); w.text != "" {
		return w
	}
	if m.lastLoadErr != nil && m.hasLoaded {
		return warnMsg("stale since " + m.tree.AsOf.Format("15:04") + " " + m.g.Dot + " r retries")
	}
	return footerMsg{}
}

// ---------- the key line ----------

// keyHint is one key and what it does here.
type keyHint struct{ key, desc string }

// keyLine is the frame's last row at 30+ rows: the keys of the pane or
// sheet that has the keyboard, as many as fit.
func (m Model) keyLine(p framePlan) gline {
	l := gl()
	hints := slices.DeleteFunc(m.keyHints(), func(h keyHint) bool { return h.key == "" })
	for i, h := range hints {
		next := gl()
		if i > 0 {
			next = next.pad(2)
		} else {
			next = next.pad(1)
		}
		next = next.add(h.key, tFg)
		if h.desc != "" {
			next = next.pad(1).add(h.desc, tDim)
		}
		if l.width()+next.width() > p.w {
			break
		}
		l = l.join(next)
	}
	return l.padTo(p.w, m.g)
}

func (m Model) keyHints() []keyHint {
	switch m.sheetOpen() {
	case sheetActions:
		return []keyHint{{m.g.UpDown, "move"}, {"enter", "run"}, {"or its key", ""}, {"esc", ""}}
	case sheetConfirm:
		verb := strings.Fields(confirmVerb(m.confirm.choice, m.confirm.label))
		return []keyHint{{"enter", "cancel"}, {m.confirm.key, verb[0]}, {"esc", "back"}}
	case sheetNewProject:
		return []keyHint{{"enter", "create"}, {"tab", "field"}, {"esc", "cancel"}}
	case sheetHarness:
		return []keyHint{{m.g.UpDown, "move"}, {"enter", "use"}, {"esc", "cancel"}}
	case sheetDiff:
		return []keyHint{{m.g.UpDown, "scroll"}, {"esc", "close"}}
	case sheetKeys:
		return []keyHint{{"any key", "closes"}}
	}
	if m.emptyWorkspace() {
		return []keyHint{{"n", "new project"}, {"r", "refresh"}, {"q", "quit"}}
	}
	switch m.focus {
	case paneDetail:
		return []keyHint{{m.g.UpDown, "field"}, {"y", "copy"}, {"esc", "list"}, m.toolHint("t"), {"enter", "show"}}
	case paneBox:
		return []keyHint{{m.g.UpDown, "move"}, {"enter", "crew"}, {"a", "assign"}, m.toolHint("t"), {"esc", ""}}
	}
	if m.cur().kind == frameWorkspace {
		return []keyHint{{m.g.UpDown, "move"}, {"enter", "open"}, m.toolHint("t"), {"a", "act"}, {"n", "new"}, {"?", ""}}
	}
	return []keyHint{{m.g.UpDown, "move"}, {"enter", "show"}, m.toolHint("t"), {"a", "act"}, {"tab", "pane"}, {"?", ""}}
}

// crewByID resolves a Crew row against the Project frame it belongs to.
func (m Model) crewByID(id string) (query.CrewNode, bool) {
	for _, c := range m.currentProject().Crews {
		if c.CrewID == id {
			return c, true
		}
	}
	return query.CrewNode{}, false
}

// daemonFooterMsg is the auto daemon's standing refusal line, taken verbatim
// from the notice the daemon recorded: internal/send already names which
// composer state it observed and quotes the screen it read that from, and a
// reader deciding whether to take the Mate's composer back needs that
// observation rather than a reworded summary of it.
//
// The newest notice wins when more than one Project has one, and its Project
// is named, because the reader is looking at one Project's rows and the line
// may well be about another's.
func daemonFooterMsg(projects []query.ProjectNode) footerMsg {
	var newest query.ProjectNode
	for _, p := range projects {
		if p.Daemon.Notice == "" {
			continue
		}
		if newest.Daemon.Notice == "" || p.Daemon.NoticeAt.After(newest.Daemon.NoticeAt) {
			newest = p
		}
	}
	if newest.Daemon.Notice == "" {
		return footerMsg{}
	}
	return warnMsg(newest.Daemon.Notice)
}

// warningsFooterMsg is the standing "N field(s) unknown" line, built only
// from the read layer's own warning list (query.FieldWarning: field, row,
// reason) - never invented here. A single warning names itself in full;
// more than one names the count and the first, since the message line is
// one line and cannot enumerate them all.
func warningsFooterMsg(warnings []query.FieldWarning) footerMsg {
	if len(warnings) == 0 {
		return footerMsg{}
	}
	first := warnings[0]
	if len(warnings) == 1 {
		return unknownMsg(fmt.Sprintf("1 field unknown: %s of %s (%s)", first.Field, first.Row.Label, first.Reason))
	}
	return unknownMsg(fmt.Sprintf("%d fields unknown; first: %s of %s (%s)", len(warnings), first.Field, first.Row.Label, first.Reason))
}
