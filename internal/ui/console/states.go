package console

import "fmt"

// Frames with nothing to list (design H): the empty workspace, a read that
// failed, the first read still in flight, and a split too small to draw.

// tooSmallLines is H4: what the split is, what it needs, and q. Under 20
// columns it is one line.
func (m Model) tooSmallLines(p framePlan) []gline {
	need := fmt.Sprintf("%d%s%d", minCols, timesGlyph(m.g), minRows)
	if p.w < tinyCols {
		return []gline{gl().add("mate "+need, tDim)}
	}
	return []gline{
		m.ruleLine(gl().add("mate", tAcc), gl(), p.w),
		gl(),
		gl().pad(2).add(fmt.Sprintf("too small: %d%s%d", p.w, timesGlyph(m.g), p.h), tFg),
		gl().pad(2).add("needs "+need+" or more", tDim),
		gl(),
		gl().pad(2).add("widen the split, or", tDim),
		gl().pad(2).add(padRight("q", 5), tDim).add("quit", tFg),
		gl(),
		gl().pad(2).add("next pane unchanged", tDim),
	}
}

func timesGlyph(g glyphSet) string {
	if g.Name == "ascii" {
		return "x"
	}
	return "×"
}

// loadingLines is the first read in flight. The snapshot arrives whole,
// so there is nothing to show row by row; no spinner.
func (m Model) loadingLines(p framePlan) []gline {
	return []gline{
		m.ruleLine(gl().add("mate", tAcc), gl().add("reading"+m.g.Ellipsis, tDim), p.w),
		gl(),
		gl().pad(2).add("reading the workspace"+m.g.Ellipsis, tDim),
	}
}

// failedLines is H2: what failed, what came back, when, and what to press.
// The next pane is left alone.
func (m Model) failedLines(p framePlan) []gline {
	out := []gline{
		m.ruleLine(gl().add("mate", tAcc), gl().add(m.g.Bang, tRed), p.w),
		gl(),
		gl().pad(2).add("can't read the workspace", tRed),
		gl(),
	}
	errText := "no error text"
	if m.loadErr != nil {
		errText = oneLine(m.loadErr.Error())
	}
	for i, part := range wrapWords(errText, p.w-8) {
		lead := gl().pad(8)
		if i == 0 {
			lead = gl().pad(2).add(padRight("got", 6), tDim)
		}
		out = append(out, lead.add(part, tRed))
	}
	out = append(out, gl())
	for _, part := range wrapWords("mate reads the workspace's .mate/ records; nothing below is live until that read works.", p.w-4) {
		out = append(out, gl().pad(2).add(part, tDim))
	}
	out = append(out, gl(),
		gl().add(m.g.Selected, tAcc).pad(1).add(padRight("r", 5), tFg).add("Retry now", tFg).padTo(p.w, m.g).selected(true),
		gl().pad(2).add(padRight("q", 5), tDim).add("Quit", tFg),
	)
	return out
}

// emptyWorkspaceLines is H1: the one screen that says what a Project is.
// Nothing pretends to be there.
func (m Model) emptyWorkspaceLines(p framePlan) []gline {
	out := []gline{
		m.listRule(p.w),
		gl(),
		gl().pad(2).add("No projects in "+m.workspaceName()+" yet.", tFg),
		gl(),
	}
	for _, part := range wrapWords("A project is its repos, one mate and the crews it spawns.", p.w-4) {
		out = append(out, gl().pad(2).add(part, tDim))
	}
	out = append(out, gl(),
		gl().add(m.g.Selected, tAcc).pad(1).add(padRight("n", 5), tFg).add("New project"+m.g.Ellipsis, tFg).padTo(p.w, m.g).selected(true),
		gl().pad(2).add(padRight("r", 5), tDim).add("Refresh", tFg),
		gl().pad(2).add(padRight("q", 5), tDim).add("Quit", tFg),
	)
	return out
}

// emptyWorkspace reports whether the workspace frame has no Projects.
func (m Model) emptyWorkspace() bool {
	return m.phase == phaseReady && m.cur().kind == frameWorkspace && len(m.tree.Projects) == 0
}
