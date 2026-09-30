package console

import (
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/names"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// Bottom sheets (design F, G, K): actions, confirm, new project, the
// harness picker, a diff, and the key list. A sheet replaces the lower
// panes and never covers the selected row (layout.go's planStacked).

type sheetKind int

const (
	sheetNone sheetKind = iota
	sheetActions
	sheetConfirm
	sheetNewProject
	sheetHarness
	sheetDiff
	sheetKeys
)

// sheetOpen is the sheet on screen, in the order the keyboard reaches them.
func (m Model) sheetOpen() sheetKind {
	switch {
	case m.diff.open:
		return sheetDiff
	case m.confirm != nil:
		return sheetConfirm
	case m.actionInputMode:
		return sheetNewProject
	case m.harnessPick:
		return sheetHarness
	case m.actions:
		return sheetActions
	case m.keysOpen:
		return sheetKeys
	}
	return sheetNone
}

// sheetTitle is the open sheet's rule title and meta.
func (m Model) sheetTitle() (gline, string) {
	switch m.sheetOpen() {
	case sheetActions:
		t := gl().add("actions", tAcc)
		if obj := m.actionObjectTitle(); len(obj.segs) > 0 {
			t = t.add(" "+m.g.Dot+" ", tDim).join(dimmed(obj))
		}
		return t, "esc"
	case sheetConfirm:
		return gl().add(confirmVerb(m.confirm.choice, m.confirm.label)+"?", tAcc), "esc"
	case sheetNewProject:
		return gl().add("new project", tAcc), "esc"
	case sheetHarness:
		return gl().add("harness", tAcc).add(" "+m.g.Dot+" new mate", tDim), "esc"
	case sheetDiff:
		return gl().add(m.diffTitle(), tAcc), "esc"
	case sheetKeys:
		return gl().add("keys", tAcc), "any key"
	}
	return gl(), ""
}

// dimmed is l with every segment in the dim token.
func dimmed(l gline) gline {
	out := gl()
	for _, s := range l.segs {
		out = out.add(s.text, tDim)
	}
	return out
}

// actionObjectTitle names the row the sheet acts on.
func (m Model) actionObjectTitle() gline {
	r := m.actionRow
	switch r.kind {
	case rowMate:
		return kindName(m.g.Mate, m.currentProject().Name)
	case rowCrew:
		if c, ok := m.crewByID(r.id); ok {
			return kindName(m.g.Crew, shortID(c.CrewID, m.g))
		}
	case rowProject:
		if p, ok := m.projectByID(r.id); ok {
			return gl().add(p.Name, tFg)
		}
	}
	return gl()
}

// sheetLines draws the open sheet's body.
func (m Model) sheetLines(p framePlan) []gline {
	switch m.sheetOpen() {
	case sheetActions:
		return m.actionSheetLines(p)
	case sheetConfirm:
		return m.confirmSheetLines(p)
	case sheetNewProject:
		return m.newProjectSheetLines(p)
	case sheetHarness:
		return m.harnessSheetLines(p)
	case sheetDiff:
		return m.diffSheetLines(p)
	case sheetKeys:
		return m.keysSheetLines(p)
	}
	return nil
}

// ---------- F: actions ----------

// menuKeyWidth is the key column: "enter" and two blanks.
const menuKeyWidth = 7

func (m Model) actionSheetLines(p framePlan) []gline {
	var out []gline
	for i, e := range m.menu {
		on := i == m.actionIndex
		lead := gl().pad(1)
		if on {
			lead = gl().add(m.g.Selected, tAcc)
		}
		var l gline
		if e.enabled {
			right := gl()
			if e.confirms() {
				right = gl().add("confirm", tDim).pad(1)
			}
			l = spread(gl().pad(1).add(padRight(e.key, menuKeyWidth), tFg).add(e.label, tFg), right, p.w-1, m.g)
		} else {
			// The label keeps its cells; the reason is cut to what is left.
			left := gl().pad(1).add(padRight(m.g.Dot, menuKeyWidth), tFaint).add(e.label, tFaint)
			room := p.w - 1 - left.width() - 2
			reason := gl()
			if room >= 4 {
				reason = gl().add(truncateEnd(e.reason, room-1, m.g), tDim).pad(1)
			}
			l = spread(left, reason, p.w-1, m.g)
		}
		out = append(out, lead.join(l).padTo(p.w, m.g).selected(on))
	}
	if i := m.actionIndex; i >= 0 && i < len(m.menu) && m.menu[i].about != "" {
		out = append(out, gl())
		for _, part := range wrapWords(m.menu[i].about, p.w-4) {
			out = append(out, gl().pad(2).add(part, tDim))
		}
	}
	return out
}

// ---------- G: confirm ----------

// confirmLabelWidth is the confirm sheet's label column.
const confirmLabelWidth = 10

// confirmVerb is the question the sheet asks: "stop mate", "merge crew".
func confirmVerb(c actionChoice, label string) string {
	if label != "" {
		return strings.ToLower(strings.TrimSuffix(label, "…"))
	}
	return string(c.action)
}

func (m Model) confirmSheetLines(p framePlan) []gline {
	c := m.confirm
	object, scope, effect := m.actionObjectDescription(c.choice)
	vw := maxInt(8, p.w-confirmLabelWidth-1)
	block := func(label string, lines []gline) []gline {
		out := make([]gline, 0, len(lines)+1)
		for i, l := range lines {
			lead := gl().pad(confirmLabelWidth)
			if i == 0 {
				lead = gl().pad(2).add(padRight(label, confirmLabelWidth-2), tDim)
			}
			out = append(out, lead.join(l))
		}
		return append(out, gl())
	}
	objLines := []gline{gl().add(object, tFg)}
	if obj := m.actionObjectTitle(); len(obj.segs) > 0 {
		objLines = []gline{obj}
		if sub := m.confirmObjectSub(m.actionRow); sub != "" {
			objLines = append(objLines, wrapped(sub, tDim, vw)...)
		}
	}
	out := []gline{gl()}
	if c.choice.confirmPrompt != "" {
		// The action's own question, when it has one: a merge names both
		// branches here, where the key that runs it is pressed.
		for _, part := range wrapWords(c.choice.confirmPrompt, p.w-4) {
			out = append(out, gl().pad(2).add(part, tFg))
		}
		out = append(out, gl())
	}
	out = append(out, block("object", objLines)...)
	out = append(out, block("scope", wrapped(scope, tDim, vw))...)
	out = append(out, block("effect", wrapped(effect, tDim, vw))...)
	verb := confirmVerb(c.choice, c.label)
	out = append(out,
		gl().add(m.g.Selected, tAcc).pad(1).add(padRight("enter", menuKeyWidth), tFg).add("cancel", tFg).padTo(p.w, m.g).selected(true),
		gl().pad(2).add(padRight(c.key, menuKeyWidth), tFg).add(verb, tRed),
	)
	return out
}

// confirmObjectSub is the line under the object: its agent and status.
func (m Model) confirmObjectSub(r row) string {
	switch r.kind {
	case rowMate:
		mate := m.currentProject().Mate
		parts := []string{}
		if a := m.mateAgent(mate); a != "" {
			parts = append(parts, a)
		}
		parts = append(parts, mateWord(mate))
		return strings.Join(parts, " "+m.g.Dot+" ")
	case rowCrew:
		if c, ok := m.crewByID(r.id); ok {
			return string(c.HarnessKind) + " " + m.g.Dot + " " + crewWord(c)
		}
	}
	return ""
}

// ---------- K: new project ----------

func (m Model) newProjectSheetLines(p framePlan) []gline {
	input := func(f onboardField, value, placeholder string) gline {
		if m.actionField == f {
			return gl().add(m.g.Selected, tAcc).pad(1).add(value, tFg).add(m.g.Cursor, tAcc).padTo(p.w, m.g).selected(true)
		}
		if value == "" {
			return gl().pad(2).add(placeholder, tFaint)
		}
		return gl().pad(2).add(value, tFg)
	}
	name := strings.TrimSpace(m.actionInput)
	rule, ruleTok := names.ProjectRule+", unique", tDim
	if why := m.newProjectNameProblem(name); why != "" && name != "" {
		rule, ruleTok = why, tAmber
	}
	out := []gline{
		gl().pad(2).add("name", tDim),
		input(fieldName, m.actionInput, ""),
	}
	for _, part := range wrapWords(rule, p.w-6) {
		out = append(out, gl().pad(4).add(part, ruleTok))
	}
	out = append(out,
		gl().pad(2).add("repo "+m.g.Dot+" optional", tDim),
		input(fieldRepo, m.actionRepo, "path or git url"),
		gl().pad(4).add("leave blank to add later", tDim),
		gl(),
	)
	enter := "create " + name
	if name == "" {
		enter = "create"
	}
	out = append(out,
		gl().pad(2).add(padRight("enter", menuKeyWidth), tDim).add(enter, tFg),
		gl().pad(2+menuKeyWidth).add("no mate is started", tDim),
		gl().pad(2).add(padRight("tab", menuKeyWidth), tDim).add("next field", tFg),
		gl().pad(2).add(padRight("esc", menuKeyWidth), tDim).add("cancel", tFg),
	)
	return out
}

// newProjectNameProblem is why a typed name would be refused, or "". The
// shape is the store's own rule (internal/names); uniqueness is read off
// the snapshot the captain is looking at.
func (m Model) newProjectNameProblem(name string) string {
	if !names.ValidProject(name) {
		return "not a project name: " + names.ProjectRule
	}
	for _, p := range m.tree.Projects {
		if p.Name == name || p.ProjectID == name {
			return name + " already exists"
		}
	}
	return ""
}

// ---------- the harness picker ----------

func (m Model) harnessSheetLines(p framePlan) []gline {
	var out []gline
	for i, kind := range harnessOrder {
		on := i == m.harnessIndex
		lead := gl().pad(1)
		if on {
			lead = gl().add(m.g.Selected, tAcc)
		}
		l := lead.pad(1).add(harnessIcon(string(kind), m.g), tFg).pad(1).add(string(kind), tFg)
		if m.recordedHarness() == kind {
			l = l.add("  current", tDim)
		}
		out = append(out, l.padTo(p.w, m.g).selected(on))
	}
	out = append(out, gl())
	for _, part := range wrapWords("Creates and starts the Mate of "+m.currentProject().Name+" with this harness.", p.w-4) {
		out = append(out, gl().pad(2).add(part, tDim))
	}
	return out
}

// ---------- a diff ----------

func (m Model) diffSheetLines(p framePlan) []gline {
	raw := m.diffLines(p.w)
	out := make([]gline, 0, len(raw))
	for _, t := range raw[clampInt(m.diff.top, 0, max0(len(raw)-1)):] {
		out = append(out, gl().pad(1).add(t, diffLineTok(t)).cut(p.w, m.g))
	}
	return out
}

func diffTextLines(text string) []string {
	if strings.TrimSpace(text) == "" {
		return []string{"the command returned nothing"}
	}
	return strings.Split(strings.TrimRight(text, "\n"), "\n")
}

// diffLineTok tints a unified-diff line: additions green, removals red,
// headers dim.
func diffLineTok(text string) tok {
	switch {
	case strings.HasPrefix(text, "+++"), strings.HasPrefix(text, "---"):
		return tDim
	case strings.HasPrefix(text, "+"):
		return tGreen
	case strings.HasPrefix(text, "-"):
		return tRed
	case strings.HasPrefix(text, "@@"), strings.HasPrefix(text, "diff --git"),
		strings.HasPrefix(text, "index "), strings.HasPrefix(text, "new file"),
		strings.HasPrefix(text, "deleted file"), strings.HasPrefix(text, "similarity index"),
		strings.HasPrefix(text, "rename "):
		return tDim
	}
	return tFg
}

// ---------- ?: the key list ----------

// keyTable is design I's "7 Keys and mouse", less what this Console does
// not do.
var keyTable = [][2]string{
	{"↑ ↓", "move; fields in detail"},
	{"enter", "show in next pane"},
	{"a", "actions"},
	{"tab", "next pane"},
	{"esc", "back to the list"},
	{"n", "new project"},
	{"s", "start the mate"},
	{"m", "flip its mode"},
	{"y", "copy"},
	{"l", "box: whole log"},
	{"r", "refresh"},
	{"q", "quit"},
	{"click", "same as enter"},
	{"click rule", "focus that pane"},
	{"[assign]", "hand to the mate"},
	{"wheel", "scroll that pane"},
	{"shift+drag", "select text"},
}

func (m Model) keysSheetLines(p framePlan) []gline {
	kw := 0
	for _, k := range keyTable {
		kw = maxInt(kw, cells(k[0]))
	}
	kw = minInt(kw+2, p.w/3)
	out := make([]gline, 0, len(keyTable))
	for _, k := range keyTable {
		key := k[0]
		if m.g.Name == "ascii" {
			key = strings.NewReplacer("↑ ↓", "^ v", "▸", "+", "·", ".").Replace(key)
		}
		out = append(out, gl().pad(2).add(padRight(key, kw), tFg).add(strings.ReplaceAll(k[1], "·", m.g.Dot), tDim).cut(p.w, m.g))
	}
	return out
}

// actionObjectDescription is the confirm sheet's scope and effect, in the
// words each dangerous action needs (design G: object, scope, effect).
func (m Model) actionObjectDescription(c actionChoice) (string, string, string) {
	object := c.req.Target
	if object == "" {
		object = "workspace"
	}
	scope := "Recorded state and the runtime resources named by this action."
	effect := "The snapshot is re-read when the service returns."
	switch c.req.Action {
	case ActionStop:
		if c.req.TargetKind == "crew" {
			scope = "This crew's agent only. Its worktree and branch stay."
		} else {
			scope = "This Mate only. Its crews keep running."
		}
		effect = "The agent stops; the binding is released once the service confirms it."
	case ActionRestartMate:
		scope = "This Mate only. Its crews keep running."
		effect = "The Mate is stopped and started again; the next pane shows the new one."
	case ActionRepair:
		scope = "The recorded binding only; no live agent is assumed."
		effect = "A stale binding is cleared. Unknown state is never treated as safe to clean."
	case ActionRestartCrew:
		object = c.req.Target + "/" + c.req.Crew
		scope = "This Crew's agent and pane only. Its worktree, branch and status file stay."
		effect = "A live agent is stopped first; then the harness starts again in the same worktree and the brief is pointed at once more."
	case ActionMerge:
		object = c.req.Target + "/" + c.req.Crew
		scope = "The Project's default branch, and this crew's branch, worktree and pane."
		effect = "Fast-forward only; anything else refuses and changes nothing. If it lands, the crew is finished and its branch and worktree are removed."
	}
	return object, scope, effect
}

// recordedHarness is the current Project's Mate harness, or "".
func (m Model) recordedHarness() query.HarnessKind {
	mate := m.currentProject().Mate
	if !mate.Designated.IsKnown() {
		return ""
	}
	return mate.Designated.Value.HarnessKind
}

// itoa is fmt.Sprint for an int, for the few places a count is text.
func itoa(n int) string { return fmt.Sprint(n) }
