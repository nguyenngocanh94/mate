package console

// View implements tea.Model: exactly h lines of exactly w cells, built from
// the plan (layout.go) and nothing else.
func (m Model) View() string {
	if m.quitting || m.w <= 0 || m.h <= 0 {
		// No size yet, or Bubble Tea is about to leave the alt screen:
		// drawing one more frame would flash it onto the shell.
		return ""
	}
	p := m.plan()
	s := newGrid(m.w, m.h)
	switch {
	case p.tooSmall:
		s.push(m.tooSmallLines(p)...)
		return s.String(m.p)
	case m.phase == phaseLoading:
		s.push(fit(m.loadingLines(p), m.h-p.footer)...)
	case m.phase == phaseFailed:
		s.push(fit(m.failedLines(p), m.h-p.footer)...)
	case m.emptyWorkspace() && m.sheetOpen() == sheetNone:
		s.push(fit(m.emptyWorkspaceLines(p), m.h-p.footer)...)
	default:
		for _, sl := range p.slots {
			s.push(m.paneRule(sl.kind, p.w))
			s.push(fit(m.slotBody(sl, p), sl.h-1)...)
		}
		s.push(fit(nil, m.h-p.footer-len(s.lines))...)
	}
	s.push(m.footerLines(p)...)
	return s.String(m.p)
}

// footerLines are the status line and, at 30+ rows, the key line. On a
// short frame `?` shows the key line in the status line's place until the
// next key.
func (m Model) footerLines(p framePlan) []gline {
	if !p.tall {
		return []gline{m.statusLine(p)}
	}
	return []gline{m.statusLine(p), m.keyLine(p)}
}

// slotBody draws a pane's body.
func (m Model) slotBody(sl slot, p framePlan) []gline {
	_, h := sl.body()
	switch sl.kind {
	case slotList:
		return m.listBody(p, h).lines
	case slotDetail:
		return m.detailWindow(p, h)
	case slotBox:
		return m.boxWindow(p, h)
	case slotSheet:
		return m.sheetLines(p)
	}
	return nil
}

// detailWindow is the detail body at h rows, scrolled so the field under
// the cursor is in view.
func (m Model) detailWindow(p framePlan, h int) []gline {
	lines := m.detailLines(p)
	top := m.detailTop(p, len(lines), h)
	return lines[top:minInt(len(lines), top+h)]
}

// detailTop is the first detail line drawn; the mouse reads it too.
func (m Model) detailTop(p framePlan, n, h int) int {
	if n <= h || m.focus != paneDetail {
		return 0
	}
	return clampInt(m.detailCursorLine(p)-h+1, 0, n-h)
}

// detailCursorLine is the line, within detail, of the field cursor.
func (m Model) detailCursorLine(p framePlan) int {
	fields := m.detailFields(p.w)
	sel := clampInt(m.detailSel, 0, len(fields)-1)
	n := 0
	for i := 0; i < sel; i++ {
		n += len(fields[i].lines)
	}
	return n
}

// boxWindow is the box body at h rows, scrolled so the selected item's
// title is in view.
func (m Model) boxWindow(p framePlan, h int) []gline {
	b := m.boxLines(p, m.boxMode(p))
	top := m.boxTop(b, h)
	return b.lines[top:minInt(len(b.lines), top+h)]
}

// boxTop is the first box line drawn; the mouse reads it too.
func (m Model) boxTop(b boxBody, h int) int {
	if len(b.lines) <= h {
		return 0
	}
	sel := m.boxSelection(m.boxItems())
	for i, hit := range b.hits {
		if hit.item == sel {
			return clampInt(i, 0, len(b.lines)-h)
		}
	}
	return 0
}
