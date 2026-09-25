package console

import (
	"sort"
	"strconv"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The box pane (design A, B, D, J): what is waiting on somebody, newest
// first. It holds the question and one decision - who takes it - and never
// answers for the captain or draws a conversation. On the workspace level
// it gathers every Project's box; on a Project, that Project's.

// boxItem is one box entry and the Project it belongs to.
type boxItem struct {
	project string
	e       query.BoxEntry
}

// boxItems are the entries the box shows: the inbox, or the whole log
// while `l` has toggled it, newest first.
func (m Model) boxItems() []boxItem {
	var out []boxItem
	collect := func(p query.ProjectNode) {
		if !p.Box.IsKnown() {
			return
		}
		entries := p.Box.Value.Inbox
		if m.boxAll {
			entries = p.Box.Value.Entries
		}
		for _, e := range entries {
			out = append(out, boxItem{project: p.ProjectID, e: e})
		}
	}
	if m.cur().kind == frameProject {
		collect(m.currentProject())
	} else {
		for _, p := range m.tree.Projects {
			collect(p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].e.At.After(out[j].e.At) })
	return out
}

// boxSelection is the selected item: the reader's, or the newest.
func (m Model) boxSelection(items []boxItem) int {
	if len(items) == 0 {
		return -1
	}
	if m.boxSel < 0 {
		return 0
	}
	return clampInt(m.boxSel, 0, len(items)-1)
}

// boxMode is how much of each item the pane draws.
type boxMode int

const (
	// boxCompact: the top item's title and meta, and at 30+ rows on a
	// Project the first line of its question.
	boxCompact boxMode = iota
	// boxFull: every item, the selected one open in full.
	boxFull
)

func (m Model) boxMode(p framePlan) boxMode {
	if m.focus == paneBox || p.h >= comfortableRows {
		return boxFull
	}
	return boxCompact
}

// boxHit is a click target in the box body: the item a row belongs to, and
// the [assign] button's columns on the item's title line.
type boxHit struct {
	item             int
	assignX0, assign int // [assign] spans columns assignX0..assign-1; 0,0 when not drawn
}

type boxBody struct {
	lines []gline
	hits  []boxHit
}

func (b *boxBody) push(l gline, h boxHit) {
	b.lines = append(b.lines, l)
	b.hits = append(b.hits, h)
}

// boxLines draws the box body.
func (m Model) boxLines(p framePlan, mode boxMode) boxBody {
	var out boxBody
	items := m.boxItems()
	sel := m.boxSelection(items)
	focused := m.focus == paneBox
	if len(items) == 0 {
		out.push(gl().pad(2).add("nothing waiting", tDim), boxHit{item: -1})
		return out
	}
	defer func() {
		for i := range out.lines {
			out.lines[i] = out.lines[i].cut(p.w, m.g)
		}
	}()
	shown := items
	if mode == boxCompact {
		shown = items[sel : sel+1]
	}
	for k, it := range shown {
		i := k
		if mode == boxCompact {
			i = sel
		}
		on := focused && i == sel
		title, hit := m.boxTitleLine(it, on, p.w)
		hit.item = i
		out.push(title.selected(on), hit)
		out.push(m.boxMetaLine(it, p).padTo(p.w, m.g).selected(on), boxHit{item: i})
		switch {
		case mode == boxFull && i == sel:
			out.push(gl(), boxHit{item: i})
			for _, part := range wrapWords(oneLine(it.e.Text), maxInt(8, p.w-6)) {
				out.push(gl().pad(5).add(part, tFg), boxHit{item: i})
			}
			if hint, ok := m.boxAssignHint(it); ok {
				out.push(gl(), boxHit{item: i})
				out.push(hint, boxHit{item: i})
			}
			if k < len(shown)-1 {
				out.push(gl(), boxHit{item: -1})
			}
		case mode == boxCompact && p.tall && m.cur().kind == frameProject && it.e.Text != "":
			out.push(gl().pad(5).add(oneLine(it.e.Text), tDim).cut(p.w, m.g), boxHit{item: i})
		}
	}
	return out
}

// boxTitleLine is an item's first line: who asked, what about, and the
// [assign] button - or what [assign] already did.
func (m Model) boxTitleLine(it boxItem, selected bool, w int) (gline, boxHit) {
	lead := gl().add(" ", tFg)
	if selected {
		lead = gl().add(m.g.Selected, tAcc)
	}
	name := gl().pad(1).join(kindName(m.boxAsker(it), m.boxSubject(it)))
	right := gl()
	var hit boxHit
	if suffix := boxAssignSuffix(it.e); suffix != "" {
		right = gl().add(suffix, tDim).pad(2)
	} else if it.e.Resolvable() {
		right = gl().add("[", tDim).add("assign", tFg).add("]", tDim).pad(2)
		hit.assignX0 = w - right.width()
		hit.assign = hit.assignX0 + cells("[assign]")
	}
	return lead.join(spread(name, right, w-1, m.g)), hit
}

// boxAsker is the kind mark of whoever asked: the Mate for its own
// incident, a Crew otherwise.
func (m Model) boxAsker(it boxItem) string {
	if it.e.Crew == "mate" || it.e.Crew == "" {
		return m.g.Mate
	}
	return m.g.Crew
}

// boxSubject names an item by its crew's task, or by the crew id when the
// task is not in the snapshot; the Mate's own items by its Project.
func (m Model) boxSubject(it boxItem) string {
	proj, _ := m.projectByID(it.project)
	if it.e.Crew == "mate" || it.e.Crew == "" {
		if it.e.Kind == query.BoxMessage {
			return boxEntryWho(it.e)
		}
		return proj.Name
	}
	for _, c := range proj.Crews {
		if c.CrewID == it.e.Crew {
			if t := strings.TrimSpace(oneLine(c.Task)); t != "" {
				return t
			}
		}
	}
	return shortID(it.e.Crew, m.g)
}

// boxMetaLine is an item's second line: where it came from, what it needs
// and how long it has waited. On the workspace level the Project names the
// source; on a Project, the crew id.
func (m Model) boxMetaLine(it boxItem, p framePlan) gline {
	from := shortID(it.e.Crew, m.g)
	if m.cur().kind == frameWorkspace || it.e.Crew == "mate" || it.e.Crew == "" {
		if proj, ok := m.projectByID(it.project); ok {
			from = proj.Name
		}
	}
	l := gl().pad(5).add(from, tDim)
	if need := boxNeed(it.e); need != "" {
		t := tDim
		if it.e.Attention {
			t = tAmber
		}
		l = l.add(" "+m.g.Dot+" ", tDim).add(need, t)
	}
	if since := m.since(it.e.At); since != "" {
		l = l.add(" "+m.g.Dot+" "+since, tDim)
	}
	return l
}

// boxNeed is what an item needs, in the one-line status vocabulary.
func boxNeed(e query.BoxEntry) string {
	if e.Kind == query.BoxIncident {
		return boxNeedPhrase(e)
	}
	return shortStatus(e.Verb)
}

// boxAssignHint says where [assign] sends the item: to the Project's Mate.
func (m Model) boxAssignHint(it boxItem) (gline, bool) {
	if !it.e.Resolvable() || it.e.Assigned.State != "" {
		return gline{}, false
	}
	proj, ok := m.projectByID(it.project)
	if !ok {
		return gline{}, false
	}
	return gl().pad(5).add("[assign] hands it to ", tDim).join(kindName(m.g.Mate, proj.Name)), true
}

// boxNeedPhrase is an incident's need, named by its kind (mvp.md section
// 4b). A verb the box does not know renders as itself.
func boxNeedPhrase(e query.BoxEntry) string {
	switch e.Verb {
	case "stale":
		return "stuck"
	case "runtime_lost":
		return "agent gone"
	case "wedged":
		return "wedged"
	case "budget":
		return "over budget"
	}
	return e.Verb
}

// boxAssignSuffix is what [assign] has done for an item (mvp.md task 30):
// queued while the line waits for the Mate's composer, the time once it
// reached it. The item stays either way - handing a question to the Mate
// is not answering the crew - so the suffix keeps a reader from pressing
// [assign] twice.
func boxAssignSuffix(e query.BoxEntry) string {
	switch e.Assigned.State {
	case query.BoxAssignQueued:
		return "queued"
	case query.BoxAssignSent:
		return "sent " + e.Assigned.SentAt.UTC().Format("15:04")
	}
	return ""
}

// boxEntryWho names a message's pair, "source->target", the shape
// box.Line uses for sent.log.
func boxEntryWho(e query.BoxEntry) string {
	if e.Target == "" {
		return e.Source
	}
	return e.Source + "->" + e.Target
}

// boxRuleMeta is the box rule's meta: the waiting count, "N waiting" when
// focused, "all N" while the whole log is shown.
func (m Model) boxRuleMeta(items []boxItem) string {
	n := strconv.Itoa(len(items))
	switch {
	case m.boxAll:
		return "all " + n
	case m.focus == paneBox:
		return n + " waiting"
	}
	return n
}

// RenderBox draws one Project's box on its own, focused and uncoloured -
// its rule and every item, the selected one open - for the live proofs in
// cmd/mate, which assert on what the captain would read.
func RenderBox(project string, box query.Field[query.BoxView], w, h int) []string {
	m := Model{g: unicodeGlyphs, p: plainPalette(), w: w, h: h, boxSel: -1, focus: paneBox}
	m.tree.Projects = []query.ProjectNode{{ProjectID: project, Name: project, Box: box}}
	m.stack = []frame{{kind: frameWorkspace}, {kind: frameProject, id: project}}
	p := sizeClass(w, h)
	lines := append([]gline{m.paneRule(slotBox, w)}, m.boxLines(p, boxFull).lines...)
	out := make([]string, 0, h)
	for _, l := range fit(lines, h) {
		out = append(out, strings.TrimRight(l.render(w, m.p), " "))
	}
	return out
}
