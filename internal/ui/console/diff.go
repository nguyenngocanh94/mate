package console

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The diff overlay (mvp.md task 21): what `mate diff <project> <crew>`
// prints, on the project frame, scrollable.
//
// It reaches this package the way every other action's outcome does - as
// the string an ActionFunc returned (cmd/mate/console_actions.go calls
// the same function the command does) - so the Console still runs no git of
// its own and still imports nothing new. What is different is only where
// the result is put: a diff is a screenful, and one line of it on the
// message line would be a truncated `diff --git` header.
//
// The overlay is modal while it is open. It owns the keyboard (update.go)
// and the main region (frame.go), because a key that moved the selection
// behind a full-screen patch would be invisible, and scrolling is the whole
// point of the surface.

// diffFlow is one open diff: the crew it names, the branch it compares, the
// text exactly as the command printed it, and the scroll offset.
//
// branch is carried here rather than looked up at render time because the
// title has to keep naming the branch the text was produced from even after
// a background refresh drops the crew from the snapshot - a crew that was
// merged and stopped while its diff was on screen is precisely the case.
type diffFlow struct {
	open   bool
	crew   string
	branch string
	text   string
	top    int
}

// diffBranch is the branch a Crew row records, from the snapshot the menu
// was built against. Empty when the crew is not in the snapshot or its
// worktree row could not be read - in which case diffChoice has already
// refused, so the title never has to invent one.
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

// openDiff puts one ActionDiff result on screen.
func (m Model) openDiff(crew, branch, text string) Model {
	m.diff = diffFlow{open: true, crew: crew, branch: branch, text: text}
	// The whole result is the overlay: a leftover "Action diff completed"
	// under a patch would be the Console congratulating itself for something
	// the reader is already looking at.
	m.msg = footerMsg{}
	m.actionAfterRead = nil
	return m
}

func (m Model) closeDiff() Model {
	m.diff = diffFlow{}
	return m
}

// diffTitle is the overlay's heading, in the wording of the command it
// shows: `diff · <crew> · <branch>`.
func (m Model) diffTitle() string {
	parts := []string{"diff"}
	if m.diff.crew != "" {
		parts = append(parts, m.diff.crew)
	}
	if m.diff.branch != "" {
		parts = append(parts, m.diff.branch)
	}
	return strings.Join(parts, " "+m.g.Dot+" ")
}

// diffBodyLines is the patch itself, one *line per line of text, with no
// wrapping: a patch is already laid out in columns, and re-wrapping it would
// put a "+" in the middle of a hunk. A line wider than the frame is cut with
// the glyph set's ellipsis rather than silently - in a patch, a line that
// merely stops is indistinguishable from one that says something shorter.
func (m Model) diffBodyLines(w int) []*line {
	raw := strings.Split(strings.TrimRight(m.diff.text, "\n"), "\n")
	if strings.TrimSpace(m.diff.text) == "" {
		raw = []string{"the command returned nothing"}
	}
	out := make([]*line, 0, len(raw))
	for _, text := range raw {
		out = append(out, newLine().add(" "+text, diffLineStyle(text, m.p)).cut(w, m.g))
	}
	return out
}

// diffLineStyle colours a patch the way a reader already expects one:
// additions green, deletions red, file and hunk headers dim. It is only ever
// a second signal - the leading "+", "-" and "@@" are the first, and a
// monochrome terminal loses nothing.
func diffLineStyle(text string, p palette) lipgloss.Style {
	switch {
	case strings.HasPrefix(text, "+++"), strings.HasPrefix(text, "---"):
		return p.Dim
	case strings.HasPrefix(text, "+"):
		return p.Green
	case strings.HasPrefix(text, "-"):
		return p.Red
	case strings.HasPrefix(text, "@@"), strings.HasPrefix(text, "diff --git"),
		strings.HasPrefix(text, "index "), strings.HasPrefix(text, "new file"),
		strings.HasPrefix(text, "deleted file"), strings.HasPrefix(text, "similarity index"),
		strings.HasPrefix(text, "rename "):
		return p.Dim
	}
	return p.Fg
}

// diffLines fills a region of h lines: title, rule, the scrolled body, and
// a footer.
//
// The footer does not repeat the keys. The frame's own key line already
// names them (seams.go), and this package's rule is that one surface names
// its keys once - what the footer says instead is the thing the key line
// cannot, which is that a reader can scroll around in here as long as they
// like without touching anything.
func (m Model) diffLines(w, h int) []*line {
	if h <= 0 {
		return nil
	}
	body := m.diffBodyLines(w)
	head := []*line{
		leftRight(
			newLine().pad(1).add(m.diffTitle(), m.p.Bold),
			newLine().add(plural(len(body), "line", "lines")+" ", m.p.Dim),
			w, m.g,
		),
		newLine().add(strings.Repeat(m.g.HRule, max0(w)), m.p.Faint),
	}
	hint := newLine().pad(1).
		add("read-only "+m.g.Dot+" nothing here changes the crew, its branch or its worktree", m.p.Dim).
		cut(w, m.g)
	inner := h - len(head) - 1
	if inner < 1 {
		// Too short for a body: the title and the way out still have to be
		// drawn, so the body is what gives way.
		return fitLines(append(head, hint), h)
	}
	out := append(head, windowContent(body, m.diff.top, inner, m.g, m.p)...)
	for len(out) < h-1 {
		out = append(out, newLine())
	}
	out = append(out, hint)
	return fitLines(out, h)
}

// diffBodyHeight is the number of content lines the overlay shows at the
// current size: what a page key has to move by, and what the scroll clamp
// measures against. It mirrors diffLines' own arithmetic rather than
// guessing, so PgDn cannot step past the last line of a patch.
func diffBodyHeight(l frameLayout) int {
	const chrome = 3 // title, rule, hint
	return max0(l.Body - chrome)
}

// onDiffKey is the overlay's keyboard. It is modal: scroll and close, and
// nothing else, so no key here can move the list behind the patch.
//
// q closes rather than quits, which is the one place besides the project
// name input (update.go) where it does not mean "leave the Console". A
// full-screen reader whose q exits the application is a trap every pager
// has trained people out of, and Ctrl+C is still the way out - the key line
// says so while the overlay is open (seams.go).
func (m Model) onDiffKey(key string, l frameLayout) Model {
	switch key {
	case "esc", "backspace", "enter", "q":
		return m.closeDiff()
	case "up", "k":
		return m.scrollDiff(-1, l)
	case "down", "j":
		return m.scrollDiff(1, l)
	case "pgup":
		return m.scrollDiff(-diffPage(l), l)
	case "pgdown":
		return m.scrollDiff(diffPage(l), l)
	case "home":
		m.diff.top = 0
		return m
	}
	return m
}

// diffPage is one page for PgUp/PgDn: the body height less one line of
// overlap, so a reader paging through a patch keeps a line of context
// rather than having to remember what fell off the top.
func diffPage(l frameLayout) int {
	h := diffBodyHeight(l)
	if h <= 1 {
		return 1
	}
	return h - 1
}

func (m Model) scrollDiff(delta int, l frameLayout) Model {
	// clampScrollTop, not clampTop (model.go): a patch has no selected row,
	// and clampTop's "keep the selection on screen" rule would drag the
	// offset back to the top on every keystroke.
	m.diff.top = clampScrollTop(m.diff.top+delta, len(m.diffBodyLines(l.Cols)), diffBodyHeight(l))
	return m
}

// RenderDiffOverlay draws the diff overlay's own lines for one text, at w
// cells wide and h lines tall, scrolled to the top and with colour stripped
// the way a golden fixture renders.
//
// It is exported for one caller, the live proof in cmd/mate, for the same
// reason RenderInboxRail is (box.go): whether a real crew's commit is
// legible on this surface is a claim only the renderer can settle, and a
// live test that asserted on the ActionFunc's string alone would pass while
// the overlay showed a patch cut at "diff --g…".
func RenderDiffOverlay(text, crew, branch string, w, h int) []string {
	m := Model{g: unicodeGlyphs, p: plainPalette(), w: w, h: h}
	m.diff = diffFlow{open: true, crew: crew, branch: branch, text: text}
	out := make([]string, 0, h)
	for _, l := range fitLines(m.diffLines(w, h), h) {
		out = append(out, l.render(w))
	}
	return out
}
