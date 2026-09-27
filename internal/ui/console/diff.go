package console

import "strings"

// A crew's diff (mvp.md task 21) is a sheet under the list: the text one
// ActionDiff returned, read-only, scrolled with ↑↓ and closed with Esc.
// Nothing is re-read while it is open: a diff changes no recorded state.

// diffFlow is the open diff; its zero value is closed.
type diffFlow struct {
	title  string // optional title for another read-only text result
	wrap   bool   // prose results wrap; code diffs retain their columns
	open   bool
	crew   string
	branch string
	text   string
	top    int
}

// diffBranch is the recorded branch of a crew, for the sheet's title.
func (m Model) diffBranch(crewID string) string {
	for _, p := range m.tree.Projects {
		for _, c := range p.Crews {
			if c.CrewID == crewID && c.Worktree.IsKnown() {
				return c.Worktree.Value.Branch
			}
		}
	}
	return ""
}

func (m Model) openDiff(crew, branch, text string) Model {
	m.diff = diffFlow{open: true, crew: crew, branch: branch, text: text}
	m.msg = footerMsg{}
	m.actionAfterRead = nil
	return m
}

func (m Model) closeDiff() Model {
	m.diff = diffFlow{}
	return m
}

// diffTitle is "diff · <crew> · <branch>"; the rule cuts what does not fit.
func (m Model) diffTitle() string {
	if m.diff.title != "" {
		return m.diff.title
	}
	parts := []string{"diff"}
	if m.diff.crew != "" {
		parts = append(parts, shortID(m.diff.crew, m.g))
	}
	if m.diff.branch != "" {
		parts = append(parts, m.diff.branch)
	}
	return strings.Join(parts, " "+m.g.Dot+" ")
}

// onDiffKey scrolls or closes. q closes too, the way a pager does; Ctrl+C
// still quits (update.go).
func (m Model) onDiffKey(key string) Model {
	switch key {
	case "esc", "backspace", "enter", "q":
		return m.closeDiff()
	case "up", "k":
		return m.scrollDiff(-1)
	case "down", "j":
		return m.scrollDiff(1)
	case "pgup":
		return m.scrollDiff(-10)
	case "pgdown", " ":
		return m.scrollDiff(10)
	case "home", "g":
		m.diff.top = 0
	}
	return m
}

func (m Model) scrollDiff(delta int) Model {
	n := len(m.diffLines(m.w))
	m.diff.top = clampInt(m.diff.top+delta, 0, max0(n-1))
	return m
}

func (m Model) diffLines(width int) []string {
	lines := diffTextLines(m.diff.text)
	if !m.diff.wrap {
		return lines
	}
	var out []string
	for _, line := range lines {
		if line == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrapWords(line, maxInt(1, width-1))...)
	}
	return out
}

// RenderDiffOverlay draws a diff sheet at w x h on its own, uncoloured,
// for the live proofs in cmd/mate.
func RenderDiffOverlay(text, crew, branch string, w, h int) []string {
	m := Model{g: unicodeGlyphs, p: plainPalette(), w: w, h: h}
	m.diff = diffFlow{open: true, crew: crew, branch: branch, text: text}
	p := sizeClass(w, h)
	lines := append([]gline{m.ruleLine(gl().add(m.diffTitle(), tAcc), gl().add("esc", tDim), w)}, m.diffSheetLines(p)...)
	out := make([]string, 0, h)
	for _, l := range fit(lines, h) {
		out = append(out, strings.TrimRight(l.render(w, m.p), " "))
	}
	return out
}
